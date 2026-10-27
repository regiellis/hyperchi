package helpers

import (
	"errors"
	"testing"
)

// Try and TryValue convert a panic into an error instead of reporting
// success, and Catch then calls its handler.

func TestTryConvertsPanicToError(t *testing.T) {
	err := Try(func() error { panic("boom") })
	if err == nil {
		t.Fatal("Try should return an error when fn panics")
	}
}

func TestTryReturnsNilOnSuccess(t *testing.T) {
	if err := Try(func() error { return nil }); err != nil {
		t.Fatalf("Try returned %v on success", err)
	}
}

func TestTryPropagatesReturnedError(t *testing.T) {
	sentinel := errors.New("real error")
	if err := Try(func() error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("Try should propagate the returned error, got %v", err)
	}
}

func TestTryValueConvertsPanicAndZeroValue(t *testing.T) {
	v, err := TryValue(func() (int, error) { panic("boom") })
	if err == nil {
		t.Fatal("TryValue should return an error when fn panics")
	}
	if v != 0 {
		t.Fatalf("TryValue should return the zero value on panic, got %d", v)
	}
}

func TestCatchInvokesHandlerOnPanic(t *testing.T) {
	var got error
	Catch(func() error { panic("boom") }, func(e error) { got = e })
	if got == nil {
		t.Fatal("Catch handler should be invoked when fn panics")
	}
}
