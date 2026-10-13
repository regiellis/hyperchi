package hyperchi

import (
	"bytes"
	"testing"
)

func TestKVCacheBytesRoundTrip(t *testing.T) {
	registry := NewKVRegistry()
	store := NewMemoryStore()
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := registry.Register("memory", store); err != nil {
		t.Fatal(err)
	}
	cache, err := NewKVCache(registry, KVCacheConfig{Backend: "memory"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		value any
		want  []byte
	}{
		{"text_bytes", []byte("hello"), []byte("hello")},
		{"binary", []byte{0, 255, 1, 128}, []byte{0, 255, 1, 128}},
		{"empty", []byte{}, []byte{}},
		{"nil", []byte(nil), nil},
		{"plain_string", "hello", []byte("hello")},
		{"base64_string", "aGVsbG8=", []byte("aGVsbG8=")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := cache.Set(tc.name, tc.value); err != nil {
				t.Fatal(err)
			}
			got, ok := cache.GetBytes(tc.name)
			if !ok || !bytes.Equal(got, tc.want) {
				t.Fatalf("GetBytes = %q, %v; want %q, true", got, ok, tc.want)
			}
			if _, isBytes := tc.value.([]byte); isBytes {
				value, ok := cache.Get(tc.name)
				if _, restored := value.([]byte); !ok || !restored {
					t.Errorf("Get = %T, %v; want []byte, true", value, ok)
				}
			}
		})
	}
}
