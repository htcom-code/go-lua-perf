package main

import (
	"reflect"
	"testing"
)

func TestObserveDemo(t *testing.T) {
	compiles, keys, stats, err := observeDemo()
	if err != nil {
		t.Fatalf("observeDemo: %v", err)
	}
	if compiles != 2 {
		t.Fatalf("compiles: got %d, want 2", compiles)
	}
	if want := []string{"a", "b"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("pool keys: got %v, want %v", keys, want)
	}
	if stats.Pools != 2 {
		t.Fatalf("stats.Pools: got %d, want 2", stats.Pools)
	}
}
