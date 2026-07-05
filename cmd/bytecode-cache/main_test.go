package main

import (
	"testing"

	lua "github.com/htcom-code/lua-pure/lua"
)

const benchSource = `
	local sum = 0
	for i = 1, 100 do
		sum = sum + i
	end
	return sum
`

// Verify a cache hit returns the same *lua.Proto pointer and that it
// actually executes.
func TestGetOrCompileCacheHit(t *testing.T) {
	bc := NewBytecodeCache()

	p1, err := bc.GetOrCompile("sum", benchSource)
	if err != nil {
		t.Fatalf("first compile failed: %v", err)
	}
	p2, err := bc.GetOrCompile("sum", benchSource)
	if err != nil {
		t.Fatalf("second compile failed: %v", err)
	}
	if p1 != p2 {
		t.Fatalf("expected cache hit to return the same *Proto, got different pointers")
	}

	// Run the prototype on a State and check the result (sum 1..100 = 5050).
	L := lua.NewState()
	defer L.Close()
	rets, err := L.CallProto(p1, 1)
	if err != nil {
		t.Fatalf("CallProto failed: %v", err)
	}
	if got := rets[0].AsInt(); got != 5050 {
		t.Fatalf("expected 5050, got %d", got)
	}
}

// Parse + compile every time (no cache).
func BenchmarkCompileEveryTime(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := lua.CompileString(benchSource, "bench"); err != nil {
			b.Fatal(err)
		}
	}
}

// Reuse the cached prototype.
func BenchmarkCachedCompile(b *testing.B) {
	bc := NewBytecodeCache()
	if _, err := bc.GetOrCompile("bench", benchSource); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := bc.GetOrCompile("bench", benchSource); err != nil {
			b.Fatal(err)
		}
	}
}
