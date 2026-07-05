package main

import "sync"

// CachingLoader (hybrid #1) wraps any SourceLoader with an in-memory cache: the
// first Load for a key hits the backend (file/DB/network); later Loads are served
// from memory. This keeps a slow backend lazy while bounding residency to the keys
// actually used — unlike MapLoader, which holds every registered source forever.
//
// Invalidation pairs with hot reload: when the underlying source changes you must
// Invalidate(key) BEFORE calling Runtime.Notify, otherwise the drop reloads and the
// cache hands back the stale source. (The runtime calls Load again only after a
// drop, so a never-invalidated entry is never refetched.)
//
// Since: 2026-06-08
type CachingLoader struct {
	backend SourceLoader
	mu      sync.RWMutex
	cache   map[string]cacheRec
}

type cacheRec struct{ src, version, displayVersion string }

// SourceLoader is the contract this example implements; it mirrors
// luart.SourceLoader so the wrapper can hold any backend (FileLoader, DBLoader, …)
// without importing a concrete type.
type SourceLoader interface {
	Load(key string) (src, version, displayVersion string, err error)
}

// NewCachingLoader wraps backend.
func NewCachingLoader(backend SourceLoader) *CachingLoader {
	return &CachingLoader{backend: backend, cache: make(map[string]cacheRec)}
}

// Load returns the cached record, or fetches from the backend on a miss and caches
// the result. Errors are not cached (a transient backend failure can be retried).
func (l *CachingLoader) Load(key string) (src, version, displayVersion string, err error) {
	l.mu.RLock()
	rec, ok := l.cache[key]
	l.mu.RUnlock()
	if ok {
		return rec.src, rec.version, rec.displayVersion, nil
	}

	src, version, displayVersion, err = l.backend.Load(key)
	if err != nil {
		return "", "", "", err
	}
	l.mu.Lock()
	l.cache[key] = cacheRec{src: src, version: version, displayVersion: displayVersion}
	l.mu.Unlock()
	return src, version, displayVersion, nil
}

// Invalidate drops the cached entry for key so the next Load refetches from the
// backend. Call it before Runtime.Notify on an external change.
func (l *CachingLoader) Invalidate(key string) {
	l.mu.Lock()
	delete(l.cache, key)
	l.mu.Unlock()
}
