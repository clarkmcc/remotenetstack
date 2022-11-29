package transportp2p

import (
	"github.com/clarkmcc/remotenetstack/netstack"
	"github.com/clarkmcc/remotenetstack/utils"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"go.uber.org/zap"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
)

// Protocol defines a libp2p protocol ID that can be used by clients and servers to
// identify the p2p remote-netstack transport.
const Protocol = "/rns/one-way/0.0.1"

// Option is a function that knows how to customize the Config struct.
type Option func(*Config)

func WithLogger(logger *zap.Logger) Option {
	return func(o *Config) {
		o.Logger = logger
	}
}

type Config struct {
	Logger *zap.Logger
}

// Transport is a p2p transport based on libp2p that uses a netstack endpoint
// as the data link layer.
type Transport struct {
	host   host.Host
	ep     *netstack.Endpoint
	logger *zap.Logger
}

// handler handles new streams over the p2p transport. It copies all bytes
// received from the stream to the netstack.Endpoint and reads all packets
// from the netstack.Endpoint and writes them to the stream. This is only
// utilized by the server side of the transport (the netstack that we're
// trying to talk through).
func (t *Transport) handler(s network.Stream) {
	t.logger.Debug("accepting stream",
		zap.String("id", s.ID()),
		zap.String("peer_id", s.Conn().RemotePeer().String()),
		zap.String("peer_addr", s.Conn().RemoteMultiaddr().String()))
	utils.Join(t.ep, s)
}

// New creates a new p2p transport using the given channel endpoint as the source
// of data sent over the transport.
func New(h host.Host, ep *channel.Endpoint, opts ...Option) (*Transport, error) {
	cfg := Config{
		Logger: zap.NewNop(),
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	t := &Transport{
		host:   h,
		logger: cfg.Logger.Named(Protocol),
		ep:     netstack.WrapChannel(ep),
	}
	h.SetStreamHandler(Protocol, t.handler)
	return t, nil
}
