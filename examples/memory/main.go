package main

import (
	"crypto/tls"
	"fmt"
	"github.com/clarkmcc/remotenetstack/netstack"
	netstackhttp "github.com/clarkmcc/remotenetstack/netstack/http"
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

	// Create one netstack with the IP address 10.0.0.1
	s1, err := netstack.NewTestStack(logger.Named("s1"), "10.0.0.1", []string{"192.168.1.0/24"}, false)
	if err != nil {
		panic(err)
	}

	// Create another netstack with the IP address 192.168.0.1
	s2, err := netstack.NewTestStack(logger.Named("s2"), "192.168.1.1", []string{"0.0.0.0/0"}, true)
	if err != nil {
		panic(err)
	}

	// Connect the two netstacks using a data-link layer that exists only in-memory
	go netstack.MemoryPipe(s1.Endpoint, s2.Endpoint)

	// Get a new http.Client that dials using the netstack
	client := netstackhttp.GetClient(s1.Stack, 1,
		netstackhttp.WithLogger(logger),
		netstackhttp.WithTLSConfig(&tls.Config{InsecureSkipVerify: true}))

	// Make an HTTP request
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
