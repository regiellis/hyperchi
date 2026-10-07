package tests

import (
	"fmt"
	"os"
	"testing"
)

// TestMain fails the run if any test leaves a database file in this source
// directory. Fixtures belong in t.TempDir(); hyperchi.New() resolves its
// default database path against the working directory, which is tests/ here.
func TestMain(m *testing.M) {
	leftovers := []string{"hyperchi.db", "hyperchi.db-wal", "hyperchi.db-shm", "hyperchi.db-journal"}
	before := map[string]bool{}
	for _, name := range leftovers {
		if _, err := os.Stat(name); err == nil {
			before[name] = true
		}
	}
	code := m.Run()
	for _, name := range leftovers {
		if _, err := os.Stat(name); err == nil && !before[name] {
			fmt.Fprintf(os.Stderr, "tests wrote %s into the source tree; use t.TempDir()\n", name)
			if code == 0 {
				code = 1
			}
		}
	}
	os.Exit(code)
}
