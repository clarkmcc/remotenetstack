package netstack

import (
	"crypto/tls"
	"fmt"
	netstackhttp "github.com/clarkmcc/remotenetstack/netstack/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"io"
	"net"
	"net/http"
	"net/netip"
	"testing"
)

func TestVirtualNetworkInterface(t *testing.T) {
	logger := zap.NewExample()

	// In-memory transport
	n1, n2 := net.Pipe()

	// Create our entrance virtual network interface
	v1, err := NewVNI(VNIConfig{
		Address:   netip.MustParseAddr("192.168.1.134"),
		Mode:      Entrance,
		LinkLayer: n1,
		Logger:    logger,
	})
	assert.NoError(t, err)
	defer v1.Stop()

	// Create our exit virtual network interface
	v2, err := NewVNI(VNIConfig{
		Address:   netip.MustParseAddr("192.168.1.1"),
		Mode:      Exit,
		LinkLayer: n2,
		Logger:    logger,
	})
	assert.NoError(t, err)

	// Allow connections through the exit interface to 192.168.1.134/32
	require.NoError(t, v2.ExposeRoutes([]string{
		"192.168.1.134/32",
	}))
	defer v2.Stop()

	// Get a new http.Client that dials using the netstack
	client := netstackhttp.GetClient(v1.Stack, 1,
		netstackhttp.WithLogger(zap.NewExample()),
		netstackhttp.WithTLSConfig(&tls.Config{InsecureSkipVerify: true}))

	// Make an HTTP request
	req, err := http.NewRequest(http.MethodGet, "http://192.168.1.134", nil)
	if err != nil {
		panic(err)
	}
	res, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(b))
}
