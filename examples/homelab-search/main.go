// Homelab search: a read-only, self-hosted search over a directory of
// manuals, notes, configs, and runbooks, built on HyperChi. See README.md.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/config"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// loadConfig reads the framework configuration from the environment
// (ENVIRONMENT, HOST, PORT, DB_PATH, and the rest) and applies this app's
// defaults on top.
func loadConfig() *config.Config {
	cfg := config.FromEnv()
	if os.Getenv("DB_PATH") == "" && os.Getenv("DB_NAME") == "" {
		cfg.Database.Name = "homelab-search.db"
	}
	// Development takes the next free port when 8080 is busy. Production binds
	// exactly the address it was given.
	cfg.Server.PortFallback = cfg.IsDevelopment()
	return cfg
}

func run() error {
	cfg := loadConfig()
	opts, err := optionsFromEnv()
	if err != nil {
		return err
	}
	s, err := newSite(cfg, opts)
	if err != nil {
		return err
	}
	if cfg.IsProduction() && opts.AuthUser == "" {
		log.Printf("homelab-search: production mode without BASIC_AUTH_USER: anyone who can reach %s can read the library", cfg.Server.Address())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := s.start(ctx); err != nil {
		return errors.Join(err, s.close())
	}
	log.Printf("homelab-search: library %s (%s mode)", s.ix.dir, cfg.Server.Environment)
	serveErr := s.app.ServeContext(ctx, cfg.Server.Address())
	stop()
	return errors.Join(serveErr, s.close())
}

// options are the app's own settings; the framework's come from config.FromEnv.
type options struct {
	LibraryDir    string        // LIBRARY_DIR, default ./library
	MaxFileBytes  int64         // MAX_FILE_BYTES: larger files are listed, not indexed
	MaxRawBytes   int64         // MAX_RAW_BYTES: larger files cannot be downloaded through the app
	WatchInterval time.Duration // WATCH_INTERVAL: how often the library is polled for changes
	AuthUser      string        // BASIC_AUTH_USER and BASIC_AUTH_PASSWORD: both or neither
	AuthPassword  string
}

func defaultOptions() options {
	return options{
		LibraryDir:    "library",
		MaxFileBytes:  1 << 20,
		MaxRawBytes:   32 << 20,
		WatchInterval: 2 * time.Second,
	}
}

func optionsFromEnv() (options, error) {
	o := defaultOptions()
	if v := os.Getenv("LIBRARY_DIR"); v != "" {
		o.LibraryDir = v
	}
	for _, setting := range []struct {
		name string
		dst  *int64
	}{{"MAX_FILE_BYTES", &o.MaxFileBytes}, {"MAX_RAW_BYTES", &o.MaxRawBytes}} {
		if v := os.Getenv(setting.name); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n <= 0 {
				return o, fmt.Errorf("%s must be a positive number of bytes, got %q", setting.name, v)
			}
			*setting.dst = n
		}
	}
	if v := os.Getenv("WATCH_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return o, fmt.Errorf("WATCH_INTERVAL must be a positive duration such as 2s, got %q", v)
		}
		o.WatchInterval = d
	}
	o.AuthUser, o.AuthPassword = os.Getenv("BASIC_AUTH_USER"), os.Getenv("BASIC_AUTH_PASSWORD")
	if (o.AuthUser == "") != (o.AuthPassword == "") {
		return o, errors.New("set both BASIC_AUTH_USER and BASIC_AUTH_PASSWORD, or neither")
	}
	return o, nil
}
