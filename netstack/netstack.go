package netstack

import (
	"errors"
	"github.com/clarkmcc/remotenetstack/utils"
	"go.uber.org/zap"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"net"
	"time"
)

type TestStack struct {
	Stack    *stack.Stack
	Endpoint *channel.Endpoint
	logger   *zap.Logger
}

// NewTestStack creates a new netstack for testing with a single nic, and with the provided routes.
// The routes are expected to be CIDR blocks that this Stack is allowed to route to.
func NewTestStack(logger *zap.Logger, nic string, routes []string, forwarding bool) (*TestStack, error) {
	// Create the network Stack
	s := stack.New(stack.Options{
		NetworkProtocols: []stack.NetworkProtocolFactory{
			ipv4.NewProtocol,
		},
		TransportProtocols: []stack.TransportProtocolFactory{
			tcp.NewProtocol,
			udp.NewProtocol,
		},
	})

	// Create the network interface
	ep := channel.New(128, 1024, "")
	tcpErr := s.CreateNIC(1, ep)
	if tcpErr != nil {
		return nil, errors.New(tcpErr.String())
	}

	// Attach an address to the network interface
	tcpErr = s.AddProtocolAddress(1, tcpip.ProtocolAddress{
		Protocol:          ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.Address(net.ParseIP(nic).To4()).WithPrefix(),
	}, stack.AddressProperties{})
	if tcpErr != nil {
		return nil, errors.New(tcpErr.String())
	}

	// Setup protocol handlers
	if forwarding {
		tcpFwd := TCPForwarder{
			Logger: logger.Named("tcp-forwarder"),
		}
		s.SetTransportProtocolHandler(tcp.ProtocolNumber, tcp.NewForwarder(s, 0, 5, tcpFwd.Handle).HandlePacket)

		udpFwd := UDPForwarder{
			Logger:  logger.Named("udp-forwarder"),
			Stack:   s,
			Timeout: 10 * time.Second,
			MTU:     1024,
		}
		s.SetTransportProtocolHandler(udp.ProtocolNumber, udp.NewForwarder(s, udpFwd.Handle).HandlePacket)
	}

	s.SetSpoofing(1, true)
	s.SetPromiscuousMode(1, true)

	// Add routes
	for _, r := range routes {
		_, cidr, err := net.ParseCIDR(r)
		if err != nil {
			return nil, err
		}
		prefix, err := utils.ToNetIpPrefix(*cidr)
		if err != nil {
			return nil, err
		}
		r := tcpip.Route{
			Destination: tcpip.AddressWithPrefix{
				Address:   tcpip.Address(prefix.Masked().Addr().AsSlice()),
				PrefixLen: prefix.Bits(),
			}.Subnet(),
			Gateway: tcpip.Address(net.ParseIP(nic).To4()),
			NIC:     1,
		}
		logger.Info("adding route", zap.String("route", r.String()))
		s.AddRoute(r)
	}

	return &TestStack{
		Stack:    s,
		Endpoint: ep,
		logger:   logger,
	}, nil
}
