![](./assets/banner.png)

RemoteNetstack provides utilities for running userspace network stacks using a data link layer implementation that only needs to implement the [`io.Reader`](https://pkg.go.dev/io#Reader) and [`io.Writer`](https://pkg.go.dev/io#Writer) interfaces. This single abstraction allows for interesting functionality, like the ability to dial through an existing connection, and out of a userspace networking stack located on another physical machine, effectively acting as a remote network interface. Think about it like TCP tunneling, except at layer 3 rather than layer 4.

![](./assets/architecture-1.png)

This project maintains the core primitive that makes this work [`netstack.Endpoint`](./netstack/endpoint.go) as well as some other useful utilities.
* Custom data link layer [using libp2p streams](#libp2p) as the underlying transport. Any libp2p host can be used as a data link layer by attaching a custom stream handler. This pattern can also be used to make other data link layer implementations like a QUIC-based implementation (coming soon).
* Create HTTP clients using a netstack as the underlying transport.
* Custom TCP and UDP forwarding implementations which allow netstacks to forward TCP requests to the host's TCP stack.

Some primitives in this project aim to be un-opinionated in order to be flexible at the cost of requiring a bit more manual plumbing to implement such as [`netstack.Endpoint`](./netstack/endpoint.go), while other primitives such as [`vni.Interface`](./netstack/vni/vni.go) handle all the netstack plumbing for you, but are harder to configure.

This project is extremely experimental. While most of the heavy lifting is being performed by production-ready projects like gvisor's netstack, and libp2p, the APIs exposed in this project will change until the first major release.

## Use Cases
* TCP/UDP tunneling without the need to configure the tunnel endpoint up-front. The userspace netstack allows callers to dial through to any IP address supported by the netstack's routing table.
* Remote network interface. This allows you to put a "exit network interface" on one machine, an "entrance network interface" on another machine, connect the two somehow (libp2p, quic, tcp, etc) and then make network connections from the "entrance network interface" as if you were making connections from the "exit network interface".
* As a pluggable package in an existing project to provide VPN-like functionality on top of already-secured and authorized connections.

## Example
The following example demonstrates how to use the virtual network interface (VNI) to dial through an entrance netstack and out through an exit netstack. The link layer in this example is an in-memory pipe ([`net.Pipe`](https://pkg.go.dev/net#Pipe)). In this example we're making HTTP requests (using a custom dialer that dials over the userspace networking stack) through the entrance networking stack, and the HTTP request is forwarded to the 192.168.1.1 IP address by the exit networking stack.

In this example both the VNIs are running in memory, however, given a `LinkLayer` between them, these VNIs can be run on completely different machines.

```go
package main

import (
	"fmt"
	netstackhttp "github.com/clarkmcc/remotenetstack/netstack/http"
	"github.com/clarkmcc/remotenetstack/netstack/vni"
	"io"
	"net"
	"net/http"
)

func main() {
	// This is the IP address of some device on your local network. In this case, this
	// is the IP address to my router. We're going to connect to this from the entrance
	// virtual network interface, and out to the local network through the second virtual 
	// network interface.
	ipAddress := "192.168.1.1"

	// Set up an in-memory pipe. Packets sent to the entrance interface will flow
	// through this pipe and exit the exit interface.
	l1, l2 := net.Pipe()

	// Set up the entrance interface
	entrance, _ := vni.New(vni.Config{
		Mode:      vni.Entrance,
		LinkLayer: l1,
	})

	// Set up the exit interface.
	exit, _ := vni.New(vni.Config{
		Mode:      vni.Exit,
		LinkLayer: l2,
	})
	exit.ExposeRoutes([]string{
		"192.168.1.1/32",
	})

	// Get a new http.Client that dials using the netstack
	client := netstackhttp.GetClient(entrance.Stack, 1)
	req, _ := http.NewRequest(http.MethodGet, "http://"+ipAddress, nil)
	res, _ := client.Do(req)
	b, _ := io.ReadAll(res.Body)
	fmt.Println(string(b))
}

```

## Data-Link Layers
The following data-link layer implementations are provided by this project. Other data-link layers that are coming-soon:
* [QUIC](https://github.com/lucas-clemente/quic-go)

### libp2p
It's very simple to attach a userspace netstack to an existing libp2p host. The following example is not a fully-working example, but does show the basic idea. For a fully-working example, see [examples/libp2p/main.go](./examples/libp2p/main.go)

```go
// Create a netstack and channel endpoint
s := stack.New(stack.Options{})
e := channel.New(128, 1024, "")
s.CreateNIC(1, e)

// Create a libp2p host
host, err := libp2p.New()
if err != nil {
    panic(err)
}

// Initialize the p2p netstack protocol. This effectively attaches the netstack 
// above to the libp2p host. Packets sent to this libp2p host using the appropriate 
// protocol will be forwarded and handled by the netstack.
_, err = transportp2p.New(host, e)
if err != nil {
    panic(err)
}
```

## Thanks
This projects is built on, or was inspired by the work in these great projects:
* [gvisor (netstack)](https://gvisor.dev/)
* [libp2p](https://libp2p.io/)
* [Tailscale](https://github.com/tailscale/tailscale)
* [Nebula](https://github.com/slackhq/nebula)