package hyperchi

import "github.com/regiellis/hyperchi/hyperchi/i18n"

// Locale returns the locale resolved by i18n.Bundle.Middleware, or empty when
// it is not installed. Pass this value into templates and catalog lookups.
func (c *Context) Locale() string { return i18n.Locale(c.Ctx()) }
