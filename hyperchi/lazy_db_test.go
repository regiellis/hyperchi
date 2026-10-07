package hyperchi

import (
	"os"
	"testing"
)

func TestNewDoesNotCreateADatabaseUntilUsed(t *testing.T) {
	t.Chdir(t.TempDir())
	app := New()
	t.Cleanup(func() {
		if err := app.CloseDB(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	if _, err := os.Stat("hyperchi.db"); !os.IsNotExist(err) {
		t.Fatalf("New() created hyperchi.db before any query: %v", err)
	}
	if _, err := app.DB.Exec("CREATE TABLE t (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("hyperchi.db"); err != nil {
		t.Fatalf("first query did not create hyperchi.db: %v", err)
	}
}
