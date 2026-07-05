package luart

import (
	"log/slog"
	"time"
)

// TraceHook receives per-request, per-stage timing for developer profiling. It
// is distinct from Metrics (which is for production aggregation): TraceHook is a
// request-level profiler for development. Stages are "load" (external fetch),
// "compile", "acquire" (idle reuse / wait / LRU, includes build if one
// happened), "build" (NewState + preload), "execute" (the call), and "release".
//
// It is invoked only when Config.Trace is non-nil, so leaving it unset has zero
// overhead (no time.Now calls). The hook must be safe for concurrent use.
// Since: 2026-06-07
type TraceHook func(stage, key string, dur time.Duration)

// Metrics receives lifecycle events for monitoring. Implementations must be safe
// for concurrent use. The zero-overhead default is noopMetrics, so wiring a sink
// is opt-in and adds no required dependency.
//
// The events map naturally to counters; combine them with Runtime.PoolStats and
// Runtime.Stats (gauges) for a full picture.
// Since: 2026-06-07
type Metrics interface {
	OnCompile(key string) // a source was loaded and compiled (new pool created)
	OnBuild(key string)   // a new LState was built (pool miss)
	OnReuse(key string)   // an idle LState was reused (pool hit)
	OnEvict(key string)   // an idle LState was LRU-evicted at the cap
	OnDrop(key string)    // a pool was dropped by a change notification
}

// noopMetrics is the default Metrics: every method does nothing.
type noopMetrics struct{}

func (noopMetrics) OnCompile(string) {}
func (noopMetrics) OnBuild(string)   {}
func (noopMetrics) OnReuse(string)   {}
func (noopMetrics) OnEvict(string)   {}
func (noopMetrics) OnDrop(string)    {}

// Logger receives structured log events as a message plus key/value pairs
// (slog-style). The zero-overhead default is noopLogger.
// Since: 2026-06-07
type Logger interface {
	Info(msg string, keyvals ...any)
	Error(msg string, keyvals ...any)
}

// noopLogger is the default Logger: every method does nothing.
type noopLogger struct{}

func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Error(string, ...any) {}

// NewSlogLogger adapts a *slog.Logger to the Logger interface. Pass it via
// Config.Logger to route luart events into slog.
// Since: 2026-06-07
func NewSlogLogger(l *slog.Logger) Logger { return slogLogger{l: l} }

type slogLogger struct{ l *slog.Logger }

func (s slogLogger) Info(msg string, kv ...any)  { s.l.Info(msg, kv...) }
func (s slogLogger) Error(msg string, kv ...any) { s.l.Error(msg, kv...) }
