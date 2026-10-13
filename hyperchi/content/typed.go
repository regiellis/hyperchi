package content

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"maps"
	"reflect"
	"strings"
	"time"
)

type modelField struct {
	name       string
	definition FieldDefinition
}

// SchemaFor derives a schema from a Go struct. JSON tags name front matter
// fields; content:"required,ref=authors" marks required fields and references
// to content IDs. References may be strings or slices of strings. Exported
// scalar fields, time.Time, slices of strings/numbers/bools/any, map[string]any,
// any, and optional pointers to scalars are supported. Embedded fields and nested structs are
// rejected. Validators run on decoded values after normal schema validation.
func SchemaFor[T any](name string, validators ...func(T) error) (*ContentSchema, error) {
	fields, err := modelFields(reflect.TypeFor[T]())
	if err != nil {
		return nil, err
	}
	schema := &ContentSchema{Name: name, Fields: make(map[string]FieldDefinition, len(fields))}
	for _, field := range fields {
		schema.Fields[field.name] = field.definition
	}
	for _, validate := range validators {
		if validate == nil {
			return nil, fmt.Errorf("content: nil typed validator")
		}
	}
	schema.Validators = append(schema.Validators, func(item *Content) error {
		value, err := DecodeData[T](item.Data)
		if err != nil {
			return err
		}
		for _, validate := range validators {
			if err := validate(value); err != nil {
				return err
			}
		}
		return nil
	})
	return schema, nil
}

// DecodeData decodes front matter into a struct without changing or sharing
// the input map. Unknown fields, wrong types, overflow, malformed dates, and
// missing required fields return errors. Missing optional fields remain zero.
// Supported types and tags are the same as SchemaFor. References are decoded
// as IDs; validate or resolve them against a collection snapshot separately.
func DecodeData[T any](data map[string]any) (T, error) {
	var value T
	fields, err := modelFields(reflect.TypeFor[T]())
	if err != nil {
		return value, err
	}
	known := make(map[string]modelField, len(fields))
	normalized := maps.Clone(data)
	if normalized == nil {
		normalized = map[string]any{}
	}
	for _, field := range fields {
		known[field.name] = field
		raw, present := data[field.name]
		if !present {
			if field.definition.Required {
				return value, fmt.Errorf("content: required field %q missing", field.name)
			}
			continue
		}
		if raw == nil {
			return value, fmt.Errorf("content: field %q is null", field.name)
		}
		if field.definition.Type == FieldTypeTime {
			if text, ok := raw.(string); ok {
				parsed, ok := ParseTime(text)
				if !ok {
					return value, fmt.Errorf("content: invalid date in field %q", field.name)
				}
				normalized[field.name] = parsed
			}
		}
	}
	for _, name := range sortedKeys(data) {
		if _, ok := known[name]; !ok {
			return value, fmt.Errorf("content: unknown field %q", name)
		}
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return value, fmt.Errorf("content: encode front matter: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		var zero T
		return zero, fmt.Errorf("content: decode %s: %w", reflect.TypeFor[T](), err)
	}
	return value, nil
}

// Entry is a decoded document. Data and its slices/maps belong to the caller.
// The source collection is never mutated by Decode or QueryTyped.
type Entry[T any] struct {
	ID         string
	Slug       string
	Path       string
	Collection string
	Body       string
	HTML       template.HTML
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Data       T
}

// Decode decodes a document's front matter and copies its document metadata.
func Decode[T any](item *Content) (Entry[T], error) {
	if item == nil {
		return Entry[T]{}, fmt.Errorf("content: cannot decode nil document")
	}
	data, err := DecodeData[T](item.Data)
	if err != nil {
		return Entry[T]{}, fmt.Errorf("%s: %w", item.Path, err)
	}
	return Entry[T]{ID: item.ID, Slug: item.Slug, Path: item.Path, Collection: item.Collection,
		Body: item.Body, HTML: item.HTML, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt, Data: data}, nil
}

// QueryTyped applies the existing query API and decodes the selected documents.
// It returns no partial typed result if any document fails to decode. The
// second result is the total number of matches before pagination.
func QueryTyped[T any](collection *Collection, query ContentQuery) ([]Entry[T], int, error) {
	if collection == nil {
		return nil, 0, fmt.Errorf("content: nil collection")
	}
	for id, item := range collection.Contents {
		if item == nil {
			return nil, 0, fmt.Errorf("content: nil document %q", id)
		}
	}
	result, err := collection.Query(query)
	if err != nil {
		return nil, 0, err
	}
	entries := make([]Entry[T], 0, len(result.Contents))
	for _, item := range result.Contents {
		entry, err := Decode[T](item)
		if err != nil {
			return nil, 0, err
		}
		entries = append(entries, entry)
	}
	return entries, result.Total, nil
}

func modelFields(typ reflect.Type) ([]modelField, error) {
	if typ.Kind() != reflect.Struct {
		return nil, fmt.Errorf("content: typed model must be a struct, got %s", typ)
	}
	fields := make([]modelField, 0, typ.NumField())
	names := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := strings.Split(field.Tag.Get("json"), ",")
		if tag[0] == "-" || !field.IsExported() {
			continue
		}
		if field.Anonymous {
			return nil, fmt.Errorf("content: embedded field %s is unsupported", field.Name)
		}
		name := tag[0]
		if name == "" {
			name = field.Name
		}
		if names[name] {
			return nil, fmt.Errorf("content: duplicate field %q", name)
		}
		names[name] = true
		for _, option := range tag[1:] {
			if option != "omitempty" && option != "omitzero" && option != "" {
				return nil, fmt.Errorf("content: unsupported JSON option %q", option)
			}
		}
		definition, err := fieldForType(field.Type)
		if err != nil {
			return nil, fmt.Errorf("content: field %s: %w", field.Name, err)
		}
		for _, option := range strings.Split(field.Tag.Get("content"), ",") {
			switch {
			case option == "":
			case option == "required":
				definition.Required = true
			case strings.HasPrefix(option, "ref="):
				definition.Reference = strings.TrimPrefix(option, "ref=")
				if definition.Reference == "" {
					return nil, fmt.Errorf("content: empty reference collection for %s", name)
				}
			default:
				return nil, fmt.Errorf("content: unknown tag option %q", option)
			}
		}
		if err := validReferenceDefinition(definition); err != nil {
			return nil, fmt.Errorf("content: field %s: %w", name, err)
		}
		fields = append(fields, modelField{name: name, definition: definition})
	}
	return fields, nil
}

func fieldForType(typ reflect.Type) (FieldDefinition, error) {
	if typ.Kind() == reflect.Pointer {
		switch typ.Elem().Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface:
			return FieldDefinition{}, fmt.Errorf("only scalar pointers are supported")
		}
		typ = typ.Elem()
	}
	definition := FieldDefinition{}
	if typ == reflect.TypeFor[time.Time]() {
		definition.Type = FieldTypeTime
		return definition, nil
	}
	switch typ.Kind() {
	case reflect.String:
		definition.Type = FieldTypeString
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		definition.Type = FieldTypeInt
	case reflect.Float32, reflect.Float64:
		definition.Type = FieldTypeFloat
	case reflect.Bool:
		definition.Type = FieldTypeBool
	case reflect.Interface:
		if typ.NumMethod() != 0 {
			return definition, fmt.Errorf("only empty interfaces are supported")
		}
		definition.Type = FieldTypeAny
	case reflect.Map:
		if typ.Key().Kind() != reflect.String || typ.Elem() != reflect.TypeFor[any]() {
			return definition, fmt.Errorf("only map[string]any is supported")
		}
		definition.Type = FieldTypeMap
	case reflect.Slice:
		switch typ.Elem().Kind() {
		case reflect.Pointer, reflect.Map, reflect.Slice:
			return definition, fmt.Errorf("only slices of strings, numbers, bools, or any are supported")
		}
		element, err := fieldForType(typ.Elem())
		if err != nil || element.Type == FieldTypeTime {
			return definition, fmt.Errorf("only slices of strings, numbers, bools, or any are supported")
		}
		definition.Type = FieldTypeSlice
		definition.ElementType = element.Type
	default:
		return definition, fmt.Errorf("unsupported type %s", typ)
	}
	return definition, nil
}
