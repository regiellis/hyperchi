// Package performance is an optional HyperChi module with its own go.mod. It
// adds a BadgerDB key-value store, a rate limiter over any KV store (fixed
// window, sliding window, token bucket), and a job queue backed by KV.
package performance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
)

// DefaultWorkers is the BadgerDB compactor count Install uses when
// Config.Workers is 0.
const DefaultWorkers = 4

// IntegrationName is the name Install registers the module under in
// app.Integrations().
const IntegrationName = "performance"

// Module owns the store, rate limiter, and job queue that Install creates.
// Install registers it with the app's integration manager, which starts the
// job queue with the app and closes everything when the app closes.
type Module struct {
	config  Config
	stores  map[string]hyperchi.KVStore
	limiter *RateLimiter
	queue   *JobQueue
}

// Config configures the performance module.
type Config struct {
	Dir        string // BadgerDB data directory
	SyncWrites bool   // fsync every BadgerDB write
	// Workers is the BadgerDB compactor count. 0 uses DefaultWorkers.
	// Badger needs at least 2, so Install rejects 1 and negative values.
	Workers int

	// BadgerLogger receives Badger's log lines; nil uses slog.Default().
	// BadgerLogLevel is the lowest level passed on; nil means slog.LevelWarn,
	// so Badger's routine INFO lines are dropped.
	BadgerLogger   *slog.Logger
	BadgerLogLevel slog.Leveler

	// EnableRateLimit builds a RateLimiter on the Badger store. When
	// RateLimitRules is not empty, Install also adds its middleware to the
	// app with app.Use, so Install must run before routes are registered.
	EnableRateLimit    bool
	RateLimitRules     []RateLimitRule
	RateLimitAlgorithm RateLimitAlgorithm // FixedWindow by default

	EnableJobQueue bool
	JobWorkers     int
	RetryLimit     int
}

// DefaultConfig returns a Config with data in ./data/performance and both the
// rate limiter and the job queue enabled. It has no rate limit rules, so no
// requests are limited until some are added.
func DefaultConfig() Config {
	return Config{
		Dir:             "./data/performance",
		SyncWrites:      false,
		Workers:         DefaultWorkers,
		EnableRateLimit: true,
		EnableJobQueue:  true,
		JobWorkers:      8,
		RetryLimit:      3,
	}
}

// New returns a Module for config. An empty Dir becomes ./data/performance.
func New(config Config) *Module {
	if config.Dir == "" {
		config.Dir = "./data/performance"
	}

	return &Module{
		config: config,
		stores: make(map[string]hyperchi.KVStore),
	}
}

// Install opens the BadgerDB store, registers it in the app's KV registry as
// "badger", builds the rate limiter and job queue the config enables, and
// registers the module as the integration IntegrationName. Rate limit rules
// are validated here, and their middleware is added with app.Use: install
// any client IP or authentication middleware first, and call Install before
// registering routes. Refused requests go through the app's error pipeline
// (see RateLimitConfig.App).
//
// The job queue's workers start when the app starts its integrations
// (app.Serve does, or call app.Integrations().Start). app.Close, which Serve
// calls on shutdown, stops the workers and then closes the store.
func (m *Module) Install(app *hyperchi.HyperChi) error {
	var rules []RateLimitRule
	if m.config.EnableRateLimit {
		var err error
		if rules, err = PrepareRateLimitRules(m.config.RateLimitRules); err != nil {
			return err
		}
	}

	workers := m.config.Workers
	switch {
	case workers == 0:
		workers = DefaultWorkers
	case workers < 2:
		return fmt.Errorf("performance: Config.Workers is %d: Badger needs at least 2 compactors (0 uses %d)", workers, DefaultWorkers)
	}

	badgerConfig := DefaultBadgerConfig(m.config.Dir)
	badgerConfig.SyncWrites = m.config.SyncWrites
	badgerConfig.NumCompactors = workers
	badgerConfig.Logger = m.config.BadgerLogger
	badgerConfig.LogLevel = m.config.BadgerLogLevel

	store, err := NewBadgerAdvancedStore(badgerConfig)
	if err != nil {
		return fmt.Errorf("failed to create BadgerDB store: %w", err)
	}

	registry := app.GetKVRegistry()
	if err := registry.Register("badger", store); err != nil {
		return errors.Join(fmt.Errorf("failed to register BadgerDB store: %w", err), store.Close())
	}
	// From here the registry owns the store too: app.Close closes it even if
	// Install fails below. Closing it twice is safe.
	m.stores["badger"] = store

	if m.config.EnableRateLimit {
		limiter, err := NewRateLimiter(registry, RateLimitConfig{
			Backend:   "badger",
			KeyPrefix: "ratelimit:",
			Algorithm: m.config.RateLimitAlgorithm,
			App:       app,
		})
		if err != nil {
			return fmt.Errorf("failed to create rate limiter: %w", err)
		}
		m.limiter = limiter
	}

	if m.config.EnableJobQueue {
		queue, err := NewJobQueue(registry, JobQueueConfig{
			Backend:       "badger",
			KeyPrefix:     "jobs:",
			Workers:       m.config.JobWorkers,
			RetryLimit:    m.config.RetryLimit,
			ProcessingTTL: 10 * time.Minute,
		})
		if err != nil {
			return fmt.Errorf("failed to create job queue: %w", err)
		}
		m.queue = queue
	}

	if err := app.Integrations().Register(IntegrationName, m); err != nil {
		return fmt.Errorf("failed to register performance integration: %w", err)
	}

	if len(rules) > 0 {
		app.Use(m.limiter.RateLimitMiddleware(rules))
	}
	return nil
}

// RateLimiter returns the limiter Install built, or nil when EnableRateLimit
// is off. Use it for routes the configured rules do not cover.
func (m *Module) RateLimiter() *RateLimiter {
	return m.limiter
}

// JobQueue returns the queue Install built, or nil when EnableJobQueue is
// off. Register handlers and push jobs on it.
func (m *Module) JobQueue() *JobQueue {
	return m.queue
}

// GetStore returns the store Install registered under name ("badger"), or nil.
func (m *Module) GetStore(name string) hyperchi.KVStore {
	return m.stores[name]
}

// Start starts the job queue's workers. The integration manager calls it.
func (m *Module) Start(context.Context) error {
	if m.queue != nil {
		m.queue.Start()
	}
	return nil
}

// Close stops the job queue, waiting for running jobs until ctx is done, then
// closes the stores the module opened. When ctx ends first, the stores are
// closed anyway: a job still running then gets hyperchi.ErrStoreClosed from
// the store. The integration manager calls Close when the app closes. It is
// safe to call more than once.
func (m *Module) Close(ctx context.Context) error {
	var errs []error
	if m.queue != nil {
		stopped := make(chan struct{})
		go func() {
			m.queue.Stop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-ctx.Done():
			errs = append(errs, fmt.Errorf("stop job queue: %w", ctx.Err()))
		}
	}
	for name, store := range m.stores {
		if err := store.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close %s store: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// Stats returns the BadgerDB statistics and the module's config.
func (m *Module) Stats() map[string]any {
	stats := make(map[string]any)

	if store, ok := m.stores["badger"].(*BadgerAdvancedStore); ok {
		stats["badger"] = store.GetStats()
	}

	stats["config"] = m.config
	return stats
}
