package luart

// Unit tests for observability.go — Metrics/Logger injection.
// Convention: basic behavior + exception cases (CONTRIBUTING.md).

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	lua "github.com/htcom-code/lua-pure/lua"
)

// spyMetrics counts each event. Safe for concurrent use.
type spyMetrics struct {
	compile, build, reuse, evict, drop int64
}

func (s *spyMetrics) OnCompile(string) { atomic.AddInt64(&s.compile, 1) }
func (s *spyMetrics) OnBuild(string)   { atomic.AddInt64(&s.build, 1) }
func (s *spyMetrics) OnReuse(string)   { atomic.AddInt64(&s.reuse, 1) }
func (s *spyMetrics) OnEvict(string)   { atomic.AddInt64(&s.evict, 1) }
func (s *spyMetrics) OnDrop(string)    { atomic.AddInt64(&s.drop, 1) }

// spyLogger records messages. Safe for concurrent use.
type spyLogger struct {
	mu   sync.Mutex
	info []string
	errs []string
}

func (l *spyLogger) Info(msg string, _ ...any) {
	l.mu.Lock()
	l.info = append(l.info, msg)
	l.mu.Unlock()
}
func (l *spyLogger) Error(msg string, _ ...any) {
	l.mu.Lock()
	l.errs = append(l.errs, msg)
	l.mu.Unlock()
}
func (l *spyLogger) has(list []string, sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// TestMetricsEvents verifies compile/build/reuse/drop events fire on the
// expected lifecycle transitions.
// Since: 2026-06-07
func TestMetricsEvents(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("greet", greetV1, "v1", "1.0.0")
	m := &spyMetrics{}
	rt := New(loader, Config{MaxStates: 8, IdleTTL: time.Hour, JanitorInterval: time.Hour, Metrics: m})
	t.Cleanup(rt.Close)
	ctx := context.Background()

	rt.Run(ctx, "greet", "run", lua.MkString("a")) // first: compile + build
	rt.Run(ctx, "greet", "run", lua.MkString("b")) // second: reuse idle

	if got := atomic.LoadInt64(&m.compile); got != 1 {
		t.Fatalf("compile=%d, want 1", got)
	}
	if got := atomic.LoadInt64(&m.build); got != 1 {
		t.Fatalf("build=%d, want 1", got)
	}
	if got := atomic.LoadInt64(&m.reuse); got != 1 {
		t.Fatalf("reuse=%d, want 1", got)
	}

	loader.Set("greet", greetV2, "v2", "2.0.0")
	rt.Notify("greet", "v2", "2.0.0")
	if got := atomic.LoadInt64(&m.drop); got != 1 {
		t.Fatalf("drop=%d, want 1", got)
	}
}

// TestMetricsEviction verifies an LRU eviction at the cap emits OnEvict.
// Since: 2026-06-07
func TestMetricsEviction(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("a", doubleSrc, "v1", "")
	loader.Set("b", doubleSrc, "v1", "")
	m := &spyMetrics{}
	rt := New(loader, Config{MaxStates: 1, IdleTTL: time.Hour, JanitorInterval: time.Hour, Metrics: m})
	t.Cleanup(rt.Close)
	ctx := context.Background()

	if _, err := rt.Run(ctx, "a", "run", lua.Int(1)); err != nil { // build a, then a idle
		t.Fatal(err)
	}
	if _, err := rt.Run(ctx, "b", "run", lua.Int(2)); err != nil { // cap=1 → evict a, build b
		t.Fatal(err)
	}
	if got := atomic.LoadInt64(&m.evict); got < 1 {
		t.Fatalf("evict=%d, want >=1", got)
	}
}

// TestLoggerEvents verifies pool-loaded and pool-dropped are logged at info, and
// a load failure is logged at error.
// Since: 2026-06-07
func TestLoggerEvents(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("greet", greetV1, "v1", "1.0.0")
	lg := &spyLogger{}
	rt := New(loader, Config{MaxStates: 8, IdleTTL: time.Hour, JanitorInterval: time.Hour, Logger: lg})
	t.Cleanup(rt.Close)
	ctx := context.Background()

	rt.Run(ctx, "greet", "run", lua.MkString("a"))
	if !lg.has(lg.info, "pool loaded") {
		t.Fatal("expected a 'pool loaded' info log")
	}

	loader.Set("greet", greetV2, "v2", "2.0.0")
	rt.Notify("greet", "v2", "2.0.0")
	if !lg.has(lg.info, "pool dropped") {
		t.Fatal("expected a 'pool dropped' info log")
	}

	// Failure path: unknown key → load error logged.
	if _, err := rt.Run(ctx, "ghost", "run"); err == nil {
		t.Fatal("expected error for unknown script")
	}
	if !lg.has(lg.errs, "load failed") {
		t.Fatal("expected a 'load failed' error log")
	}
}

// TestNoopDefaults verifies that without Metrics/Logger the Runtime works and
// does not panic (defaults are no-ops).
// Since: 2026-06-07
func TestNoopDefaults(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("greet", greetV1, "v1", "")
	rt := newTestManager(t, loader, 4) // no Metrics/Logger configured
	if _, err := rt.Run(context.Background(), "greet", "run", lua.MkString("a")); err != nil {
		t.Fatal(err)
	}
}

// TestSlogLoggerAdapter verifies NewSlogLogger routes events into a *slog.Logger.
// Since: 2026-06-07
func TestSlogLoggerAdapter(t *testing.T) {
	var buf bytes.Buffer
	lg := NewSlogLogger(slog.New(slog.NewTextHandler(&buf, nil)))
	loader := NewMapLoader()
	loader.Set("greet", greetV1, "v1", "1.0.0")
	rt := New(loader, Config{MaxStates: 4, IdleTTL: time.Hour, JanitorInterval: time.Hour, Logger: lg})
	t.Cleanup(rt.Close)

	if _, err := rt.Run(context.Background(), "greet", "run", lua.MkString("a")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "pool loaded") {
		t.Fatalf("slog output missing 'pool loaded': %q", buf.String())
	}
}

// spyTrace counts trace stages. Safe for concurrent use.
type spyTrace struct {
	mu     sync.Mutex
	counts map[string]int
}

func (s *spyTrace) hook(stage, _ string, _ time.Duration) {
	s.mu.Lock()
	s.counts[stage]++
	s.mu.Unlock()
}
func (s *spyTrace) count(stage string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[stage]
}

// TestTraceStages verifies that the first Run traces load/compile/build/acquire/
// execute/release, and a second Run (idle reuse) traces acquire/execute/release
// again without re-tracing load/compile/build.
// Since: 2026-06-07
func TestTraceStages(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("greet", greetV1, "v1", "")
	st := &spyTrace{counts: map[string]int{}}
	rt := New(loader, Config{MaxStates: 4, IdleTTL: time.Hour, JanitorInterval: time.Hour, Trace: st.hook})
	t.Cleanup(rt.Close)
	ctx := context.Background()

	if _, err := rt.Run(ctx, "greet", "run", lua.MkString("a")); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"load", "compile", "build", "acquire", "execute", "release"} {
		if st.count(stage) < 1 {
			t.Fatalf("stage %q was not traced on first run", stage)
		}
	}

	if _, err := rt.Run(ctx, "greet", "run", lua.MkString("b")); err != nil { // reuse idle
		t.Fatal(err)
	}
	if got := st.count("build"); got != 1 {
		t.Fatalf("build should stay 1 on reuse, got %d", got)
	}
	if got := st.count("load"); got != 1 {
		t.Fatalf("load should stay 1 on reuse, got %d", got)
	}
	if st.count("acquire") < 2 || st.count("execute") < 2 || st.count("release") < 2 {
		t.Fatalf("acquire/execute/release should fire each run: %+v", st.counts)
	}
}

// TestTraceOff verifies a nil Trace adds no behavior change (no panic).
// Since: 2026-06-07
func TestTraceOff(t *testing.T) {
	loader := NewMapLoader()
	loader.Set("greet", greetV1, "v1", "")
	rt := newTestManager(t, loader, 4) // Trace unset
	if _, err := rt.Run(context.Background(), "greet", "run", lua.MkString("a")); err != nil {
		t.Fatal(err)
	}
}
