package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoggingDemo(t *testing.T) {
	var buf bytes.Buffer
	if err := loggingDemo(&buf); err != nil {
		t.Fatalf("loggingDemo: %v", err)
	}
	out := buf.String()
	if out == "" {
		t.Fatal("expected slog output, got none")
	}
	if !strings.Contains(out, "pool loaded") {
		t.Fatalf("expected a 'pool loaded' event, got:\n%s", out)
	}
}
