package netstack

import (
	"context"
	"github.com/clarkmcc/remotenetstack/utils"
	"gvisor.dev/gvisor/pkg/bufferv2"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

// MemoryPipe is used to join two endpoints together, allowing them to communicate.
func MemoryPipe(c1, c2 *channel.Endpoint) {
	e1 := WrapChannel(c1)
	e2 := WrapChannel(c2)
	utils.Join(e1, e2)
}

// WrapChannel wraps the provided netstack channel-based Endpoint and returns a wrapper
// that implements io.Reader and io.Writer on the channel. This allows callers to read
// and write packets as raw []byte directly to the channel.
func WrapChannel(channel *channel.Endpoint) *Endpoint {
	return &Endpoint{
		Endpoint: channel,
	}
}

// Endpoint is a wrapper around a channel.Endpoint that implements
// the io.Reader and io.Writer interfaces.
type Endpoint struct {
	*channel.Endpoint
}

func (e *Endpoint) Read(p []byte) (n int, err error) {
	pkt := e.ReadContext(context.Background())
	b := pkt.ToBuffer()
	n = copy(p, b.Flatten())
	pkt.DecRef()
	return n, nil
}

func (e *Endpoint) Write(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}

	// NewPacketBuffer takes ownership of the data, so making a copy is necessary
	data := make([]byte, len(p))
	copy(data, p)
	pb := stack.NewPacketBuffer(stack.PacketBufferOptions{
		Payload: bufferv2.MakeWithData(data),
	})

	var ipv tcpip.NetworkProtocolNumber
	switch header.IPVersion(p) {
	case header.IPv4Version:
		ipv = ipv4.ProtocolNumber
	case header.IPv6Version:
		ipv = ipv6.ProtocolNumber
	default:
		// todo: log this
		return
	}
	e.InjectInbound(ipv, pb)
	return len(p), nil
}
