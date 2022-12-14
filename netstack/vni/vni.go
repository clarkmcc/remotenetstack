package vni

import (
	"errors"
	"fmt"
	"github.com/clarkmcc/remotenetstack/netstack"
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

// defaultNicAddress is the address of the NIC in the virtual network interface. It's assigned arbitrarily
// because it doesn't actually matter (right?) since we're not interfacing with any other networking systems
var defaultNicAddress = tcpip.Address(netip.MustParseAddr("100.127.255.255").AsSlice())
var defaultGatewayAddress = tcpip.Address(netip.MustParseAddr("100.127.255.255").AsSlice())

// Mode determines how the Interface operates. In Entrance
// mode, the routes determine whether packets are forwarded to the Exit interface. In Exit mode,
// the routes determine what packets are allowed to be forwarded. We always route from
// entrance -> exit, not the other way around.
type Mode uint

const (
	Entrance Mode = iota
	Exit
)

func (m Mode) String() string {
	switch m {
	case Entrance:
		return "entrance"
	case Exit:
		return "exit"
	default:
		return "unknown"
	}
}

// Interface acts as a network interface that can be accessed remotely
type Interface struct {
	logger    *zap.Logger
	Stack     *stack.Stack       // Userspace networking Stack
	ep        *channel.Endpoint  // The internal netstack endpoint
	epw       *netstack.Endpoint // The wrapper around the netstack endpoint that allows us to read/write packets over arbitrary transports
	routes    []tcpip.Route      // Routes that are exposed via this network interface
	mode      Mode               // Determines how this interface operates
	nicId     tcpip.NICID        // The ID of the network interface in the netstack
	linkLayer io.ReadWriter
	stopChan  chan struct{}
}

type Config struct {
	Logger    *zap.Logger
	Mode      Mode          // The mode that this network interface should operate under
	LinkLayer io.ReadWriter // The linkLayer where packets are read/written
	MTU       uint32        // Maximum transmission unit
}

func New(config Config) (*Interface, error) {
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
			Address:   defaultNicAddress,
			PrefixLen: 32,
		},
	}, stack.AddressProperties{})

	epw := netstack.WrapChannel(ep)
	epw.Logger = logger.Named(config.Mode.String())
	iface := &Interface{
		Stack:     s,
		nicId:     nicId,
		epw:       epw,
		ep:        ep,
		mode:      config.Mode,
		logger:    logger,
		linkLayer: config.LinkLayer,
		stopChan:  make(chan struct{}),
	}

	switch config.Mode {
	case Entrance:
		// For entrance interfaces, we want to accept packets for all routes
		// and route them through this network interface.
		iface.addRoute(tcpip.Route{
			Destination: tcpip.AddressWithPrefix{
				Address:   tcpip.Address(netip.MustParseAddr("0.0.0.0").AsSlice()),
				PrefixLen: 0,
			}.Subnet(),
			Gateway: defaultGatewayAddress,
			NIC:     nicId,
		})
	case Exit:
		// Setup protocol forwarders on the exit interface
		s.SetTransportProtocolHandler(tcp.ProtocolNumber, tcp.NewForwarder(s, 0, 5, (&netstack.TCPForwarder{
			Logger: config.Logger.Named("tcp-forwarder"),
		}).Handle).HandlePacket)
		s.SetTransportProtocolHandler(udp.ProtocolNumber, udp.NewForwarder(s, (&netstack.UDPForwarder{
			Logger: config.Logger.Named("udp-forwarder"),
		}).Handle).HandlePacket)

		// Add the route back to the gateway
		iface.addRoute(tcpip.Route{
			Destination: tcpip.AddressWithPrefix{
				Address:   defaultNicAddress,
				PrefixLen: 32,
			}.Subnet(),
			Gateway: defaultGatewayAddress,
			NIC:     nicId,
		})

		s.SetPromiscuousMode(nicId, true)
		s.SetSpoofing(nicId, true)
	}

	go iface.linkLayerWorker()
	return iface, nil
}

// Stop stops the Interface and prevents it from forwarding any more packets to/from the linkLayer
func (v *Interface) Stop() {
	close(v.stopChan)
}

// linkLayerWorker reads/writes packets to/from the linkLayer and reads/writes them to the netstack.
func (v *Interface) linkLayerWorker() {
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

// addRoute adds a new route to the network interface and updates the netstack's routing table
func (v *Interface) addRoute(route tcpip.Route) {
	v.routes = append(v.routes, route)
	v.logger.Debug("adding route", zap.String("route", route.String()))
	v.Stack.SetRouteTable(v.routes)
}

// ExposeRoutes allows the caller to expose routes to the remote network interface. This should
// only be called on Exit interfaces, entrance interfaces will automatically expose all routes.
func (v *Interface) ExposeRoutes(routes []string) error {
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
		v.addRoute(tcpip.Route{
			Destination: tcpip.AddressWithPrefix{
				Address:   tcpip.Address(r.Masked().Addr().AsSlice()),
				PrefixLen: r.Bits(),
			}.Subnet(),
			Gateway: defaultGatewayAddress,
			NIC:     v.nicId,
		})
	}
	return nil
}
