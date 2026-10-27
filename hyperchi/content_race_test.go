package hyperchi

import (
	"fmt"
	"sync"
	"testing"
)

// TestContentManagerConcurrentAccess: ContentManager.Get runs in concurrent
// handlers and writes cm.cache on a miss, while Create, Update, and Delete
// change cm.cache and col.Items. Without the RWMutex this is a fatal
// "concurrent map writes" crash and a slice data race. Run with -race.
func TestContentManagerConcurrentAccess(t *testing.T) {
	app := New()
	cm := app.Content
	cm.Collection("posts", "posts", nil)

	// Seed items, then drop them from the cache so concurrent Get calls take
	// the miss path that writes cm.cache.
	const seed = 30
	for i := 0; i < seed; i++ {
		if err := cm.Create("posts", &ContentItem{
			Slug:     fmt.Sprintf("seed-%d", i),
			Title:    "seed",
			Metadata: map[string]any{"k": "v"},
		}); err != nil {
			t.Fatalf("seed Create: %v", err)
		}
	}
	cm.mu.Lock()
	cm.cache = make(map[contentKey]*ContentItem)
	cm.mu.Unlock()

	var wg sync.WaitGroup
	const workers = 16
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				switch i % 5 {
				case 0:
					// Concurrent uncached Get on the same keys → miss-write path.
					_, _ = cm.Get("posts", fmt.Sprintf("seed-%d", i%seed))
				case 1:
					_ = cm.Create("posts", &ContentItem{
						Slug:     fmt.Sprintf("w%d-i%d", w, i),
						Title:    "x",
						Metadata: map[string]any{"k": "v"},
					})
				case 2:
					_ = cm.Update("posts", fmt.Sprintf("seed-%d", i%seed), map[string]any{"title": "updated"})
				case 3:
					_, _ = cm.Query("posts").Where("k", "v").All()
				case 4:
					_ = cm.Delete("posts", fmt.Sprintf("w%d-i%d", w, i-4))
				}
			}
		}(w)
	}
	wg.Wait()
}
