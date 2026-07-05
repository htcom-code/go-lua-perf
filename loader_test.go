package luart

// Unit tests for loader.go — SourceLoader (MapLoader) and CompileCache.
// Convention: basic behavior + exception cases + benchmarks (CONTRIBUTING.md).

import (
	"sync"
	"testing"
)

// ── test fixtures (loader-only) ──

const (
	loaderSrcV1 = `function run(x) return x end`
	loaderSrcV2 = `function run(x) return x .. "!" end`
	compSrc     = `local s = 0; for i = 1, 10 do s = s + i end; return s`
	compBadSrc  = `function run( oops` // syntax error
)

// ─────────────────────────────────────────────────────────────────────────────
// MapLoader
// ─────────────────────────────────────────────────────────────────────────────

// TestMapLoader_Basic verifies that Load returns what Set stored and that the
// Load call count is tracked.
// Since: 2026-06-07
func TestMapLoader_Basic(t *testing.T) {
	l := NewMapLoader()
	l.Set("k", loaderSrcV1, "v1", "1.0.0")

	src, ver, disp, err := l.Load("k")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if src != loaderSrcV1 || ver != "v1" || disp != "1.0.0" {
		t.Fatalf("got src=%q ver=%q disp=%q", src, ver, disp)
	}
	if l.Loads() != 1 {
		t.Fatalf("Loads()=%d, want 1", l.Loads())
	}

	// Update via Set (simulating an external change).
	l.Set("k", loaderSrcV2, "v2", "2.0.0")
	if _, ver, disp, _ = l.Load("k"); ver != "v2" || disp != "2.0.0" {
		t.Fatalf("after update ver=%q disp=%q, want v2/2.0.0", ver, disp)
	}
}

// TestMapLoader_Exceptions verifies MapLoader edge cases: unknown-key error and
// concurrent Load count accuracy (no race).
// Since: 2026-06-07
func TestMapLoader_Exceptions(t *testing.T) {
	l := NewMapLoader()

	// Unknown key → error + empty values.
	if src, ver, disp, err := l.Load("missing"); err == nil || src != "" || ver != "" || disp != "" {
		t.Fatalf("missing key should error with empty values, got src=%q ver=%q disp=%q err=%v", src, ver, disp, err)
	}

	// Concurrent Load count (atomic verified under go test -race).
	l.Set("k", loaderSrcV1, "v1", "")
	const n = 100
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, _, err := l.Load("k"); err != nil {
				t.Errorf("concurrent Load: %v", err)
			}
		}()
	}
	wg.Wait()
	if l.Loads() != int64(n+1) { // +1: the "missing" call
		t.Fatalf("Loads()=%d, want %d", l.Loads(), n+1)
	}
}

// TestHashVersion verifies the content hash is equal for the same source,
// differs for different sources, and has sha256 hex length.
// Since: 2026-06-07
func TestHashVersion(t *testing.T) {
	a := HashVersion(loaderSrcV1)
	b := HashVersion(loaderSrcV1)
	c := HashVersion(loaderSrcV2)
	if a != b {
		t.Fatal("same source must hash equal")
	}
	if a == c {
		t.Fatal("different source must hash differently")
	}
	if len(a) != 64 {
		t.Fatalf("sha256 hex length = %d, want 64", len(a))
	}
}

// BenchmarkMapLoaderLoad measures the cost of MapLoader.Load.
// Since: 2026-06-07
func BenchmarkMapLoaderLoad(b *testing.B) {
	l := NewMapLoader()
	l.Set("k", loaderSrcV1, "v1", "1.0.0")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, _, _, err := l.Load("k"); err != nil {
			b.Fatal(err)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CompileCache
// ─────────────────────────────────────────────────────────────────────────────

// TestCompileCache_Basic verifies a cache hit returns the same *FunctionProto
// pointer and the same key compiles only once.
// Since: 2026-06-07
func TestCompileCache_Basic(t *testing.T) {
	cc := NewCompileCache()
	p1, err := cc.GetOrCompile("k:v1", "k", compSrc)
	if err != nil {
		t.Fatalf("first compile: %v", err)
	}
	if p1 == nil {
		t.Fatal("nil proto")
	}
	p2, err := cc.GetOrCompile("k:v1", "k", compSrc)
	if err != nil {
		t.Fatalf("second compile: %v", err)
	}
	if p1 != p2 {
		t.Fatal("cache hit should return the same *FunctionProto pointer")
	}
	if cc.CompileCount() != 1 {
		t.Fatalf("CompileCount()=%d, want 1", cc.CompileCount())
	}
}

// TestCompileCache_DifferentKeys verifies distinct cache keys each compile once
// (the basis for recompiling when key:version changes).
// Since: 2026-06-07
func TestCompileCache_DifferentKeys(t *testing.T) {
	cc := NewCompileCache()
	if _, err := cc.GetOrCompile("k:v1", "k", compSrc); err != nil {
		t.Fatal(err)
	}
	if _, err := cc.GetOrCompile("k:v2", "k", loaderSrcV1); err != nil {
		t.Fatal(err)
	}
	if cc.CompileCount() != 2 {
		t.Fatalf("CompileCount()=%d, want 2", cc.CompileCount())
	}
}

// TestCompileCache_Exceptions verifies edge cases:
// (a) a compile error is cached, so retries do not recompile;
// (b) N goroutines racing on the same key compile exactly once.
// Since: 2026-06-07
func TestCompileCache_Exceptions(t *testing.T) {
	// (a) cached compile error
	cc := NewCompileCache()
	if _, err := cc.GetOrCompile("bad:v1", "bad", compBadSrc); err == nil {
		t.Fatal("expected compile error")
	}
	if _, err := cc.GetOrCompile("bad:v1", "bad", compBadSrc); err == nil {
		t.Fatal("expected cached compile error")
	}
	if cc.CompileCount() != 1 {
		t.Fatalf("error should be cached: CompileCount()=%d, want 1", cc.CompileCount())
	}

	// (b) compile once under concurrency
	cc2 := NewCompileCache()
	const n = 100
	var wg sync.WaitGroup
	protos := make([]interface{}, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p, err := cc2.GetOrCompile("k:v1", "k", compSrc)
			if err != nil {
				t.Errorf("goroutine %d: %v", i, err)
				return
			}
			protos[i] = p
		}(i)
	}
	wg.Wait()
	if cc2.CompileCount() != 1 {
		t.Fatalf("concurrent same-key CompileCount()=%d, want 1", cc2.CompileCount())
	}
	for i := 1; i < n; i++ {
		if protos[i] != protos[0] {
			t.Fatalf("goroutine %d got a different proto pointer", i)
		}
	}
}

// BenchmarkCompileCacheHit measures the cache-hit path (no recompile).
// Since: 2026-06-07
func BenchmarkCompileCacheHit(b *testing.B) {
	cc := NewCompileCache()
	if _, err := cc.GetOrCompile("k:v1", "k", compSrc); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := cc.GetOrCompile("k:v1", "k", compSrc); err != nil {
			b.Fatal(err)
		}
	}
}
