package netstack

import (
	"context"
	"fmt"
	"github.com/clarkmcc/remotenetstack/utils"
	"go.uber.org/atomic"
	"go.uber.org/zap"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
	"io"
	"net"
	"net/http"
	"time"
)

// TCPForwarder knows how to forward TCP traffic from the userspace network Stack to
// the host's network Stack. TCP dialing requests from the userspace network Stack are
// done by the host's network Stack which allows userspace traffic to exit the userspace
// Stack.
type TCPForwarder struct {
	Logger *zap.Logger
}

func (f *TCPForwarder) Handle(r *tcp.ForwarderRequest) {
	req := r.ID()
	logger := f.Logger.With(
		zap.Uint16("local_port", req.LocalPort),
		zap.String("local_address", net.IP(req.LocalAddress).String()),
		zap.Uint16("remote_port", req.RemotePort),
		zap.String("remote_address", net.IP(req.RemoteAddress).String()))
	logger.Debug("forwarding tcp")

	var wq waiter.Queue
	ep, tcpErr := r.CreateEndpoint(&wq)
	if tcpErr != nil {
		r.Complete(true)
		return
	}
	r.Complete(false)
	ep.SocketOptions().SetKeepAlive(true)

	source := gonet.NewTCPConn(&wq, ep)
	defer source.Close()

	dstAddr := fmt.Sprintf("%s:%d", req.LocalAddress.String(), req.LocalPort)
	// Establish outbound TCP connection to the target host:port
	var dialer net.Dialer
	target, err := dialer.Dial("tcp", dstAddr)
	if err != nil {
		return
	}
	defer target.Close()
	utils.Join(source, target)
}

// HTTPOverTCPForwarder is a proof-of-concept for a TCP forwarder that only handles HTTP
// requests and forwards them to a local HTTP server.
type HTTPOverTCPForwarder struct {
	Server http.Server
	Logger *zap.Logger
}

func (f *HTTPOverTCPForwarder) Handle(r *tcp.ForwarderRequest) {
	req := r.ID()
	logger := f.Logger.With(
		zap.Uint16("local_port", req.LocalPort),
		zap.String("local_address", net.IP(req.LocalAddress).String()),
		zap.Uint16("remote_port", req.RemotePort),
		zap.String("remote_address", net.IP(req.RemoteAddress).String()))
	logger.Debug("forwarding http over tcp")

	var wq waiter.Queue
	ep, tcpErr := r.CreateEndpoint(&wq)
	if tcpErr != nil {
		r.Complete(true)
		return
	}
	r.Complete(false)
	ep.SocketOptions().SetKeepAlive(true)

	source := gonet.NewTCPConn(&wq, ep)
	defer source.Close()

	err := f.Server.Serve(&singleConnListener{conn: source})
	if err != nil {
		logger.Error("serving http", zap.Error(err))
	}
}

// UDPForwarder knows how to forward UDP traffic from the userspace network Stack through
// the host networking Stack.
type UDPForwarder struct {
	Logger  *zap.Logger
	Stack   *stack.Stack
	Timeout time.Duration
	MTU     int
}

func (u *UDPForwarder) Handle(r *udp.ForwarderRequest) {
	req := r.ID()
	logger := u.Logger.With(zap.Reflect("req", req))

	go func() {
		logger.Info("forwarding udp")
		var wq waiter.Queue
		ep, tcpErr := r.CreateEndpoint(&wq)
		if tcpErr != nil {
			logger.Error("creating endpoint", zap.String("error", tcpErr.String()))
			return
		}
		src := gonet.NewUDPConn(u.Stack, &wq, ep)
		defer src.Close()

		// Check if destination is the local Nebula IP and if so, forward to localhost instead
		dstAddr := &net.UDPAddr{IP: net.IP(req.LocalAddress), Port: int(req.LocalPort)}
		localAddr := &net.UDPAddr{IP: net.IP{0, 0, 0, 0}, Port: 0}
		srcAddr := &net.UDPAddr{IP: net.IP(req.RemoteAddress), Port: int(req.RemotePort)}

		// Set up listener to receive UDP packets coming back from target
		dest, err := net.ListenUDP("udp", localAddr)
		if err != nil {
			logger.Error("starting udp listener", zap.Error(err))
			return
		}
		defer dest.Close()

		// Start a goroutine to copy data in each direction for the proxy and then
		// wait for completion
		copy := func(ctx context.Context, dst net.PacketConn, dstAddr net.Addr, src net.PacketConn, errC chan<- error) {
			buf := make([]byte, u.MTU)
			for {
				select {
				case <-ctx.Done():
					return
				default:
					var n int
					var err error
					n, _, err = src.ReadFrom(buf)
					if err == nil {
						_, err = dst.WriteTo(buf[:n], dstAddr)
					}

					// Return error code or nil to the error channel. Nil value
					// is used to signal activity.
					select {
					case errC <- err:
					default:
					}
				}
			}
		}

		ctx, cancel := context.WithCancel(context.Background())
		errors := make(chan error, 2)
		go copy(ctx, dest, dstAddr, src, errors)
		go copy(ctx, src, srcAddr, dest, errors)

		// Tear down the forwarding if there is no activity after a certain
		// period of time
		for keepGoing := true; keepGoing; {
			select {
			case err := <-errors:
				if err != nil {
					logger.Error("forwarding udp", zap.Error(err))
					keepGoing = false
				}
				// If err is nil then this means some activity has occurred, so
				// reset the timeout timer by restarting the select
			case <-time.After(u.Timeout):
				logger.Debug("udp forward timed out")
				keepGoing = false
			}
		}
		cancel()
		logger.Debug("udp forwarder stopped")
	}()
}

var _ net.Listener = &singleConnListener{}

// singleConnListener is a net.Listener that only provides a single connection.
type singleConnListener struct {
	conn net.Conn
	done atomic.Bool
}

func (s *singleConnListener) Accept() (net.Conn, error) {
	if s.done.Load() {
		return nil, io.EOF
	}
	return s.conn, nil
}

func (s *singleConnListener) Close() error {
	return nil
}

func (s *singleConnListener) Addr() net.Addr {
	return &net.TCPAddr{}
}
