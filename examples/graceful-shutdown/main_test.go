package main

import (
	"errors"
	"testing"

	luart "github.com/htcom-code/go-lua-perf"
)

func TestShutdownDemo(t *testing.T) {
	afterErr, err := shutdownDemo()
	if err != nil {
		t.Fatalf("shutdownDemo: %v", err)
	}
	if !errors.Is(afterErr, luart.ErrClosed) {
		t.Fatalf("Run after Shutdown: got %v, want luart.ErrClosed", afterErr)
	}
}
