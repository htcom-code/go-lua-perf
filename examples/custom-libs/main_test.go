package main

import "testing"

// TestCustomLibsRun verifies the custom library (clamp + host.get) added via
// Config.ExtraLibs is reachable from the script alongside the default sandbox
// libs, and that clamp bounds its input (150 → 100).
func TestCustomLibsRun(t *testing.T) {
	got, err := run()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "clamped=100 region=kr up=OK"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
