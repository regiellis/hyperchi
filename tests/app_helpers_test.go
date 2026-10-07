package tests

import (
	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/config"
)

// newFormApp is hyperchi.New with token CSRF off. These suites post forms
// through AutoMiddleware to test routing and validation; the CSRF middleware
// AutoMiddleware installs by default has its own tests in the framework
// package.
func newFormApp() *hyperchi.HyperChi {
	cfg := config.Default()
	cfg.Security.CSRF = false
	return hyperchi.NewWithConfig(cfg)
}
