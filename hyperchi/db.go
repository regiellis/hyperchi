package hyperchi

import (
	"fmt"
	"log"

	"github.com/regiellis/hyperchi/hyperchi/database"
)

// InitDB replaces app.DB with a database opened from config, closing the old
// one first (a close failure is logged).
func (g *HyperChi) InitDB(config *database.DBConfig) error {
	if g.DB != nil {
		if err := g.DB.Close(); err != nil {
			log.Printf("hyperchi: failed to close existing database before reinitializing: %v", err)
		}
	}

	db, err := database.NewDB(config)
	if err != nil {
		return err
	}

	g.DB = db
	return nil
}

// SetDBPath replaces app.DB with the database at path, opened with
// database.DefaultDBConfig settings.
func (g *HyperChi) SetDBPath(path string) error {
	config := database.DefaultDBConfig()
	config.Path = path
	return g.InitDB(config)
}

// Migrate runs migrations in order through app.DB.AutoMigrate, stopping at
// the first failure. It fails when the app has no database.
func (g *HyperChi) Migrate(migrations ...string) error {
	if g.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	return g.DB.AutoMigrate(migrations...)
}

// CloseDB closes app.DB, if there is one.
func (g *HyperChi) CloseDB() error {
	if g.DB != nil {
		return g.DB.Close()
	}
	return nil
}
