**English** | [한국어](README.ko.md)

# Examples

Small, self-contained programs that **use the `luart` library** as a consumer would —
one folder per public feature. Each is runnable and test-covered.

> These differ from [`cmd/`](../cmd/), which are low-level "how it works under the hood"
> demos (bytecode caching, pooling techniques) that mostly re-implement mechanics inline.
> `examples/` is "how you use the library"; `cmd/` is "how the library works".

Run any example:

```bash
go run ./examples/basics
```

Each folder also has a `main_test.go`, so `go test ./examples/...` (and `make all`) covers them.

## Suggested reading order

Start with `basics`, then pick by the feature you need.

| Example | Demonstrates | Key API |
|---|---|---|
| [basics](basics/) | Load a script, run a function with args, read the result | `New`, `Run`, `NewMapLoader`/`Set`, `HashVersion` |
| [hot-reload](hot-reload/) | Drop-and-reload on an external change (no restart) | `Notify` / `NotifyChanges` |
| [ttl-eviction](ttl-eviction/) | Janitor reclaims idle pools after `IdleTTL` | `Config.IdleTTL`, `JanitorInterval`, `Stats` |
| [memory-budget](memory-budget/) | Cap live VMs by a memory budget (derived `MaxStates` + LRU) | `Config.MemoryBudgetBytes` |
| [exec-timeout](exec-timeout/) | Abort a runaway script via hard cap or caller deadline | `Config.ExecTimeout`, `Run(ctx, …)` |
| [graceful-shutdown](graceful-shutdown/) | Drain in-flight calls; `ErrClosed` afterward | `Shutdown(ctx)` vs `Close()` |
| [config-loading](config-loading/) | Build `Config` from JSON/YAML/env with precedence | `luartconfig.ResolveJSONString` / `FromEnv` / `Load` |
| [metrics](metrics/) | Count lifecycle events (compile/build/reuse/…) | `Config.Metrics` |
| [logging](logging/) | Route events into `log/slog` | `Config.Logger`, `NewSlogLogger` |
| [trace-profiling](trace-profiling/) | Per-stage request timing for profiling | `Config.Trace` (`TraceHook`) |
| [observability](observability/) | Read-only introspection for dashboards | `Stats`, `PoolStats`, `CompileCount` |
| [sandbox-libs](sandbox-libs/) | Control which Lua stdlib a script can reach | `Config.Libs` |
| [custom-libs](custom-libs/) | Add a user-authored library (Go funcs + module table) on top of the sandbox | `Config.ExtraLibs` |
| [custom-loaders](custom-loaders/) | Implement `SourceLoader` for File/DB/Memory + caching & routing backends | `SourceLoader`, `HashVersion` ([guide](../docs/SourceLoader.md)) |

See the [root README](../README.md) for the full public-API overview and the
[tuning guide](../docs/tuning.md) for choosing config values by workload.
