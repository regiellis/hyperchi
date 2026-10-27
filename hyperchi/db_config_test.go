package hyperchi

import (
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/config"
)

// Query logging prints every statement with its arguments, which can be whole
// documents or personal data, so it is opt-in even in development.
func TestDatabaseQueryLoggingIsOptIn(t *testing.T) {
	if databaseConfigFrom(config.Default()).Debug {
		t.Fatal("development logs every query by default")
	}
	t.Setenv("DB_LOG_QUERIES", "true")
	if !databaseConfigFrom(config.FromEnv()).Debug {
		t.Fatal("DB_LOG_QUERIES=true did not enable query logging")
	}
}
