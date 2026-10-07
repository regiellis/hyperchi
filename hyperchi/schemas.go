package hyperchi

import (
	"sync"

	"github.com/regiellis/hyperchi/hyperchi/validator"
)

// SchemaRegistry holds named validator schemas for c.ValidateSchema, for input
// whose shape is only known at runtime. Request input with a fixed shape is
// simpler with hyperchi.Bind and validate tags on a struct.
//
//	app.Schemas.Register("signup", validator.NewSchema().
//		Field("email", validator.String().Required().Email()))
//
//	data, err := c.ValidateSchema("signup")
type SchemaRegistry struct {
	mu      sync.RWMutex
	schemas map[string]*validator.Schema
}

func newSchemaRegistry() *SchemaRegistry {
	return &SchemaRegistry{schemas: make(map[string]*validator.Schema)}
}

// Register stores schema under name, replacing any earlier one.
func (r *SchemaRegistry) Register(name string, schema *validator.Schema) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.schemas[name] = schema
}

// Get returns the schema registered under name.
func (r *SchemaRegistry) Get(name string) (*validator.Schema, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.schemas[name]
	return s, ok
}

// Len reports how many schemas are registered.
func (r *SchemaRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.schemas)
}

// fieldMessages groups a validator result's errors by field.
func fieldMessages(errs []validator.ValidationError) map[string][]string {
	fields := make(map[string][]string, len(errs))
	for _, e := range errs {
		fields[e.Field] = append(fields[e.Field], e.Message)
	}
	return fields
}
