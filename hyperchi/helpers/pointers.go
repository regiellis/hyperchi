package helpers

import (
	"reflect"
	"strings"
)

// Nil-safe helpers for pointers, strings, slices, and maps.

// Ptr returns a pointer to a copy of v, for filling pointer fields from
// literals: Ptr(42), Ptr("name").
func Ptr[T any](v T) *T {
	return &v
}

// Value returns *ptr, or the zero value of T when ptr is nil.
func Value[T any](ptr *T) T {
	if ptr == nil {
		var zero T
		return zero
	}
	return *ptr
}

// ValueOr returns *ptr, or defaultValue when ptr is nil.
func ValueOr[T any](ptr *T, defaultValue T) T {
	if ptr == nil {
		return defaultValue
	}
	return *ptr
}

// IsNil reports whether v is nil, including a typed nil pointer, slice, map,
// channel, or func stored in an interface. Other kinds are never nil.
func IsNil(v any) bool {
	if v == nil {
		return true
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return rv.IsNil()
	default:
		return false
	}
}

// IsNotNil is !IsNil(v).
func IsNotNil(v any) bool {
	return !IsNil(v)
}

// Safe wraps a possibly nil pointer so a value can be read and transformed
// without nil checks. The zero Safe holds nil.
type Safe[T any] struct {
	value *T
}

// NewSafe wraps ptr, which may be nil.
func NewSafe[T any](ptr *T) Safe[T] {
	return Safe[T]{value: ptr}
}

// Get returns the wrapped value, or the zero value of T when it is nil.
func (s Safe[T]) Get() T {
	return Value(s.value)
}

// GetOr returns the wrapped value, or defaultValue when it is nil.
func (s Safe[T]) GetOr(defaultValue T) T {
	return ValueOr(s.value, defaultValue)
}

// IsNil reports whether the wrapped pointer is nil.
func (s Safe[T]) IsNil() bool {
	return s.value == nil
}

// IsNotNil reports whether the wrapped pointer is not nil.
func (s Safe[T]) IsNotNil() bool {
	return s.value != nil
}

// Map returns a Safe holding fn applied to the value. A nil Safe is
// returned unchanged without calling fn.
func (s Safe[T]) Map(fn func(T) T) Safe[T] {
	if s.value == nil {
		return s
	}
	result := fn(*s.value)
	return NewSafe(&result)
}

// FlatMap returns fn applied to the value. A nil Safe is returned unchanged
// without calling fn.
func (s Safe[T]) FlatMap(fn func(T) Safe[T]) Safe[T] {
	if s.value == nil {
		return s
	}
	return fn(*s.value)
}

// Filter returns s when its value is non-nil and predicate is true for it,
// and a nil Safe otherwise.
func (s Safe[T]) Filter(predicate func(T) bool) Safe[T] {
	if s.value == nil || !predicate(*s.value) {
		return NewSafe[T](nil)
	}
	return s
}

// IfPresent calls fn with the value when it is not nil.
func (s Safe[T]) IfPresent(fn func(T)) {
	if s.value != nil {
		fn(*s.value)
	}
}

// OrElse returns s when its value is not nil, and other otherwise.
func (s Safe[T]) OrElse(other Safe[T]) Safe[T] {
	if s.value != nil {
		return s
	}
	return other
}

// SafeString wraps a possibly nil *string. A nil pointer reads as "".
type SafeString struct {
	value *string
}

// NewSafeString wraps s, which may be nil.
func NewSafeString(s *string) SafeString {
	return SafeString{value: s}
}

// Get returns the string, or "" when the pointer is nil.
func (ss SafeString) Get() string {
	if ss.value == nil {
		return ""
	}
	return *ss.value
}

// GetOr returns the string, or defaultValue when the pointer is nil. An
// empty string is returned as is.
func (ss SafeString) GetOr(defaultValue string) string {
	if ss.value == nil {
		return defaultValue
	}
	return *ss.value
}

// IsEmpty reports whether the pointer is nil or the string is "".
func (ss SafeString) IsEmpty() bool {
	return ss.value == nil || *ss.value == ""
}

// IsNotEmpty reports whether the pointer is non-nil and the string is not
// "".
func (ss SafeString) IsNotEmpty() bool {
	return ss.value != nil && *ss.value != ""
}

// Trim returns a SafeString holding the string without leading and trailing
// whitespace. A nil SafeString stays nil.
func (ss SafeString) Trim() SafeString {
	if ss.value == nil {
		return ss
	}
	trimmed := strings.TrimSpace(*ss.value)
	return NewSafeString(&trimmed)
}

// Contains reports whether the string contains substr. A nil pointer
// contains nothing.
func (ss SafeString) Contains(substr string) bool {
	if ss.value == nil {
		return false
	}
	return strings.Contains(*ss.value, substr)
}

// SafeSlice wraps a slice for bounds-checked element access.
type SafeSlice[T any] struct {
	value []T
}

// NewSafeSlice wraps slice, which may be nil.
func NewSafeSlice[T any](slice []T) SafeSlice[T] {
	return SafeSlice[T]{value: slice}
}

// Get returns a Safe pointing at the element at index, or a nil Safe when
// index is out of range. The pointer refers to the slice's own element.
func (ss SafeSlice[T]) Get(index int) Safe[T] {
	if ss.value == nil || index < 0 || index >= len(ss.value) {
		return NewSafe[T](nil)
	}
	return NewSafe(&ss.value[index])
}

// First returns Get(0).
func (ss SafeSlice[T]) First() Safe[T] {
	return ss.Get(0)
}

// Last returns a Safe pointing at the last element, or a nil Safe when the
// slice is empty.
func (ss SafeSlice[T]) Last() Safe[T] {
	if len(ss.value) == 0 {
		return NewSafe[T](nil)
	}
	return ss.Get(len(ss.value) - 1)
}

// IsEmpty reports whether the slice has no elements.
func (ss SafeSlice[T]) IsEmpty() bool {
	return len(ss.value) == 0
}

// IsNotEmpty reports whether the slice has at least one element.
func (ss SafeSlice[T]) IsNotEmpty() bool {
	return len(ss.value) > 0
}

// Len returns the number of elements.
func (ss SafeSlice[T]) Len() int {
	if ss.value == nil {
		return 0
	}
	return len(ss.value)
}

// SafeMap wraps a map, which may be nil, for lookups that return Safe
// values.
type SafeMap[K comparable, V any] struct {
	value map[K]V
}

// NewSafeMap wraps m, which may be nil.
func NewSafeMap[K comparable, V any](m map[K]V) SafeMap[K, V] {
	return SafeMap[K, V]{value: m}
}

// Get returns a Safe holding a copy of the value at key, or a nil Safe when
// key is absent.
func (sm SafeMap[K, V]) Get(key K) Safe[V] {
	if sm.value == nil {
		return NewSafe[V](nil)
	}
	if value, exists := sm.value[key]; exists {
		return NewSafe(&value)
	}
	return NewSafe[V](nil)
}

// GetOr returns the value at key, or defaultValue when key is absent.
func (sm SafeMap[K, V]) GetOr(key K, defaultValue V) V {
	if sm.value == nil {
		return defaultValue
	}
	if value, exists := sm.value[key]; exists {
		return value
	}
	return defaultValue
}

// Has reports whether key is present.
func (sm SafeMap[K, V]) Has(key K) bool {
	if sm.value == nil {
		return false
	}
	_, exists := sm.value[key]
	return exists
}

// IsEmpty reports whether the map has no entries.
func (sm SafeMap[K, V]) IsEmpty() bool {
	return len(sm.value) == 0
}

// IsNotEmpty reports whether the map has at least one entry.
func (sm SafeMap[K, V]) IsNotEmpty() bool {
	return len(sm.value) > 0
}

// Len returns the number of entries.
func (sm SafeMap[K, V]) Len() int {
	if sm.value == nil {
		return 0
	}
	return len(sm.value)
}

// Keys returns the keys in no particular order, or an empty slice for a nil
// map.
func (sm SafeMap[K, V]) Keys() []K {
	if sm.value == nil {
		return []K{}
	}
	keys := make([]K, 0, len(sm.value))
	for k := range sm.value {
		keys = append(keys, k)
	}
	return keys
}

// Values returns the values in no particular order, or an empty slice for a
// nil map.
func (sm SafeMap[K, V]) Values() []V {
	if sm.value == nil {
		return []V{}
	}
	values := make([]V, 0, len(sm.value))
	for _, v := range sm.value {
		values = append(values, v)
	}
	return values
}
