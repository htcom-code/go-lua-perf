package main

import (
	"testing"

	lua "github.com/htcom-code/lua-pure/lua"
)

// Verify the preloaded process function takes an argument and returns the
// correct result.
func TestExecuteLuaTask(t *testing.T) {
	got, err := ExecuteLuaTask("Data-1")
	if err != nil {
		t.Fatalf("ExecuteLuaTask failed: %v", err)
	}
	if want := "Processed: Data-1"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

// Verify a VM taken from the pool still works after being returned and taken
// again (stack cleared) — reuse safety.
func TestExecuteLuaTaskReuse(t *testing.T) {
	for i := 0; i < 100; i++ {
		got, err := ExecuteLuaTask("X")
		if err != nil {
			t.Fatalf("iteration %d failed: %v", i, err)
		}
		if got != "Processed: X" {
			t.Fatalf("iteration %d: unexpected result %q", i, got)
		}
	}
}

// Pooled variant (Get/Put a preloaded VM) — parallel.
func BenchmarkExecuteWithPool(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := ExecuteLuaTask("Data"); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// Control: lua.NewState + compile + run every time, no pool — parallel.
func BenchmarkExecuteWithoutPool(b *testing.B) {
	const src = `
		function process(heavyData)
			return "Processed: " .. heavyData
		end
	`
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			L := lua.NewState(lua.WithOpenLibs())
			proto, err := compileLuaSource("noPool", src)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := L.CallProto(proto, 0); err != nil {
				b.Fatal(err)
			}
			fn := L.GetGlobal("process")
			if _, err := L.Call(fn, []lua.Value{lua.MkString("Data")}, 1); err != nil {
				b.Fatal(err)
			}
			L.Close()
		}
	})
}
