**English** | [한국어](tuning.ko.md)

# Tuning guide (`luart`)

How to **choose** `luart.Config` values for your workload, plus the architecture.
For what each field means and how to load it see [config.md](config.md); for the
full benchmark tables see [BENCHMARKS.md](BENCHMARKS.md) — here we focus on
**"which value, and why."**

## 1. Architecture at a glance

`Runtime` keeps a pool of preloaded `LState`s per script key and reuses/reclaims them
within a global cap.

```
Run(ctx, key, fn, args)
  │
  ├─ getPool(key)           first time only: loader.Load → CompileCache(key:version) → create pool
  │                         (per-key sync.Once → concurrent first calls compile once)
  ├─ acquire(ctx)           ① reuse an idle State (LIFO, warmest cache)
  │                         ② under cap → newState + preload proto (one PCall)
  │                         ③ at cap → LRU-evict the globally oldest idle State, then create
  │                         ④ no idle to evict → back-pressure until a slot frees (ctx-cancelable)
  ├─ execute               [SetContext if ExecTimeout/cancelable ctx] CallByParam → copy results
  └─ release               SetTop(0) then return to idle (Close if dropped/version-mismatch/closed)

janitor (every JanitorInterval)   evicts whole idle pools past pool.lastUsed + IdleTTL
global cap = MaxStates             (or derived from MemoryBudgetBytes ÷ measured perState)
```

- The **proto is immutable and shared** (cached by `key:version`); the `LState` is reused from the per-script pool. Code: `Run`/`acquire`/`release`/`janitor` in `manager.go`.
- Hot reload: an external `Notify` drops just that pool → reloaded on next use (in-flight States finish on the old version, then are discarded).

## 2. `MaxStates` vs `MemoryBudgetBytes` — capping concurrent VMs

Two ways to bound the total number of live `LState`s globally (use one).

| Way | When | Behavior |
|---|---|---|
| **`MaxStates > 0`** | you **know** the concurrency bound (worker count, expected concurrent requests) | that value is the global cap |
| **`MaxStates == 0` + `MemoryBudgetBytes`** | you want to **bound by memory** | at `New`, the average per-State heap cost (perState) is measured over 8 samples and the cap is `budget ÷ perState` (minimum 1) |

- **Sizing example**: with the selective library set (base/table/string/math), perState ≈ **136.7 KiB** → **an 8 MiB budget ≈ 59 states**.

  | MemoryBudgetBytes | approx MaxStates (perState ≈ 137 KiB) |
  |---|---|
  | 2 MiB | ~15 |
  | 8 MiB | ~59 |
  | 32 MiB | ~240 |

  perState is measured against your actual `Libs`, so opening more libraries **automatically lowers** the state count for the same budget (no over-estimation).
- **At the cap**: when a new State is needed, the globally oldest **idle State is LRU-evicted** and a new one is built; if all are in use, the request **waits (back-pressure)** and returns an error if the ctx is canceled. So the cap protects memory without dropping requests — it queues them.
- **Too low** → higher latency from back-pressure; **too high** → more memory. Start from: concurrent-request count if known, otherwise a memory budget.

## 3. `IdleTTL` / `JanitorInterval` — reclaim aggressiveness

The janitor wakes every `JanitorInterval` and closes pools idle longer than `IdleTTL`.

- **Short IdleTTL** → reclaim memory fast (good for many rarely-used scripts) / but more **cold starts** (NewState + preload ≈ **74–93 µs**).
- **Long IdleTTL** → stay warm, stable latency / higher resident memory.
- `JanitorInterval` trades **reclaim responsiveness vs sweep cost**. Keep it well below IdleTTL (defaults: `IdleTTL=5m`, `JanitorInterval=30s`).
- Steady 24/7 traffic → longer (stay warm); bursty/sporadic → shorter (reclaim when idle).

## 4. `ExecTimeout` — guarding against runaway scripts

A per-execution hard cap. When `> 0`, each `Run` must finish within it, aborting infinite
loops and the like (`0` = disabled, zero overhead). A cancelable ctx passed by the caller
is always honored too.

- Only **pure-Lua loops** can be interrupted — inside a C function / native tight loop there is no interruption point (opcode boundary), so it may not stop immediately.
- **Always set it** for untrusted / multi-tenant scripts. For trusted-only scripts, `0` (unlimited) is fine.
- `MaxInstructions` adds an orthogonal cap on executed Lua opcodes (returns `ErrInstructionLimit`) — a runaway-CPU guard that also covers tight loops without a wall-clock deadline. See [config.md](config.md).

## 5. `Libs` — tuning sandbox and performance together

The set of Lua libraries opened on pooled States. Default `base/table/string/math/utf8/coroutine`
(lua-pure's 5.4 sandbox set); `os/io/package/debug` are **excluded** and the base globals
`load/loadfile/dofile` (compile/run arbitrary code or files) are **removed**.

- Opening fewer libraries is **faster (fewer allocs) and narrower (sandbox)**: set `Config.Libs` to only the openers you need instead of the default six. (An empty `Libs` falls back to the default set, so pass the explicit minimal slice you want.)
- Add only what you need. The moment you open `os`/`io`, file/process access is exposed and the sandbox breaks down.

## 6. Per-workload presets

| Scenario | MaxStates / budget | IdleTTL | ExecTimeout | Libs |
|---|---|---|---|---|
| **Few fixed scripts · high QPS** | `MaxStates` directly (concurrent requests) | long (e.g. `1h`) — stay warm | 0 or generous | only what's needed |
| **Many scripts · sporadic calls** | `MemoryBudgetBytes` (bound by memory) | short (e.g. `1m`) — reclaim when idle | per workload | minimal |
| **Adversarial / multi-tenant** | `MaxStates` conservative | short | **required** (short) | **minimal** (base/string, …) |

## 7. Adjusting from observability

Tune the values above from measurements, not guesses.

| Signal (source) | Meaning | Response |
|---|---|---|
| rising `acquire` time (`acquire` stage of `Config.Trace`) | back-pressure — the cap is low for the load | raise `MaxStates`/budget |
| frequent evictions (`Metrics.OnEvict`) | too many active scripts for the cap → frequent LRU | raise the cap or review active-script count |
| frequent recompiles (rising `CompileCount()`) | drops/reloads or rebuilds after TTL reclaim | raise IdleTTL or review reload frequency |
| many idle in `PoolStats()` | wasted resident memory | lower IdleTTL or the cap |

- Global: `Stats()` (live/pools/max), per-pool: `PoolStats()` (idle/checkedOut/displayVersion), compiles: `CompileCount()`, per-stage cost: `Config.Trace TraceHook(stage,key,dur)`.
- The observability interfaces (`Metrics`/`Logger`/`Trace`) are all opt-in — zero hot-path overhead when unset.

---

> See also: value formats/defaults and JSON/YAML/env loading in [config.md](config.md); full benchmark tables and methodology in [BENCHMARKS.md](BENCHMARKS.md).
