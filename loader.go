// Package luart is a reusable, thread-safe runtime for executing many Lua
// scripts (lua-pure) from many goroutines. It lazily loads script sources
// from an external SourceLoader, compiles once per key:version, pools preloaded
// LStates per script, evicts idle pools, caps total VMs by a memory budget, and
// hot-reloads on external notification (drop-and-reload).
//
// Observability (Metrics, Logger), config loading (subpackage luartconfig:
// JSON/YAML/env), graceful shutdown (Shutdown), and developer profiling
// (TraceHook) are all opt-in and add no required dependency beyond lua-pure.
//
// Since: 2026-06-07
package luart

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"

	lua "github.com/htcom-code/lua-pure/lua"
)

// SourceLoader abstracts where script sources come from (an external cache or
// service backed by a DB, etc.). version is the engine-facing change key
// (a content hash is recommended) — it must change whenever the source changes.
// displayVersion is a human-facing label (e.g. "1.0.0"); when empty, callers
// fall back to the hash prefix.
type SourceLoader interface {
	Load(key string) (src, version, displayVersion string, err error)
}

// MapLoader is an in-memory SourceLoader for tests and demos. Set simulates an
// external change.
type MapLoader struct {
	mu    sync.RWMutex
	recs  map[string]scriptRec
	loads int64 // atomic — number of Load calls (used to verify lazy loading)
}

type scriptRec struct{ src, version, displayVersion string }

// NewMapLoader returns an empty in-memory loader.
func NewMapLoader() *MapLoader { return &MapLoader{recs: make(map[string]scriptRec)} }

// Set registers or updates a script. Calling it with a changed version (hash)
// simulates an external change. displayVersion is the human label (may be empty
// — callers fall back).
func (l *MapLoader) Set(key, src, version, displayVersion string) {
	l.mu.Lock()
	l.recs[key] = scriptRec{src: src, version: version, displayVersion: displayVersion}
	l.mu.Unlock()
}

// Load returns the (source, version, displayVersion) for key, or an error if
// the key is unknown.
func (l *MapLoader) Load(key string) (string, string, string, error) {
	atomic.AddInt64(&l.loads, 1)
	l.mu.RLock()
	rec, ok := l.recs[key]
	l.mu.RUnlock()
	if !ok {
		return "", "", "", fmt.Errorf("luart: script %q not found", key)
	}
	return rec.src, rec.version, rec.displayVersion, nil
}

// Loads returns the number of Load calls so far.
func (l *MapLoader) Loads() int64 { return atomic.LoadInt64(&l.loads) }

// HashVersion returns the content hash (sha256 hex) of src — the recommended
// engine-facing version (any content change yields a different version). The
// external system usually provides it; this helper is for tests, demos, and
// self-verification.
// Since: 2026-06-07
func HashVersion(src string) string {
	sum := sha256.Sum256([]byte(src))
	return hex.EncodeToString(sum[:])
}

// ─────────────────────────────────────────────────────────────────────────────
// Compile cache — one sync.Once per key. The key is "key:version", so a version
// change recompiles.
// ─────────────────────────────────────────────────────────────────────────────

type compileResult struct {
	proto *lua.Proto
	err   error
}

type cacheEntry struct {
	once sync.Once
	res  compileResult
}

// CompileCache compiles sources into immutable *lua.Proto and caches
// them permanently.
type CompileCache struct {
	mu       sync.Mutex
	entries  map[string]*cacheEntry
	compiles int64 // atomic
}

// NewCompileCache returns an empty cache.
func NewCompileCache() *CompileCache {
	return &CompileCache{entries: make(map[string]*cacheEntry)}
}

// GetOrCompile returns the compile result for cacheKey (the same key compiles
// exactly once; different keys compile in parallel). cacheKey is normally of the
// form "scriptKey:version".
func (c *CompileCache) GetOrCompile(cacheKey, name, src string) (*lua.Proto, error) {
	c.mu.Lock()
	e, ok := c.entries[cacheKey]
	if !ok {
		e = &cacheEntry{}
		c.entries[cacheKey] = e
	}
	c.mu.Unlock()

	e.once.Do(func() {
		atomic.AddInt64(&c.compiles, 1)
		proto, err := lua.CompileString(src, name)
		e.res = compileResult{proto: proto, err: err}
	})
	return e.res.proto, e.res.err
}

// CompileCount returns the number of actual compiles (distinct key:version).
func (c *CompileCache) CompileCount() int64 { return atomic.LoadInt64(&c.compiles) }
