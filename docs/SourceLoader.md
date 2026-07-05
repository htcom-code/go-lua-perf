**English** | [한국어](SourceLoader.ko.md)

# Source loading (`SourceLoader`)

`SourceLoader` is the one interface you implement to use `luart` — it tells the
runtime **where script sources come from** (a file tree, a database, a cache, a
service). `New(loader, cfg)` takes it, and the runtime calls `Load` lazily on a
script's first use (once per key, even under concurrent access).

The runtime never retains the source string: it reads it once at compile time and
drops it. **Source residency is the loader's responsibility** — the built-in
`NewMapLoader` keeps every source in memory forever (fine for tests/demos), while a
production loader should fetch on demand so unused sources stay out of memory. See
the *Memory model* note in the [root README](../README.md).

## 1. The contract

Implement one method; one helper is optional.

| Method / helper | Signature | Returns | Rule |
|---|---|---|---|
| `SourceLoader.Load` (you implement) | `Load(key string)` | `src, version, displayVersion string, err error` | Return the script body, an engine `version`, a human label, or a **non-nil error** for an unknown `key`. Must be **goroutine-safe**. |
| `HashVersion` (helper) | `HashVersion(src string)` | `string` | sha256-hex of `src`. Use it to produce `version` when your backend doesn't already supply one. |

**`version` vs `displayVersion`:**

| Field | Role | Effect |
|---|---|---|
| `version` | engine change-key (a content hash is recommended) | Keys the compile cache (`key:version`) and drives hot reload: `Notify` with a **changed** version drops the pool; the **same** version is idempotent (no recompile, no drop). It *must* change whenever the source changes. |
| `displayVersion` | human label (e.g. `"1.0.0"`) | Cosmetic — shown in `PoolStats` and logs. Changing it alone refreshes the label without a reload. Empty is fine; the runtime falls back to the version hash prefix. |

**Rules to follow:**
- **Error, don't return empty**, on an unknown key (e.g. `fmt.Errorf("luart: script %q not found", key)`).
- `Load` runs **outside the runtime lock**, so blocking I/O (disk, DB, network) is fine.
- Same source ⇒ same `version` ⇒ idempotent. Derive `version` deterministically from content (or have your backend store a content hash).

The examples below are the verified source of [`examples/custom-loaders/`](../examples/custom-loaders/) — run them with `go run ./examples/custom-loaders`.

## 2. File-backed loader

Reads `<dir>/<key>.lua`; the content hash is the version, so editing the file and
notifying with the new hash triggers a reload.

```go
type FileLoader struct{ dir string }

func NewFileLoader(dir string) *FileLoader { return &FileLoader{dir: dir} }

func (l *FileLoader) Load(key string) (src, version, displayVersion string, err error) {
	b, err := os.ReadFile(filepath.Join(l.dir, key+".lua"))
	if err != nil {
		return "", "", "", fmt.Errorf("luart: script %q not found: %w", key, err)
	}
	src = string(b)
	return src, luart.HashVersion(src), "", nil
}
```

## 3. Database-backed loader

Uses the standard `database/sql` package and imports **no specific driver**, so the
backend (Postgres, MySQL, SQLite, …) is your choice — open the `*sql.DB` with your
driver and pass it in. Map `sql.ErrNoRows` to a not-found error.

```go
type DBLoader struct {
	db    *sql.DB
	query string
}

func NewDBLoader(db *sql.DB) *DBLoader {
	return &DBLoader{
		db:    db,
		query: `SELECT src, version, display FROM scripts WHERE key = ?`,
	}
}

func (l *DBLoader) Load(key string) (src, version, displayVersion string, err error) {
	var display sql.NullString
	err = l.db.QueryRow(l.query, key).Scan(&src, &version, &display)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", fmt.Errorf("luart: script %q not found", key)
	}
	if err != nil {
		return "", "", "", fmt.Errorf("luart: load %q: %w", key, err)
	}
	return src, version, display.String, nil
}
```

Expected schema (the writer sets `version` to a content hash):

```sql
CREATE TABLE scripts (
    key     TEXT PRIMARY KEY,
    src     TEXT NOT NULL,
    version TEXT NOT NULL, -- engine version: a content hash the writer sets
    display TEXT           -- human label, e.g. "1.0.0" (may be empty)
);
```

> The example's `DBLoader` is compile-verified but **not run** in tests — exercising
> it would require importing a concrete SQL driver (a new module dependency). In your
> service you open a real `*sql.DB` and pass it to `NewDBLoader`.

## 4. In-memory loader

A `map` guarded by an `RWMutex`. This is the same shape as the built-in
`luart.MapLoader` — use `MapLoader` for tests/demos; write your own when you want to
seed from config at startup. Like `MapLoader`, it keeps every source resident, so it
suits a small, fixed set of scripts, not a large catalog.

```go
type MemoryLoader struct {
	mu   sync.RWMutex
	recs map[string]memRec
}

type memRec struct{ src, version, displayVersion string }

func NewMemoryLoader() *MemoryLoader {
	return &MemoryLoader{recs: make(map[string]memRec)}
}

func (l *MemoryLoader) Set(key, src, displayVersion string) {
	l.mu.Lock()
	l.recs[key] = memRec{src: src, version: luart.HashVersion(src), displayVersion: displayVersion}
	l.mu.Unlock()
}

func (l *MemoryLoader) Load(key string) (src, version, displayVersion string, err error) {
	l.mu.RLock()
	rec, ok := l.recs[key]
	l.mu.RUnlock()
	if !ok {
		return "", "", "", fmt.Errorf("luart: script %q not found", key)
	}
	return rec.src, rec.version, rec.displayVersion, nil
}
```

## 5. Caching wrapper (hybrid)

Wrap any loader with an in-memory cache: the first `Load` for a key hits the backend
(file/DB/network), later `Load`s are served from memory. This keeps a slow backend
lazy while bounding residency to the keys actually used — unlike `MapLoader`, which
holds everything.

```go
type SourceLoader interface {
	Load(key string) (src, version, displayVersion string, err error)
}

type CachingLoader struct {
	backend SourceLoader
	mu      sync.RWMutex
	cache   map[string]cacheRec
}

type cacheRec struct{ src, version, displayVersion string }

func NewCachingLoader(backend SourceLoader) *CachingLoader {
	return &CachingLoader{backend: backend, cache: make(map[string]cacheRec)}
}

func (l *CachingLoader) Load(key string) (src, version, displayVersion string, err error) {
	l.mu.RLock()
	rec, ok := l.cache[key]
	l.mu.RUnlock()
	if ok {
		return rec.src, rec.version, rec.displayVersion, nil
	}

	src, version, displayVersion, err = l.backend.Load(key)
	if err != nil {
		return "", "", "", err // errors are not cached → transient failures can retry
	}
	l.mu.Lock()
	l.cache[key] = cacheRec{src: src, version: version, displayVersion: displayVersion}
	l.mu.Unlock()
	return src, version, displayVersion, nil
}

func (l *CachingLoader) Invalidate(key string) {
	l.mu.Lock()
	delete(l.cache, key)
	l.mu.Unlock()
}
```

> **Invalidation pairs with hot reload.** The runtime calls `Load` again only after a
> drop, so a cached entry is never refetched on its own. When the underlying source
> changes you must `Invalidate(key)` **before** `Notify`, or the drop reloads and the
> cache hands back the stale source.

## 6. Routing loader (hybrid)

Dispatch by a key prefix so one runtime can mix sources: `file:checkout` reads from
disk, `db:pricing` from a database, `mem:healthcheck` from memory. The prefix is part
of the key you pass to `Run`, so the chosen backend is explicit. The backend receives
the key **without** the prefix.

```go
type RoutingLoader struct {
	routes map[string]SourceLoader // prefix (without ':') → backend
}

func NewRoutingLoader(routes map[string]SourceLoader) *RoutingLoader {
	return &RoutingLoader{routes: routes}
}

func (l *RoutingLoader) Load(key string) (src, version, displayVersion string, err error) {
	prefix, rest, ok := strings.Cut(key, ":")
	if !ok {
		return "", "", "", fmt.Errorf("luart: key %q has no backend prefix (want \"<prefix>:<key>\")", key)
	}
	backend, ok := l.routes[prefix]
	if !ok {
		return "", "", "", fmt.Errorf("luart: no backend for prefix %q in key %q", prefix, key)
	}
	return backend.Load(rest)
}
```

## 7. Wiring & hot reload

Compose the loaders and pass the outermost one to `New`. On an external change,
update the backend, invalidate any cache, then `Notify` with the new version so the
pool is dropped and reloaded on the next `Run`.

```go
cachedFile := NewCachingLoader(NewFileLoader(dir))
mem := NewMemoryLoader()
mem.Set("healthcheck", `function status() return "ok" end`, "1.0.0")

router := NewRoutingLoader(map[string]SourceLoader{
	"file": cachedFile,
	"mem":  mem,
})

rt := luart.New(router, luart.Config{MaxStates: 4})
defer rt.Close()

out, _ := rt.Run(ctx, "file:checkout", "discount", lua.LNumber(100))

// External change to the file-backed script:
os.WriteFile(filepath.Join(dir, "checkout.lua"), []byte(checkoutV2), 0o644)
cachedFile.Invalidate("checkout") // backend-local key (no prefix)
rt.Notify("file:checkout", luart.HashVersion(checkoutV2), "2.0.0")

out, _ = rt.Run(ctx, "file:checkout", "discount", lua.LNumber(100)) // new version
```

See [`examples/custom-loaders/`](../examples/custom-loaders/) for the full runnable
program and [`examples/hot-reload/`](../examples/hot-reload/) for a focused reload demo.

---

> See also: the public-API overview in the [root README](../README.md); config
> field formats in [config.md](config.md); choosing pool size / budget / TTL in the
> [tuning guide](tuning.md).
