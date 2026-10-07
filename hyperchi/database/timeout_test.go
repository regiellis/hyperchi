package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestTimeoutAppliesToEveryConnection(t *testing.T) {
	db, err := NewDB(&DBConfig{Path: filepath.Join(t.TempDir(), "test.db"), Timeout: 75 * time.Millisecond, MaxOpenConns: 2, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	first, err := db.Raw().Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := first.Close(); err != nil {
			t.Errorf("close first conn: %v", err)
		}
	})
	second, err := db.Raw().Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("close second conn: %v", err)
		}
	})
	for _, conn := range []*sql.Conn{first, second} {
		var timeout int
		if err := conn.QueryRowContext(context.Background(), "PRAGMA busy_timeout").Scan(&timeout); err != nil {
			t.Fatal(err)
		}
		if timeout != 75 {
			t.Errorf("timeout %d, want 75", timeout)
		}
	}
}
