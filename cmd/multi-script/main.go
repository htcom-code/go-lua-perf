// Command multi-script demonstrates a thread-safe runtime that runs MANY Lua
// scripts from MANY goroutines, addressing two concerns the simpler
// bytecode-cache / pool-preload examples leave open:
//
//	Concern 1 (concurrent compile): even when many goroutines request the same
//	            script for the first time at once, it compiles exactly once
//	            (per-key sync.Once), while different scripts compile in parallel
//	            (the global lock is held only for the map lookup).
//	Concern 2 (GC vs Pool): VM pooling is offered in two strategies — sync.Pool
//	            (GC may reclaim) and a fixed channel pool (persistent, predictable
//	            memory).
//
// Engine: lua-pure (github.com/htcom-code/lua-pure).
package main

import (
	"fmt"
	"log"
	"sync"
	"sync/atomic"

	lua "github.com/htcom-code/lua-pure/lua"
)

// ─────────────────────────────────────────────────────────────────────────────
// Concern 1 — concurrent compile: per-key sync.Once cache
// ─────────────────────────────────────────────────────────────────────────────

type compileResult struct {
	proto *lua.Proto
	err   error
}

// cacheEntry guarantees a single key compiles exactly once.
type cacheEntry struct {
	once sync.Once
	res  compileResult
}

// CompileCache compiles sources into immutable *lua.Proto and caches
// them permanently. The global lock (mu) is held only while looking up/creating
// an entry, so different keys compile in parallel and the same key compiles only
// once even under concurrent first access.
type CompileCache struct {
	mu       sync.Mutex
	entries  map[string]*cacheEntry
	compiles int64 // atomic — number of actual compiles (asserted ==1 in tests)
}

func NewCompileCache() *CompileCache {
	return &CompileCache{entries: make(map[string]*cacheEntry)}
}

// GetOrCompile returns the compile result for key. The same key compiles once.
// Note: compile errors are also cached per key (immutable-proto philosophy); to
// retry, delete the err!=nil entry under mu in the caller.
func (c *CompileCache) GetOrCompile(key, src string) (*lua.Proto, error) {
	c.mu.Lock()
	e, ok := c.entries[key]
	if !ok {
		e = &cacheEntry{}
		c.entries[key] = e
	}
	c.mu.Unlock() // compile outside the global lock — parallel with other keys

	e.once.Do(func() {
		atomic.AddInt64(&c.compiles, 1)
		proto, err := lua.CompileString(src, key)
		e.res = compileResult{proto: proto, err: err}
	})
	return e.res.proto, e.res.err
}

// CompileCount returns the number of actual compiles performed so far.
func (c *CompileCache) CompileCount() int64 { return atomic.LoadInt64(&c.compiles) }

// ─────────────────────────────────────────────────────────────────────────────
// VM creation — open only the needed stdlib (Concern 2: lower miss cost + sandbox)
// ─────────────────────────────────────────────────────────────────────────────

// newConfiguredState starts with no libraries and opens only base/table/string/
// math. This lowers creation cost vs full OpenLibs and tightens the sandbox by
// withholding os/io/package/debug.
func newConfiguredState() *lua.LState {
	L := lua.NewState()
	L.OpenBase()
	L.OpenTable()
	L.OpenString()
	L.OpenMath()
	return L
}

// buildPreloadedState creates a fresh State and runs proto once to define
// (preload) its globals, then returns it. proto is immutable and shared by all
// States.
func buildPreloadedState(key string, proto *lua.Proto) *lua.LState {
	L := newConfiguredState()
	if _, err := L.CallProto(proto, 0); err != nil {
		L.Close()
		panic(fmt.Errorf("preload %q: %w", key, err))
	}
	return L
}

// ─────────────────────────────────────────────────────────────────────────────
// Concern 2 — pool abstraction: sync.Pool vs fixed channel pool
// (a small shared interface — the slot where Phase 2's ManagedPool fits in)
// ─────────────────────────────────────────────────────────────────────────────

// statePool is the acquire/return interface for preloaded LStates. Call/CallProto
// leave no residual data stack between invocations, so Put has no stack to clear.
type statePool interface {
	Get() *lua.LState
	Put(*lua.LState)
}

// syncStatePool — sync.Pool backend. GC may reclaim idle States (no leak:
// lua-pure uses no finalizer/goroutine, so a dropped un-Closed State is simply
// GC'd). When reclaimed, the next Get rebuilds it via New (=buildPreloadedState).
type syncStatePool struct{ p sync.Pool }

func newSyncStatePool(build func() *lua.LState) *syncStatePool {
	return &syncStatePool{p: sync.Pool{New: func() any { return build() }}}
}

func (s *syncStatePool) Get() *lua.LState  { return s.p.Get().(*lua.LState) }
func (s *syncStatePool) Put(L *lua.LState) { s.p.Put(L) } // globals persist (intended preload reuse)

// fixedStatePool — holds a fixed N States persistently via a channel. GC does not
// reclaim them, so memory is predictable, and Get blocks when exhausted, giving
// natural back-pressure. Close() must be called on shutdown to Close every State.
type fixedStatePool struct{ ch chan *lua.LState }

func newFixedStatePool(n int, build func() *lua.LState) *fixedStatePool {
	p := &fixedStatePool{ch: make(chan *lua.LState, n)}
	for i := 0; i < n; i++ {
		p.ch <- build()
	}
	return p
}

func (p *fixedStatePool) Get() *lua.LState  { return <-p.ch }
func (p *fixedStatePool) Put(L *lua.LState) { p.ch <- L }

// Close closes every held State (freeing temp files and call-stack segments).
func (p *fixedStatePool) Close() {
	close(p.ch)
	for L := range p.ch {
		L.Close()
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Multi-script runtime — a preloaded pool per script key
// ─────────────────────────────────────────────────────────────────────────────

// poolKind selects which pool strategy the runtime uses.
type poolKind int

const (
	poolSync  poolKind = iota // sync.Pool (default)
	poolFixed                 // fixed channel pool
)

// scriptPool pairs one script's immutable proto with its dedicated State pool.
type scriptPool struct {
	key   string
	proto *lua.Proto
	pool  statePool
}

// run takes a State from the pool, calls entryFn (a preloaded global function)
// with args, and copies out the results. Return is guaranteed via defer (Put
// clears the stack).
//
// Note: if a returned LValue is a reference type (e.g. a table) it may mutate
// after the State is reused. This example's entry functions return strings, so
// it is safe.
func (sp *scriptPool) run(entryFn string, args ...lua.Value) ([]lua.Value, error) {
	L := sp.pool.Get()
	defer sp.pool.Put(L)

	fn := L.GetGlobal(entryFn)
	return L.Call(fn, args, -1) // -1 = all results (multRet)
}

// ScriptRuntime registers and runs many scripts concurrently and safely.
type ScriptRuntime struct {
	cc     *CompileCache
	kind   poolKind
	fixedN int // States per script when poolFixed

	mu      sync.Mutex
	sources map[string]string      // key -> source (replaced by a DB loader in Phase 2)
	pools   map[string]*scriptPool // key -> preloaded pool
}

// NewScriptRuntime builds a runtime using the sync.Pool strategy (default).
func NewScriptRuntime() *ScriptRuntime {
	return &ScriptRuntime{
		cc:      NewCompileCache(),
		kind:    poolSync,
		sources: make(map[string]string),
		pools:   make(map[string]*scriptPool),
	}
}

// NewScriptRuntimeFixed builds a runtime using the fixed channel pool strategy,
// holding n States per script persistently. Close() is required after use.
func NewScriptRuntimeFixed(n int) *ScriptRuntime {
	r := NewScriptRuntime()
	r.kind = poolFixed
	r.fixedN = n
	return r
}

// Register registers a script source (compiling and creating the pool eagerly)
// and returns any error.
func (r *ScriptRuntime) Register(key, src string) error {
	r.mu.Lock()
	r.sources[key] = src
	r.mu.Unlock()
	_, err := r.getPool(key)
	return err
}

// Run executes the key script's entryFn with args and returns its results.
// Returns an error for an unregistered key.
func (r *ScriptRuntime) Run(key, entryFn string, args ...lua.Value) ([]lua.Value, error) {
	sp, err := r.getPool(key)
	if err != nil {
		return nil, err
	}
	return sp.run(entryFn, args...)
}

// getPool returns the key's pool or lazily creates it. Compilation runs outside
// the lock (CompileCache guarantees once); the map update is done once via a
// double check.
func (r *ScriptRuntime) getPool(key string) (*scriptPool, error) {
	r.mu.Lock()
	if sp, ok := r.pools[key]; ok {
		r.mu.Unlock()
		return sp, nil
	}
	src, ok := r.sources[key]
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("script %q not registered", key)
	}

	proto, err := r.cc.GetOrCompile(key, src)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if sp, ok := r.pools[key]; ok { // double-check
		return sp, nil
	}
	sp := r.newScriptPool(key, proto)
	r.pools[key] = sp
	return sp, nil
}

func (r *ScriptRuntime) newScriptPool(key string, proto *lua.Proto) *scriptPool {
	build := func() *lua.LState { return buildPreloadedState(key, proto) }
	var p statePool
	switch r.kind {
	case poolFixed:
		p = newFixedStatePool(r.fixedN, build)
	default:
		p = newSyncStatePool(build)
	}
	return &scriptPool{key: key, proto: proto, pool: p}
}

// Close closes every State held by fixed channel pools (no-op for sync.Pool).
func (r *ScriptRuntime) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, sp := range r.pools {
		if fp, ok := sp.pool.(*fixedStatePool); ok {
			fp.Close()
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Demo
// ─────────────────────────────────────────────────────────────────────────────

func main() {
	rt := NewScriptRuntime()

	// Two scripts defining different globals (pools are separated by key).
	if err := rt.Register("upper", `function process(s) return "UP:" .. string.upper(s) end`); err != nil {
		log.Fatal(err)
	}
	if err := rt.Register("twice", `function transform(n) return tostring(n * 2) end`); err != nil {
		log.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 1; i <= 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			up, err := rt.Run("upper", "process", lua.MkString(fmt.Sprintf("data-%d", id)))
			if err != nil {
				log.Printf("[%d] upper error: %v", id, err)
				return
			}
			tw, err := rt.Run("twice", "transform", lua.Int(int64(id)))
			if err != nil {
				log.Printf("[%d] twice error: %v", id, err)
				return
			}
			fmt.Printf("[%d] upper=%s twice=%s\n", id, up[0].Str(), tw[0].Str())
		}(i)
	}
	wg.Wait()
}
