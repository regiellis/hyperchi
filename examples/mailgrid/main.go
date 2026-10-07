// Mailgrid is an internal tool for a small team: import recipient lists from
// CSV, write letters with merge fields, and render a letter for every
// recipient into an outbox. It never sends email; the outbox is exported as
// CSV or .eml files for whatever system does the sending.
//
// Run it with `go run .` or `air`. Configuration comes from the environment;
// see README.md.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
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

func run() error {
	cfg := config.FromEnv()
	if os.Getenv("DB_PATH") == "" && os.Getenv("DB_NAME") == "" {
		cfg.Database.Name = filepath.Join("data", "mailgrid.db")
	}
	// Assets are embedded; there is nothing on disk to hot-reload.
	cfg.Assets.HotReload = false
	// Development moves to the next free port when the configured one is
	// taken; production binds exactly what it was given.
	cfg.Server.PortFallback = cfg.IsDevelopment()
	if err := os.MkdirAll(filepath.Dir(cfg.Database.Name), 0o750); err != nil {
		return err
	}

	pace, err := durationEnv("MAILGRID_PACE", 0)
	if err != nil {
		return err
	}
	if os.Getenv("MAILGRID_PACE") == "" && cfg.IsDevelopment() {
		pace = 75 * time.Millisecond
	}
	maxUpload := int64(2 << 20)
	if v := os.Getenv("MAILGRID_MAX_UPLOAD"); v != "" {
		if maxUpload, err = strconv.ParseInt(v, 10, 64); err != nil || maxUpload <= 0 {
			return fmt.Errorf("MAILGRID_MAX_UPLOAD must be a positive number of bytes, got %q", v)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s, err := NewServer(Options{
		Config:        cfg,
		SessionPath:   filepath.Join(filepath.Dir(cfg.Database.Name), "sessions.db"),
		Pace:          pace,
		ProgressEvery: 150 * time.Millisecond,
		MaxUpload:     maxUpload,
		From:          os.Getenv("MAILGRID_FROM"),
		SeedDemo:      cfg.IsDevelopment(),
		AdminEmail:    os.Getenv("MAILGRID_ADMIN_EMAIL"),
		AdminPassword: os.Getenv("MAILGRID_ADMIN_PASSWORD"),
		AdminName:     os.Getenv("MAILGRID_ADMIN_NAME"),
		Shutdown:      ctx,
	})
	if err != nil {
		return err
	}
	log.Printf("mailgrid: %s mode, database %s; letters go to the outbox and are never emailed",
		cfg.Server.Environment, cfg.Database.Name)
	// ServeContext logs every address it answers on and drains requests and
	// jobs when ctx ends.
	return s.app.ServeContext(ctx, cfg.Server.Address())
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%s must be a duration such as 100ms, got %q", key, v)
	}
	return d, nil
}
