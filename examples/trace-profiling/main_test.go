package main

import "testing"

func TestTraceDemo(t *testing.T) {
	stages, err := traceDemo()
	if err != nil {
		t.Fatalf("traceDemo: %v", err)
	}
	// A cold first run touches load + compile (pool creation), build (new State),
	// and execute (the call).
	for _, want := range []string{"load", "compile", "build", "execute"} {
		if _, ok := stages[want]; !ok {
			t.Fatalf("missing trace stage %q; saw %v", want, stages)
		}
	}
}
