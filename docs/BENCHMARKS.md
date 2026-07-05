# Benchmarks (`luart`)

Where luart's performance claims come from, and how to reproduce them. The three
levers are **compile-once caching**, **VM pooling**, and a **self-managing
registry**; this page measures each.

> **Numbers below were measured on Apple M2 Max (darwin/arm64), Go 1.24,
> lua-pure v0.1.1.** They vary by machine — the meaningful figures are the
> *ratios* and *allocation counts* (stable across runs), not the absolute
> nanoseconds. Reproduce everything with `make bench` and `go run ./performance`.

## Methodology

- `go test -bench=. -benchmem ./...` (via `make bench`) — per-op time, bytes, and
  allocation counts. For stable comparisons use `-benchtime=2s -count=8` and
  summarize with [`benchstat`](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat).
- `go run ./performance` — compile cost vs PUC-Lua's C compiler on identical
  scripts (needs a `lua` 5.4 binary on `PATH` for the PUC columns).
- "Trivial functions" scripts (`function fN() return N end`) are a deliberate
  worst case for per-prototype overhead; real scripts expand less, so read the
  engine-vs-engine / cached-vs-cold *ratio*, not the absolute megabytes.

## 1. Bytecode cache — compile once per `key:version`

A script is parsed + compiled only on its first run; every later run is a cache
hit. The hit is **zero-allocation** and orders of magnitude cheaper than
recompiling.

| Benchmark | ns/op | B/op | allocs/op | Meaning |
|---|---:|---:|---:|---|
| `BenchmarkCompileEveryTime` | 1914 | 3352 | 42 | recompile the chunk on every call (baseline) |
| `BenchmarkCachedCompile` | **15** | **0** | **0** | cache hit — **~128× faster, zero-alloc** |
| `BenchmarkCompileCacheHit` | 15 | 0 | 0 | `CompileCache` lookup by `key:version` |
| `BenchmarkMapLoaderLoad` | 18 | 0 | 0 | loader lookup (in-memory `MapLoader`) |

Reproduce: `go test -bench='Cache|Compile|Load' -benchmem . ./cmd/bytecode-cache`.

## 2. VM pooling — reuse warm States

Building a fresh `LState` (open libraries + preload the compiled proto) costs
~100 µs and hundreds of allocations. A per-script pool reuses warm States, so a
steady-state call is a few hundred nanoseconds.

| Benchmark | ns/op | B/op | allocs/op | Meaning |
|---|---:|---:|---:|---|
| `BenchmarkExecuteWithPool` | **88** | 185 | 5 | pooled reuse |
| `BenchmarkExecuteWithoutPool` | 109954 | 115509 | 614 | fresh State every call → **pool ~1250× faster** |
| `BenchmarkMultiScriptPooled` | 294 | 348 | 8 | pooled, many scripts, parallel |
| `BenchmarkMultiScriptNoPool` | 68799 | 57803 | 295 | New + preload + Close each call → **pool ~234× faster** |
| `BenchmarkConcurrentCompileSameKey` | 115 | 0 | 0 | concurrent first-access compiles once (per-key `sync.Once`) |

**Library set matters.** Most of a fresh State's cost is opening libraries.
Setting `Config.Libs` to only what you need (instead of the full set) cuts both
time and allocations roughly in half:

| Benchmark | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `BenchmarkNewStateSelectiveLibs` | 204427 | 57131 | 281 |
| `BenchmarkNewStateAllLibs` | 235270 | 111963 | 564 |

→ a selective set saves ~31 µs and ~half the allocations (564 → 281) per State,
and narrows the sandbox at the same time.

Reproduce: `go test -bench='Pool|MultiScript|NewState|Concurrent' -benchmem ./cmd/...`.

## 3. Dynamic registry overhead

The registry adds a small, bounded cost over a raw pool: per `Run` it does the
global-cap / LRU / version accounting that gives you eviction, memory budgeting,
and hot reload.

| Benchmark | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `BenchmarkDynamicRun` (lazy-loaded pool reuse, parallel) | 785 | 264 | 4 |

That is still ~100× cheaper than building a State per call (§2) — the overhead
buys the cap, eviction, and reload machinery.

## 4. Large scripts — cost is the first compile, cache hits stay flat

Loading and cache-hit execution are effectively **independent of source size**;
only the one-time compile scales (roughly linearly). Generated scripts,
`examples/custom-loaders` `BenchmarkFileLoader_*`:

| Stage | 64 KB | 256 KB | 512 KB |
|---|---|---|---|
| `Load` (read + hash) | 52 µs · 1.2 GB/s · 11 allocs | 176 µs · 1.5 GB/s · 11 | 341 µs · 1.6 GB/s · 11 |
| `CompileRun` (first compile) | 2.6 ms · 4.2 MB | 10.2 ms · 17 MB | 19.5 ms · 35 MB |
| `RunCached` (cache hit) | **275 ns · 136 B · 2 allocs** | **264 ns · 136 B · 2** | **257 ns · 136 B · 2** |

The `CompileRun` byte figures are *bytes allocated* (`B/op` — churn, mostly
collected), not resident memory: only the compiled `*Proto` stays cached per
`key:version`, and that retention is **shared across the pool** (it does not grow
with `MaxStates`). The takeaway: pay compile once per version, amortize across
every subsequent run.

Reproduce: `go test -bench=BenchmarkFileLoader -benchmem ./examples/custom-loaders/`.

## 5. Compile cost vs PUC-Lua (C)

Because lua-pure ports PUC-Lua's 5.4 compiler in pure Go, compile cost stays
close to the reference C implementation and scales **linearly**. First compile,
parse+compile only:

| Source | lua-pure (ms) | lua-pure (MB) | PUC-Lua (ms) | PUC-Lua (MB) | × time | × mem |
|---|---:|---:|---:|---:|---:|---:|
| 64 KB | 1.4 | 1.0 | 1.0 | 0.4 | 1× | 2× |
| 256 KB | 6.7 | 3.9 | 3.9 | 1.7 | 2× | 2× |
| 512 KB | 14.6 | 7.8 | 7.5 | 3.4 | 2× | 2× |
| 1 MB | 27.2 | 14.4 | 14.5 | 6.4 | 2× | 2× |

Reproduce: `go run ./performance` (needs `lua` 5.4 on `PATH` for the PUC columns).

## 6. Memory-budget sizing

When `MaxStates == 0` and `MemoryBudgetBytes > 0`, the cap is derived at `New`:
the runtime measures the average per-State heap cost (`perState`) over several
samples and sets `MaxStates = MemoryBudgetBytes ÷ perState` (minimum 1). Because
`perState` is measured against **your actual `Libs`**, opening more libraries
automatically lowers the count for the same budget — no over-provisioning.

With a selective library set, `perState` lands on the order of ~130–140 KiB, so:

| MemoryBudgetBytes | approx MaxStates |
|---|---|
| 2 MiB | ~15 |
| 8 MiB | ~60 |
| 32 MiB | ~240 |

At the cap, luart evicts the globally oldest idle State (LRU) or applies
context-aware back-pressure — it queues rather than dropping requests. See the
[tuning guide](tuning.md) for choosing budgets, TTLs, and limits by workload.
