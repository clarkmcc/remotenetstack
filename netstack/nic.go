package netstack

import (
	"errors"
	"fmt"
	"github.com/clarkmcc/remotenetstack/utils"
	"go.uber.org/zap"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/icmp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"io"
	"net/netip"
)

// VNIMode determines how the VNI operates. In Entrance
// mode, the routes determine whether packets are forwarded to the Exit interface. In Exit mode,
// the routes determine what packets are allowed to be forwarded. We always route from
// entrance -> exit, not the other way around.
type VNIMode uint

const (
	Entrance VNIMode = iota
	Exit
)

func (m VNIMode) String() string {
	switch m {
	case Entrance:
		return "entrance"
	case Exit:
		return "exit"
	default:
		return "unknown"
	}
}

// VNI acts as a network interface that can be accessed remotely
type VNI struct {
	logger    *zap.Logger
	Stack     *stack.Stack      // Userspace networking Stack
	ep        *channel.Endpoint // The internal netstack endpoint
	epw       *Endpoint         // The wrapper around the netstack endpoint that allows us to read/write packets over arbitrary transports
	self      netip.Addr        // IP address of this network interface
	routes    []tcpip.Route     // Routes that are exposed via this network interface
	mode      VNIMode           // Determines how this interface operates
	nicId     tcpip.NICID       // The ID of the network interface in the netstack
	linkLayer io.ReadWriter
	stopChan  chan struct{}
}

type VNIConfig struct {
	Logger    *zap.Logger
	Address   netip.Addr    // The IP address of this network interface
	Mode      VNIMode       // The mode that this network interface should operate under
	LinkLayer io.ReadWriter // The linkLayer where packets are read/written
	MTU       uint32        // Maximum transmission unit
}

func NewVNI(config VNIConfig) (*VNI, error) {
	if config.LinkLayer == nil {
		return nil, errors.New("linkLayer cannot be nil")
	}
	if config.MTU == 0 {
		config.MTU = 1500
	}
	if config.Logger == nil {
		config.Logger = zap.NewNop()
	}
	logger := config.Logger.Named("vni").With(zap.String("mode", config.Mode.String()))

	nicId := tcpip.NICID(1)
	ep := channel.New(128, config.MTU, "")
	s := stack.New(stack.Options{
		NetworkProtocols: []stack.NetworkProtocolFactory{
			ipv4.NewProtocol,
			ipv6.NewProtocol,
		},
		TransportProtocols: []stack.TransportProtocolFactory{
			tcp.NewProtocol,
			udp.NewProtocol,
			icmp.NewProtocol4,
			icmp.NewProtocol6,
		},
	})

	// Create a network interface in the netstack
	tcpErr := s.CreateNIC(nicId, ep)
	if tcpErr != nil {
		return nil, errors.New(tcpErr.String())
	}
	s.AddProtocolAddress(nicId, tcpip.ProtocolAddress{
		Protocol: ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{
			Address:   tcpip.Address(config.Address.AsSlice()),
			PrefixLen: 32,
		},
	}, stack.AddressProperties{})

	switch config.Mode {
	case Entrance:
		// For entrance interfaces, we want to accept packets for all routes
		// and route them through this network interface.
		r := tcpip.Route{
			Destination: tcpip.AddressWithPrefix{
				Address:   tcpip.Address(netip.MustParseAddr("0.0.0.0").AsSlice()),
				PrefixLen: 0,
			}.Subnet(),
			Gateway: tcpip.Address(config.Address.AsSlice()),
			NIC:     nicId,
		}
		logger.Debug(r.String())
		s.AddRoute(r)
	case Exit:
		// Setup protocol forwarders on the exit interface
		s.SetTransportProtocolHandler(tcp.ProtocolNumber, tcp.NewForwarder(s, 0, 5, (&TCPForwarder{
			Logger: config.Logger.Named("tcp-forwarder"),
		}).Handle).HandlePacket)
		s.SetTransportProtocolHandler(udp.ProtocolNumber, udp.NewForwarder(s, (&UDPForwarder{
			Logger: config.Logger.Named("udp-forwarder"),
		}).Handle).HandlePacket)

		s.SetPromiscuousMode(nicId, true)
		s.SetSpoofing(nicId, true)
	}

	epw := WrapChannel(ep)
	epw.logger = logger.Named(config.Mode.String())
	iface := &VNI{
		Stack:     s,
		nicId:     nicId,
		epw:       epw,
		ep:        ep,
		self:      config.Address,
		mode:      config.Mode,
		logger:    logger,
		linkLayer: config.LinkLayer,
		stopChan:  make(chan struct{}),
	}
	go iface.linkLayerWorker()
	return iface, nil
}

// Stop stops the VNI and prevents it from forwarding any more packets to/from the linkLayer
func (v *VNI) Stop() {
	close(v.stopChan)
}

// linkLayerWorker reads/writes packets to/from the linkLayer and reads/writes them to the netstack.
func (v *VNI) linkLayerWorker() {
	for {
		select {
		case _, ok := <-v.stopChan:
			if !ok {
				return
			}
		default:
			utils.Join(v.epw, v.linkLayer)
		}
	}
}

// ExposeRoutes allows the caller to expose routes to the remote network interface. This should
// only be called on Exit interfaces, entrance interfaces will automatically expose all routes.
func (v *VNI) ExposeRoutes(routes []string) error {
	if v.mode == Entrance {
		return fmt.Errorf("cannot expose routes on entrance interface")
	}
	var rp []netip.Prefix
	for _, r := range routes {
		p, err := netip.ParsePrefix(r)
		if err != nil {
			return err
		}
		rp = append(rp, p)
	}
	for _, r := range rp {
		route := tcpip.Route{
			Destination: tcpip.AddressWithPrefix{
				Address:   tcpip.Address(r.Masked().Addr().AsSlice()),
				PrefixLen: r.Bits(),
			}.Subnet(),
			Gateway: tcpip.Address(v.self.AsSlice()),
			NIC:     v.nicId,
		}
		v.routes = append(v.routes, route)
		v.logger.Debug("exposed route", zap.String("route", route.String()))
	}
	v.Stack.SetRouteTable(v.routes)
	return nil
}
