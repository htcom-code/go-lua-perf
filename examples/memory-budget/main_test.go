package main

import "testing"

func TestBudgetDemo(t *testing.T) {
	maxStates, liveWithinCap, err := budgetDemo()
	if err != nil {
		t.Fatalf("budgetDemo: %v", err)
	}
	if maxStates < 1 {
		t.Fatalf("MaxStates: got %d, want >= 1 (derived from budget)", maxStates)
	}
	if !liveWithinCap {
		t.Fatal("live States exceeded the derived MaxStates cap")
	}
}
