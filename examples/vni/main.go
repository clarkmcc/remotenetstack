package main

import (
	"crypto/tls"
	"fmt"
	netstackhttp "github.com/clarkmcc/remotenetstack/netstack/http"
	"github.com/clarkmcc/remotenetstack/netstack/vni"
	"go.uber.org/zap"
	"io"
	"net"
	"net/http"
)

// This example illustrates how to use the virtual network interface (vni) package

var logger = zap.NewExample()

func main() {
	// This is the IP address of some device on your local network. In this case, this
	// is the IP address to my router. We're going to connect to this from the first
	// netstack, through the second netstack, and then into the local network.
	ipAddress := "192.168.1.1"

	// Set up an in-memory pipe. Packets sent to the entrance interface will flow
	// through this pipe and exit the exit interface.
	l1, l2 := net.Pipe()

	// Set up the entrance interface
	entrance, err := vni.New(vni.Config{
		Logger:    logger,
		Mode:      vni.Entrance,
		LinkLayer: l1,
	})
	if err != nil {
		panic(err)
	}
	defer entrance.Stop()

	// Set up the exit interface.
	exit, err := vni.New(vni.Config{
		Logger:    logger,
		Mode:      vni.Exit,
		LinkLayer: l2,
	})
	if err != nil {
		panic(err)
	}
	err = exit.ExposeRoutes([]string{
		ipAddress + "/32",
	})
	if err != nil {
		panic(err)
	}
	defer exit.Stop()

	// Get a new http.Client that dials using the netstack
	client := netstackhttp.GetClient(entrance.Stack, 1,
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
