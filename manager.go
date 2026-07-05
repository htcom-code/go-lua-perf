package luart

import (
	"context"
	"errors"
	"math"
	"runtime"
	"sync"
	"time"

	lua "github.com/htcom-code/lua-pure/lua"
)

// ErrClosed is returned when a closed Runtime is used.
var ErrClosed = errors.New("luart: runtime closed")

// ErrInstructionLimit is returned by Run when a script exceeds
// Config.MaxInstructions (a runaway pure-Lua CPU cap, orthogonal to ExecTimeout).
// Use errors.Is(err, ErrInstructionLimit) to detect it.
var ErrInstructionLimit = errors.New("luart: instruction limit exceeded")

// instrLimitMessage mirrors lua-pure's internal instrLimitMsg — the exact Lua
// error string raised when SetInstructionLimit's budget is spent. lua-pure does
// not export a typed sentinel, so Run detects the cap by matching this text (as
// the engine's own docs direct) and maps it to ErrInstructionLimit.
const instrLimitMessage = "instruction limit exceeded"

// defaultLibs are the standard libraries opened on each pooled State. Each entry
// runs in order on a freshly created (no-libs) State; the set mirrors lua-pure's
// 5.4 sandbox (WithSandbox): base/table/string/math plus utf8 and coroutine, and
// it deliberately excludes os/io/debug/package for a tighter sandbox. The final
// entry strips the base globals that load or run arbitrary code/files
// (load/loadfile/dofile), so it must run after OpenBase.
var defaultLibs = []func(*lua.LState){
	(*lua.LState).OpenBase, (*lua.LState).OpenTable, (*lua.LState).OpenString, (*lua.LState).OpenMath,
	(*lua.LState).OpenUTF8, (*lua.LState).OpenCoroutine,
	stripUnsafeLoaders,
}

// stripUnsafeLoaders removes the base globals that compile or run arbitrary code
// or files (load, loadfile, dofile), matching lua-pure's sandbox. It must run
// after OpenBase, which defines them.
func stripUnsafeLoaders(L *lua.LState) {
	for _, name := range []string{"load", "loadfile", "dofile"} {
		L.SetGlobal(name, lua.Nil)
	}
}

// Config tunes Runtime behavior.
type Config struct {
	// MaxStates, if > 0, is the global cap. If 0, it is derived from
	// MemoryBudgetBytes divided by the measured per-state heap cost.
	MaxStates         int
	MemoryBudgetBytes uint64

	IdleTTL         time.Duration // pools idle longer than this are evicted whole
	JanitorInterval time.Duration // janitor sweep period

	// ExecTimeout, if > 0, is a per-execution hard cap: each Run binds the
	// running LState to a context with this deadline so a runaway (e.g. infinite
	// loop) script is aborted. 0 disables it (no per-opcode context check, zero
	// overhead). A cancelable ctx passed to Run is honored regardless. See Run.
	// Since: 2026-06-07
	ExecTimeout time.Duration

	// MaxInstructions, if > 0, caps how many Lua bytecode instructions a single Run
	// may execute before it is aborted with ErrInstructionLimit. It is the runaway
	// pure-Lua CPU guard and is orthogonal to ExecTimeout: ExecTimeout is a
	// wall-clock budget (covers blocking I/O in callbacks via the ctx passed to
	// Run / L.Context()), while MaxInstructions bounds only opcode count — so a
	// blocking but cooperative callback is not charged against it. It is enforced
	// only at the engine's finalizer-poll gate, so the effective cap is rounded up
	// to that granularity. Reset per Run (no carry-over across pooled reuse). 0
	// disables it (zero overhead). Pure-Lua only — a blocking Go callback cannot be
	// preempted by it (use ExecTimeout + a cancelable ctx for that).
	// Since: 2026-06-28
	MaxInstructions uint64

	Libs []func(*lua.LState) // libraries to open on pooled States (defaults to defaultLibs)

	// ExtraLibs are opened on each pooled State after Libs, so you can add custom
	// libraries — register Go functions or module tables, or L.Preload lazy
	// (require) modules — without restating the default set. Leaving Libs at its
	// default and setting ExtraLibs is the safe "keep the sandbox, add mine" path.
	// Each entry runs once per pooled State (in newState), on the goroutine that
	// owns the State, so any shared Go state a lib closes over must be
	// goroutine-safe. Opened after Libs — hence after defaultLibs'
	// stripUnsafeLoaders — so re-adding load/loadfile/dofile here un-sandboxes the
	// State. With IsolateGlobals, globals a lib defines are read-visible to each
	// call via the shared-library fallback (a mutable shared table a lib exposes
	// can be mutated in place across calls, so prefer immutable/functional libs).
	// Since: 2026-07-05
	ExtraLibs []func(*lua.LState)

	// IsolateGlobals, when true, runs each call under a fresh per-call _ENV (Lua
	// 5.4 environment sandbox) so global writes do not leak across Runs that reuse
	// the same pooled State. Without it, the same pooled State carries a script's
	// globals (e.g. `counter = counter + 1`) into the next caller, making results
	// depend on which idle State is picked. Reads fall back to the shared
	// libraries. The trade-off is a chunk re-run per call (cost proportional to the
	// script's top-level definitions), so it is off by default (globals persist on
	// the pooled State — the fastest path). Requires the base library (setmetatable)
	// in Libs. See Run.
	// Since: 2026-06-28
	IsolateGlobals bool

	// ConvertValue, when set, lets RunValues materialize non-data return values
	// (function/userdata/thread) into Go values; it runs while the State still owns
	// the value. If nil, RunValues returns an error for such values (the safe
	// default — it never hands back a value tied to a reused State). Data values
	// (nil/bool/number/string/table) are converted without it.
	// Since: 2026-06-28
	ConvertValue func(L *lua.LState, lv lua.Value) (any, error)

	Metrics Metrics // monitoring sink (defaults to a no-op)
	Logger  Logger  // structured logger (defaults to a no-op)

	// Trace, if set, receives per-stage request timing for developer profiling.
	// Leaving it nil has zero overhead (no timing is taken). See TraceHook.
	Trace TraceHook
}

func (c *Config) withDefaults() {
	if c.JanitorInterval <= 0 {
		c.JanitorInterval = 30 * time.Second
	}
	if c.IdleTTL <= 0 {
		c.IdleTTL = 5 * time.Minute
	}
	if len(c.Libs) == 0 {
		c.Libs = defaultLibs
	}
	if c.Metrics == nil {
		c.Metrics = noopMetrics{}
	}
	if c.Logger == nil {
		c.Logger = noopLogger{}
	}
}

// Stats is a global observability snapshot.
type Stats struct {
	Pools      int
	LiveStates int
	MaxStates  int
}

// PoolStat is a per-pool snapshot for monitoring (displayVersion identifies
// which version is running).
type PoolStat struct {
	Key            string
	DisplayVersion string // human label
	VersionShort   string // first 8 chars of the engine version (hash)
	Idle           int
	CheckedOut     int
}

// pooledState pairs an LState with the script version it was built for. The
// version tag prevents an in-flight State from rejoining a new-version pool
// during a hot reload.
type pooledState struct {
	L         *lua.LState
	version   string
	idleSince int64 // time returned to idle (nanos) — LRU eviction key

	// Isolation support, populated only when Config.IsolateGlobals: the shared
	// metatable {__index = globals} and the engine's setmetatable, captured once
	// per State so each Run only allocates a fresh empty _ENV.
	envMeta *lua.Table
	setmt   lua.Value
}

// managedPool holds one script key's current version/proto and its idle States.
type managedPool struct {
	key            string
	version        string // engine version (content hash)
	displayVersion string // human label
	proto          *lua.Proto
	idle           []*pooledState
	checkedOut     int   // number of States currently in use (not yet released)
	lastUsed       int64 // last activity time (nanos) — TTL key
	dropped        bool  // pool retired by a drop notification; in-flight States close on release
}

// poolEntry guarantees lazy pool creation runs once per key (avoids duplicate
// loader.Load).
type poolEntry struct {
	once sync.Once
	pool *managedPool
	err  error
}

// Runtime lazily loads, executes, and reclaims many scripts concurrently and
// safely. Hot reload is a single model: external notification (Notify) driven
// drop-and-reload.
type Runtime struct {
	cc      *CompileCache
	loader  SourceLoader
	cfg     Config
	metrics Metrics
	logger  Logger
	trace   TraceHook

	mu       sync.Mutex
	pools    map[string]*managedPool
	creating map[string]*poolEntry
	live     int // total existing States (idle + checkedOut)
	closed   bool

	waiters  []*slotWaiter // FIFO queue of acquirers blocked for a free slot (guarded by mu)
	notify   chan struct{} // wakes Shutdown's drain wait when a State is released/closed
	closedCh chan struct{}
	stopJan  chan struct{}
}

// slotWaiter is one acquirer blocked in acquire for a free State slot. ch is
// buffered-1 and receives exactly one hand-off, after which the waiter is
// dequeued — so the send never blocks even while mu is held.
type slotWaiter struct{ ch chan struct{} }

// New creates a Runtime and starts its janitor. When MaxStates is unset it is
// derived from the memory budget.
func New(loader SourceLoader, cfg Config) *Runtime {
	cfg.withDefaults()
	m := &Runtime{
		cc:       NewCompileCache(),
		loader:   loader,
		cfg:      cfg, // set before measurePerStateBytes, which reads cfg.Libs via m.newState
		metrics:  cfg.Metrics,
		logger:   cfg.Logger,
		trace:    cfg.Trace,
		pools:    make(map[string]*managedPool),
		creating: make(map[string]*poolEntry),
		notify:   make(chan struct{}, 1),
		closedCh: make(chan struct{}),
		stopJan:  make(chan struct{}),
	}
	if m.cfg.MaxStates <= 0 {
		per := measurePerStateBytes(m.newState, 8) // measure with the same library set as real pooled States
		if per == 0 {
			per = 100 * 1024 // conservative default
		}
		m.cfg.MaxStates = int(m.cfg.MemoryBudgetBytes / per)
		if m.cfg.MaxStates < 1 {
			m.cfg.MaxStates = 1
		}
	}
	go m.janitor()
	return m
}

// Run executes the key script's entryFn with args (lazily loading, compiling,
// and creating the pool if needed) and returns its results.
//
// The returned []lua.Value is only safe to use synchronously, before the next
// pool operation. Scalars and strings are by-value and always safe, but reference
// types (table/function/userdata) belong to a pooled State that another goroutine
// may reuse the instant Run returns — reading or calling them afterwards is a data
// race. Extract what you need immediately, or prefer the safe paths: RunValues
// (deep-copies results to Go values) or RunWith (borrows results within the
// State's ownership window). This raw form stays the fastest path for primitive
// returns (e.g. a JSON string).
func (m *Runtime) Run(ctx context.Context, key, entryFn string, args ...lua.Value) ([]lua.Value, error) {
	var out []lua.Value
	err := m.RunWith(ctx, key, entryFn, func(_ *lua.LState, rets []lua.Value) error {
		out = rets
		return nil
	}, args...)
	return out, err
}

// RunWith executes the key script's entryFn with args and invokes handle with the
// running State and the call's results while the State is still owned by this call
// (before it returns to the pool). Inside handle any return type is safe to read —
// table/function/userdata included — and returned functions may be called on L.
//
// Do not let L or rets escape handle: once handle returns, the State may be reused
// by another goroutine, so anything still pointing into it races. Move out whatever
// you need inside handle (copy values, call returned functions). handle's error is
// returned as-is; handle is not called if the script itself errors.
func (m *Runtime) RunWith(ctx context.Context, key, entryFn string,
	handle func(L *lua.LState, rets []lua.Value) error, args ...lua.Value) error {
	pool, err := m.getPool(key)
	if err != nil {
		return err
	}

	tsAcq := m.traceStart()
	ps, err := m.acquire(ctx, pool)
	if err != nil {
		return err
	}
	m.traceEnd("acquire", key, tsAcq)

	defer func() {
		tsRel := m.traceStart()
		m.release(pool, ps)
		m.traceEnd("release", key, tsRel)
	}()

	// Bind the execution to a context so a runaway script is aborted. ExecTimeout
	// is a server-side hard cap; a cancelable caller ctx is honored too. We only
	// pay the engine's per-opcode context check when there is actually something
	// to wait on (execCtx.Done() != nil) — context.Background() with no ExecTimeout
	// keeps the original zero-overhead path.
	execCtx := ctx
	if m.cfg.ExecTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, m.cfg.ExecTimeout)
		defer cancel()
	}
	if execCtx.Done() != nil {
		ps.L.SetContext(execCtx)
		defer ps.L.SetContext(nil) // reset before the State returns to the pool (ctx persists on the LState)
	}

	// Cap runaway pure-Lua CPU by opcode count (orthogonal to ExecTimeout's
	// wall-clock budget). SetInstructionLimit resets the counter, so it is set
	// per Run; clear it before the State returns to the pool (no carry-over).
	if m.cfg.MaxInstructions > 0 {
		ps.L.SetInstructionLimit(m.cfg.MaxInstructions)
		defer ps.L.ClearInstructionLimit()
	}

	tsExec := m.traceStart()
	var out []lua.Value
	if m.cfg.IsolateGlobals {
		out, err = m.runIsolated(ps, pool.proto, entryFn, args)
	} else {
		fn := ps.L.GetGlobal(entryFn)
		out, err = ps.L.Call(fn, args, -1) // -1 = all results (multRet)
	}
	if err != nil {
		// The engine aborts via RaiseError(ctx.Err().Error()), which loses the typed
		// error; recover it so callers can errors.Is(..., context.DeadlineExceeded).
		if ce := execCtx.Err(); ce != nil {
			return ce
		}
		if isInstructionLimit(err) {
			return ErrInstructionLimit
		}
		return err
	}
	m.traceEnd("execute", key, tsExec)
	return handle(ps.L, out)
}

// isInstructionLimit reports whether err is the Lua error lua-pure raises when a
// SetInstructionLimit budget is spent. lua-pure exposes no typed sentinel, so it
// is matched by the raised string value (a *lua.LuaError carrying instrLimitMessage).
func isInstructionLimit(err error) bool {
	var le *lua.LuaError
	if !errors.As(err, &le) {
		return false
	}
	v := le.Value()
	return v.IsString() && v.Str() == instrLimitMessage
}

// Stats returns the current global snapshot.
func (m *Runtime) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Stats{Pools: len(m.pools), LiveStates: m.live, MaxStates: m.cfg.MaxStates}
}

// PoolStats returns a human-facing snapshot of the live pools (for monitoring —
// which key runs which displayVersion). Dropped pools are removed from the map
// and therefore not included.
func (m *Runtime) PoolStats() []PoolStat {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PoolStat, 0, len(m.pools))
	for _, p := range m.pools {
		out = append(out, PoolStat{
			Key:            p.key,
			DisplayVersion: p.displayVersion,
			VersionShort:   shortHash(p.version),
			Idle:           len(p.idle),
			CheckedOut:     p.checkedOut,
		})
	}
	return out
}

// CompileCount returns the number of distinct key:version compiled so far
// (observability).
func (m *Runtime) CompileCount() int64 { return m.cc.CompileCount() }

// Close stops the janitor and closes all idle States immediately. It does not
// wait for in-use States — those close when released. Idempotent. For a graceful
// drain, use Shutdown.
func (m *Runtime) Close() { m.closeIdleAndStop() }

// Shutdown is a graceful Close: it stops the janitor, closes idle States, then
// waits for in-flight States to be released (each closes on release) until none
// remain or ctx is done. Returns ctx.Err() on timeout (any still-in-flight
// States close on their eventual release, so there is no leak). Idempotent and
// safe to call alongside Close. Mirrors net/http.Server's Close/Shutdown split.
// Since: 2026-06-07
func (m *Runtime) Shutdown(ctx context.Context) error {
	m.closeIdleAndStop()
	for {
		m.mu.Lock()
		n := m.live // after idle is closed, this is the in-flight count
		m.mu.Unlock()
		if n == 0 {
			return nil
		}
		select {
		case <-m.notify: // a State was released/closed
		case <-time.After(20 * time.Millisecond): // safety re-check against lost signals
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// closeIdleAndStop marks the runtime closed, stops the janitor, and closes all
// idle States. Idempotent. In-flight States close on release (m.closed is true).
func (m *Runtime) closeIdleAndStop() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	close(m.closedCh)
	close(m.stopJan)
	for _, p := range m.pools {
		for _, ps := range p.idle {
			ps.L.Close()
			m.live--
		}
		p.idle = nil
	}
	m.mu.Unlock()
	m.signal() // wake any back-pressure / Shutdown waiters
}

// ── Lazy pool creation (once per key) ──

func (m *Runtime) getPool(key string) (*managedPool, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrClosed
	}
	if p, ok := m.pools[key]; ok {
		m.mu.Unlock()
		return p, nil
	}
	e, ok := m.creating[key]
	if !ok {
		e = &poolEntry{}
		m.creating[key] = e
	}
	m.mu.Unlock()

	e.once.Do(func() {
		tsLoad := m.traceStart()
		src, version, displayVersion, err := m.loader.Load(key) // I/O outside the lock
		m.traceEnd("load", key, tsLoad)
		if err != nil {
			e.err = err
			m.logger.Error("luart: load failed", "key", key, "err", err)
			return
		}
		tsComp := m.traceStart()
		proto, err := m.cc.GetOrCompile(key+":"+version, key, src)
		m.traceEnd("compile", key, tsComp)
		if err != nil {
			e.err = err
			m.logger.Error("luart: compile failed", "key", key, "err", err)
			return
		}
		display := displayOrFallback(displayVersion, version)
		m.mu.Lock()
		p := &managedPool{
			key:            key,
			version:        version,
			displayVersion: display,
			proto:          proto,
			lastUsed:       nowNanos(),
		}
		m.pools[key] = p
		e.pool = p
		m.mu.Unlock()
		// Emit outside the lock.
		m.metrics.OnCompile(key)
		m.logger.Info("luart: pool loaded", "key", key, "version", shortHash(version), "display", display)
	})

	if e.err != nil {
		// Drop the creating entry on failure so a later call can retry.
		m.mu.Lock()
		if m.creating[key] == e {
			delete(m.creating, key)
		}
		m.mu.Unlock()
		return nil, e.err
	}
	return e.pool, nil
}

// ── Acquire a State: reuse idle → grow if under cap → LRU-evict at cap → back-pressure ──

func (m *Runtime) acquire(ctx context.Context, pool *managedPool) (*pooledState, error) {
	granted := false // true after a freed slot is handed to us → claim it without yielding
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, ErrClosed
		}

		// Fresh entrants yield to already-queued acquirers (FIFO fairness). A
		// granted waiter (just handed a freed slot) skips the yield and claims it.
		if granted || len(m.waiters) == 0 {
			// 1. Reuse a current-version idle State (LIFO = warmer cache).
			if n := len(pool.idle); n > 0 {
				ps := pool.idle[n-1]
				pool.idle = pool.idle[:n-1]
				pool.checkedOut++
				pool.lastUsed = nowNanos()
				key := pool.key
				m.mu.Unlock()
				m.metrics.OnReuse(key)
				return ps, nil
			}

			// 2. Room under the global cap → create one (reserve the slot, build outside the lock).
			if m.live < m.cfg.MaxStates {
				m.live++
				pool.checkedOut++
				pool.lastUsed = nowNanos()
				proto, version, key := pool.proto, pool.version, pool.key
				m.mu.Unlock()

				ps, err := m.buildState(key, proto, version)
				if err != nil {
					m.mu.Lock()
					m.live--
					pool.checkedOut--
					m.wakeWaiterLocked() // the reserved slot is free again
					m.mu.Unlock()
					m.signal()
					return nil, err
				}
				m.metrics.OnBuild(key)
				return ps, nil
			}

			// 3. At cap → close the globally oldest idle State to free a slot (LRU).
			if vpool, idx := m.pickLRUIdleLocked(); vpool != nil {
				ps := vpool.idle[idx]
				vpool.idle = append(vpool.idle[:idx], vpool.idle[idx+1:]...)
				ps.L.Close()
				m.live--
				evictKey := vpool.key
				m.mu.Unlock()
				m.metrics.OnEvict(evictKey)
				continue // a slot opened up; retry (granted stays set so we keep priority)
			}
		}

		// 4. Nothing to claim (all in use, or we are yielding) → enqueue and block
		// until a release/eviction hands us a slot. No polling: each freed slot
		// wakes exactly one FIFO waiter, so there is no lost-wakeup window.
		granted = false
		w := &slotWaiter{ch: make(chan struct{}, 1)}
		m.waiters = append(m.waiters, w)
		m.mu.Unlock()
		select {
		case <-w.ch:
			granted = true
			continue
		case <-ctx.Done():
			m.mu.Lock()
			if !m.removeWaiterLocked(w) {
				// Already granted a slot but we are bailing → pass it to the next waiter.
				m.wakeWaiterLocked()
			}
			m.mu.Unlock()
			return nil, ctx.Err()
		case <-m.closedCh:
			return nil, ErrClosed
		}
	}
}

// release returns a State. If the Runtime is closed, the pool was dropped, or
// the version differs, the State is closed instead of pooled (in-flight safety).
// Note: a drop does NOT change pool.version, so the pool.dropped flag is what
// catches it — without it, a dropped (map-removed) pool's idle would grow with
// leaked LStates and live would never decrement.
func (m *Runtime) release(pool *managedPool, ps *pooledState) {
	// Call/CallProto leave no residual data stack between invocations; globals
	// persist (intended same-script reuse), so no explicit stack reset is needed.
	m.mu.Lock()
	pool.checkedOut--
	pool.lastUsed = nowNanos()
	if m.closed || pool.dropped || ps.version != pool.version {
		ps.L.Close()
		m.live--
		m.wakeWaiterLocked() // a slot freed (live dropped) → wake one acquirer
		m.mu.Unlock()
		m.signal()
		return
	}
	ps.idleSince = nowNanos()
	pool.idle = append(pool.idle, ps)
	m.wakeWaiterLocked() // an idle State is available → wake one acquirer
	m.mu.Unlock()
	m.signal()
}

// ── Notification-driven hot reload — drop-and-reload (single model) ──

// Change is one external change notification.
type Change struct {
	Key            string
	Version        string // engine version (content hash)
	DisplayVersion string // human label (optional)
}

// Notify applies one external change notification. If the key's pool exists and
// the version differs, that pool is dropped (reloaded on next use); if only the
// displayVersion differs, just the label is refreshed (no cold start). If the
// pool is absent (unused script) it is a no-op. In-flight States keep running on
// the old version after a drop and are discarded on release.
func (m *Runtime) Notify(key, version, displayVersion string) {
	m.mu.Lock()
	pool, ok := m.pools[key]
	if !ok {
		m.mu.Unlock()
		return // unused-script notification — safe no-op
	}
	if version == "" || pool.version == version {
		// Same content (or empty notice) → don't drop, just refresh the label.
		if displayVersion != "" && pool.displayVersion != displayVersion {
			pool.displayVersion = displayVersion
		}
		m.mu.Unlock()
		return
	}
	m.dropPoolLocked(key) // content changed → drop only this key's pool
	m.mu.Unlock()
	// Emit outside the lock.
	m.metrics.OnDrop(key)
	m.logger.Info("luart: pool dropped", "key", key, "newVersion", shortHash(version))
}

// NotifyChanges applies several change notifications at once.
func (m *Runtime) NotifyChanges(changes []Change) {
	for _, c := range changes {
		m.Notify(c.Key, c.Version, c.DisplayVersion)
	}
}

// dropPoolLocked retires a single key's pool (mu held). Idle States are closed
// immediately; in-use States are flagged via pool.dropped so they are discarded
// on release (in-flight safety — see release).
func (m *Runtime) dropPoolLocked(key string) {
	pool, ok := m.pools[key]
	if !ok {
		return
	}
	pool.dropped = true
	freed := len(pool.idle)
	for _, ps := range pool.idle {
		ps.L.Close()
		m.live--
	}
	pool.idle = nil
	delete(m.pools, key)
	delete(m.creating, key)
	m.wakeWaitersLocked(freed) // closed idle States freed that many slots
	m.signal()
}

// ── janitor: TTL idle eviction ──

func (m *Runtime) janitor() {
	t := time.NewTicker(m.cfg.JanitorInterval)
	defer t.Stop()
	for {
		select {
		case <-m.stopJan:
			return
		case <-t.C:
			m.sweep()
		}
	}
}

func (m *Runtime) sweep() {
	now := nowNanos()
	ttl := int64(m.cfg.IdleTTL)

	freed := 0
	m.mu.Lock()
	for key, p := range m.pools {
		if p.checkedOut == 0 && now-p.lastUsed > ttl {
			for _, ps := range p.idle {
				ps.L.Close()
				m.live--
				freed++
			}
			delete(m.pools, key)
			delete(m.creating, key)
		}
	}
	m.wakeWaitersLocked(freed)
	m.mu.Unlock()
	m.signal()
}

// ── helpers ──

func (m *Runtime) signal() {
	select {
	case m.notify <- struct{}{}:
	default:
	}
}

// wakeWaitersLocked hands freed slots to the longest-waiting acquirers (FIFO):
// it dequeues and signals up to n waiters. Must hold mu. Each send is to a
// buffered-1 channel that is signaled exactly once, so it never blocks.
func (m *Runtime) wakeWaitersLocked(n int) {
	for i := 0; i < n && len(m.waiters) > 0; i++ {
		w := m.waiters[0]
		m.waiters = m.waiters[1:]
		w.ch <- struct{}{}
	}
}

// wakeWaiterLocked hands a single freed slot to the front waiter (FIFO). Must
// hold mu.
func (m *Runtime) wakeWaiterLocked() { m.wakeWaitersLocked(1) }

// removeWaiterLocked drops w from the queue if still present, reporting whether
// it was found (i.e. not yet granted a slot). Must hold mu.
func (m *Runtime) removeWaiterLocked(w *slotWaiter) bool {
	for i, x := range m.waiters {
		if x == w {
			m.waiters = append(m.waiters[:i], m.waiters[i+1:]...)
			return true
		}
	}
	return false
}

// pickLRUIdleLocked finds the globally oldest idle State and its pool (mu held).
func (m *Runtime) pickLRUIdleLocked() (*managedPool, int) {
	var best *managedPool
	bestIdx := -1
	var bestSince int64 = math.MaxInt64
	for _, p := range m.pools {
		for i, ps := range p.idle {
			if ps.idleSince < bestSince {
				bestSince = ps.idleSince
				best = p
				bestIdx = i
			}
		}
	}
	return best, bestIdx
}

// newState builds an empty State (no libraries) and opens only the selected
// libraries. lua.NewState with no options opens nothing.
//
// WithRecoverGoPanics puts pooled States in protected mode: a non-LuaError Go
// panic from a registered callback (reachable via Config.Libs) is recovered into
// a catchable error and the VM is unwound to its pre-call state, so the panic
// does not escape to the host goroutine and the State stays reusable in the
// pool. Without this, lua-pure re-raises such a panic (PUC-faithful), which
// would break the pool's ownership invariant on return. luart's own (unprotected)
// code is unaffected — only callback panics inside a protected Call are caught.
func (m *Runtime) newState() *lua.LState {
	L := lua.NewState(lua.WithRecoverGoPanics())
	for _, open := range m.cfg.Libs {
		open(L)
	}
	for _, open := range m.cfg.ExtraLibs { // custom libs, after the sandbox defaults
		open(L)
	}
	return L
}

// buildState builds a fresh State for proto and tags it with the version. In the
// default mode it runs proto once to preload the script's globals (entry
// functions) onto the reused State. With Config.IsolateGlobals it instead leaves
// the globals untouched (the chunk runs per call under a fresh _ENV in runIsolated)
// and captures the per-State sandbox metatable. key is used only for the "build"
// trace stage.
func (m *Runtime) buildState(key string, proto *lua.Proto, version string) (*pooledState, error) {
	ts := m.traceStart()
	L := m.newState()
	ps := &pooledState{L: L, version: version}
	if m.cfg.IsolateGlobals {
		ps.setmt = L.GetGlobal("setmetatable")
		if ps.setmt.IsNil() {
			L.Close()
			return nil, errors.New("luart: IsolateGlobals requires the base library (setmetatable) in Config.Libs")
		}
		// {__index = globals}: per-call envs fall back to the shared libraries for
		// reads while keeping their writes local. Built once and reused per Run.
		ps.envMeta = lua.NewTable()
		ps.envMeta.SetStr("__index", L.Globals().Value())
	} else if _, err := L.CallProto(proto, 0); err != nil {
		L.Close()
		return nil, err
	}
	m.traceEnd("build", key, ts)
	return ps, nil
}

// runIsolated executes entryFn under a fresh per-call _ENV so the call's global
// writes are discarded with env when it returns (Lua 5.4 environment sandbox).
// It re-runs the chunk to define the script's functions into env, then calls
// entryFn from env; reads of undefined globals fall back to the shared libraries
// via env's {__index = globals} metatable.
func (m *Runtime) runIsolated(ps *pooledState, proto *lua.Proto, entryFn string, args []lua.Value) ([]lua.Value, error) {
	env := lua.NewTable()
	if _, err := ps.L.Call(ps.setmt, []lua.Value{env.Value(), ps.envMeta.Value()}, 0); err != nil {
		return nil, err
	}
	if _, err := ps.L.CallProtoEnv(proto, env, 0); err != nil {
		return nil, err
	}
	return ps.L.Call(env.GetStr(entryFn), args, -1)
}

// measurePerStateBytes measures the average heap cost of one State (for the
// memory-budget MaxStates derivation).
func measurePerStateBytes(build func() *lua.LState, n int) uint64 {
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	states := make([]*lua.LState, n)
	for i := range states {
		states[i] = build()
	}

	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(states)

	var per uint64
	if after.HeapAlloc > before.HeapAlloc {
		per = (after.HeapAlloc - before.HeapAlloc) / uint64(n)
	}
	for _, L := range states {
		L.Close()
	}
	return per
}

func nowNanos() int64 { return time.Now().UnixNano() }

// traceStart returns the current time when tracing is enabled, else the zero
// time (no time.Now call). Pair with traceEnd.
func (m *Runtime) traceStart() time.Time {
	if m.trace != nil {
		return time.Now()
	}
	return time.Time{}
}

// traceEnd emits a stage timing when tracing is enabled.
func (m *Runtime) traceEnd(stage, key string, start time.Time) {
	if m.trace != nil {
		m.trace(stage, key, time.Since(start))
	}
}

// shortHash trims a version (hash) to a readable 8-char prefix.
func shortHash(v string) string {
	if len(v) > 8 {
		return v[:8]
	}
	return v
}

// displayOrFallback falls back to the version (hash) prefix when the loader
// provides no displayVersion.
func displayOrFallback(displayVersion, version string) string {
	if displayVersion != "" {
		return displayVersion
	}
	return shortHash(version)
}
