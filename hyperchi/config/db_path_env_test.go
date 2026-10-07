package config

import "testing"

func TestFromEnvReadsDatabasePath(t *testing.T) {
	cases := []struct {
		name, dbPath, dbName, want string
	}{
		{"DB_PATH as the deployment files set it", "/app/data/app.db", "", "/app/data/app.db"},
		{"DB_NAME still honored", "", "legacy.db", "legacy.db"},
		{"DB_PATH wins over DB_NAME", "/app/data/app.db", "legacy.db", "/app/data/app.db"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DB_PATH", tc.dbPath)
			t.Setenv("DB_NAME", tc.dbName)
			if got := FromEnv().Database.Name; got != tc.want {
				t.Fatalf("Database.Name = %q, want %q", got, tc.want)
			}
		})
	}
}
