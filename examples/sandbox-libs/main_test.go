package main

import "testing"

func TestSandboxBlocksOS(t *testing.T) {
	if err := sandboxed(); err == nil {
		t.Fatal("expected default-libs script to fail (os should be unavailable)")
	}
}

func TestAllowedOpensOS(t *testing.T) {
	if err := allowed(); err != nil {
		t.Fatalf("expected os-enabled script to succeed, got: %v", err)
	}
}
