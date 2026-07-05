package main

import "testing"

func TestGreet(t *testing.T) {
	got, err := greet("luart")
	if err != nil {
		t.Fatalf("greet: %v", err)
	}
	if want := "hello, luart"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
