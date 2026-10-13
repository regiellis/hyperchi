// Package server runs an http.Server with graceful shutdown: on SIGINT,
// SIGTERM, or SIGQUIT, a cancelled context, or a call to Shutdown, it runs the
// before-shutdown hooks, drains in-flight requests up to a timeout, then runs
// the after-shutdown hooks.
package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/config"
)

// GracefulServer is an http.Server with graceful shutdown and shutdown hooks.
// Configure it before serving: the setters are not safe to call while it runs.
type GracefulServer struct {
	server          *http.Server
	shutdownTimeout time.Duration
	beforeShutdown  []func()
	afterShutdown   []func()

	// shutdownOnce runs the shutdown sequence and its hooks exactly once, even
	// when a signal and a manual Shutdown() race. done is closed when shutdown
	// completes, so a serve loop blocked on it returns instead of waiting for
	// a signal. shutdownErr carries the result.
	shutdownOnce sync.Once
	done         chan struct{}
	shutdownErr  error
}

// NewGracefulServer returns a GracefulServer for addr with config.Default's
// server limits and shutdown timeout.
func NewGracefulServer(addr string, handler http.Handler) *GracefulServer {
	return NewGracefulServerWithConfig(addr, handler, config.Default().Server)
}

// NewHTTPServer returns an http.Server for addr with cfg's header, read,
// write, and idle limits. A zero limit takes config.Default's value, so a
// server never runs without timeouts by omission.
func NewHTTPServer(addr string, handler http.Handler, cfg config.ServerConfig) *http.Server {
	defaults := config.Default().Server
	if cfg.ReadHeaderTimeout == 0 {
		cfg.ReadHeaderTimeout = defaults.ReadHeaderTimeout
	}
	if cfg.MaxHeaderBytes == 0 {
		cfg.MaxHeaderBytes = defaults.MaxHeaderBytes
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = defaults.ReadTimeout
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = defaults.WriteTimeout
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = defaults.IdleTimeout
	}
	return &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: cfg.ReadHeaderTimeout, MaxHeaderBytes: cfg.MaxHeaderBytes, ReadTimeout: cfg.ReadTimeout, WriteTimeout: cfg.WriteTimeout, IdleTimeout: cfg.IdleTimeout}
}

// NewGracefulServerWithConfig returns a GracefulServer for addr with cfg's
// limits (see NewHTTPServer) and ShutdownTimeout, defaulting a zero timeout.
func NewGracefulServerWithConfig(addr string, handler http.Handler, cfg config.ServerConfig) *GracefulServer {
	timeout := cfg.ShutdownTimeout
	if timeout == 0 {
		timeout = config.Default().Server.ShutdownTimeout
	}
	return &GracefulServer{server: NewHTTPServer(addr, handler, cfg), shutdownTimeout: timeout, done: make(chan struct{})}
}

// SetTimeouts sets the read, write, and idle timeouts. Zero means no limit,
// as in http.Server.
func (gs *GracefulServer) SetTimeouts(read, write, idle time.Duration) *GracefulServer {
	gs.server.ReadTimeout = read
	gs.server.WriteTimeout = write
	gs.server.IdleTimeout = idle
	return gs
}

// SetShutdownTimeout sets how long shutdown waits for in-flight requests
// before closing connections.
func (gs *GracefulServer) SetShutdownTimeout(timeout time.Duration) *GracefulServer {
	gs.shutdownTimeout = timeout
	return gs
}

// OnBeforeShutdown adds a hook that runs, in order of addition, before
// requests are drained. A panicking hook is logged and the sequence goes on.
func (gs *GracefulServer) OnBeforeShutdown(fn func()) *GracefulServer {
	gs.beforeShutdown = append(gs.beforeShutdown, fn)
	return gs
}

// OnAfterShutdown adds a hook that runs, in order of addition, after requests
// are drained or the drain times out. A panicking hook is logged.
func (gs *GracefulServer) OnAfterShutdown(fn func()) *GracefulServer {
	gs.afterShutdown = append(gs.afterShutdown, fn)
	return gs
}

// ListenAndServe listens on the server's address and calls Serve.
func (gs *GracefulServer) ListenAndServe() error {
	ln, err := net.Listen("tcp", gs.server.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	return gs.Serve(ln)
}

// Serve serves on ln until SIGINT, SIGTERM, or SIGQUIT, Shutdown, or a server
// error, then shuts down and returns the result. Use ServeContext when the
// caller owns shutdown.
func (gs *GracefulServer) Serve(ln net.Listener) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
	defer stop()
	return gs.ServeContext(ctx, ln)
}

// ServeContext serves on ln, taking ownership of it, until ctx is cancelled,
// Shutdown is called, or the server fails. In every case it shuts down
// gracefully before returning. It returns nil after a clean shutdown.
func (gs *GracefulServer) ServeContext(ctx context.Context, ln net.Listener) error {
	gs.server.Addr = ln.Addr().String()
	result := make(chan error, 1)
	go func() { result <- gs.server.Serve(ln) }()
	select {
	case err := <-result:
		shutdownErr := gs.shutdown()
		if errors.Is(err, http.ErrServerClosed) {
			return shutdownErr
		}
		return errors.Join(err, shutdownErr)
	case <-ctx.Done():
		err := gs.shutdown()
		<-result
		return err
	case <-gs.done:
		<-result
		return gs.shutdownErr
	}
}

// ListenAndServeTLS serves HTTPS with the given certificate and key until
// SIGINT, SIGTERM, or SIGQUIT, or Shutdown. A server that fails to start
// returns its error without running the shutdown hooks.
func (gs *GracefulServer) ListenAndServeTLS(certFile, keyFile string) error {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
	defer signal.Stop(sigChan)

	serverErr := make(chan error, 1)
	go func() {
		log.Printf("HTTPS server starting on %s", gs.server.Addr)
		if err := gs.server.ListenAndServeTLS(certFile, keyFile); err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return fmt.Errorf("TLS server failed to start: %w", err)
	case sig := <-sigChan:
		log.Printf("Received signal: %v. Initiating graceful shutdown...", sig)
		return gs.shutdown()
	case <-gs.done:
		return gs.shutdownErr
	}
}

// shutdown performs graceful shutdown exactly once. Concurrent callers, such
// as a signal and a manual Shutdown(), all receive the same result, and the
// hooks run a single time. Closing done unblocks any serve loop waiting on it.
func (gs *GracefulServer) shutdown() error {
	gs.shutdownOnce.Do(func() {
		gs.shutdownErr = gs.doShutdown()
		close(gs.done)
	})
	return gs.shutdownErr
}

// doShutdown runs the before hooks, drains requests within the shutdown
// timeout, force-closes the remaining connections if the drain fails, runs
// the after hooks, and returns the drain error.
func (gs *GracefulServer) doShutdown() error {
	log.Println("Starting graceful shutdown...")

	for _, fn := range gs.beforeShutdown {
		runHook("before-shutdown", fn)
	}

	ctx, cancel := context.WithTimeout(context.Background(), gs.shutdownTimeout)
	defer cancel()

	err := gs.server.Shutdown(ctx)
	if err != nil {
		log.Printf("Server shutdown error: %v", err)
		if closeErr := gs.server.Close(); closeErr != nil {
			log.Printf("Server force close error: %v", closeErr)
		}
	}

	for _, fn := range gs.afterShutdown {
		runHook("after-shutdown", fn)
	}

	log.Println("Server shutdown complete")
	return err
}

// runHook calls a shutdown hook and recovers a panic, so one bad hook cannot
// abort the shutdown sequence.
func runHook(phase string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Panic in %s hook: %v", phase, r)
		}
	}()
	fn()
}

// Shutdown manually triggers graceful shutdown. It is safe to call concurrently
// with signal-driven shutdown and may be called more than once.
func (gs *GracefulServer) Shutdown() error {
	return gs.shutdown()
}

// GetServer returns the underlying http.Server, for settings this type does
// not expose.
func (gs *GracefulServer) GetServer() *http.Server {
	return gs.server
}

// ServerManager runs several GracefulServers and shuts them all down together.
type ServerManager struct {
	servers []*GracefulServer
}

// NewServerManager returns an empty ServerManager.
func NewServerManager() *ServerManager {
	return &ServerManager{
		servers: make([]*GracefulServer, 0),
	}
}

// AddServer adds server to the set StartAll runs.
func (sm *ServerManager) AddServer(server *GracefulServer) *ServerManager {
	sm.servers = append(sm.servers, server)
	return sm
}

// StartAll serves every server (plain HTTP) until a signal or until one fails,
// then shuts them all down. It returns the failing server's error, or the
// last shutdown error. A manual Shutdown of one server does not end StartAll.
func (sm *ServerManager) StartAll() error {
	if len(sm.servers) == 0 {
		return fmt.Errorf("no servers to start")
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
	defer signal.Stop(sigChan)

	serverErrors := make(chan error, len(sm.servers))

	for i, server := range sm.servers {
		go func(idx int, srv *GracefulServer) {
			log.Printf("Starting server %d on %s", idx+1, srv.server.Addr)
			if err := srv.server.ListenAndServe(); err != http.ErrServerClosed {
				serverErrors <- fmt.Errorf("server %d failed: %w", idx+1, err)
			}
		}(i, server)
	}

	select {
	case err := <-serverErrors:
		log.Printf("Server error: %v. Shutting down all servers...", err)
		if shutdownErr := sm.shutdownAll(); shutdownErr != nil {
			log.Printf("Error during shutdown after server error: %v", shutdownErr)
		}
		return err
	case sig := <-sigChan:
		log.Printf("Received signal: %v. Shutting down all servers...", sig)
		return sm.shutdownAll()
	}
}

// shutdownAll shuts the servers down one after another and returns the last
// error.
func (sm *ServerManager) shutdownAll() error {
	log.Printf("Shutting down %d servers...", len(sm.servers))

	var lastErr error
	for i, server := range sm.servers {
		log.Printf("Shutting down server %d...", i+1)
		if err := server.shutdown(); err != nil {
			log.Printf("Error shutting down server %d: %v", i+1, err)
			lastErr = err
		}
	}

	log.Println("All servers shutdown complete")
	return lastErr
}

// ProductionServer returns a GracefulServer with 30-second read and write
// timeouts, a 120-second idle timeout, and a 15-second shutdown.
func ProductionServer(addr string, handler http.Handler) *GracefulServer {
	return NewGracefulServer(addr, handler).
		SetTimeouts(30*time.Second, 30*time.Second, 120*time.Second).
		SetShutdownTimeout(15 * time.Second)
}

// DevelopmentServer returns a GracefulServer with 10-second read and write
// timeouts, a 60-second idle timeout, and a 5-second shutdown.
func DevelopmentServer(addr string, handler http.Handler) *GracefulServer {
	return NewGracefulServer(addr, handler).
		SetTimeouts(10*time.Second, 10*time.Second, 60*time.Second).
		SetShutdownTimeout(5 * time.Second)
}
