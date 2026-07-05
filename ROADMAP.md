# Roadmap

`luart` (go-lua-perf) is the **runtime layer** for running Lua in Go at scale:
a per-`key:version` bytecode cache, per-script VM pooling, and a dynamic registry
(lazy load, TTL + memory-budget eviction, backpressure, notification-driven hot
reload), on top of the [lua-pure](https://github.com/htcom-code/lua-pure) engine.

The division of labour is deliberate and standing: **the Lua language lives in
lua-pure; luart owns the runtime around it** (caching, pooling, lifecycle,
sourcing, config, observability). New Lua semantics arrive by lua-pure tracking
PUC-Lua upstream — luart inherits them by bumping its lua-pure dependency, not by
changing the language surface here.

## Versioning intent

- **Current — v0.0.x.** The initial `0.x` line. Built on lua-pure v0.1.1
  (PUC-Lua 5.4). While the version is `0.x`, the public API may change between
  minor releases (see [CHANGELOG](CHANGELOG.md)).
- **Toward 1.0.** A `1.0` tag will follow once the public surface (`New`,
  `Run`/`RunWith`, `Config`, `SourceLoader`, the observability interfaces) has
  settled and been exercised by real workloads. No date is committed.
- **Engine upgrades.** As lua-pure ports newer PUC-Lua releases (e.g. 5.5),
  luart will adopt them by bumping the dependency and documenting any behaviour
  change in the CHANGELOG.

## Done (v0.0.1)

- **Bytecode caching** — parse/compile once per `key:version`; cache-hit runs are
  effectively 0-alloc and size-independent.
- **Per-script VM pooling** — concurrency-safe reuse of warm Lua States; States
  are never shared across goroutines.
- **Dynamic registry** — lazy load, TTL eviction, memory-budget-derived
  `MaxStates` with idle-LRU eviction + backpressure, and notification-driven hot
  reload.
- **`SourceLoader` contract** with `MapLoader` plus example File / DB / in-memory
  / caching / routing loaders, keyed on content version (`HashVersion`).
- **Config** — `Config.Validate` and the `luartconfig` subpackage (JSON / YAML /
  env with a documented resolution precedence).
- **Execution limits** — `ExecTimeout` (wall-clock) and `MaxInstructions`
  (opcode cap → `ErrInstructionLimit`), plus context cancellation.
- **Observability** — `Metrics`, `Logger` (`NewSlogLogger`), and a per-stage
  `TraceHook`, all zero-cost when unset.

## Directions (non-binding, under consideration)

These are candidates, not commitments — feedback via
[issues](https://github.com/htcom-code/go-lua-perf/issues) shapes priority.

- **Release automation** — tag-triggered GitHub Release with CHANGELOG notes.
- **More sourcing adapters** — first-class (non-example) loaders and a caching
  layer contract, so common backends aren't re-implemented per project.
- **Metrics exporters** — a thin bridge from the `Metrics` interface to common
  telemetry systems, kept out of the zero-dependency core.
- **Registry introspection** — richer `Stats` / eviction and reload events for
  operational visibility.
- **Benchmark tracking** — continuous benchmark results in CI to catch hot-path
  regressions over time.

## Non-goals

- **Not a Lua implementation.** Language semantics, the standard library, and the
  VM are lua-pure's domain. Requests to change Lua behaviour belong there.
- **No hidden global state.** Runtimes are explicit values (`New` → `*Runtime`
  → `Close`); luart does not install process-wide singletons.
- **No mandatory dependencies in the core.** Optional features (config YAML,
  future exporters) stay in subpackages so the core stays lua-pure-only.
