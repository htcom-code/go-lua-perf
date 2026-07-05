# Contributing to go-lua-perf (`luart`)

Thanks for your interest in improving `luart`. This guide covers the conventions
this project follows. The **Makefile is the single source of truth** for all
build and test commands.

## Getting started

```bash
git clone https://github.com/htcom-code/go-lua-perf
cd go-lua-perf
make all        # vet → test → race → build
```

Requires Go 1.24+ (the toolchain is pinned in `go.mod`). The core library depends
only on [lua-pure](https://github.com/htcom-code/lua-pure); the YAML dependency is
isolated in the `luartconfig` subpackage.

luart is the **runtime layer** on top of the lua-pure engine. Bugs in Lua
*semantics* (a script behaves differently from PUC-Lua 5.4.8) belong to lua-pure;
this repo owns caching, pooling, the registry, config, and observability.

## Architecture at a glance

Knowing where a change lands helps reviewers and keeps tests next to their subject.

| File / package | Responsibility |
|---|---|
| `loader.go` | `SourceLoader` contract, `MapLoader`, the `key:version` `CompileCache`, `HashVersion` |
| `manager.go` | `Runtime` (`New`): lazy load, per-script VM pool, TTL + memory-budget LRU eviction, backpressure, `Notify` hot reload, `Shutdown` |
| `convert.go` | Go ↔ Lua value conversion helpers |
| `config.go` | `Config` and `Config.Validate` |
| `observability.go` | `Metrics`, `Logger`, `TraceHook`, `NewSlogLogger` (all no-op / zero-cost when unset) |
| `luartconfig/` | JSON/YAML/env config loading (`LoadJSON`/`LoadYAML`/`FromEnv`/`Resolve`) — the only place YAML is imported |
| `internal/genconfig/` | `make config` generator (example config files) |
| `examples/` | one folder per public feature; consumer-facing usage |
| `cmd/` | lower-level technique demos (bytecode cache, pooling) |
| `performance/` | compile-cost comparison vs PUC-Lua (`make performance`) |

The two invariants a change must not break: **a pooled State is never touched by
two goroutines at once**, and **a `lua.Value` reference never escapes the State
that owns it** (materialize with the conversion helpers when crossing goroutines).

## Development workflow

Run the full verification before opening a PR:

```bash
make all      # vet → test → race → build (the pre-PR gate)
```

The Makefile is the single source of truth. Individual targets:

| target | what it runs |
|---|---|
| `make vet` | `go vet ./...` |
| `make test` | `go test ./...` |
| `make race` | `go test -race ./...` |
| `make build` | `go build ./...` — depends on `test`, so it only builds after tests pass |
| `make all` | `vet → test → race → build` (the gate CI mirrors) |
| `make bench` | `go test -bench=. -benchmem ./...` (observational, not a gate) |
| `make config` | regenerate `configs/luart.example.{yaml,json}` |
| `make performance` | compile-cost table vs PUC-Lua (needs a `lua` 5.4 binary on `PATH`) |
| `make memprof` | capture a heap profile and print the top allocators |
| `make doc` | print the package godoc to the terminal |
| `make doc-web` | browse the full API at a local pkgsite server |

Keep `gofmt` clean (`gofmt -l .` prints nothing) — CI enforces it.

## Benchmarking

The caching + pooling wins are the reason this library exists, so guard them.

- Any change to the hot path (loader, cache, pool, `Run`/`RunWith`) needs a
  before/after comparison. Use `-benchmem` and average several runs:

  ```bash
  go test -bench=BenchmarkDynamicRun -benchmem -count=8 . > new.txt
  # (stash your change, rerun into old.txt)
  benchstat old.txt new.txt
  ```

- Include the `benchstat` output in the PR and call out any regression. Watch
  allocation counts (`allocs/op`), not just time — a cache hit should stay
  effectively 0-alloc and size-independent.
- When you record a new headline number, update the README Performance table so
  the docs stay honest.

## Code & test conventions

These keep the codebase traceable and its performance claims honest.

1. **A test file per source file.** For `foo.go`, add `foo_test.go` in the same
   package (e.g. `loader.go → loader_test.go`). Don't pile tests into one file.
   Entry points like `main.go` are exempt when their components are tested
   elsewhere.
2. **Test the behavior, and benchmark what matters.** Testable functions get a
   basic-behavior test. Performance-sensitive functions get a `Benchmark*` with
   `b.ReportAllocs()`; record meaningful numbers in the README/docs tables.
3. **Always include exception cases.** Each test file must exercise edge cases —
   nil/empty input, missing keys, races (`-race`), context cancel/timeout,
   resource limits, duplicate calls / double-free, retry-after-error, and
   leak/invariant checks. Exercise the actual branch — don't stop at the happy path.
4. **Version new API in doc comments.** New functions/methods/tests carry a
   `// Since: YYYY-MM-DD` line. Use `// Changed: YYYY-MM-DD - reason` for
   breaking signature changes, and the standard `// Deprecated: ...` for removal.
5. **Godoc on everything.** Comments start with the identifier name
   (`// GetOrCompile ...`) so `go doc` / pkgsite render cleanly. Required for
   exported identifiers, encouraged for unexported ones.
6. **Build only after tests pass.** The pipeline order is
   `go vet` → `go test ./...` → `go test -race ./...` → `go build ./...`
   (`make build` enforces this by depending on `test`). Don't ship on red tests.

### Test file template

```go
package foo

import "testing"

// TestThing_Basic verifies the happy path of Thing.
// Since: 2026-07-05
func TestThing_Basic(t *testing.T) { /* ... */ }

// TestThing_Exceptions verifies Thing's edge/boundary cases.
// Since: 2026-07-05
func TestThing_Exceptions(t *testing.T) {
	// nil/empty input, errors, concurrency, cancellation, limits, ...
}

// BenchmarkThing measures Thing's cost.
// Since: 2026-07-05
func BenchmarkThing(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ { /* ... */ }
}
```

## Cross-platform

Line endings are normalized to LF via `.gitattributes` so `gofmt -l` stays clean
on macOS, Linux, and Windows. The library and its `go` targets must build on all
three; generators (`make config`) use `go run`, not POSIX shell.

## Pull requests

- Keep changes focused; describe what and why.
- Ensure `make all` passes and the PR template checklist is satisfied.
- The `main` branch is protected — open a PR rather than pushing directly.

## License

By contributing, you agree that your contributions are licensed under the
project's [MIT License](LICENSE).
