**English** | [한국어](CHANGELOG.ko.md)

# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

While the version is `0.x`, the public API may change between minor releases.

## [0.0.2] - 2026-07-05

### Changed

- Bump the `lua-pure` dependency to **v0.1.2** and, to satisfy its toolchain
  requirement, raise the minimum Go version to **1.25** (`go.mod` now pins
  `go 1.25.11`; was `1.24`).

## [0.0.1] - 2026-07-05

Initial public release of **`luart`** — a high-performance, concurrency-safe
runtime for running Lua scripts in Go, built on
[lua-pure](https://github.com/htcom-code/lua-pure) (a pure-Go PUC-Lua 5.4 port).
Requires Go 1.24+. The core library depends only on lua-pure; the config loader's
YAML dependency is isolated in the `luartconfig` subpackage.

### Runtime

- `New(loader SourceLoader, cfg Config) *Runtime` — a lazy-loading, pooling
  runtime with a background janitor.
- `Run(ctx, key, entryFn, args…) ([]lua.Value, error)` — compile once per
  `key:version`, then reuse a preloaded State from the per-script pool.
- `RunWith(ctx, key, entryFn, handle, args…)` — consume results while the State
  is still owned (any return type safe to read/call inside `handle`).
- `RunValues(...) ([]any, error)` — deep-copy results to Go values for use after
  the call; non-data values go through the `Config.ConvertValue` hook.
- Lifecycle: `Close()` (immediate) and `Shutdown(ctx)` (graceful drain).

### Caching & pooling

- Per-`key:version` bytecode cache (`CompileCache`): a script is parsed and
  compiled only on first use; later runs are cache hits (effectively 0-alloc,
  size-independent).
- Per-script pool of Lua States, reused across calls. States are never shared
  across goroutines — the single-owner invariant holds under load.

### Dynamic registry

- Lazy load on first `Run`; TTL idle eviction (`IdleTTL` / `JanitorInterval`).
- Memory-budget-derived `MaxStates` (`MemoryBudgetBytes ÷ measured per-State
  cost`) with global idle-LRU eviction and FIFO back-pressure at the cap
  (arrival-ordered, context-aware — no lost wakeups or polling).
- Notification-driven hot reload: `Notify(key, version, displayVersion)` /
  `NotifyChanges([]Change)` drop a pool; the next `Run` reloads it. In-flight
  States finish on the old version, then are discarded.

### Sources & config

- `SourceLoader` interface + `MapLoader` (in-memory), `HashVersion(src)`
  (content-hash versioning). Example File / DB / Memory / caching / routing
  loaders in `examples/custom-loaders` (see `docs/SourceLoader.md`).
- `luartconfig` subpackage: `LoadJSON` / `LoadYAML` / `Load` (by extension) /
  `FromEnv`, plus `Resolve` (precedence env > file/string > defaults) and
  `Config.Validate()`.

### Execution limits & safety

- `Config.ExecTimeout` — per-execution wall-clock cap; a cancelable `ctx` passed
  to `Run` is honored too (`0` = disabled, zero overhead; pure-Lua loops).
- `Config.MaxInstructions` — per-`Run` opcode cap for runaway pure-Lua CPU,
  orthogonal to `ExecTimeout`; exceeding it returns `ErrInstructionLimit`.
- `Config.IsolateGlobals` — run each `Run` under a fresh `_ENV` so a script's
  global writes don't leak into the next call reusing the same pooled State.
- Pooled States run in protected mode: a panicking Go callback is recovered into
  a catchable error and the State is unwound to a reusable state.

### Libraries

- Default library set is lua-pure's safe Lua 5.4 subset
  (`base/table/string/math/utf8/coroutine`; `load`/`loadfile`/`dofile` removed;
  no `os`/`io`/`package`/`debug`).
- `SkipOpenLibs` opens nothing by default; `Config.Libs` selects exactly what
  opens; `Config.ExtraLibs` adds custom libraries (Go functions, module tables,
  `L.Preload` lazy `require`) after the sandbox defaults.

### Observability (opt-in, zero overhead when unset)

- `Metrics` interface (no-op default), `Logger` interface + `NewSlogLogger`,
  per-stage `TraceHook`. Snapshots via `Stats()`, `PoolStats()`, `CompileCount()`.

### Tooling & docs

- `Makefile` as the single source of truth (`make all` = vet → test → race →
  build); GitHub Actions CI mirrors it.
- `examples/` (one folder per public feature), `cmd/` technique demos, and
  `performance/` (a lua-pure vs PUC-Lua compile-cost comparison).
- README (en/ko), `docs/config.md`, `docs/tuning.md`, `docs/SourceLoader.md`
  (each with a Korean `.ko.md`).

[0.0.1]: https://github.com/htcom-code/go-lua-perf/releases/tag/v0.0.1
