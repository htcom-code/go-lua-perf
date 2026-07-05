package main

import "testing"

func TestMetricsDemo(t *testing.T) {
	m, err := metricsDemo()
	if err != nil {
		t.Fatalf("metricsDemo: %v", err)
	}
	if got := m.compile.Load(); got != 1 {
		t.Fatalf("compile: got %d, want 1", got)
	}
	if got := m.build.Load(); got != 1 {
		t.Fatalf("build: got %d, want 1 (single pooled State)", got)
	}
	if got := m.reuse.Load(); got != 4 {
		t.Fatalf("reuse: got %d, want 4 (5 runs, 1 build + 4 reuse)", got)
	}
}
