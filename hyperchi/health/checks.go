// Package health runs named health checks concurrently and serves the results
// as JSON on health, liveness, and readiness endpoints. It includes checks for
// a database, an HTTP dependency, memory, and goroutine count.
package health

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"time"
)

// Status is the health of one check or of the whole service.
type Status string

// Health statuses. Any unhealthy check makes the service unhealthy. Otherwise
// any degraded check makes it degraded.
const (
	StatusHealthy   Status = "healthy"
	StatusUnhealthy Status = "unhealthy"
	StatusDegraded  Status = "degraded"
	StatusUnknown   Status = "unknown"
)

// CheckResult is one check's outcome. HealthChecker.Check fills in Name,
// Duration, and Timestamp, overwriting what the check returned.
type CheckResult struct {
	Name      string         `json:"name"`
	Status    Status         `json:"status"`
	Message   string         `json:"message,omitempty"`
	Error     string         `json:"error,omitempty"`
	Duration  time.Duration  `json:"duration"`
	Timestamp time.Time      `json:"timestamp"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// OverallHealth is the combined result of every check, with service details
// and runtime statistics.
type OverallHealth struct {
	Status      Status        `json:"status"`
	Timestamp   time.Time     `json:"timestamp"`
	Version     string        `json:"version,omitempty"`
	Service     string        `json:"service,omitempty"`
	Environment string        `json:"environment,omitempty"`
	Uptime      time.Duration `json:"uptime"`
	Checks      []CheckResult `json:"checks"`
	System      SystemInfo    `json:"system"`
}

// SystemInfo is a snapshot of Go runtime statistics. MemoryMB is the memory
// obtained from the OS (runtime.MemStats.Sys), and MemoryAlloc is the live
// heap.
type SystemInfo struct {
	GoVersion    string  `json:"go_version"`
	GOOS         string  `json:"goos"`
	GOARCH       string  `json:"goarch"`
	NumCPU       int     `json:"num_cpu"`
	NumGoroutine int     `json:"num_goroutine"`
	MemoryMB     float64 `json:"memory_mb"`
	MemoryAlloc  float64 `json:"memory_alloc_mb"`
}

// Checker is one health check. Check must return once ctx is done: the
// HealthChecker waits for every check, and its timeout only cancels ctx.
type Checker interface {
	Check(ctx context.Context) CheckResult
}

// CheckFunc adapts a function to Checker.
type CheckFunc func(ctx context.Context) CheckResult

// Check calls f(ctx).
func (f CheckFunc) Check(ctx context.Context) CheckResult {
	return f(ctx)
}

// HealthChecker holds named checks and runs them. Adding and removing checks
// is safe while it serves, but SetTimeout is not.
type HealthChecker struct {
	service     string
	version     string
	environment string
	startTime   time.Time
	checks      map[string]Checker
	timeout     time.Duration
	mu          sync.RWMutex
}

// NewHealthChecker returns a HealthChecker with no checks, a 10-second
// per-check timeout, and its uptime counted from now.
func NewHealthChecker(service, version, environment string) *HealthChecker {
	return &HealthChecker{
		service:     service,
		version:     version,
		environment: environment,
		startTime:   time.Now(),
		checks:      make(map[string]Checker),
		timeout:     10 * time.Second,
	}
}

// SetTimeout sets the per-check timeout, which HTTPHandler also applies to
// the whole request.
func (hc *HealthChecker) SetTimeout(timeout time.Duration) *HealthChecker {
	hc.timeout = timeout
	return hc
}

// AddCheck registers checker under name, replacing any check with that name.
func (hc *HealthChecker) AddCheck(name string, checker Checker) *HealthChecker {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	hc.checks[name] = checker
	return hc
}

// AddCheckFunc registers checkFunc under name.
func (hc *HealthChecker) AddCheckFunc(name string, checkFunc func(ctx context.Context) CheckResult) *HealthChecker {
	return hc.AddCheck(name, CheckFunc(checkFunc))
}

// RemoveCheck removes the check registered under name, if any.
func (hc *HealthChecker) RemoveCheck(name string) *HealthChecker {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	delete(hc.checks, name)
	return hc
}

// Check runs every check concurrently, each with its own timeout, and
// returns the combined result. A panicking check counts as unhealthy.
// Results arrive in completion order.
func (hc *HealthChecker) Check(ctx context.Context) *OverallHealth {
	hc.mu.RLock()
	checks := make(map[string]Checker)
	for name, checker := range hc.checks {
		checks[name] = checker
	}
	hc.mu.RUnlock()

	results := make([]CheckResult, 0, len(checks))
	resultsChan := make(chan CheckResult, len(checks))

	for name, checker := range checks {
		go func(n string, c Checker) {
			checkCtx, cancel := context.WithTimeout(ctx, hc.timeout)
			defer cancel()

			start := time.Now()
			defer func() {
				if r := recover(); r != nil {
					resultsChan <- CheckResult{
						Name:      n,
						Status:    StatusUnhealthy,
						Error:     fmt.Sprintf("panic: %v", r),
						Duration:  time.Since(start),
						Timestamp: time.Now(),
					}
				}
			}()

			result := c.Check(checkCtx)
			result.Name = n
			result.Duration = time.Since(start)
			result.Timestamp = time.Now()
			resultsChan <- result
		}(name, checker)
	}

	for i := 0; i < len(checks); i++ {
		results = append(results, <-resultsChan)
	}

	overallStatus := StatusHealthy
	for _, result := range results {
		switch result.Status {
		case StatusUnhealthy:
			overallStatus = StatusUnhealthy
		case StatusDegraded:
			if overallStatus == StatusHealthy {
				overallStatus = StatusDegraded
			}
		}
	}

	return &OverallHealth{
		Status:      overallStatus,
		Timestamp:   time.Now(),
		Version:     hc.version,
		Service:     hc.service,
		Environment: hc.environment,
		Uptime:      time.Since(hc.startTime),
		Checks:      results,
		System:      getSystemInfo(),
	}
}

// HTTPHandler serves the full OverallHealth as JSON: 503 when unhealthy, 200
// otherwise. The body includes check error text, metadata such as dependency
// URLs, and runtime statistics, so keep this endpoint off the public internet.
func (hc *HealthChecker) HTTPHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), hc.timeout)
		defer cancel()

		health := hc.Check(ctx)

		statusCode := http.StatusOK
		switch health.Status {
		case StatusUnhealthy:
			statusCode = http.StatusServiceUnavailable
		case StatusDegraded:
			// A degraded service still serves, so load balancers keep it.
			statusCode = http.StatusOK
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)

		if err := json.NewEncoder(w).Encode(health); err != nil {
			http.Error(w, "Failed to encode health response", http.StatusInternalServerError)
		}
	}
}

// LivenessHandler answers 200 with the service name and time, without running
// any check: it shows only that the process is serving.
func (hc *HealthChecker) LivenessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]any{
			"status":    "alive",
			"timestamp": time.Now(),
			"service":   hc.service,
		}); err != nil {
			http.Error(w, "Failed to encode health response", http.StatusInternalServerError)
		}
	}
}

// ReadinessHandler runs the checks within 5 seconds and answers 200 only when
// every check is healthy, 503 otherwise, degraded included. The body has the
// overall status but no check details.
func (hc *HealthChecker) ReadinessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		health := hc.Check(ctx)

		ready := health.Status == StatusHealthy
		statusCode := http.StatusOK
		if !ready {
			statusCode = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)

		if err := json.NewEncoder(w).Encode(map[string]any{
			"ready":     ready,
			"status":    health.Status,
			"timestamp": time.Now(),
			"service":   hc.service,
		}); err != nil {
			http.Error(w, "Failed to encode health response", http.StatusInternalServerError)
		}
	}
}

// getSystemInfo reads the current runtime statistics.
func getSystemInfo() SystemInfo {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return SystemInfo{
		GoVersion:    runtime.Version(),
		GOOS:         runtime.GOOS,
		GOARCH:       runtime.GOARCH,
		NumCPU:       runtime.NumCPU(),
		NumGoroutine: runtime.NumGoroutine(),
		MemoryMB:     float64(m.Sys) / 1024 / 1024,
		MemoryAlloc:  float64(m.Alloc) / 1024 / 1024,
	}
}

// DatabaseCheck pings db and reports its connection pool counts. A nil db or a
// failed ping is unhealthy, with the driver's error text in the result.
func DatabaseCheck(db *sql.DB) Checker {
	return CheckFunc(func(ctx context.Context) CheckResult {
		if db == nil {
			return CheckResult{
				Status:  StatusUnhealthy,
				Message: "Database connection is nil",
			}
		}

		if err := db.PingContext(ctx); err != nil {
			return CheckResult{
				Status: StatusUnhealthy,
				Error:  err.Error(),
			}
		}

		stats := db.Stats()
		metadata := map[string]any{
			"open_connections": stats.OpenConnections,
			"in_use":           stats.InUse,
			"idle":             stats.Idle,
		}

		return CheckResult{
			Status:   StatusHealthy,
			Message:  "Database connection successful",
			Metadata: metadata,
		}
	})
}

// HTTPServiceCheck GETs url: a 2xx answer is healthy, any other status is
// degraded, and a failed request is unhealthy. A zero timeout means 5 seconds.
// The returned Checker is safe for concurrent use.
func HTTPServiceCheck(name, url string, timeout time.Duration) Checker {
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	client := &http.Client{Timeout: timeout}

	return CheckFunc(func(ctx context.Context) CheckResult {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return CheckResult{
				Status: StatusUnhealthy,
				Error:  fmt.Sprintf("failed to create request: %v", err),
			}
		}

		resp, err := client.Do(req)
		if err != nil {
			return CheckResult{
				Status: StatusUnhealthy,
				Error:  fmt.Sprintf("request failed: %v", err),
			}
		}
		defer func() { _ = resp.Body.Close() }() // best-effort close

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return CheckResult{
				Status:  StatusHealthy,
				Message: fmt.Sprintf("%s is reachable", name),
				Metadata: map[string]any{
					"status_code": resp.StatusCode,
					"url":         url,
				},
			}
		}

		return CheckResult{
			Status: StatusDegraded,
			Error:  fmt.Sprintf("unexpected status code: %d", resp.StatusCode),
			Metadata: map[string]any{
				"status_code": resp.StatusCode,
				"url":         url,
			},
		}
	})
}

// MemoryCheck compares the live heap (MemStats.Alloc) with maxMemoryMB: above
// it is unhealthy, above 80% of it degraded.
func MemoryCheck(maxMemoryMB float64) Checker {
	return CheckFunc(func(ctx context.Context) CheckResult {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)

		allocMB := float64(m.Alloc) / 1024 / 1024
		sysMB := float64(m.Sys) / 1024 / 1024

		metadata := map[string]any{
			"alloc_mb":      allocMB,
			"sys_mb":        sysMB,
			"num_gc":        m.NumGC,
			"goroutines":    runtime.NumGoroutine(),
			"max_memory_mb": maxMemoryMB,
		}

		if allocMB > maxMemoryMB {
			return CheckResult{
				Status:   StatusUnhealthy,
				Message:  fmt.Sprintf("Memory usage too high: %.2f MB > %.2f MB", allocMB, maxMemoryMB),
				Metadata: metadata,
			}
		}

		if allocMB > maxMemoryMB*0.8 {
			return CheckResult{
				Status:   StatusDegraded,
				Message:  fmt.Sprintf("Memory usage high: %.2f MB", allocMB),
				Metadata: metadata,
			}
		}

		return CheckResult{
			Status:   StatusHealthy,
			Message:  fmt.Sprintf("Memory usage normal: %.2f MB", allocMB),
			Metadata: metadata,
		}
	})
}

// GoroutineCheck compares the goroutine count with maxGoroutines: above it is
// unhealthy, above 80% of it degraded.
func GoroutineCheck(maxGoroutines int) Checker {
	return CheckFunc(func(ctx context.Context) CheckResult {
		count := runtime.NumGoroutine()

		metadata := map[string]any{
			"goroutines":     count,
			"max_goroutines": maxGoroutines,
		}

		if count > maxGoroutines {
			return CheckResult{
				Status:   StatusUnhealthy,
				Message:  fmt.Sprintf("Too many goroutines: %d > %d", count, maxGoroutines),
				Metadata: metadata,
			}
		}

		if count > int(float64(maxGoroutines)*0.8) {
			return CheckResult{
				Status:   StatusDegraded,
				Message:  fmt.Sprintf("High goroutine count: %d", count),
				Metadata: metadata,
			}
		}

		return CheckResult{
			Status:   StatusHealthy,
			Message:  fmt.Sprintf("Goroutine count normal: %d", count),
			Metadata: metadata,
		}
	})
}
