package main

import "testing"

func TestEvictDemo(t *testing.T) {
	afterRun, afterTTL, err := evictDemo()
	if err != nil {
		t.Fatalf("evictDemo: %v", err)
	}
	if afterRun != 1 {
		t.Fatalf("pools after run: got %d, want 1", afterRun)
	}
	if afterTTL != 0 {
		t.Fatalf("pools after TTL: got %d, want 0 (janitor should have evicted)", afterTTL)
	}
}
