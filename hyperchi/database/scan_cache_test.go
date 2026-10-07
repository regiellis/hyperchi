package database

import (
	"reflect"
	"sync"
	"testing"
)

// TestStructFieldsAreCachedPerType checks the field mapping is built once per
// struct type and reused for every later row.
func TestStructFieldsAreCachedPerType(t *testing.T) {
	type cachedRow struct {
		ID   int64  `db:"id"`
		Name string `db:"name"`
		Skip string `db:"-"`
		Age  int
	}
	typ := reflect.TypeFor[cachedRow]()
	fieldMaps.Delete(typ)

	db := seedUsers(t)
	rows, err := ListStruct[cachedRow](db, "SELECT id, name, age AS Age FROM users ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[1].Name != "Grace" || rows[1].Age != 45 {
		t.Fatalf("rows = %+v", rows)
	}

	cached, ok := fieldMaps.Load(typ)
	if !ok {
		t.Fatal("ListStruct did not cache the field mapping")
	}
	want := map[string]int{"id": 0, "name": 1, "Age": 3}
	if !reflect.DeepEqual(cached, want) {
		t.Fatalf("cached fields = %v, want %v", cached, want)
	}
	if _, ok := structFields(reflect.TypeFor[userRow]())["internal"]; ok {
		t.Fatal("an unexported field was mapped")
	}
	again := structFields(typ)
	if reflect.ValueOf(again).Pointer() != reflect.ValueOf(cached).Pointer() {
		t.Fatal("structFields rebuilt the mapping instead of reusing the cached one")
	}
}

// TestScanIntoStructConcurrent exercises the cache from many goroutines over
// two types; run it with -race.
func TestScanIntoStructConcurrent(t *testing.T) {
	type nameOnly struct {
		Name string `db:"name"`
	}
	db := seedUsers(t)
	fieldMaps.Delete(reflect.TypeFor[userRow]())
	fieldMaps.Delete(reflect.TypeFor[nameOnly]())

	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := range 40 {
		wg.Go(func() {
			if i%2 == 0 {
				rows, err := ListStruct[userRow](db, "SELECT id, name, age FROM users ORDER BY id")
				if err == nil && (len(rows) != 3 || rows[0].Name != "Ada") {
					t.Errorf("userRow rows = %+v", rows)
				}
				errs <- err
				return
			}
			row, err := GetStruct[nameOnly](db, "SELECT name FROM users WHERE id = 3")
			if err == nil && row.Name != "Lin" {
				t.Errorf("nameOnly row = %+v", row)
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
