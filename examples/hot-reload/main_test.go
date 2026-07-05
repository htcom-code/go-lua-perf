package main

import "testing"

func TestReloadDemo(t *testing.T) {
	before, after, err := reloadDemo()
	if err != nil {
		t.Fatalf("reloadDemo: %v", err)
	}
	if before != 100 {
		t.Fatalf("before reload: got %.1f, want 100", before)
	}
	if after != 150 {
		t.Fatalf("after reload: got %.1f, want 150 (new version not picked up)", after)
	}
}
