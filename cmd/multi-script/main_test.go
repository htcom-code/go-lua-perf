package main

import (
	"fmt"
	"runtime"
	"sync"
	"testing"

	lua "github.com/htcom-code/lua-pure/lua"
)

const upperSrc = `function process(s) return "UP:" .. string.upper(s) end`
const twiceSrc = `function transform(n) return tostring(n * 2) end`

// Concern 1: N goroutines requesting the same key concurrently compile exactly once.
func TestCompileOnceUnderConcurrency(t *testing.T) {
	cc := NewCompileCache()
	const n = 100

	var wg sync.WaitGroup
	protos := make([]*lua.Proto, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			protos[i], errs[i] = cc.GetOrCompile("same", upperSrc)
		}(i)
	}
	wg.Wait()

	if got := cc.CompileCount(); got != 1 {
		t.Fatalf("expected exactly 1 compile, got %d", got)
	}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if protos[i] != protos[0] {
			t.Fatalf("goroutine %d got a different *Proto pointer", i)
		}
	}
}

// Different keys each compile once.
func TestCompileDifferentKeys(t *testing.T) {
	cc := NewCompileCache()
	if _, err := cc.GetOrCompile("a", upperSrc); err != nil {
		t.Fatal(err)
	}
	if _, err := cc.GetOrCompile("b", twiceSrc); err != nil {
		t.Fatal(err)
	}
	if _, err := cc.GetOrCompile("a", upperSrc); err != nil { // cache hit
		t.Fatal(err)
	}
	if got := cc.CompileCount(); got != 2 {
		t.Fatalf("expected 2 compiles (a,b), got %d", got)
	}
}

// A compile error is cached and compilation is attempted only once.
func TestCompileErrorCached(t *testing.T) {
	cc := NewCompileCache()
	bad := `function process( oops` // syntax error
	_, err1 := cc.GetOrCompile("bad", bad)
	_, err2 := cc.GetOrCompile("bad", bad)
	if err1 == nil || err2 == nil {
		t.Fatal("expected compile error")
	}
	if got := cc.CompileCount(); got != 1 {
		t.Fatalf("expected 1 compile attempt, got %d", got)
	}
}

// Multi-script execution + pool isolation.
func TestMultiScriptRun(t *testing.T) {
	rt := NewScriptRuntime()
	mustRegister(t, rt, "upper", upperSrc)
	mustRegister(t, rt, "twice", twiceSrc)

	up, err := rt.Run("upper", "process", lua.MkString("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if got := up[0].Str(); got != "UP:HELLO" {
		t.Fatalf("upper: want UP:HELLO, got %q", got)
	}

	tw, err := rt.Run("twice", "transform", lua.Int(21))
	if err != nil {
		t.Fatal(err)
	}
	if got := tw[0].Str(); got != "42" {
		t.Fatalf("twice: want 42, got %q", got)
	}
}

func TestRunUnregistered(t *testing.T) {
	rt := NewScriptRuntime()
	if _, err := rt.Run("nope", "process"); err == nil {
		t.Fatal("expected error for unregistered script")
	}
}

// Repeated reuse stays correct thanks to stack cleanup.
func TestPoolReuseStackClean(t *testing.T) {
	rt := NewScriptRuntime()
	mustRegister(t, rt, "upper", upperSrc)
	for i := 0; i < 200; i++ {
		out, err := rt.Run("upper", "process", lua.MkString("x"))
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if got := out[0].Str(); got != "UP:X" {
			t.Fatalf("iter %d: want UP:X, got %q", i, got)
		}
	}
}

// Run several keys concurrently from goroutines (checked with go test -race).
func TestParallelDifferentKeys(t *testing.T) {
	rt := NewScriptRuntime()
	mustRegister(t, rt, "upper", upperSrc)
	mustRegister(t, rt, "twice", twiceSrc)

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				out, err := rt.Run("upper", "process", lua.MkString(fmt.Sprintf("n%d", i)))
				if err != nil {
					t.Errorf("upper %d: %v", i, err)
					return
				}
				if want := fmt.Sprintf("UP:N%d", i); out[0].Str() != want {
					t.Errorf("upper %d: want %s got %s", i, want, out[0].Str())
				}
			} else {
				out, err := rt.Run("twice", "transform", lua.Int(int64(i)))
				if err != nil {
					t.Errorf("twice %d: %v", i, err)
					return
				}
				if want := fmt.Sprintf("%d", i*2); out[0].Str() != want {
					t.Errorf("twice %d: want %s got %s", i, want, out[0].Str())
				}
			}
		}(i)
	}
	wg.Wait()
}

// Fixed channel pool behavior + Close.
func TestFixedPool(t *testing.T) {
	rt := NewScriptRuntimeFixed(4)
	defer rt.Close()
	mustRegister(t, rt, "upper", upperSrc)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, err := rt.Run("upper", "process", lua.MkString("y"))
			if err != nil {
				t.Errorf("iter %d: %v", i, err)
				return
			}
			if out[0].Str() != "UP:Y" {
				t.Errorf("iter %d: got %q", i, out[0].Str())
			}
		}(i)
	}
	wg.Wait()
}

// The sync.Pool path still works after GC by rebuilding (Concern 2 illustration).
func TestPoolGCBehavior(t *testing.T) {
	rt := NewScriptRuntime()
	mustRegister(t, rt, "upper", upperSrc)

	if _, err := rt.Run("upper", "process", lua.MkString("a")); err != nil {
		t.Fatal(err)
	}
	// Idle pool entries may be reclaimed by GC — even if so, the next Get rebuilds via New.
	runtime.GC()
	runtime.GC()
	out, err := rt.Run("upper", "process", lua.MkString("b"))
	if err != nil {
		t.Fatalf("after GC: %v", err)
	}
	if got := out[0].Str(); got != "UP:B" {
		t.Fatalf("after GC: want UP:B, got %q", got)
	}
}

func mustRegister(t *testing.T, rt *ScriptRuntime, key, src string) {
	t.Helper()
	if err := rt.Register(key, src); err != nil {
		t.Fatalf("register %q: %v", key, err)
	}
}

// ─────────────────────────────── benchmarks ───────────────────────────────

// Pooled reuse (sync.Pool) — parallel.
func BenchmarkMultiScriptPooled(b *testing.B) {
	rt := NewScriptRuntime()
	if err := rt.Register("upper", upperSrc); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := rt.Run("upper", "process", lua.MkString("data")); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// Pooled reuse (fixed channel pool) — parallel.
func BenchmarkMultiScriptFixed(b *testing.B) {
	rt := NewScriptRuntimeFixed(runtime.GOMAXPROCS(0))
	defer rt.Close()
	if err := rt.Register("upper", upperSrc); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := rt.Run("upper", "process", lua.MkString("data")); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// Control — build + preload + run + Close a State every iteration.
func BenchmarkMultiScriptNoPool(b *testing.B) {
	cc := NewCompileCache()
	proto, err := cc.GetOrCompile("upper", upperSrc)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			L := buildPreloadedState("upper", proto)
			fn := L.GetGlobal("process")
			if _, err := L.Call(fn, []lua.Value{lua.MkString("data")}, 1); err != nil {
				b.Fatal(err)
			}
			L.Close()
		}
	})
}

// Concurrent compile of the same key (cache-hit path).
func BenchmarkConcurrentCompileSameKey(b *testing.B) {
	cc := NewCompileCache()
	if _, err := cc.GetOrCompile("k", upperSrc); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := cc.GetOrCompile("k", upperSrc); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// NewState library-loading cost: only-needed vs full OpenLibs.
func BenchmarkNewStateSelectiveLibs(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		L := newConfiguredState()
		L.Close()
	}
}

func BenchmarkNewStateAllLibs(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		L := lua.NewState(lua.WithOpenLibs()) // all standard libraries
		L.Close()
	}
}
