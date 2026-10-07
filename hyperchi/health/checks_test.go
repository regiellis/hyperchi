package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// The zero-timeout default used to be written into the captured variable on
// every Check, a data race once two checks ran at once. Run with -race.
func TestHTTPServiceCheckConcurrentChecks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	check := HTTPServiceCheck("upstream", srv.URL, 0)
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]CheckResult, 8)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i] = check.Check(context.Background())
		}()
	}
	close(start)
	wg.Wait()

	for i, res := range results {
		if res.Status != StatusHealthy {
			t.Fatalf("check %d: status %s, error %q", i, res.Status, res.Error)
		}
	}
}
