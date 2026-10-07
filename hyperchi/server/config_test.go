package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/config"
)

func TestConfiguredServerLimits(t *testing.T) {
	c := config.Default().Server
	c.ReadHeaderTimeout = 2 * time.Second
	c.MaxHeaderBytes = 8192
	c.ShutdownTimeout = 3 * time.Second
	g := NewGracefulServerWithConfig(":0", http.NotFoundHandler(), c)
	s := g.GetServer()
	if s.ReadHeaderTimeout != 2*time.Second || s.MaxHeaderBytes != 8192 || g.shutdownTimeout != 3*time.Second {
		t.Fatal("configured limits ignored")
	}
	defaults := NewHTTPServer(":0", http.NotFoundHandler(), config.ServerConfig{})
	if defaults.ReadHeaderTimeout <= 0 || defaults.ReadTimeout <= 0 || defaults.WriteTimeout <= 0 || defaults.IdleTimeout <= 0 || defaults.MaxHeaderBytes != 32<<10 {
		t.Fatal("unsafe zero defaults")
	}
}
