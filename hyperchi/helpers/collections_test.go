package helpers

import (
	"reflect"
	"sort"
	"strconv"
	"testing"
)

func eq[T any](t *testing.T, name string, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", name, got, want)
	}
}

func TestSliceBasics(t *testing.T) {
	s := NewSlice(10, 20, 30)
	eq(t, "ToSlice", s.ToSlice(), []int{10, 20, 30})
	eq(t, "Len", s.Len(), 3)
	if s.IsEmpty() || !s.IsNotEmpty() || !NewSlice[int]().IsEmpty() || FromSlice[int](nil).IsNotEmpty() {
		t.Error("IsEmpty/IsNotEmpty")
	}

	for _, tc := range []struct {
		index int
		want  int
		ok    bool
	}{{0, 10, true}, {2, 30, true}, {3, 0, false}, {-1, 0, false}} {
		v, ok := s.Get(tc.index)
		if v != tc.want || ok != tc.ok {
			t.Errorf("Get(%d) = %d, %v", tc.index, v, ok)
		}
	}
	eq(t, "GetOr hit", s.GetOr(1, -1), 20)
	eq(t, "GetOr miss", s.GetOr(5, -1), -1)

	if v, ok := s.First(); v != 10 || !ok {
		t.Error("First")
	}
	if v, ok := s.Last(); v != 30 || !ok {
		t.Error("Last")
	}
	var empty Slice[string]
	if v, ok := empty.First(); v != "" || ok {
		t.Error("First on empty")
	}
	if v, ok := empty.Last(); v != "" || ok {
		t.Error("Last on empty")
	}
}

func TestFromSliceSharesBackingArray(t *testing.T) {
	src := []int{1, 2}
	FromSlice(src)[0] = 9
	if src[0] != 9 {
		t.Error("FromSlice should share the source's backing array")
	}
}

func TestSliceInsertPrependAppend(t *testing.T) {
	s := NewSlice(1, 2, 3)
	eq(t, "Append", s.Append(4, 5), NewSlice(1, 2, 3, 4, 5))
	eq(t, "Prepend", s.Prepend(-1, 0), NewSlice(-1, 0, 1, 2, 3))
	eq(t, "Insert middle", s.Insert(1, 9), NewSlice(1, 9, 2, 3))
	eq(t, "Insert negative clamps to front", s.Insert(-5, 9), NewSlice(9, 1, 2, 3))
	eq(t, "Insert past end clamps to back", s.Insert(99, 9), NewSlice(1, 2, 3, 9))
	eq(t, "Insert leaves receiver", s, NewSlice(1, 2, 3))

	// Prepend must not write into spare capacity of the items argument.
	backing := []int{7, 0, 0}
	items := backing[:1]
	got := NewSlice(1, 2).Prepend(items...)
	eq(t, "Prepend result", got, NewSlice(7, 1, 2))
	eq(t, "Prepend items backing array", backing, []int{7, 0, 0})
}

func TestSliceRemoveDoesNotMutateReceiver(t *testing.T) {
	s := NewSlice(1, 2, 3, 2)
	eq(t, "Remove", s.Remove(0), NewSlice(2, 3, 2))
	eq(t, "Remove last", s.Remove(3), NewSlice(1, 2, 3))
	eq(t, "Remove out of range", s.Remove(4), s)
	eq(t, "Remove negative", s.Remove(-1), s)
	eq(t, "RemoveValue", s.RemoveValue(2), NewSlice(1, 3, 2))
	eq(t, "RemoveValue absent", s.RemoveValue(9), s)
	eq(t, "RemoveAll", s.RemoveAll(2), NewSlice(1, 3))
	eq(t, "receiver after removals", s, NewSlice(1, 2, 3, 2))
}

func TestSliceFunctional(t *testing.T) {
	s := NewSlice(1, 2, 3, 4, 5)
	isEven := func(n int) bool { return n%2 == 0 }

	eq(t, "Map", Map(s, strconv.Itoa), NewSlice("1", "2", "3", "4", "5"))
	eq(t, "MapWithIndex", MapWithIndex(NewSlice("a", "b"), func(i int, v string) string { return v + strconv.Itoa(i) }), NewSlice("a0", "b1"))
	eq(t, "Filter", s.Filter(isEven), NewSlice(2, 4))
	eq(t, "FilterWithIndex", s.FilterWithIndex(func(i, _ int) bool { return i >= 3 }), NewSlice(4, 5))
	eq(t, "Reduce", Reduce(s, func(acc, n int) int { return acc + n }, 0), 15)
	eq(t, "ReduceWithIndex", ReduceWithIndex(s, func(acc, i, n int) int { return acc + i*n }, 0), 40)
	eq(t, "Reduce empty", Reduce(NewSlice[int](), func(acc, n int) int { return acc + n }, 7), 7)

	if v, ok := s.Find(isEven); v != 2 || !ok {
		t.Error("Find")
	}
	if v, ok := s.Find(func(n int) bool { return n > 9 }); v != 0 || ok {
		t.Error("Find miss")
	}
	eq(t, "FindIndex", s.FindIndex(isEven), 1)
	eq(t, "FindIndex miss", s.FindIndex(func(n int) bool { return n > 9 }), -1)
	eq(t, "CountIf", s.CountIf(isEven), 2)
	if !s.Some(isEven) || s.Every(isEven) || s.None(isEven) {
		t.Error("Some/Every/None")
	}
	if !NewSlice[int]().Every(isEven) || NewSlice[int]().Some(isEven) {
		t.Error("vacuous Every/Some on empty slice")
	}

	var visited []int
	s.ForEach(func(n int) { visited = append(visited, n) })
	eq(t, "ForEach", visited, []int{1, 2, 3, 4, 5})
	var indexes []int
	s.ForEachWithIndex(func(i, _ int) { indexes = append(indexes, i) })
	eq(t, "ForEachWithIndex", indexes, []int{0, 1, 2, 3, 4})
}

func TestSliceSearchUsesDeepEqual(t *testing.T) {
	s := NewSlice([]int{1}, []int{2}, []int{1})
	if !s.Contains([]int{2}) || s.Contains([]int{3}) {
		t.Error("Contains")
	}
	if !s.ContainsAny([]int{9}, []int{1}) || s.ContainsAny([]int{9}) {
		t.Error("ContainsAny")
	}
	if !s.ContainsAll([]int{1}, []int{2}) || s.ContainsAll([]int{1}, []int{9}) {
		t.Error("ContainsAll")
	}
	eq(t, "Index", s.Index([]int{1}), 0)
	eq(t, "LastIndex", s.LastIndex([]int{1}), 2)
	eq(t, "Index miss", s.Index([]int{7}), -1)
	eq(t, "LastIndex miss", s.LastIndex([]int{7}), -1)
	eq(t, "Count", s.Count([]int{1}), 2)
}

func TestSliceOrdering(t *testing.T) {
	s := NewSlice(3, 1, 2)
	eq(t, "Reverse", s.Reverse(), NewSlice(2, 1, 3))
	eq(t, "Reverse even", NewSlice(1, 2, 3, 4).Reverse(), NewSlice(4, 3, 2, 1))
	eq(t, "Reverse empty", NewSlice[int]().Reverse(), Slice[int]{})
	eq(t, "Sort", s.Sort(func(a, b int) bool { return a < b }), NewSlice(1, 2, 3))
	eq(t, "Sort leaves receiver", s, NewSlice(3, 1, 2))
}

func TestSliceUnique(t *testing.T) {
	eq(t, "ints", NewSlice(3, 1, 3, 2, 1).Unique(), NewSlice(3, 1, 2))
	eq(t, "strings", NewSlice("a", "b", "a").Unique(), NewSlice("a", "b"))
	eq(t, "empty", NewSlice[int]().Unique(), Slice[int](nil))

	// Non-comparable elements must not panic ("hash of unhashable type").
	eq(t, "slices", NewSlice([]int{1}, []int{2}, []int{1}).Unique(), NewSlice([]int{1}, []int{2}))
	type tagged struct {
		Tags []string
	}
	eq(t, "structs with slices", NewSlice(tagged{[]string{"x"}}, tagged{[]string{"x"}}).Unique(), NewSlice(tagged{[]string{"x"}}))
	eq(t, "mixed any", NewSlice[any](1, []int{1}, 1, []int{1}, nil, nil, "1").Unique(), NewSlice[any](1, []int{1}, nil, "1"))

	eq(t, "Union", NewSlice(1, 2).Union(NewSlice(2, 3)), NewSlice(1, 2, 3))
	eq(t, "Union of slices", NewSlice([]int{1}).Union(NewSlice([]int{1}, []int{2})), NewSlice([]int{1}, []int{2}))
	eq(t, "Intersect", NewSlice(1, 2, 2, 3).Intersect(NewSlice(2, 3, 4)), NewSlice(2, 3))
	eq(t, "Intersect none", NewSlice(1).Intersect(NewSlice(2)), Slice[int](nil))
	eq(t, "Difference", NewSlice(1, 2, 3).Difference(NewSlice(2)), NewSlice(1, 3))
}

func TestSliceWindows(t *testing.T) {
	s := NewSlice(1, 2, 3, 4, 5)
	tests := []struct {
		name string
		got  Slice[int]
		want Slice[int]
	}{
		{"Slice", s.Slice(1, 3), NewSlice(2, 3)},
		{"Slice negative", s.Slice(-2, 5), NewSlice(4, 5)},
		{"Slice negative end", s.Slice(0, -1), NewSlice(1, 2, 3, 4)},
		{"Slice clamps", s.Slice(-99, 99), s},
		{"Slice inverted", s.Slice(4, 1), Slice[int]{}},
		{"Take", s.Take(2), NewSlice(1, 2)},
		{"Take zero", s.Take(0), Slice[int]{}},
		{"Take over", s.Take(9), s},
		{"TakeLast", s.TakeLast(2), NewSlice(4, 5)},
		{"TakeLast negative", s.TakeLast(-1), Slice[int]{}},
		{"TakeLast over", s.TakeLast(9), s},
		{"Skip", s.Skip(3), NewSlice(4, 5)},
		{"Skip zero", s.Skip(0), s},
		{"Skip over", s.Skip(9), Slice[int]{}},
		{"SkipLast", s.SkipLast(3), NewSlice(1, 2)},
		{"SkipLast zero", s.SkipLast(0), s},
		{"SkipLast over", s.SkipLast(5), Slice[int]{}},
	}
	for _, tc := range tests {
		eq(t, tc.name, tc.got, tc.want)
	}

	eq(t, "Chunk", s.Chunk(2), []Slice[int]{{1, 2}, {3, 4}, {5}})
	eq(t, "Chunk exact", NewSlice(1, 2).Chunk(2), []Slice[int]{{1, 2}})
	eq(t, "Chunk zero size", s.Chunk(0), []Slice[int]{})
	eq(t, "Chunk empty", NewSlice[int]().Chunk(3), []Slice[int](nil))
}

func TestSliceGrouping(t *testing.T) {
	words := NewSlice("apple", "avocado", "banana", "cherry", "blueberry")
	groups := GroupBy(words, func(w string) byte { return w[0] })
	eq(t, "GroupBy a", groups['a'], NewSlice("apple", "avocado"))
	eq(t, "GroupBy b", groups['b'], NewSlice("banana", "blueberry"))
	eq(t, "GroupBy size", len(groups), 3)

	long, short := words.Partition(func(w string) bool { return len(w) > 6 })
	eq(t, "Partition true", long, NewSlice("avocado", "blueberry"))
	eq(t, "Partition false", short, NewSlice("apple", "banana", "cherry"))

	eq(t, "Flatten", Flatten([]Slice[int]{{1}, nil, {2, 3}}), NewSlice(1, 2, 3))
	eq(t, "FlatMap", FlatMap(NewSlice(1, 2), func(n int) Slice[int] { return Repeat(n, n) }), NewSlice(1, 2, 2))

	pairs := Zip(NewSlice(1, 2, 3), NewSlice("a", "b"))
	eq(t, "Zip", pairs, Slice[Pair[int, string]]{{1, "a"}, {2, "b"}})
	eq(t, "Zip empty", Zip(NewSlice[int](), NewSlice("a")), Slice[Pair[int, string]]{})
}

func TestRangesAndGenerators(t *testing.T) {
	eq(t, "Range", Range(2, 5), NewSlice(2, 3, 4))
	eq(t, "Range empty", Range(5, 5), Slice[int]{})
	eq(t, "Range inverted", Range(5, 2), Slice[int]{})
	eq(t, "RangeStep", RangeStep(0, 10, 3), NewSlice(0, 3, 6, 9))
	eq(t, "RangeStep negative", RangeStep(5, 0, -2), NewSlice(5, 3, 1))
	eq(t, "RangeStep zero step", RangeStep(0, 5, 0), Slice[int]{})
	eq(t, "RangeStep wrong direction", RangeStep(0, 5, -1), Slice[int]{})
	eq(t, "RangeStep wrong direction negative", RangeStep(5, 0, 1), Slice[int]{})

	eq(t, "Repeat", Repeat("x", 3), NewSlice("x", "x", "x"))
	eq(t, "Repeat zero", Repeat("x", 0), Slice[string]{})
	eq(t, "Repeat negative", Repeat("x", -1), Slice[string]{})

	eq(t, "Times", Times(3, func(i int) int { return i * i }), NewSlice(0, 1, 4))
	called := false
	eq(t, "Times negative", Times(-2, func(int) int { called = true; return 0 }), Slice[int]{})
	if called {
		t.Error("Times called fn for a negative count")
	}
}

func TestMapConversions(t *testing.T) {
	type user struct {
		ID   int
		Name string
	}
	users := NewSlice(user{1, "ana"}, user{2, "bo"}, user{1, "ana2"})
	byID := ToMap(users, func(u user) int { return u.ID })
	eq(t, "ToMap last wins", byID, map[int]user{1: {1, "ana2"}, 2: {2, "bo"}})
	names := ToMapValue(users, func(u user) int { return u.ID }, func(u user) string { return u.Name })
	eq(t, "ToMapValue", names, map[int]string{1: "ana2", 2: "bo"})

	m := map[string]int{"b": 2, "a": 1}
	keys := Keys(m).ToSlice()
	sort.Strings(keys)
	eq(t, "Keys", keys, []string{"a", "b"})
	values := Values(m).ToSlice()
	sort.Ints(values)
	eq(t, "Values", values, []int{1, 2})
	eq(t, "Keys nil map", Keys[string, int](nil), Slice[string]{})
}

func TestMinMaxBy(t *testing.T) {
	less := func(a, b string) bool { return len(a) < len(b) }
	words := NewSlice("ccc", "a", "bb", "d")
	if v, ok := MinBy(words, less); v != "a" || !ok {
		t.Errorf("MinBy = %q, %v (want the first shortest)", v, ok)
	}
	if v, ok := MaxBy(words, less); v != "ccc" || !ok {
		t.Errorf("MaxBy = %q, %v", v, ok)
	}
	if v, ok := MinBy(NewSlice[string](), less); v != "" || ok {
		t.Error("MinBy empty")
	}
	if v, ok := MaxBy(NewSlice[string](), less); v != "" || ok {
		t.Error("MaxBy empty")
	}
}
