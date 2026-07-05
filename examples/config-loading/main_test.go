package main

import (
	"testing"
	"time"
)

func TestResolveDemo(t *testing.T) {
	cfg, err := resolveDemo()
	if err != nil {
		t.Fatalf("resolveDemo: %v", err)
	}
	if cfg.MaxStates != 16 {
		t.Fatalf("MaxStates: got %d, want 16 (env should override JSON)", cfg.MaxStates)
	}
	if cfg.IdleTTL != 5*time.Minute {
		t.Fatalf("IdleTTL: got %s, want 5m (from JSON base)", cfg.IdleTTL)
	}
}
