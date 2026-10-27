package hyperchi

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/server"
)

// portScanRange is how many ports above the requested one port fallback tries
// (8080, then 8081, up to 8180).
const portScanRange = 100

// Serve runs the app until SIGINT or SIGTERM, then drains requests and closes
// services. An empty addr serves on Config.Server's host and port.
func (g *HyperChi) Serve(addr string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return g.ServeContext(ctx, addr)
}

// ServeContext serves until ctx is cancelled, then drains requests and closes
// the app (see Close). It binds addr exactly, unless the app is in development
// mode with Server.PortFallback set: then a taken port moves to the next free
// one. Registered integrations start before the listener opens, with a
// 30-second limit, and are closed when the bind fails.
func (g *HyperChi) ServeContext(ctx context.Context, addr string) error {
	if addr == "" {
		addr = g.Config.Server.Address()
	}
	// ctx signals shutdown, not a startup deadline: a context cancelled before
	// serving still starts and then drains cleanly.
	startup, cancelStartup := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	err := g.Integrations().Start(startup)
	cancelStartup()
	if err != nil {
		return err
	}
	ln, err := g.listen(addr)
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return errors.Join(err, g.Integrations().Close(cleanup))
	}
	gs := server.NewGracefulServerWithConfig(ln.Addr().String(), g.buildHandler(), g.Config.Server)
	gs.OnBeforeShutdown(g.StopAssetWatcher)
	gs.OnAfterShutdown(func() {
		if err := g.Close(); err != nil {
			log.Printf("hyperchi: close: %v", err)
		}
	})
	logListenAddresses(ln.Addr())
	if g.hotReloadRouteSetup {
		log.Printf("hyperchi: hot reload at /__hyperchi/hot-reload")
	}
	return gs.ServeContext(ctx, ln)
}

func (g *HyperChi) listen(addr string) (net.Listener, error) {
	if g.isDevelopment() && g.Config.Server.PortFallback {
		return listenWithFallback(addr)
	}
	return net.Listen("tcp", addr)
}

// Close stops the asset watcher and closes the integrations, the database, and
// the KV stores. It runs once; later calls return the first result. Serve
// calls it after in-flight requests finish.
func (g *HyperChi) Close() error {
	if g.closeState == nil {
		return g.CloseDB()
	}
	g.closeState.once.Do(func() {
		g.StopAssetWatcher()
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		g.closeState.err = errors.Join(g.Integrations().Close(cleanup), g.CloseDB(), g.CleanupKVStores())
	})
	return g.closeState.err
}

// logListenAddresses prints every URL the server answers on. For a wildcard
// bind it lists localhost and the IPv4 address of each running interface, so
// the address to open from another device is in the log. Container and VM
// bridges are skipped: nothing outside this machine reaches the server there.
func logListenAddresses(addr net.Addr) {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok || !tcp.IP.IsUnspecified() {
		log.Printf("hyperchi: listening on http://%s", addr.String())
		return
	}
	log.Printf("hyperchi: listening on http://localhost:%d", tcp.Port)
	ifaces, err := net.Interfaces()
	if err != nil {
		log.Printf("hyperchi: could not list network interfaces: %v", err)
		return
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagRunning == 0 ||
			iface.Flags&net.FlagLoopback != 0 || isBridgeInterface(iface.Name) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			log.Printf("hyperchi: could not list addresses for %s: %v", iface.Name, err)
			continue
		}
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				log.Printf("hyperchi: listening on http://%s:%d", ipnet.IP.String(), tcp.Port)
			}
		}
	}
}

func isBridgeInterface(name string) bool {
	for _, prefix := range []string{"docker", "br-", "veth", "virbr", "cni", "podman"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// listenWithFallback binds a TCP listener for addr. When the port is taken it
// scans forward up to portScanRange ports for an open one. A port of 0 binds
// an OS-assigned ephemeral port with no scanning.
func listenWithFallback(addr string) (net.Listener, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		// No port component: try the address once.
		return net.Listen("tcp", addr)
	}

	basePort, err := strconv.Atoi(portStr)
	if err != nil {
		return net.Listen("tcp", addr)
	}

	// Ephemeral port requested: bind once, let the OS choose.
	if basePort == 0 {
		return net.Listen("tcp", addr)
	}

	var lastErr error
	for offset := 0; offset <= portScanRange; offset++ {
		candidate := net.JoinHostPort(host, strconv.Itoa(basePort+offset))
		ln, listenErr := net.Listen("tcp", candidate)
		if listenErr == nil {
			if offset > 0 {
				log.Printf("hyperchi: port %d in use, bound to %d instead", basePort, basePort+offset)
			}
			return ln, nil
		}
		lastErr = listenErr
		if !isAddrInUse(listenErr) {
			// Another port will not fix any other error (permission denied,
			// for one), so stop here.
			return nil, listenErr
		}
	}
	return nil, fmt.Errorf("no available port in range %d-%d: %w", basePort, basePort+portScanRange, lastErr)
}

// isAddrInUse reports whether err is an "address already in use" bind error.
func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}

// Run serves on Config.Server.Host and $PORT, or Config.Server.Port when PORT
// is unset. See Serve.
func (g *HyperChi) Run() error {
	port := os.Getenv("PORT")
	if port == "" {
		port = strconv.Itoa(g.Config.Server.Port)
	}
	return g.Serve(net.JoinHostPort(g.Config.Server.Host, port))
}
