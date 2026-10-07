package helpers

import (
	"sort"
	"testing"
)

func TestPtrValue(t *testing.T) {
	p := Ptr(42)
	if *p != 42 {
		t.Fatal("Ptr")
	}
	*p = 7
	if Value(p) != 7 || ValueOr(p, 1) != 7 {
		t.Error("Value/ValueOr of a set pointer")
	}
	var nilInt *int
	if Value(nilInt) != 0 || ValueOr(nilInt, 5) != 5 {
		t.Error("Value/ValueOr of nil")
	}
	if Value[string](nil) != "" {
		t.Error("Value[string](nil)")
	}
	if a, b := Ptr("x"), Ptr("x"); a == b {
		t.Error("Ptr must return distinct pointers")
	}
}

func TestIsNil(t *testing.T) {
	var (
		nilPtr   *int
		nilSlice []int
		nilMap   map[string]int
		nilChan  chan int
		nilFunc  func()
		nilIface error
	)
	for name, v := range map[string]any{
		"untyped nil": nil,
		"pointer":     nilPtr,
		"slice":       nilSlice,
		"map":         nilMap,
		"chan":        nilChan,
		"func":        nilFunc,
		"interface":   nilIface,
	} {
		if !IsNil(v) || IsNotNil(v) {
			t.Errorf("%s: IsNil = false", name)
		}
	}
	for name, v := range map[string]any{
		"zero int":    0,
		"empty str":   "",
		"struct":      struct{}{},
		"empty slice": []int{},
		"pointer":     Ptr(1),
		"map":         map[string]int{},
	} {
		if IsNil(v) || !IsNotNil(v) {
			t.Errorf("%s: IsNil = true", name)
		}
	}
}

func TestSafe(t *testing.T) {
	present := NewSafe(Ptr(3))
	absent := NewSafe[int](nil)

	if present.Get() != 3 || present.GetOr(9) != 3 || absent.Get() != 0 || absent.GetOr(9) != 9 {
		t.Error("Get/GetOr")
	}
	if present.IsNil() || !present.IsNotNil() || !absent.IsNil() || absent.IsNotNil() {
		t.Error("IsNil/IsNotNil")
	}

	double := func(n int) int { return n * 2 }
	if present.Map(double).Get() != 6 || !absent.Map(double).IsNil() {
		t.Error("Map")
	}
	if present.Get() != 3 {
		t.Error("Map mutated the original value")
	}
	half := func(n int) Safe[int] {
		if n%2 != 0 {
			return NewSafe[int](nil)
		}
		v := n / 2
		return NewSafe(&v)
	}
	if !present.FlatMap(half).IsNil() || NewSafe(Ptr(8)).FlatMap(half).Get() != 4 || !absent.FlatMap(half).IsNil() {
		t.Error("FlatMap")
	}
	odd := func(n int) bool { return n%2 == 1 }
	if present.Filter(odd).Get() != 3 || !NewSafe(Ptr(2)).Filter(odd).IsNil() || !absent.Filter(odd).IsNil() {
		t.Error("Filter")
	}

	calls := 0
	present.IfPresent(func(int) { calls++ })
	absent.IfPresent(func(int) { calls++ })
	if calls != 1 {
		t.Errorf("IfPresent calls = %d", calls)
	}
	if present.OrElse(NewSafe(Ptr(5))).Get() != 3 || absent.OrElse(NewSafe(Ptr(5))).Get() != 5 {
		t.Error("OrElse")
	}
}

func TestSafeString(t *testing.T) {
	padded := NewSafeString(Ptr("  hi  "))
	empty := NewSafeString(Ptr(""))
	missing := NewSafeString(nil)

	if padded.Get() != "  hi  " || missing.Get() != "" || missing.GetOr("d") != "d" || empty.GetOr("d") != "" {
		t.Error("Get/GetOr")
	}
	if padded.IsEmpty() || !padded.IsNotEmpty() || !empty.IsEmpty() || empty.IsNotEmpty() || !missing.IsEmpty() || missing.IsNotEmpty() {
		t.Error("IsEmpty/IsNotEmpty")
	}
	if padded.Trim().Get() != "hi" || padded.Get() != "  hi  " || !missing.Trim().IsEmpty() {
		t.Error("Trim")
	}
	if !padded.Contains("hi") || padded.Contains("x") || missing.Contains("") {
		t.Error("Contains")
	}
}

func TestSafeSlice(t *testing.T) {
	s := NewSafeSlice([]string{"a", "b", "c"})
	if s.Get(1).Get() != "b" || !s.Get(3).IsNil() || !s.Get(-1).IsNil() {
		t.Error("Get")
	}
	if s.First().Get() != "a" || s.Last().Get() != "c" {
		t.Error("First/Last")
	}
	if s.Len() != 3 || s.IsEmpty() || !s.IsNotEmpty() {
		t.Error("Len/IsEmpty")
	}
	for name, empty := range map[string]SafeSlice[string]{"nil": NewSafeSlice[string](nil), "empty": NewSafeSlice([]string{})} {
		if !empty.First().IsNil() || !empty.Last().IsNil() || !empty.Get(0).IsNil() || empty.Len() != 0 || !empty.IsEmpty() || empty.IsNotEmpty() {
			t.Errorf("%s slice not treated as empty", name)
		}
	}
}

func TestSafeMap(t *testing.T) {
	m := NewSafeMap(map[string]int{"a": 1, "zero": 0})
	if m.Get("a").Get() != 1 || !m.Get("x").IsNil() || m.Get("zero").IsNil() {
		t.Error("Get distinguishes missing keys from zero values")
	}
	if m.GetOr("zero", 9) != 0 || m.GetOr("x", 9) != 9 {
		t.Error("GetOr")
	}
	if !m.Has("zero") || m.Has("x") || m.Len() != 2 || m.IsEmpty() || !m.IsNotEmpty() {
		t.Error("Has/Len/IsEmpty")
	}
	keys := m.Keys()
	sort.Strings(keys)
	if len(keys) != 2 || keys[0] != "a" || keys[1] != "zero" {
		t.Errorf("Keys = %v", keys)
	}
	values := m.Values()
	sort.Ints(values)
	if len(values) != 2 || values[0] != 0 || values[1] != 1 {
		t.Errorf("Values = %v", values)
	}

	nilMap := NewSafeMap[string, int](nil)
	if !nilMap.Get("a").IsNil() || nilMap.GetOr("a", 4) != 4 || nilMap.Has("a") || nilMap.Len() != 0 || !nilMap.IsEmpty() || nilMap.IsNotEmpty() {
		t.Error("nil map")
	}
	if k, v := nilMap.Keys(), nilMap.Values(); k == nil || v == nil || len(k) != 0 || len(v) != 0 {
		t.Error("Keys/Values of a nil map should be empty, not nil")
	}
}
