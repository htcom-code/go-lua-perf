package main

import (
	"context"
	"errors"
	"testing"
)

func TestExecTimeout(t *testing.T) {
	if err := timeoutDemo(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ExecTimeout: got %v, want context.DeadlineExceeded", err)
	}
}

func TestContextDeadline(t *testing.T) {
	if err := ctxDemo(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ctx deadline: got %v, want context.DeadlineExceeded", err)
	}
}
