package luart

// Unit tests for manager.go — Runtime (lazy load, TTL, drop hot reload, LRU,
// memory sizing) and helpers.
// Convention: basic behavior + exception cases + benchmarks (CONTRIBUTING.md).

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	lua "github.com/htcom-code/lua-pure/lua"
)

// ── test fixtures (manager-only) ──

const (
	greetV1   = `function run(name) return "v1:" .. name end`
	greetV2   = `function run(name) return "v2:" .. name end`
	doubleSrc = `function run(n) return tostring(n * 2) end`

	// spinSrc has a runaway loop (spin) plus a fast function (ok) sharing one
	// pooled State — used to test ExecTimeout abort and clean pooled reuse.
	spinSrc = `function spin() while true do end end
function ok(name) return "ok:" .. name end`
)

// newTestManager builds a Runtime with long TTL/interval so the janitor does not
// interfere during tests.
// Since: 2026-06-07
func newTestManager(t *testing.T, loader SourceLoader, maxStates int) *Runtime {
	t.Helper()
	rt := New(loader, Config{
		MaxStates:       maxStates,
		IdleTTL:         time.Hour,
		JanitorInterval: time.Hour,
	})
	t.Cleanup(rt.Close)
	return rt
}

// ─────────────────────────────────────────────────────────────────────────────
// Basic behavior
// ─────────────────────────────────────────────────────────────────────────────

// TestLazyLoadFromDB verifies that running the same key many times loads and
// compiles the source only once.
// Since: 2026-06-07
func TestLazyLoadFromDB(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("greet", greetV1, "v1", "")
	rt := newTestManager(t, loader, 8)

	ctx := context.Background()
	for i := 0; i < 10; i++ {
		out, err := rt.Run(ctx, "greet", "run", lua.MkString("x"))
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if out[0].Str() != "v1:x" {
			t.Fatalf("iter %d: got %q", i, out[0].Str())
		}
	}
	if got := loader.Loads(); got != 1 {
		t.Fatalf("expected 1 lazy load, got %d", got)
	}
	if got := rt.cc.CompileCount(); got != 1 {
		t.Fatalf("expected 1 compile, got %d", got)
	}
}

// TestConcurrentLazyLoadOnce verifies that many goroutines requesting the same
// key for the first time concurrently still load the source only once.
// Since: 2026-06-07
func TestConcurrentLazyLoadOnce(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("greet", greetV1, "v1", "")
	rt := newTestManager(t, loader, 16)

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := rt.Run(context.Background(), "greet", "run", lua.MkString("y")); err != nil {
				t.Errorf("run: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := loader.Loads(); got != 1 {
		t.Fatalf("expected exactly 1 load under concurrency, got %d", got)
	}
}

// TestIdleEviction verifies the janitor evicts idle pools past their TTL so live
// state count returns to zero.
// Since: 2026-06-07
func TestIdleEviction(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("greet", greetV1, "v1", "")
	rt := New(loader, Config{
		MaxStates:       8,
		IdleTTL:         50 * time.Millisecond,
		JanitorInterval: 20 * time.Millisecond,
	})
	t.Cleanup(rt.Close)

	if _, err := rt.Run(context.Background(), "greet", "run", lua.MkString("a")); err != nil {
		t.Fatal(err)
	}
	// Confirm the pool was actually created via a single load (an immediate
	// live>0 assertion would race the fast janitor and be flaky).
	if loader.Loads() != 1 {
		t.Fatalf("expected the pool to have been created (1 load), got %d", loader.Loads())
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		s := rt.Stats()
		if s.Pools == 0 && s.LiveStates == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("idle pool not evicted: pools=%d live=%d", s.Pools, s.LiveStates)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestMaxPoolSizeWithLRU verifies many-key requests are served without exceeding
// the global cap (LRU eviction) and that eviction does not trigger recompiles.
// Since: 2026-06-07
func TestMaxPoolSizeWithLRU(t *testing.T) {
	loader := NewMapLoader()
	keys := []string{"k0", "k1", "k2", "k3", "k4"}
	for _, k := range keys {
		loader.Set(k, doubleSrc, "v1", "")
	}
	const maxStates = 2
	rt := newTestManager(t, loader, maxStates)

	var wg sync.WaitGroup
	for i := 0; i < 300; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k := keys[i%len(keys)]
			out, err := rt.Run(context.Background(), k, "run", lua.Int(int64(i)))
			if err != nil {
				t.Errorf("%s #%d: %v", k, i, err)
				return
			}
			if want := fmt.Sprintf("%d", i*2); out[0].Str() != want {
				t.Errorf("%s #%d: want %s got %s", k, i, want, out[0].Str())
			}
		}(i)
	}
	wg.Wait()

	if live := rt.Stats().LiveStates; live > maxStates {
		t.Fatalf("live states %d exceeded cap %d", live, maxStates)
	}
	if got := rt.cc.CompileCount(); got != int64(len(keys)) {
		t.Fatalf("expected %d compiles (one per key), got %d — eviction must not recompile", len(keys), got)
	}
}

// TestMemoryBudgetSizing verifies MaxStates is derived as MemoryBudgetBytes /
// measured perState and scales roughly with the budget.
// Since: 2026-06-07
func TestMemoryBudgetSizing(t *testing.T) {
	loader := NewMapLoader()
	const ratio = 32
	small := New(loader, Config{MemoryBudgetBytes: 2 << 20, IdleTTL: time.Hour, JanitorInterval: time.Hour})
	defer small.Close()
	big := New(loader, Config{MemoryBudgetBytes: 2 * ratio << 20, IdleTTL: time.Hour, JanitorInterval: time.Hour})
	defer big.Close()

	sm, bg := small.Stats().MaxStates, big.Stats().MaxStates
	if sm < 1 {
		t.Fatalf("small budget should yield >=1 state, got %d", sm)
	}
	if bg < sm*4 {
		t.Fatalf("budget %dx should yield many more states: small=%d big=%d", ratio, sm, bg)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Exception / edge cases — safety, failures, lifecycle, accounting
// ─────────────────────────────────────────────────────────────────────────────

// TestUnknownScript verifies running an unregistered key returns an error.
// Since: 2026-06-07
func TestUnknownScript(t *testing.T) {
	rt := newTestManager(t, NewMapLoader(), 4)
	if _, err := rt.Run(context.Background(), "nope", "run"); err == nil {
		t.Fatal("expected error for unknown script")
	}
}

// (the swap-based in-flight discard test is replaced by the drop-model
// TestNotifyDropWhileInUse)

// TestRunContextCancelled verifies Run returns the context error when it is
// blocked on back-pressure (capacity exhausted) and the ctx expires.
// Since: 2026-06-07
func TestRunContextCancelled(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("greet", greetV1, "v1", "")
	rt := newTestManager(t, loader, 1) // one slot

	pool, err := rt.getPool("greet")
	if err != nil {
		t.Fatal(err)
	}
	ps, err := rt.acquire(context.Background(), pool) // hold the only slot
	if err != nil {
		t.Fatal(err)
	}
	defer rt.release(pool, ps)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := rt.Run(ctx, "greet", "run", lua.MkString("a")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded under exhausted capacity, got %v", err)
	}
}

// TestRunExecTimeout verifies that a runaway (infinite-loop) script is aborted by
// the server-side ExecTimeout hard cap and surfaces a typed DeadlineExceeded.
// Since: 2026-06-07
func TestRunExecTimeout(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("spin", spinSrc, "v1", "")
	rt := New(loader, Config{MaxStates: 4, IdleTTL: time.Hour, JanitorInterval: time.Hour, ExecTimeout: 50 * time.Millisecond})
	t.Cleanup(rt.Close)

	start := time.Now()
	_, err := rt.Run(context.Background(), "spin", "spin")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded from ExecTimeout, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("runaway script not aborted promptly: ran %s", elapsed)
	}
}

// TestRunCallerCtxCancelDuringExec verifies that, even with ExecTimeout disabled,
// a cancelable ctx the caller passes aborts a running script (Canceled).
// Since: 2026-06-07
func TestRunCallerCtxCancelDuringExec(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("spin", spinSrc, "v1", "")
	rt := newTestManager(t, loader, 4) // ExecTimeout = 0 (disabled)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	_, err := rt.Run(ctx, "spin", "spin")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected Canceled from caller ctx, got %v", err)
	}
}

// TestExecTimeoutPooledReuseClean verifies that after an execution times out, the
// pooled State is reset (RemoveContext) so a subsequent call on the same State
// succeeds — i.e. the expired context does not leak into the next reuse.
// Since: 2026-06-07
func TestExecTimeoutPooledReuseClean(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("spin", spinSrc, "v1", "")
	// MaxStates: 1 forces the next Run to reuse the very same pooled State.
	rt := New(loader, Config{MaxStates: 1, IdleTTL: time.Hour, JanitorInterval: time.Hour, ExecTimeout: 100 * time.Millisecond})
	t.Cleanup(rt.Close)

	if _, err := rt.Run(context.Background(), "spin", "spin"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout on first call, got %v", err)
	}
	out, err := rt.Run(context.Background(), "spin", "ok", lua.MkString("z"))
	if err != nil {
		t.Fatalf("reused State after timeout must run cleanly, got %v", err)
	}
	if out[0].Str() != "ok:z" {
		t.Fatalf("got %q, want ok:z", out[0].Str())
	}
}

// TestNoExecTimeoutCompletes verifies the zero-overhead path: with ExecTimeout
// disabled and a background ctx, a normal script runs to completion.
// Since: 2026-06-07
func TestNoExecTimeoutCompletes(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("greet", greetV1, "v1", "")
	rt := newTestManager(t, loader, 4) // ExecTimeout = 0, context.Background()

	out, err := rt.Run(context.Background(), "greet", "run", lua.MkString("a"))
	if err != nil {
		t.Fatalf("normal run should complete: %v", err)
	}
	if out[0].Str() != "v1:a" {
		t.Fatalf("got %q, want v1:a", out[0].Str())
	}
}

// TestCloseSemantics verifies Close closes idle States, subsequent calls return
// ErrClosed, and a double Close is safe.
// Since: 2026-06-07
func TestCloseSemantics(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("greet", greetV1, "v1", "")
	rt := New(loader, Config{MaxStates: 4, IdleTTL: time.Hour, JanitorInterval: time.Hour})

	if _, err := rt.Run(context.Background(), "greet", "run", lua.MkString("a")); err != nil {
		t.Fatal(err)
	}
	rt.Close()
	if live := rt.Stats().LiveStates; live != 0 {
		t.Fatalf("Close should close idle states, live=%d", live)
	}
	if _, err := rt.Run(context.Background(), "greet", "run", lua.MkString("a")); !errors.Is(err, ErrClosed) {
		t.Fatalf("expected ErrClosed after Close, got %v", err)
	}
	rt.Close() // double Close must be a no-op (no panic)
}

// TestShutdownNoInflight verifies Shutdown returns nil promptly when nothing is
// in flight and leaves no live States.
// Since: 2026-06-07
func TestShutdownNoInflight(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("greet", greetV1, "v1", "")
	rt := New(loader, Config{MaxStates: 4, IdleTTL: time.Hour, JanitorInterval: time.Hour})
	if _, err := rt.Run(context.Background(), "greet", "run", lua.MkString("a")); err != nil {
		t.Fatal(err)
	}
	if err := rt.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if live := rt.Stats().LiveStates; live != 0 {
		t.Fatalf("after Shutdown live=%d, want 0", live)
	}
}

// TestShutdownDrainsInflight verifies Shutdown waits for an in-flight State to be
// released, then returns nil with zero live States.
// Since: 2026-06-07
func TestShutdownDrainsInflight(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("greet", greetV1, "v1", "")
	rt := New(loader, Config{MaxStates: 4, IdleTTL: time.Hour, JanitorInterval: time.Hour})

	pool, err := rt.getPool("greet")
	if err != nil {
		t.Fatal(err)
	}
	ps, err := rt.acquire(context.Background(), pool) // in flight
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		rt.release(pool, ps)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rt.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown should drain and return nil: %v", err)
	}
	if live := rt.Stats().LiveStates; live != 0 {
		t.Fatalf("after drain live=%d, want 0", live)
	}
}

// TestShutdownTimeout verifies Shutdown returns the context error when an
// in-flight State never releases within the deadline (no leak — it closes on its
// eventual release).
// Since: 2026-06-07
func TestShutdownTimeout(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("greet", greetV1, "v1", "")
	rt := New(loader, Config{MaxStates: 4, IdleTTL: time.Hour, JanitorInterval: time.Hour})

	pool, err := rt.getPool("greet")
	if err != nil {
		t.Fatal(err)
	}
	ps, err := rt.acquire(context.Background(), pool) // held, never released until cleanup
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := rt.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
	if live := rt.Stats().LiveStates; live != 1 {
		t.Fatalf("in-flight state should remain until released, live=%d", live)
	}
	rt.release(pool, ps) // closes it (runtime is closed)
	if live := rt.Stats().LiveStates; live != 0 {
		t.Fatalf("after release live=%d, want 0", live)
	}
}

// TestShutdownIdempotent verifies Close then Shutdown is safe and a double
// Shutdown returns nil.
// Since: 2026-06-07
func TestShutdownIdempotent(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("greet", greetV1, "v1", "")
	rt := New(loader, Config{MaxStates: 4, IdleTTL: time.Hour, JanitorInterval: time.Hour})
	if _, err := rt.Run(context.Background(), "greet", "run", lua.MkString("a")); err != nil {
		t.Fatal(err)
	}
	rt.Close()
	if err := rt.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown after Close: %v", err)
	}
	if err := rt.Shutdown(context.Background()); err != nil {
		t.Fatalf("double Shutdown: %v", err)
	}
}

// TestRetryAfterLoaderError verifies that after a transient loader error, a
// retry succeeds once the script exists (failure path).
// Since: 2026-06-07
func TestRetryAfterLoaderError(t *testing.T) {
	loader := NewMapLoader()
	rt := newTestManager(t, loader, 4)

	if _, err := rt.Run(context.Background(), "lazy", "run", lua.MkString("a")); err == nil {
		t.Fatal("expected error before script exists")
	}
	loader.Set("lazy", greetV1, "v1", "")
	out, err := rt.Run(context.Background(), "lazy", "run", lua.MkString("a"))
	if err != nil {
		t.Fatalf("retry after transient error should succeed: %v", err)
	}
	if out[0].Str() != "v1:a" {
		t.Fatalf("got %q", out[0].Str())
	}
}

// TestCompileError verifies a malformed script surfaces a compile error through
// the manager path (failure path).
// Since: 2026-06-07
func TestCompileError(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("bad", `function run( oops`, "v1", "")
	rt := newTestManager(t, loader, 4)
	if _, err := rt.Run(context.Background(), "bad", "run"); err == nil {
		t.Fatal("expected compile error for malformed script")
	}
}

// TestLiveCountInvariant verifies that after a workload live == sum of idle
// states across pools, checkedOut is zero, and live <= max (leak / double-count
// detector).
// Since: 2026-06-07
func TestLiveCountInvariant(t *testing.T) {
	loader := NewMapLoader()
	keys := []string{"a", "b", "c"}
	for _, k := range keys {
		loader.Set(k, doubleSrc, "v1", "")
	}
	rt := newTestManager(t, loader, 4)

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := rt.Run(context.Background(), keys[i%len(keys)], "run", lua.Int(int64(i))); err != nil {
				t.Errorf("#%d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	rt.mu.Lock()
	defer rt.mu.Unlock()
	sum := 0
	for _, p := range rt.pools {
		if p.checkedOut != 0 {
			t.Fatalf("pool %q has checkedOut=%d after all runs returned", p.key, p.checkedOut)
		}
		sum += len(p.idle)
	}
	if rt.live != sum {
		t.Fatalf("live count %d != sum of idle states %d (leak or double-count)", rt.live, sum)
	}
	if rt.live > rt.cfg.MaxStates {
		t.Fatalf("live %d exceeds max %d", rt.live, rt.cfg.MaxStates)
	}
}

// TestMeasurePerStateBytes verifies measurePerStateBytes reports a plausible
// per-state heap cost (skips when it returns 0 due to GC noise; non-deterministic).
// Since: 2026-06-07
func TestMeasurePerStateBytes(t *testing.T) {
	per := measurePerStateBytes(func() *lua.LState { return lua.NewState() }, 8)
	if per == 0 {
		t.Skip("measurement returned 0 (GC noise); non-deterministic")
	}
	if per < 1024 {
		t.Fatalf("per-state bytes implausibly small: %d", per)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Notification-driven hot reload (drop-and-reload)
// ─────────────────────────────────────────────────────────────────────────────

// TestNotifyDropsPool verifies a notification with a different version drops the
// key's pool and the next Run reloads the new version.
// Since: 2026-06-07
func TestNotifyDropsPool(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("greet", greetV1, "v1", "1.0.0")
	rt := newTestManager(t, loader, 8)
	ctx := context.Background()

	if out, _ := rt.Run(ctx, "greet", "run", lua.MkString("a")); out[0].Str() != "v1:a" {
		t.Fatalf("got %q", out[0].Str())
	}
	if s := rt.Stats(); s.Pools != 1 || s.LiveStates == 0 {
		t.Fatalf("expected 1 live pool, got %+v", s)
	}

	loader.Set("greet", greetV2, "v2", "2.0.0")
	rt.Notify("greet", "v2", "2.0.0")
	if s := rt.Stats(); s.Pools != 0 || s.LiveStates != 0 {
		t.Fatalf("Notify should drop the pool: %+v", s)
	}

	out, err := rt.Run(ctx, "greet", "run", lua.MkString("a"))
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Str() != "v2:a" {
		t.Fatalf("after reload want v2:a, got %q", out[0].Str())
	}
}

// TestContentHashVersion verifies that, with content-hash versions, identical
// content is idempotent (ignored) and changed content triggers a reload.
// Since: 2026-06-07
func TestContentHashVersion(t *testing.T) {
	loader := NewMapLoader()
	v1 := HashVersion(greetV1)
	loader.Set("greet", greetV1, v1, "1.0.0")
	rt := newTestManager(t, loader, 8)
	ctx := context.Background()
	rt.Run(ctx, "greet", "run", lua.MkString("a"))

	rt.Notify("greet", v1, "1.0.0") // same hash → idempotent
	if rt.Stats().Pools != 1 {
		t.Fatalf("same hash must not drop: %+v", rt.Stats())
	}

	v2 := HashVersion(greetV2)
	if v2 == v1 {
		t.Fatal("different content must hash differently")
	}
	loader.Set("greet", greetV2, v2, "2.0.0")
	rt.Notify("greet", v2, "2.0.0")
	out, _ := rt.Run(ctx, "greet", "run", lua.MkString("a"))
	if out[0].Str() != "v2:a" {
		t.Fatalf("want v2:a, got %q", out[0].Str())
	}
}

// TestDisplayVersionExposed verifies PoolStats exposes displayVersion and falls
// back to the hash prefix when the loader provides none.
// Since: 2026-06-07
func TestDisplayVersionExposed(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("greet", greetV1, HashVersion(greetV1), "1.2.3")
	dh := HashVersion(doubleSrc)
	loader.Set("double", doubleSrc, dh, "") // no displayVersion → fallback
	rt := newTestManager(t, loader, 8)
	ctx := context.Background()
	rt.Run(ctx, "greet", "run", lua.MkString("a"))
	rt.Run(ctx, "double", "run", lua.Int(2))

	got := map[string]string{}
	for _, ps := range rt.PoolStats() {
		got[ps.Key] = ps.DisplayVersion
	}
	if got["greet"] != "1.2.3" {
		t.Fatalf("greet display=%q, want 1.2.3", got["greet"])
	}
	if want := dh[:8]; got["double"] != want {
		t.Fatalf("double display=%q, want hash-prefix %q", got["double"], want)
	}
}

// TestDisplayVersionLabelOnlyRefresh verifies a same-hash notification that only
// changes displayVersion refreshes the label without dropping (no cold start).
// Since: 2026-06-07
func TestDisplayVersionLabelOnlyRefresh(t *testing.T) {
	loader := NewMapLoader()
	h := HashVersion(greetV1)
	loader.Set("greet", greetV1, h, "1.0.0")
	rt := newTestManager(t, loader, 8)
	ctx := context.Background()
	rt.Run(ctx, "greet", "run", lua.MkString("a"))
	liveBefore := rt.Stats().LiveStates

	rt.Notify("greet", h, "1.0.1") // same hash, label only
	if s := rt.Stats(); s.Pools != 1 || s.LiveStates != liveBefore {
		t.Fatalf("label-only refresh must not drop: %+v", s)
	}
	for _, ps := range rt.PoolStats() {
		if ps.Key == "greet" && ps.DisplayVersion != "1.0.1" {
			t.Fatalf("label not refreshed: %q", ps.DisplayVersion)
		}
	}
}

// TestNotifyDropWhileInUse is the ★ core safety test: when a drop notification
// arrives while a State is in use, ① execution completes on the old version,
// ② release closes it instead of pooling, ③ live recovers (no leak), and
// ④ a new Run uses the new-version pool.
// Since: 2026-06-07
func TestNotifyDropWhileInUse(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("greet", greetV1, "v1", "1.0.0")
	rt := newTestManager(t, loader, 8)

	pool, err := rt.getPool("greet")
	if err != nil {
		t.Fatal(err)
	}
	ps, err := rt.acquire(context.Background(), pool) // in use (checked-out)
	if err != nil {
		t.Fatal(err)
	}
	if ps.version != "v1" {
		t.Fatalf("want v1 state, got %q", ps.version)
	}
	liveAfterAcquire := rt.Stats().LiveStates

	// External change notification while in use → drop the pool.
	loader.Set("greet", greetV2, "v2", "2.0.0")
	rt.Notify("greet", "v2", "2.0.0")
	if rt.Stats().Pools != 0 {
		t.Fatalf("pool should be removed from map after drop: %+v", rt.Stats())
	}
	if rt.Stats().LiveStates != liveAfterAcquire {
		t.Fatalf("in-use state must NOT be closed by drop: live=%d", rt.Stats().LiveStates)
	}

	// ① execution completes on the old version
	fn := ps.L.GetGlobal("run")
	rets, err := ps.L.Call(fn, []lua.Value{lua.MkString("z")}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := rets[0].Str(); got != "v1:z" {
		t.Fatalf("in-flight should complete on old version: got %q", got)
	}

	// ②③ release → Close instead of pool return, live recovers
	rt.release(pool, ps)
	if rt.Stats().LiveStates != 0 {
		t.Fatalf("released dropped-pool state must be Closed (no leak): live=%d", rt.Stats().LiveStates)
	}

	// ④ a new Run uses the new-version pool
	out, err := rt.Run(context.Background(), "greet", "run", lua.MkString("z"))
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Str() != "v2:z" {
		t.Fatalf("new run want v2:z, got %q", out[0].Str())
	}
}

// TestNotifyIdempotent verifies re-notifying the same version does not change
// state (idempotent).
// Since: 2026-06-07
func TestNotifyIdempotent(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("greet", greetV1, "v1", "1.0.0")
	rt := newTestManager(t, loader, 8)
	rt.Run(context.Background(), "greet", "run", lua.MkString("a"))
	before := rt.Stats()

	rt.Notify("greet", "v1", "1.0.0")
	rt.Notify("greet", "v1", "1.0.0")
	if after := rt.Stats(); after.Pools != before.Pools || after.LiveStates != before.LiveStates {
		t.Fatalf("idempotent notify changed state: %+v -> %+v", before, after)
	}
}

// TestNotifyUnknownKey verifies notifying a key with no pool is a safe no-op.
// Since: 2026-06-07
func TestNotifyUnknownKey(t *testing.T) {
	rt := newTestManager(t, NewMapLoader(), 4)
	rt.Notify("ghost", "v9", "9.0.0")
	rt.NotifyChanges([]Change{{Key: "ghost", Version: "v9", DisplayVersion: "9.0.0"}})
	if rt.Stats().Pools != 0 {
		t.Fatalf("unknown-key notify should be no-op, pools=%d", rt.Stats().Pools)
	}
}

// TestNotifyEmptyVersion verifies an empty/invalid version notification is a
// no-op (no drop) without panicking.
// Since: 2026-06-07
func TestNotifyEmptyVersion(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("greet", greetV1, "v1", "1.0.0")
	rt := newTestManager(t, loader, 8)
	rt.Run(context.Background(), "greet", "run", lua.MkString("a"))
	before := rt.Stats()

	rt.Notify("greet", "", "")
	if after := rt.Stats(); after.Pools != before.Pools || after.LiveStates != before.LiveStates {
		t.Fatalf("empty-version notify must be no-op: %+v -> %+v", before, after)
	}
}

// TestNotifyConcurrentInvariant verifies the accounting invariant (live == sum
// of idle, checkedOut == 0) holds when concurrent Run and drop notifications are
// mixed (no leak/drift, -race).
// Since: 2026-06-07
func TestNotifyConcurrentInvariant(t *testing.T) {
	loader := NewMapLoader()
	keys := []string{"a", "b", "c"}
	for _, k := range keys {
		loader.Set(k, doubleSrc, "v1", "")
	}
	rt := newTestManager(t, loader, 4)

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// The pool may be mid-drop, so ignore errors (a reload makes the next call succeed).
			_, _ = rt.Run(context.Background(), keys[i%len(keys)], "run", lua.Int(int64(i)))
		}(i)
	}
	for i := 0; i < 40; i++ {
		// Vary the version each time to force a drop (the loader keeps v1, so a reload recreates a v1 pool).
		rt.Notify(keys[i%len(keys)], fmt.Sprintf("nv%d", i), "")
	}
	wg.Wait()

	rt.mu.Lock()
	defer rt.mu.Unlock()
	sum := 0
	for _, p := range rt.pools {
		if p.checkedOut != 0 {
			t.Fatalf("pool %q checkedOut=%d after wait", p.key, p.checkedOut)
		}
		sum += len(p.idle)
	}
	if rt.live != sum {
		t.Fatalf("live %d != idle sum %d (leak/drift)", rt.live, sum)
	}
}

// TestDefaultLibsSandbox verifies the default library set: the 5.4 sandbox libs
// (utf8, coroutine) are available, while the base globals that compile or run
// arbitrary code/files (load, loadfile, dofile) are removed.
// Since: 2026-06-28
func TestDefaultLibsSandbox(t *testing.T) {
	loader := NewMapLoader()
	// present: utf8 + coroutine are part of the default sandbox set.
	loader.Set("ok", `function run()
		assert(utf8.len("hi") == 2)
		local co = coroutine.create(function() coroutine.yield(7) end)
		local _, v = coroutine.resume(co)
		assert(v == 7)
		return true
	end`, "v1", "")
	// removed: each of these globals must be nil under the default sandbox.
	for _, g := range []string{"load", "loadfile", "dofile"} {
		loader.Set("no_"+g, fmt.Sprintf(`function run() return %s end`, g), "v1", "")
	}

	rt := newTestManager(t, loader, 4)

	if _, err := rt.Run(context.Background(), "ok", "run"); err != nil {
		t.Fatalf("utf8/coroutine should be available under default libs: %v", err)
	}
	for _, g := range []string{"load", "loadfile", "dofile"} {
		out, err := rt.Run(context.Background(), "no_"+g, "run")
		if err != nil {
			t.Fatalf("%s probe errored: %v", g, err)
		}
		if len(out) != 1 || !out[0].IsNil() {
			t.Fatalf("%s should be removed (nil) under default libs, got %v", g, out)
		}
	}
}

// TestGoCallbackPanicRecovered verifies that a panicking Go callback (reachable
// via Config.Libs) does not escape to the host goroutine: protected mode turns it
// into a catchable error that wraps *lua.GoPanicError, and the same pooled State
// stays reusable for the next Run (VM unwound, not corrupted, no leak).
// Since: 2026-06-28
func TestGoCallbackPanicRecovered(t *testing.T) {
	const src = `
		function boom() return panicfn() end
		function safe() return "ok" end`
	loader := NewMapLoader()
	loader.Set("k", src, "v1", "")

	registerPanic := func(L *lua.LState) {
		L.Register("panicfn", func(L *lua.LState) int { panic("kaboom") })
	}
	rt := New(loader, Config{
		MaxStates:       1, // force the same State to be reused across both Runs
		IdleTTL:         time.Hour,
		JanitorInterval: time.Hour,
		Libs:            []func(*lua.LState){(*lua.LState).OpenBase, registerPanic},
	})
	t.Cleanup(rt.Close)

	// 1) the panicking callback surfaces as an error, not a host panic.
	if _, err := rt.Run(context.Background(), "k", "boom"); err == nil {
		t.Fatal("expected an error from the panicking callback")
	} else {
		var gpe *lua.GoPanicError
		if !errors.As(err, &gpe) {
			t.Fatalf("expected error to wrap *lua.GoPanicError, got %T: %v", err, err)
		}
		if gpe.Value != "kaboom" {
			t.Fatalf("recovered panic value = %v, want kaboom", gpe.Value)
		}
	}

	// 2) the same pooled State is reusable afterwards (unwound, not corrupted).
	out, err := rt.Run(context.Background(), "k", "safe")
	if err != nil {
		t.Fatalf("State should be reusable after a recovered panic: %v", err)
	}
	if len(out) != 1 || out[0].Str() != "ok" {
		t.Fatalf("got %v, want [ok]", out)
	}

	if s := rt.Stats(); s.LiveStates != 1 {
		t.Fatalf("LiveStates = %d, want 1 (no leak)", s.LiveStates)
	}
}

const counterSrc = `
	counter = 0
	function inc() counter = counter + 1; return tostring(counter) end
	function usesLib() return string.upper("hi") end`

// TestIsolateGlobals verifies that with Config.IsolateGlobals each call runs under
// a fresh _ENV: a global write does not leak across Runs that reuse the same
// pooled State (counter stays 1), while reads still reach the shared libraries.
// Since: 2026-06-28
func TestIsolateGlobals(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("k", counterSrc, "v1", "")
	rt := New(loader, Config{
		MaxStates: 1, // force State reuse so a leak would show
		IdleTTL:   time.Hour, JanitorInterval: time.Hour,
		IsolateGlobals: true,
	})
	t.Cleanup(rt.Close)

	for i := 0; i < 3; i++ {
		out, err := rt.Run(context.Background(), "k", "inc")
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if out[0].Str() != "1" {
			t.Fatalf("iter %d: counter=%s, want 1 (per-call isolation)", i, out[0].Str())
		}
	}
	// reads of undefined globals fall back to the shared libraries via __index.
	out, err := rt.Run(context.Background(), "k", "usesLib")
	if err != nil {
		t.Fatalf("usesLib: %v", err)
	}
	if out[0].Str() != "HI" {
		t.Fatalf("usesLib = %q, want HI (library fallback)", out[0].Str())
	}
}

// TestGlobalsPersistWithoutIsolation documents the default mode: globals persist
// on the reused pooled State (counter accumulates), which is exactly what
// IsolateGlobals opts out of.
// Since: 2026-06-28
func TestGlobalsPersistWithoutIsolation(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("k", counterSrc, "v1", "")
	rt := newTestManager(t, loader, 1) // default: IsolateGlobals == false

	for i := 1; i <= 3; i++ {
		out, err := rt.Run(context.Background(), "k", "inc")
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if want := fmt.Sprintf("%d", i); out[0].Str() != want {
			t.Fatalf("iter %d: counter=%s, want %s (globals persist on reused State)", i, out[0].Str(), want)
		}
	}
}

// TestIsolateGlobalsRequiresBaseLib verifies the guard: IsolateGlobals without the
// base library (no setmetatable) surfaces an error instead of panicking.
// Since: 2026-06-28
func TestIsolateGlobalsRequiresBaseLib(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("k", counterSrc, "v1", "")
	rt := New(loader, Config{
		MaxStates: 1, IdleTTL: time.Hour, JanitorInterval: time.Hour,
		IsolateGlobals: true,
		Libs:           []func(*lua.LState){(*lua.LState).OpenString}, // no OpenBase
	})
	t.Cleanup(rt.Close)

	if _, err := rt.Run(context.Background(), "k", "inc"); err == nil {
		t.Fatal("expected an error when IsolateGlobals is set without the base library")
	}
}

// TestMaxInstructions verifies the opcode cap: a runaway pure-Lua loop is aborted
// with ErrInstructionLimit, the same pooled State stays reusable afterwards, a
// short script runs fine under the cap, and the budget re-arms per Run.
// Since: 2026-06-28
func TestMaxInstructions(t *testing.T) {
	const src = `
		function spin() while true do end end
		function add() return tostring(1 + 1) end`
	loader := NewMapLoader()
	loader.Set("k", src, "v1", "")
	rt := New(loader, Config{
		MaxStates: 1, IdleTTL: time.Hour, JanitorInterval: time.Hour,
		MaxInstructions: 100000,
	})
	t.Cleanup(rt.Close)

	// 1) a runaway loop is capped (does not hang).
	if _, err := rt.Run(context.Background(), "k", "spin"); !errors.Is(err, ErrInstructionLimit) {
		t.Fatalf("spin: got %v, want ErrInstructionLimit", err)
	}
	// 2) the same pooled State is reusable, and a short script completes under the cap.
	out, err := rt.Run(context.Background(), "k", "add")
	if err != nil {
		t.Fatalf("add after cap: %v", err)
	}
	if out[0].Str() != "2" {
		t.Fatalf("add = %s, want 2", out[0].Str())
	}
	// 3) the budget re-arms per Run — a second runaway is capped too.
	if _, err := rt.Run(context.Background(), "k", "spin"); !errors.Is(err, ErrInstructionLimit) {
		t.Fatalf("spin #2: got %v, want ErrInstructionLimit (budget not re-armed)", err)
	}
}

// TestRunWith verifies the borrow API: handle sees the call's results (including
// a table) while the State is still owned, handle's error propagates, and handle
// is not invoked when the script itself errors.
// Since: 2026-06-28
func TestRunWith(t *testing.T) {
	const src = `
		function make() return {a = 1, b = "two"}, 42 end`
	loader := NewMapLoader()
	loader.Set("k", src, "v1", "")
	rt := newTestManager(t, loader, 2)

	// 1) borrow and read a returned table within ownership.
	var a, n int64
	var b string
	var nrets int
	err := rt.RunWith(context.Background(), "k", "make", func(L *lua.LState, rets []lua.Value) error {
		nrets = len(rets)
		tbl := rets[0].AsTable()
		a = tbl.GetStr("a").AsInt()
		b = tbl.GetStr("b").Str()
		n = rets[1].AsInt()
		return nil
	})
	if err != nil {
		t.Fatalf("RunWith: %v", err)
	}
	if nrets != 2 || a != 1 || b != "two" || n != 42 {
		t.Fatalf("got nrets=%d a=%d b=%q n=%d", nrets, a, b, n)
	}

	// 2) handle's error propagates unchanged.
	sentinel := errors.New("handle boom")
	if err := rt.RunWith(context.Background(), "k", "make", func(L *lua.LState, rets []lua.Value) error {
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("handle error: got %v, want %v", err, sentinel)
	}

	// 3) handle is skipped when the script errors (missing entry function).
	called := false
	if err := rt.RunWith(context.Background(), "k", "missing", func(L *lua.LState, rets []lua.Value) error {
		called = true
		return nil
	}); err == nil {
		t.Fatal("expected an error calling a missing entry function")
	}
	if called {
		t.Fatal("handle must not run when the script errors")
	}
}

// TestBackPressureNoDeadlock runs many concurrent acquirers against a tiny cap so
// every Run but a few must wait on the FIFO queue; all must complete (no lost
// wakeup / deadlock) and the pool must be clean afterwards.
// Since: 2026-06-28
func TestBackPressureNoDeadlock(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("k", `function q() return "x" end`, "v1", "")
	rt := New(loader, Config{MaxStates: 2, IdleTTL: time.Hour, JanitorInterval: time.Hour})
	t.Cleanup(rt.Close)

	const goroutines = 64
	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, err := rt.Run(context.Background(), "k", "q")
			if err == nil && (len(out) != 1 || out[0].Str() != "x") {
				err = fmt.Errorf("bad result %v", out)
			}
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	if s := rt.Stats(); s.LiveStates > 2 {
		t.Fatalf("LiveStates = %d, want <= 2", s.LiveStates)
	}
	// no slot leaked: every State is idle (none checked out).
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.waiters) != 0 {
		t.Fatalf("waiters left in queue: %d", len(rt.waiters))
	}
	for _, p := range rt.pools {
		if p.checkedOut != 0 {
			t.Fatalf("pool %q checkedOut=%d after wait", p.key, p.checkedOut)
		}
	}
}

// TestBackPressureContextCancel verifies a blocked acquirer honors ctx
// cancellation and that the slot it was waiting for is not stranded.
// Since: 2026-06-28
func TestBackPressureContextCancel(t *testing.T) {
	hold := make(chan struct{})
	var enterOnce, holdOnce sync.Once
	entered := make(chan struct{})
	releaseHolder := func() { holdOnce.Do(func() { close(hold) }) }
	libs := []func(*lua.LState){
		(*lua.LState).OpenBase,
		func(L *lua.LState) {
			L.Register("hold", func(L *lua.LState) int {
				enterOnce.Do(func() { close(entered) })
				<-hold
				return 0
			})
		},
	}
	loader := NewMapLoader()
	loader.Set("k", `
		function holder() hold() end
		function quick() return "ok" end`, "v1", "")
	rt := New(loader, Config{MaxStates: 1, IdleTTL: time.Hour, JanitorInterval: time.Hour, Libs: libs})
	t.Cleanup(func() { releaseHolder(); rt.Close() })

	// Occupy the only slot.
	done := make(chan error, 1)
	go func() { _, err := rt.Run(context.Background(), "k", "holder"); done <- err }()
	<-entered

	// A second acquire must block, then return promptly on ctx cancel.
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() { _, err := rt.Run(ctx, "k", "quick"); res <- err }()
	cancel()
	if err := <-res; !errors.Is(err, context.Canceled) {
		t.Fatalf("blocked acquire: got %v, want context.Canceled", err)
	}

	// Release the holder; the freed slot must be reusable (not stranded).
	releaseHolder()
	if err := <-done; err != nil {
		t.Fatalf("holder: %v", err)
	}
	out, err := rt.Run(context.Background(), "k", "quick")
	if err != nil || len(out) != 1 || out[0].Str() != "ok" {
		t.Fatalf("slot stranded after cancel: out=%v err=%v", out, err)
	}
}

// ── ExtraLibs (custom libraries) ──

// customKitLib is a user-authored library: it registers a global `triple` Go
// function. Used to exercise Config.ExtraLibs.
// Since: 2026-07-05
func customKitLib(L *lua.LState) {
	L.Register("triple", func(L *lua.LState) int {
		L.Push(lua.Int(L.CheckInt(1) * 3))
		return 1
	})
}

// TestExtraLibs_Basic verifies a custom library added via ExtraLibs is callable
// from scripts while the default sandbox libraries remain available.
// Since: 2026-07-05
func TestExtraLibs_Basic(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("s", `function run(n) return triple(n) .. ":" .. string.upper("ok") end`, "v1", "")
	rt := New(loader, Config{
		MaxStates: 2, IdleTTL: time.Hour, JanitorInterval: time.Hour,
		ExtraLibs: []func(*lua.LState){customKitLib},
	})
	t.Cleanup(rt.Close)

	out, err := rt.Run(context.Background(), "s", "run", lua.Int(4))
	if err != nil {
		t.Fatal(err)
	}
	if got := out[0].Str(); got != "12:OK" { // triple(4)=12 (custom) + string.upper (default lib)
		t.Fatalf("got %q, want %q", got, "12:OK")
	}
}

// TestExtraLibs_Exceptions covers the edges: the sandbox is preserved (os stays
// absent even with an ExtraLib), an empty ExtraLibs is a no-op, IsolateGlobals
// still exposes the lib via read-fallback without leaking script writes, and the
// lib works across a pool of States concurrently (run under -race).
// Since: 2026-07-05
func TestExtraLibs_Exceptions(t *testing.T) {
	// Sandbox preserved: ExtraLib runs after stripUnsafeLoaders, so os is still
	// absent (nil) while both the custom and default libs are present.
	t.Run("sandbox_preserved", func(t *testing.T) {
		loader := NewMapLoader()
		loader.Set("s", `function run() return type(os), type(triple), type(string.upper) end`, "v1", "")
		rt := New(loader, Config{
			MaxStates: 1, IdleTTL: time.Hour, JanitorInterval: time.Hour,
			ExtraLibs: []func(*lua.LState){customKitLib},
		})
		t.Cleanup(rt.Close)
		out, err := rt.Run(context.Background(), "s", "run")
		if err != nil {
			t.Fatal(err)
		}
		if out[0].Str() != "nil" {
			t.Fatalf("os leaked into sandbox: type(os)=%q", out[0].Str())
		}
		if out[1].Str() != "function" || out[2].Str() != "function" {
			t.Fatalf("libs missing: triple=%q string.upper=%q", out[1].Str(), out[2].Str())
		}
	})

	// Empty ExtraLibs is a no-op — default behavior is unchanged.
	t.Run("empty_noop", func(t *testing.T) {
		loader := NewMapLoader()
		loader.Set("s", `function run(n) return tostring(n * 2) end`, "v1", "")
		rt := New(loader, Config{
			MaxStates: 1, IdleTTL: time.Hour, JanitorInterval: time.Hour,
			ExtraLibs: nil,
		})
		t.Cleanup(rt.Close)
		out, err := rt.Run(context.Background(), "s", "run", lua.Int(21))
		if err != nil {
			t.Fatal(err)
		}
		if out[0].Str() != "42" {
			t.Fatalf("got %q, want 42", out[0].Str())
		}
	})

	// With IsolateGlobals the lib is reachable via the shared-library read fallback,
	// and a per-call global write does not leak into the next call.
	t.Run("isolate_globals", func(t *testing.T) {
		loader := NewMapLoader()
		loader.Set("s", `function run() local v = triple(2); seen = (seen or 0) + 1; return v .. ":" .. seen end`, "v1", "")
		rt := New(loader, Config{
			MaxStates: 1, IdleTTL: time.Hour, JanitorInterval: time.Hour,
			IsolateGlobals: true,
			ExtraLibs:      []func(*lua.LState){customKitLib},
		})
		t.Cleanup(rt.Close)
		for i := 0; i < 3; i++ { // same pooled State reused each iteration
			out, err := rt.Run(context.Background(), "s", "run")
			if err != nil {
				t.Fatalf("run %d: %v", i, err)
			}
			if got := out[0].Str(); got != "6:1" { // triple reachable (6) + seen isolated (always 1)
				t.Fatalf("run %d: got %q, want 6:1", i, got)
			}
		}
	})

	// The lib is applied to every pooled State and is safe under concurrent Runs.
	t.Run("concurrent", func(t *testing.T) {
		loader := NewMapLoader()
		loader.Set("s", `function run(n) return tostring(triple(n)) end`, "v1", "")
		rt := New(loader, Config{
			MaxStates: 3, IdleTTL: time.Hour, JanitorInterval: time.Hour,
			ExtraLibs: []func(*lua.LState){customKitLib},
		})
		t.Cleanup(rt.Close)
		var wg sync.WaitGroup
		for i := 0; i < 24; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				out, err := rt.Run(context.Background(), "s", "run", lua.Int(int64(i)))
				if err != nil {
					t.Errorf("run %d: %v", i, err)
					return
				}
				if got, want := out[0].Str(), fmt.Sprintf("%d", i*3); got != want {
					t.Errorf("run %d: got %s want %s", i, got, want)
				}
			}(i)
		}
		wg.Wait()
	})
}

// ─────────────────────────────── benchmarks ───────────────────────────────

// BenchmarkDynamicRun measures the cost of Run reusing a lazily-loaded pool, in
// parallel.
// Since: 2026-06-07
func BenchmarkDynamicRun(b *testing.B) {
	loader := NewMapLoader()
	loader.Set("double", doubleSrc, "v1", "")
	rt := New(loader, Config{MaxStates: 16, IdleTTL: time.Hour, JanitorInterval: time.Hour})
	defer rt.Close()

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := rt.Run(context.Background(), "double", "run", lua.Int(7)); err != nil {
				b.Fatal(err)
			}
		}
	})
}
