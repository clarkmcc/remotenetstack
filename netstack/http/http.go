package netstackhttp

import (
	"context"
	"crypto/tls"
	"go.uber.org/zap"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"net"
	"net/http"
	"net/netip"
)

type Option func(*Config)

func WithTLSConfig(cfg *tls.Config) Option {
	return func(config *Config) {
		config.TLSConfig = cfg
	}
}

func WithLogger(logger *zap.Logger) Option {
	return func(config *Config) {
		config.Logger = logger
	}
}

type Config struct {
	TLSConfig *tls.Config
	Logger    *zap.Logger
}

// GetClient returns an HTTP client that uses the provided netstack as its transport.
func GetClient(s *stack.Stack, nicId tcpip.NICID, opts ...Option) *http.Client {
	cfg := Config{
		Logger: zap.NewNop(),
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	logger := cfg.Logger
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: cfg.TLSConfig,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				address := netip.MustParseAddrPort(addr)
				logger.Debug("dialing", zap.String("addr", addr), zap.Int("nic", int(nicId)))
				return gonet.DialTCP(s, tcpip.FullAddress{
					NIC:  nicId,
					Addr: tcpip.Address(address.Addr().AsSlice()),
					Port: address.Port(),
				}, ipv4.ProtocolNumber)
			},
		},
	}
}
