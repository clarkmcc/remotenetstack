package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/clarkmcc/remotenetstack/netstack"
	netstackhttp "github.com/clarkmcc/remotenetstack/netstack/http"
	"github.com/clarkmcc/remotenetstack/transport/libp2p"
	"github.com/clarkmcc/remotenetstack/utils"
	"github.com/libp2p/go-libp2p/core/peer"
	"go.uber.org/zap"
	"io"
	"net/http"
)

var logger = zap.NewExample()

func main() {
	// This is the IP address of some device on your local network. In this case, this
	// is the IP address to my router. We're going to connect to this from the first
	// netstack, through the second netstack, and then into the local network.
	ipAddress := "192.168.1.1"

	// Set up a networking stack and attach it to a p2p host
	s1, err := netstack.NewTestStack(logger.Named("s1"), "10.0.0.1", []string{"192.168.1.0/24"}, false)
	if err != nil {
		panic(err)
	}
	h1, err := transportp2p.MakeTestHost(10000)
	if err != nil {
		panic(err)
	}
	_, err = transportp2p.New(h1, s1.Endpoint, transportp2p.WithLogger(logger))
	if err != nil {
		panic(err)
	}

	// Set up another networking stack and attach it to a p2p host
	s2, err := netstack.NewTestStack(logger.Named("s2"), "192.168.1.1", []string{"10.0.0.0/24"}, true)
	if err != nil {
		panic(err)
	}
	h2, err := transportp2p.MakeTestHost(10001)
	if err != nil {
		panic(err)
	}
	_, err = transportp2p.New(h2, s2.Endpoint, transportp2p.WithLogger(logger))
	if err != nil {
		panic(err)
	}

	err = h1.Connect(context.Background(), peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()})
	if err != nil {
		panic(err)
	}
	s, err := h1.NewStream(context.Background(), h2.ID(), transportp2p.Protocol)
	if err != nil {
		panic(err)
	}
	go utils.Join(s, &netstack.Endpoint{
		Endpoint: s1.Endpoint,
	})

	// Start talking to one stack through the other stack
	client := netstackhttp.GetClient(s1.Stack, 1,
		netstackhttp.WithLogger(logger),
		netstackhttp.WithTLSConfig(&tls.Config{InsecureSkipVerify: true}))
	req, err := http.NewRequest(http.MethodGet, "http://"+ipAddress, nil)
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
