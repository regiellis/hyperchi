package performance_test

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/modules/performance"
)

// Install opens the Badger store, adds the rate limit middleware, and
// registers the module as the "performance" integration. app.Serve starts
// the job workers, and on shutdown stops them and closes the store.
func Example_install() {
	app := hyperchi.New()

	config := performance.DefaultConfig()
	config.Dir = "./data/performance"
	config.RateLimitRules = []performance.RateLimitRule{
		{Path: "/login", Method: http.MethodPost, Rate: "5/minute", PerIP: true},
		{Path: "/api/*", Rate: "100/minute", PerUser: true},
	}
	perf := performance.New(config)
	// Install adds the rate limit middleware with app.Use, so it runs
	// before any route is registered.
	if err := perf.Install(app); err != nil {
		log.Fatal(err)
	}

	perf.JobQueue().RegisterHandler("welcome-email", func(job *performance.Job) error {
		address, ok := job.Payload["email"].(string)
		if !ok {
			return fmt.Errorf("job %s: payload has no email", job.ID)
		}
		log.Printf("sending the welcome email to %s", address)
		return nil
	})

	app.Post("/signup", func(c *hyperchi.Context) error {
		payload := map[string]any{"email": c.FormValue("email")}
		if _, err := perf.JobQueue().Push("welcome-email", payload); err != nil {
			return err
		}
		return c.Text("Check your inbox")
	})

	if err := app.Serve("0.0.0.0:8080"); err != nil {
		log.Fatal(err)
	}
}

// The Badger store is a hyperchi.AdvancedKVStore, so the framework's
// sessions and cache can keep their data in it, with TTLs.
func Example_sessionsAndCache() {
	app := hyperchi.New()

	perf := performance.New(performance.Config{Dir: "./data/performance"})
	if err := perf.Install(app); err != nil {
		log.Fatal(err)
	}

	// Install registered the store in the app's KV registry as "badger".
	if err := app.UseSessions(hyperchi.KVSessionConfig{Backend: "badger", MaxAge: 7 * 24 * time.Hour}); err != nil {
		log.Fatal(err)
	}
	if err := app.UseKVCache(hyperchi.KVCacheConfig{Backend: "badger", DefaultTTL: 10 * time.Minute}); err != nil {
		log.Fatal(err)
	}
	app.Use(app.SessionMiddleware())

	app.Get("/", func(c *hyperchi.Context) error {
		session := c.Session()
		session.Set("visits", session.GetInt("visits")+1)
		if err := session.Save(); err != nil {
			return err
		}
		return c.Text(fmt.Sprintf("visit %d", session.GetInt("visits")))
	})

	if err := app.Serve("0.0.0.0:8080"); err != nil {
		log.Fatal(err)
	}
}

// PushWithOptions sets a job's priority, start time, and attempts.
func ExampleJobQueue_PushWithOptions() {
	registry := hyperchi.NewKVRegistry()
	if err := registry.Register("memory", hyperchi.NewMemoryAdvancedStore()); err != nil {
		log.Fatal(err)
	}
	queue, err := performance.NewJobQueue(registry, performance.JobQueueConfig{Backend: "memory"})
	if err != nil {
		log.Fatal(err)
	}

	tonight := time.Now().Add(6 * time.Hour)
	job, err := queue.PushWithOptions("report", map[string]any{"month": "2026-09"}, performance.JobOptions{
		Priority:    10,
		ScheduleAt:  &tonight,
		MaxAttempts: 5,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(job.Status, job.MaxAttempts)
	// Output: pending 5
}

// A limiter can run outside an app, on any registered KV store. Check
// counts one request and reports whether it is allowed.
func ExampleRateLimiter_Check() {
	registry := hyperchi.NewKVRegistry()
	if err := registry.Register("memory", hyperchi.NewMemoryAdvancedStore()); err != nil {
		log.Fatal(err)
	}
	limiter, err := performance.NewRateLimiter(registry, performance.RateLimitConfig{
		Backend:   "memory",
		Algorithm: performance.TokenBucket,
	})
	if err != nil {
		log.Fatal(err)
	}

	rule := performance.RateLimitRule{MaxRequests: 2, WindowSize: time.Hour}
	for range 3 {
		result, err := limiter.Check(rule, "client-1")
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("allowed=%v remaining=%d\n", result.Allowed, result.Remaining)
	}
	// Output:
	// allowed=true remaining=1
	// allowed=true remaining=0
	// allowed=false remaining=0
}

// Without an app, RateLimitMiddleware wraps any http.Handler and answers a
// refusal with plain text.
func ExampleRateLimiter_RateLimitMiddleware() {
	registry := hyperchi.NewKVRegistry()
	if err := registry.Register("memory", hyperchi.NewMemoryAdvancedStore()); err != nil {
		log.Fatal(err)
	}
	limiter, err := performance.NewRateLimiter(registry, performance.RateLimitConfig{Backend: "memory"})
	if err != nil {
		log.Fatal(err)
	}
	rules, err := performance.PrepareRateLimitRules([]performance.RateLimitRule{
		{Path: "*", Rate: "60/minute", PerIP: true},
	})
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte("ok")); err != nil {
			log.Print(err)
		}
	})
	log.Fatal(http.ListenAndServe("0.0.0.0:8080", limiter.RateLimitMiddleware(rules)(mux)))
}

// Backup writes the whole database to one file; Restore loads it into a
// store, keeping keys the backup does not have.
func ExampleBadgerStore_Backup() {
	store, err := performance.NewBadgerAdvancedStore(performance.DefaultBadgerConfig("./data/performance"))
	if err != nil {
		log.Fatal(err)
	}
	if err := store.Backup("./backups/performance.bak"); err != nil {
		log.Fatal(err)
	}
	if err := store.Close(); err != nil {
		log.Fatal(err)
	}

	restored, err := performance.NewBadgerAdvancedStore(performance.DefaultBadgerConfig("./data/restored"))
	if err != nil {
		log.Fatal(err)
	}
	if err := restored.Restore("./backups/performance.bak"); err != nil {
		log.Fatal(err)
	}
	if err := restored.Close(); err != nil {
		log.Fatal(err)
	}
}
