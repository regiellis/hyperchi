package content

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
)

// LoadRejectedError means a load did not publish any new collections. The
// previous snapshot remains available. Ordinary per-file schema failures are
// not rejected loads: they retain the legacy partial-load behavior.
type LoadRejectedError struct{ Err error }

func (e *LoadRejectedError) Error() string { return "content load rejected: " + e.Err.Error() }
func (e *LoadRejectedError) Unwrap() error { return e.Err }

// References declares a reference to content IDs in another collection. String
// fields store one ID; slices store a list of IDs. Targets are checked after
// all collections have loaded, so forward references and cycles are allowed.
func (fb *FieldBuilder) References(collection string) *FieldBuilder {
	fb.fieldDef.Reference = collection
	return fb
}

// Elements constrains a slice's elements to a scalar field type. It also
// supplies the element type used by GenerateTypes. An unset type means any.
func (fb *FieldBuilder) Elements(typ FieldType) *FieldBuilder {
	fb.fieldDef.ElementType = typ
	return fb
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func validReferenceDefinition(field FieldDefinition) error {
	if field.Reference == "" {
		return nil
	}
	if field.Type != FieldTypeString && field.Type != FieldTypeSlice {
		return fmt.Errorf("reference requires a string or string slice")
	}
	if field.Type == FieldTypeSlice && field.ElementType != "" && field.ElementType != FieldTypeString {
		return fmt.Errorf("reference slice elements must be strings")
	}
	return nil
}

// ValidateRelations checks all reference fields against one collection
// snapshot. Errors are returned in collection, field, and document ID order.
// IDs are exact and collection-scoped; slugs are never used. Nil, failed, or
// missing targets and duplicate IDs within a relation list are errors.
// No caller-owned collection or document is changed.
func ValidateRelations(collections map[string]*Collection) error {
	errs := []error{}
	for _, name := range sortedKeys(collections) {
		collection := collections[name]
		if collection == nil {
			errs = append(errs, fmt.Errorf("content: nil collection %q", name))
			continue
		}
		if collection.Schema == nil {
			continue
		}
		for _, fieldName := range sortedKeys(collection.Schema.Fields) {
			field := collection.Schema.Fields[fieldName]
			if field.Reference == "" {
				continue
			}
			if err := validReferenceDefinition(field); err != nil {
				errs = append(errs, fmt.Errorf("content: %s.%s: %w", name, fieldName, err))
				continue
			}
			if collections[field.Reference] == nil {
				errs = append(errs, fmt.Errorf("content: %s.%s references missing collection %q", name, fieldName, field.Reference))
				continue
			}
			for _, id := range sortedKeys(collection.Contents) {
				item := collection.Contents[id]
				if item == nil {
					errs = append(errs, fmt.Errorf("content: nil document %s/%s", name, id))
					continue
				}
				if _, failed := item.Data["_error"]; failed {
					continue
				}
				_, err := ResolveReferences(collections, item, fieldName)
				if err != nil {
					errs = append(errs, err)
				}
			}
		}
	}
	return errors.Join(errs...)
}

// ResolveReferences returns a field's referenced documents in declared order,
// from a single snapshot returned by GetAllCollections. Optional absent fields
// return an empty slice. The returned document pointers belong to the snapshot:
// treat them as read-only. Call Decode to obtain independently owned typed data.
func ResolveReferences(collections map[string]*Collection, item *Content, fieldName string) ([]*Content, error) {
	if item == nil {
		return nil, fmt.Errorf("content: nil source document")
	}
	source := collections[item.Collection]
	if source == nil || source.Schema == nil {
		return nil, fmt.Errorf("content: no schema for collection %q", item.Collection)
	}
	field, ok := source.Schema.Fields[fieldName]
	if !ok || field.Reference == "" {
		return nil, fmt.Errorf("content: %s.%s is not a reference", item.Collection, fieldName)
	}
	fail := func(err error) ([]*Content, error) {
		return nil, fmt.Errorf("content: %s (%s/%s), field %s: %w", item.Path, item.Collection, item.ID, fieldName, err)
	}
	if err := validReferenceDefinition(field); err != nil {
		return fail(err)
	}
	target := collections[field.Reference]
	if target == nil {
		return fail(fmt.Errorf("missing collection %q", field.Reference))
	}
	raw, present := item.Data[fieldName]
	if !present {
		required := field.Required
		for _, name := range source.Schema.Required {
			if name == fieldName {
				required = true
			}
		}
		if required {
			return fail(fmt.Errorf("required reference missing"))
		}
		return []*Content{}, nil
	}
	ids := []string{}
	if field.Type == FieldTypeString {
		id, ok := raw.(string)
		if !ok || id == "" {
			return fail(fmt.Errorf("expected a nonempty content ID"))
		}
		ids = append(ids, id)
	} else {
		list := reflect.ValueOf(raw)
		if !list.IsValid() || list.Kind() != reflect.Slice {
			return fail(fmt.Errorf("expected a list of content IDs"))
		}
		for i := 0; i < list.Len(); i++ {
			id, ok := list.Index(i).Interface().(string)
			if !ok || id == "" {
				return fail(fmt.Errorf("reference %d must be a nonempty string", i))
			}
			ids = append(ids, id)
		}
	}
	found := make([]*Content, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return fail(fmt.Errorf("duplicate reference %q", id))
		}
		seen[id] = true
		document := target.Contents[id]
		if document == nil {
			return fail(fmt.Errorf("missing target %s/%s", field.Reference, id))
		}
		if _, failed := document.Data["_error"]; failed {
			return fail(fmt.Errorf("target %s/%s failed to load", field.Reference, id))
		}
		found = append(found, document)
	}
	return found, nil
}

func validElementDefinition(field FieldDefinition) error {
	if field.ElementType == "" {
		return nil
	}
	if field.Type != FieldTypeSlice {
		return fmt.Errorf("element type requires a slice")
	}
	switch field.ElementType {
	case FieldTypeString, FieldTypeInt, FieldTypeFloat, FieldTypeBool, FieldTypeAny:
		return nil
	default:
		return fmt.Errorf("unsupported slice element type %q", field.ElementType)
	}
}
