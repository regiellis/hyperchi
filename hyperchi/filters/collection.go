package filters

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
)

func registerCollectionFilters() {
	Register("len", length)
	Register("first", first)
	Register("last", last)
	Register("index", index)
	Register("slice", slice)
	Register("reverse", reverse)
	Register("sort", sortItems)
	Register("sortBy", sortBy)
	Register("filter", filterItems)
	Register("map", mapItems)
	Register("reduce", reduce)
	Register("find", find)
	Register("findIndex", findIndex)
	Register("contains", contains)
	Register("unique", unique)
	Register("group", group)
	Register("chunk", chunk)
	Register("flatten", flatten)
	Register("zip", zip)
	Register("keys", keys)
	Register("values", values)
	Register("pairs", pairs)
	Register("merge", mergeItems)
	Register("random", random)
	Register("shuffle", shuffle)
	Register("take", take)
	Register("skip", skip)
	Register("pluck", pluck)
}

func toSlice(v any) []any {
	if v == nil {
		return []any{}
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		result := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			result[i] = rv.Index(i).Interface()
		}
		return result
	default:
		return []any{v}
	}
}

func length(v any) int {
	if v == nil {
		return 0
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map, reflect.String:
		return rv.Len()
	default:
		return 0
	}
}

func first(v any) any {
	items := toSlice(v)
	if len(items) > 0 {
		return items[0]
	}
	return nil
}

func last(v any) any {
	items := toSlice(v)
	if len(items) > 0 {
		return items[len(items)-1]
	}
	return nil
}

func index(v any, i int) any {
	items := toSlice(v)
	if i >= 0 && i < len(items) {
		return items[i]
	}
	if i < 0 && -i <= len(items) {
		return items[len(items)+i]
	}
	return nil
}

func slice(v any, start int, end ...int) []any {
	items := toSlice(v)

	if start < 0 {
		start = len(items) + start
	}
	if start < 0 {
		start = 0
	}
	if start >= len(items) {
		return []any{}
	}

	e := len(items)
	if len(end) > 0 {
		e = end[0]
		if e < 0 {
			e = len(items) + e
		}
		if e > len(items) {
			e = len(items)
		}
	}

	if e <= start {
		return []any{}
	}

	return items[start:e]
}

func reverse(v any) []any {
	items := toSlice(v)
	result := make([]any, len(items))

	for i, j := 0, len(items)-1; i < len(items); i, j = i+1, j-1 {
		result[i] = items[j]
	}

	return result
}

func sortItems(v any) []any {
	items := toSlice(v)
	result := make([]any, len(items))
	copy(result, items)

	sort.Slice(result, func(i, j int) bool {
		return fmt.Sprint(result[i]) < fmt.Sprint(result[j])
	})

	return result
}

func sortBy(v any, key string) []any {
	items := toSlice(v)
	result := make([]any, len(items))
	copy(result, items)

	sort.Slice(result, func(i, j int) bool {
		vi := getFieldValue(result[i], key)
		vj := getFieldValue(result[j], key)
		return fmt.Sprint(vi) < fmt.Sprint(vj)
	})

	return result
}

func getFieldValue(item any, key string) any {
	rv := reflect.ValueOf(item)
	if rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}

	switch rv.Kind() {
	case reflect.Map:
		value := rv.MapIndex(reflect.ValueOf(key))
		if value.IsValid() {
			return value.Interface()
		}
	case reflect.Struct:
		field := rv.FieldByName(key)
		if field.IsValid() {
			return field.Interface()
		}
	}

	return nil
}

func filterItems(v any, field string, value any) []any {
	items := toSlice(v)
	var result []any

	for _, item := range items {
		fieldValue := getFieldValue(item, field)
		if reflect.DeepEqual(fieldValue, value) {
			result = append(result, item)
		}
	}

	return result
}

func mapItems(v any, field string) []any {
	items := toSlice(v)
	result := make([]any, len(items))

	for i, item := range items {
		result[i] = getFieldValue(item, field)
	}

	return result
}

func reduce(v any, fn func(acc, val any) any, initial any) any {
	items := toSlice(v)
	acc := initial

	for _, item := range items {
		acc = fn(acc, item)
	}

	return acc
}

func find(v any, field string, value any) any {
	items := toSlice(v)

	for _, item := range items {
		fieldValue := getFieldValue(item, field)
		if reflect.DeepEqual(fieldValue, value) {
			return item
		}
	}

	return nil
}

func findIndex(v any, field string, value any) int {
	items := toSlice(v)

	for i, item := range items {
		fieldValue := getFieldValue(item, field)
		if reflect.DeepEqual(fieldValue, value) {
			return i
		}
	}

	return -1
}

// contains reports whether container holds item. A string container (any
// string kind) holds item when item is a string that occurs in it. A slice or
// array holds item when an element equals it by the rules of eq, so a named
// string type matches its literal and 2 matches 2.0. Any other container
// holds only a value equal to itself; nil holds nothing.
func contains(container, item any) bool {
	if container == nil {
		return false
	}
	if c := reflect.ValueOf(container); c.Kind() == reflect.String {
		i := reflect.ValueOf(item)
		return i.IsValid() && i.Kind() == reflect.String && strings.Contains(c.String(), i.String())
	}
	for _, element := range toSlice(container) {
		if equalValues(element, item) {
			return true
		}
	}
	return false
}

func unique(v any) []any {
	items := toSlice(v)
	seen := make(map[string]bool)
	var result []any

	for _, item := range items {
		key := fmt.Sprint(item)
		if !seen[key] {
			seen[key] = true
			result = append(result, item)
		}
	}

	return result
}

func group(v any, field string) map[string][]any {
	items := toSlice(v)
	groups := make(map[string][]any)

	for _, item := range items {
		key := fmt.Sprint(getFieldValue(item, field))
		groups[key] = append(groups[key], item)
	}

	return groups
}

func chunk(v any, size int) [][]any {
	if size <= 0 {
		return [][]any{}
	}

	items := toSlice(v)
	var chunks [][]any

	for i := 0; i < len(items); i += size {
		end := i + size
		if end > len(items) {
			end = len(items)
		}
		chunks = append(chunks, items[i:end])
	}

	return chunks
}

func flatten(v any) []any {
	var result []any

	items := toSlice(v)
	for _, item := range items {
		if rv := reflect.ValueOf(item); rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
			subItems := toSlice(item)
			result = append(result, subItems...)
		} else {
			result = append(result, item)
		}
	}

	return result
}

func zip(slices ...any) [][]any {
	if len(slices) == 0 {
		return [][]any{}
	}

	converted := make([][]any, len(slices))
	minLen := -1

	for i, s := range slices {
		converted[i] = toSlice(s)
		if minLen == -1 || len(converted[i]) < minLen {
			minLen = len(converted[i])
		}
	}

	result := make([][]any, minLen)
	for i := 0; i < minLen; i++ {
		row := make([]any, len(slices))
		for j, s := range converted {
			row[j] = s[i]
		}
		result[i] = row
	}

	return result
}

func keys(v any) []string {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Map {
		return []string{}
	}

	var result []string
	for _, key := range rv.MapKeys() {
		result = append(result, fmt.Sprint(key.Interface()))
	}

	sort.Strings(result)
	return result
}

func values(v any) []any {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Map {
		return []any{}
	}

	var result []any
	for _, key := range rv.MapKeys() {
		result = append(result, rv.MapIndex(key).Interface())
	}

	return result
}

func pairs(v any) [][]any {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Map {
		return [][]any{}
	}

	var result [][]any
	for _, key := range rv.MapKeys() {
		pair := []any{key.Interface(), rv.MapIndex(key).Interface()}
		result = append(result, pair)
	}

	return result
}

func mergeItems(maps ...any) map[string]any {
	result := make(map[string]any)

	for _, m := range maps {
		rv := reflect.ValueOf(m)
		if rv.Kind() != reflect.Map {
			continue
		}

		for _, key := range rv.MapKeys() {
			result[fmt.Sprint(key.Interface())] = rv.MapIndex(key).Interface()
		}
	}

	return result
}

func random(v any) any {
	items := toSlice(v)
	if len(items) == 0 {
		return nil
	}

	return items[rand.Intn(len(items))]
}

func shuffle(v any) []any {
	items := toSlice(v)
	result := make([]any, len(items))
	copy(result, items)

	rand.Shuffle(len(result), func(i, j int) {
		result[i], result[j] = result[j], result[i]
	})

	return result
}

func take(v any, n int) []any {
	items := toSlice(v)

	if n < 0 {
		n = 0
	}
	if n > len(items) {
		n = len(items)
	}

	return items[:n]
}

func skip(v any, n int) []any {
	items := toSlice(v)

	if n < 0 {
		n = 0
	}
	if n >= len(items) {
		return []any{}
	}

	return items[n:]
}

func pluck(v any, field string) []any {
	items := toSlice(v)
	result := make([]any, 0, len(items))

	for _, item := range items {
		value := getFieldValue(item, field)
		if value != nil {
			result = append(result, value)
		}
	}

	return result
}
