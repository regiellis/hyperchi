package helpers

import (
	"reflect"
	"slices"
	"sort"
)

// Slice is a slice with chainable query and transform methods. Equality
// methods (Contains, Index, RemoveValue, and the set operations) compare
// with reflect.DeepEqual, so any element type works.
type Slice[T any] []T

// NewSlice returns a Slice of items.
func NewSlice[T any](items ...T) Slice[T] {
	return Slice[T](items)
}

// FromSlice converts slice to a Slice. It shares slice's backing array.
func FromSlice[T any](slice []T) Slice[T] {
	return Slice[T](slice)
}

// ToSlice returns s as a plain slice, sharing its backing array.
func (s Slice[T]) ToSlice() []T {
	return []T(s)
}

// Len returns the number of elements.
func (s Slice[T]) Len() int {
	return len(s)
}

// IsEmpty reports whether s has no elements.
func (s Slice[T]) IsEmpty() bool {
	return len(s) == 0
}

// IsNotEmpty reports whether s has at least one element.
func (s Slice[T]) IsNotEmpty() bool {
	return len(s) > 0
}

// Get returns the element at index and true, or the zero value and false
// when index is out of range.
func (s Slice[T]) Get(index int) (T, bool) {
	var zero T
	if index < 0 || index >= len(s) {
		return zero, false
	}
	return s[index], true
}

// GetOr returns the element at index, or defaultValue when index is out of
// range.
func (s Slice[T]) GetOr(index int, defaultValue T) T {
	if value, ok := s.Get(index); ok {
		return value
	}
	return defaultValue
}

// First returns the first element and true, or the zero value and false
// when s is empty.
func (s Slice[T]) First() (T, bool) {
	return s.Get(0)
}

// Last returns the last element and true, or the zero value and false when
// s is empty.
func (s Slice[T]) Last() (T, bool) {
	return s.Get(len(s) - 1)
}

// Append returns s with items added, using the built-in append: when s has
// spare capacity the result shares its backing array.
func (s Slice[T]) Append(items ...T) Slice[T] {
	return append(s, items...)
}

// Prepend returns a new slice with items followed by the elements of s.
// Neither s nor the items argument is modified.
func (s Slice[T]) Prepend(items ...T) Slice[T] {
	result := make(Slice[T], 0, len(items)+len(s))
	result = append(result, items...)
	return append(result, s...)
}

// Insert returns a new slice with items placed before index. An index below
// 0 inserts at the start; one past the end appends. s is not modified.
func (s Slice[T]) Insert(index int, items ...T) Slice[T] {
	if index < 0 {
		index = 0
	}
	if index > len(s) {
		index = len(s)
	}

	result := make(Slice[T], 0, len(s)+len(items))
	result = append(result, s[:index]...)
	result = append(result, items...)
	result = append(result, s[index:]...)
	return result
}

// Remove returns a new slice without the element at index. The receiver is
// not modified; an out-of-range index returns s unchanged.
func (s Slice[T]) Remove(index int) Slice[T] {
	if index < 0 || index >= len(s) {
		return s
	}
	result := make(Slice[T], 0, len(s)-1)
	result = append(result, s[:index]...)
	return append(result, s[index+1:]...)
}

// RemoveValue returns a new slice without the first element equal to value,
// or s itself when there is none.
func (s Slice[T]) RemoveValue(value T) Slice[T] {
	for i, item := range s {
		if reflect.DeepEqual(item, value) {
			return s.Remove(i)
		}
	}
	return s
}

// RemoveAll returns a new slice without any element equal to value. The
// result is nil when every element matched.
func (s Slice[T]) RemoveAll(value T) Slice[T] {
	var result Slice[T]
	for _, item := range s {
		if !reflect.DeepEqual(item, value) {
			result = append(result, item)
		}
	}
	return result
}

// Map returns a new slice holding fn applied to each element.
func Map[T, U any](slice Slice[T], fn func(T) U) Slice[U] {
	result := make(Slice[U], len(slice))
	for i, item := range slice {
		result[i] = fn(item)
	}
	return result
}

// MapWithIndex is Map with each element's index passed to fn.
func MapWithIndex[T, U any](slice Slice[T], fn func(int, T) U) Slice[U] {
	result := make(Slice[U], len(slice))
	for i, item := range slice {
		result[i] = fn(i, item)
	}
	return result
}

// Filter returns a new slice of the elements for which predicate is true.
func (s Slice[T]) Filter(predicate func(T) bool) Slice[T] {
	var result Slice[T]
	for _, item := range s {
		if predicate(item) {
			result = append(result, item)
		}
	}
	return result
}

// FilterWithIndex is Filter with each element's index passed to predicate.
func (s Slice[T]) FilterWithIndex(predicate func(int, T) bool) Slice[T] {
	var result Slice[T]
	for i, item := range s {
		if predicate(i, item) {
			result = append(result, item)
		}
	}
	return result
}

// Reduce folds the slice into one value, starting from initial and calling
// fn(accumulator, element) for each element in order.
func Reduce[T, U any](slice Slice[T], fn func(U, T) U, initial U) U {
	acc := initial
	for _, item := range slice {
		acc = fn(acc, item)
	}
	return acc
}

// ReduceWithIndex is Reduce with each element's index passed to fn.
func ReduceWithIndex[T, U any](slice Slice[T], fn func(U, int, T) U, initial U) U {
	acc := initial
	for i, item := range slice {
		acc = fn(acc, i, item)
	}
	return acc
}

// Find returns the first element for which predicate is true and true, or
// the zero value and false.
func (s Slice[T]) Find(predicate func(T) bool) (T, bool) {
	var zero T
	for _, item := range s {
		if predicate(item) {
			return item, true
		}
	}
	return zero, false
}

// FindIndex returns the index of the first element for which predicate is
// true, or -1.
func (s Slice[T]) FindIndex(predicate func(T) bool) int {
	for i, item := range s {
		if predicate(item) {
			return i
		}
	}
	return -1
}

// Contains reports whether an element equals value.
func (s Slice[T]) Contains(value T) bool {
	for _, item := range s {
		if reflect.DeepEqual(item, value) {
			return true
		}
	}
	return false
}

// ContainsAny reports whether s contains at least one of values.
func (s Slice[T]) ContainsAny(values ...T) bool {
	for _, value := range values {
		if s.Contains(value) {
			return true
		}
	}
	return false
}

// ContainsAll reports whether s contains every one of values.
func (s Slice[T]) ContainsAll(values ...T) bool {
	for _, value := range values {
		if !s.Contains(value) {
			return false
		}
	}
	return true
}

// Index returns the index of the first element equal to value, or -1.
func (s Slice[T]) Index(value T) int {
	for i, item := range s {
		if reflect.DeepEqual(item, value) {
			return i
		}
	}
	return -1
}

// LastIndex returns the index of the last element equal to value, or -1.
func (s Slice[T]) LastIndex(value T) int {
	for i := len(s) - 1; i >= 0; i-- {
		if reflect.DeepEqual(s[i], value) {
			return i
		}
	}
	return -1
}

// Count returns the number of elements equal to value.
func (s Slice[T]) Count(value T) int {
	count := 0
	for _, item := range s {
		if reflect.DeepEqual(item, value) {
			count++
		}
	}
	return count
}

// CountIf returns the number of elements for which predicate is true.
func (s Slice[T]) CountIf(predicate func(T) bool) int {
	count := 0
	for _, item := range s {
		if predicate(item) {
			count++
		}
	}
	return count
}

// Every reports whether predicate is true for every element. It is true for
// an empty slice.
func (s Slice[T]) Every(predicate func(T) bool) bool {
	for _, item := range s {
		if !predicate(item) {
			return false
		}
	}
	return true
}

// Some reports whether predicate is true for at least one element.
func (s Slice[T]) Some(predicate func(T) bool) bool {
	for _, item := range s {
		if predicate(item) {
			return true
		}
	}
	return false
}

// None reports whether predicate is false for every element.
func (s Slice[T]) None(predicate func(T) bool) bool {
	return !s.Some(predicate)
}

// ForEach calls fn with each element in order.
func (s Slice[T]) ForEach(fn func(T)) {
	for _, item := range s {
		fn(item)
	}
}

// ForEachWithIndex calls fn with each index and element in order.
func (s Slice[T]) ForEachWithIndex(fn func(int, T)) {
	for i, item := range s {
		fn(i, item)
	}
}

// Reverse returns a reversed copy of s.
func (s Slice[T]) Reverse() Slice[T] {
	result := make(Slice[T], len(s))
	for i, j := 0, len(s)-1; i <= j; i, j = i+1, j-1 {
		result[i] = s[j]
		if i != j {
			result[j] = s[i]
		}
	}
	return result
}

// Sort returns a sorted copy of s, ordered by less. The sort is not stable.
func (s Slice[T]) Sort(less func(T, T) bool) Slice[T] {
	result := make(Slice[T], len(s))
	copy(result, s)
	sort.Slice(result, func(i, j int) bool {
		return less(result[i], result[j])
	})
	return result
}

// Unique returns the first occurrence of each element, preserving order.
// Comparable elements are deduplicated with ==; elements that cannot be map
// keys (slices, maps, structs holding them) fall back to reflect.DeepEqual,
// the comparison Contains uses.
func (s Slice[T]) Unique() Slice[T] {
	seen := make(map[any]struct{})
	var result Slice[T]

	for _, item := range s {
		if v := reflect.ValueOf(item); v.IsValid() && v.Comparable() {
			if _, dup := seen[item]; dup {
				continue
			}
			seen[item] = struct{}{}
		} else if result.Contains(item) {
			continue
		}
		result = append(result, item)
	}
	return result
}

// Slice returns the elements from start up to end. A negative index counts
// from the end, indices are clamped to the bounds, and start past end gives
// an empty slice. The result shares s's backing array.
func (s Slice[T]) Slice(start, end int) Slice[T] {
	length := len(s)

	if start < 0 {
		start = length + start
	}
	if end < 0 {
		end = length + end
	}

	if start < 0 {
		start = 0
	}
	if end > length {
		end = length
	}
	if start > end {
		return Slice[T]{}
	}

	return s[start:end]
}

// Take returns the first n elements, or all of s when it is shorter. The
// result shares s's backing array.
func (s Slice[T]) Take(n int) Slice[T] {
	if n <= 0 {
		return Slice[T]{}
	}
	if n >= len(s) {
		return s
	}
	return s[:n]
}

// TakeLast returns the last n elements, or all of s when it is shorter. The
// result shares s's backing array.
func (s Slice[T]) TakeLast(n int) Slice[T] {
	if n <= 0 {
		return Slice[T]{}
	}
	if n >= len(s) {
		return s
	}
	return s[len(s)-n:]
}

// Skip returns s without its first n elements. The result shares s's
// backing array.
func (s Slice[T]) Skip(n int) Slice[T] {
	if n <= 0 {
		return s
	}
	if n >= len(s) {
		return Slice[T]{}
	}
	return s[n:]
}

// SkipLast returns s without its last n elements. The result shares s's
// backing array.
func (s Slice[T]) SkipLast(n int) Slice[T] {
	if n <= 0 {
		return s
	}
	if n >= len(s) {
		return Slice[T]{}
	}
	return s[:len(s)-n]
}

// Chunk splits s into consecutive pieces of size elements; the last piece
// may be shorter. A non-positive size returns no chunks. The pieces share
// s's backing array.
func (s Slice[T]) Chunk(size int) []Slice[T] {
	if size <= 0 {
		return []Slice[T]{}
	}

	var chunks []Slice[T]
	for i := 0; i < len(s); i += size {
		end := i + size
		if end > len(s) {
			end = len(s)
		}
		chunks = append(chunks, s[i:end])
	}
	return chunks
}

// GroupBy returns the elements grouped by keyFn, each group in its original
// order.
func GroupBy[T any, K comparable](slice Slice[T], keyFn func(T) K) map[K]Slice[T] {
	groups := make(map[K]Slice[T])
	for _, item := range slice {
		key := keyFn(item)
		groups[key] = append(groups[key], item)
	}
	return groups
}

// Partition returns the elements for which predicate is true, then the
// rest, each in original order.
func (s Slice[T]) Partition(predicate func(T) bool) (Slice[T], Slice[T]) {
	var trueSlice, falseSlice Slice[T]
	for _, item := range s {
		if predicate(item) {
			trueSlice = append(trueSlice, item)
		} else {
			falseSlice = append(falseSlice, item)
		}
	}
	return trueSlice, falseSlice
}

// Flatten concatenates slices into one new slice.
func Flatten[T any](slices []Slice[T]) Slice[T] {
	var result Slice[T]
	for _, slice := range slices {
		result = append(result, slice...)
	}
	return result
}

// FlatMap calls fn on each element and concatenates the results.
func FlatMap[T, U any](slice Slice[T], fn func(T) Slice[U]) Slice[U] {
	var result Slice[U]
	for _, item := range slice {
		mapped := fn(item)
		result = append(result, mapped...)
	}
	return result
}

// Zip pairs elements at the same index. The result is as long as the
// shorter input.
func Zip[T, U any](slice1 Slice[T], slice2 Slice[U]) Slice[Pair[T, U]] {
	minLen := len(slice1)
	if len(slice2) < minLen {
		minLen = len(slice2)
	}

	result := make(Slice[Pair[T, U]], minLen)
	for i := 0; i < minLen; i++ {
		result[i] = Pair[T, U]{First: slice1[i], Second: slice2[i]}
	}
	return result
}

// Pair is one element of a Zip result.
type Pair[T, U any] struct {
	First  T
	Second U
}

// Union returns the elements of s followed by those of other, without
// duplicates (see Unique). It builds a new slice and never writes into s's
// backing array.
func (s Slice[T]) Union(other Slice[T]) Slice[T] {
	return slices.Concat(s, other).Unique()
}

// Intersect returns the elements of s that are also in other, without
// duplicates, in s's order.
func (s Slice[T]) Intersect(other Slice[T]) Slice[T] {
	var result Slice[T]
	for _, item := range s {
		if other.Contains(item) && !result.Contains(item) {
			result = append(result, item)
		}
	}
	return result
}

// Difference returns the elements of s that are not in other, in s's order.
func (s Slice[T]) Difference(other Slice[T]) Slice[T] {
	var result Slice[T]
	for _, item := range s {
		if !other.Contains(item) {
			result = append(result, item)
		}
	}
	return result
}

// Range returns the integers from start up to but not including end. It is
// empty when start >= end.
func Range(start, end int) Slice[int] {
	if start >= end {
		return Slice[int]{}
	}
	result := make(Slice[int], end-start)
	for i := start; i < end; i++ {
		result[i-start] = i
	}
	return result
}

// RangeStep returns start, start+step, and so on, stopping before end. A
// negative step counts down. A zero step, or a step pointing away from end,
// returns an empty slice.
func RangeStep(start, end, step int) Slice[int] {
	if step == 0 || (step > 0 && start >= end) || (step < 0 && start <= end) {
		return Slice[int]{}
	}

	var result Slice[int]
	if step > 0 {
		for i := start; i < end; i += step {
			result = append(result, i)
		}
	} else {
		for i := start; i > end; i += step {
			result = append(result, i)
		}
	}
	return result
}

// Repeat creates a slice with value repeated n times. A non-positive n
// returns an empty slice.
func Repeat[T any](value T, n int) Slice[T] {
	if n <= 0 {
		return Slice[T]{}
	}
	result := make(Slice[T], n)
	for i := 0; i < n; i++ {
		result[i] = value
	}
	return result
}

// Times calls fn with 0..n-1 and collects the results. A non-positive n
// returns an empty slice without calling fn.
func Times[T any](n int, fn func(int) T) Slice[T] {
	if n <= 0 {
		return Slice[T]{}
	}
	result := make(Slice[T], n)
	for i := 0; i < n; i++ {
		result[i] = fn(i)
	}
	return result
}

// ToMap returns a map from keyFn(element) to element. A later element wins
// when two share a key.
func ToMap[T any, K comparable](slice Slice[T], keyFn func(T) K) map[K]T {
	result := make(map[K]T)
	for _, item := range slice {
		result[keyFn(item)] = item
	}
	return result
}

// ToMapValue returns a map from keyFn(element) to valueFn(element). A later
// element wins when two share a key.
func ToMapValue[T any, K comparable, V any](slice Slice[T], keyFn func(T) K, valueFn func(T) V) map[K]V {
	result := make(map[K]V)
	for _, item := range slice {
		result[keyFn(item)] = valueFn(item)
	}
	return result
}

// Keys returns the keys of m in no particular order.
func Keys[K comparable, V any](m map[K]V) Slice[K] {
	result := make(Slice[K], 0, len(m))
	for k := range m {
		result = append(result, k)
	}
	return result
}

// Values returns the values of m in no particular order.
func Values[K comparable, V any](m map[K]V) Slice[V] {
	result := make(Slice[V], 0, len(m))
	for _, v := range m {
		result = append(result, v)
	}
	return result
}

// MinBy returns the smallest element by less and true, or the zero value and
// false when slice is empty. Ties keep the first.
func MinBy[T any](slice Slice[T], less func(T, T) bool) (T, bool) {
	var zero T
	if len(slice) == 0 {
		return zero, false
	}

	min := slice[0]
	for i := 1; i < len(slice); i++ {
		if less(slice[i], min) {
			min = slice[i]
		}
	}
	return min, true
}

// MaxBy returns the largest element by less and true, or the zero value and
// false when slice is empty. Ties keep the first.
func MaxBy[T any](slice Slice[T], less func(T, T) bool) (T, bool) {
	var zero T
	if len(slice) == 0 {
		return zero, false
	}

	max := slice[0]
	for i := 1; i < len(slice); i++ {
		if less(max, slice[i]) {
			max = slice[i]
		}
	}
	return max, true
}

// OrEmpty returns a non-nil slice so JSON encodes an empty collection as [].
// Non-nil slices are returned unchanged, without copying their backing array.
func (s Slice[T]) OrEmpty() Slice[T] {
	if s == nil {
		return Slice[T]{}
	}
	return s
}
