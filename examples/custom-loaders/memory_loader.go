package main

import (
	"fmt"
	"sync"

	luart "github.com/htcom-code/go-lua-perf"
)

// MemoryLoader is a minimal in-memory loader: scripts are held in a map guarded by
// an RWMutex. It is the same shape as the built-in luart.MapLoader — use MapLoader
// for tests/demos; this exists to show the pattern when you want your own (e.g. to
// seed from config at startup). Like MapLoader, it keeps every source resident, so
// it suits a small, fixed set of scripts, not a large catalog (see the memory model
// note in the README).
//
// Since: 2026-06-08
type MemoryLoader struct {
	mu   sync.RWMutex
	recs map[string]memRec
}

type memRec struct{ src, version, displayVersion string }

// NewMemoryLoader returns an empty loader.
func NewMemoryLoader() *MemoryLoader {
	return &MemoryLoader{recs: make(map[string]memRec)}
}

// Set registers or updates a script. The version is derived from the source, so a
// changed body yields a changed version (pair with Notify to reload).
func (l *MemoryLoader) Set(key, src, displayVersion string) {
	l.mu.Lock()
	l.recs[key] = memRec{src: src, version: luart.HashVersion(src), displayVersion: displayVersion}
	l.mu.Unlock()
}

// Load returns the record for key, or a not-found error.
func (l *MemoryLoader) Load(key string) (src, version, displayVersion string, err error) {
	l.mu.RLock()
	rec, ok := l.recs[key]
	l.mu.RUnlock()
	if !ok {
		return "", "", "", fmt.Errorf("luart: script %q not found", key)
	}
	return rec.src, rec.version, rec.displayVersion, nil
}

// Version returns the current engine version for key (used to drive Notify after a
// Set). Empty if the key is unknown.
func (l *MemoryLoader) Version(key string) string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.recs[key].version
}
