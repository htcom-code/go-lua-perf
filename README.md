**English** | [한국어](README.ko.md)

# go-lua-perf (luart)

[![Go Reference](https://pkg.go.dev/badge/github.com/htcom-code/go-lua-perf.svg)](https://pkg.go.dev/github.com/htcom-code/go-lua-perf)
[![CI](https://github.com/htcom-code/go-lua-perf/actions/workflows/ci.yml/badge.svg)](https://github.com/htcom-code/go-lua-perf/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/htcom-code/go-lua-perf)](https://goreportcard.com/report/github.com/htcom-code/go-lua-perf)
[![Go 1.24+](https://img.shields.io/badge/go-1.24%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/dl/)
[![Lua 5.4](https://img.shields.io/badge/Lua-5.4-000080.svg)](https://www.lua.org/)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**`luart`** runs Lua scripts in Go with **high performance and concurrency safety** —
a per-`key:version` bytecode cache, per-script VM pooling, and a self-managing dynamic
registry — on top of [lua-pure](https://github.com/htcom-code/lua-pure), a pure-Go
PUC-Lua 5.4 engine.

- Module `github.com/htcom-code/go-lua-perf` · Go 1.24+ · lua-pure v0.1.1 (Lua 5.4) · **v0.0.1**
- The core library depends only on lua-pure (the config loader's YAML dependency is isolated in `luartconfig`).

## Features

- **Bytecode cache — compile once per `key:version`.** A script is parsed and compiled only on its first run; every later run is a 0-alloc cache hit, independent of source size.
- **Concurrency-safe VM pooling.** Each script gets its own pool of Lua States, never shared across goroutines, so thousands of concurrent `Run` calls reuse warm VMs.
- **Self-managing registry — lazy load, TTL & memory-budget eviction, hot reload.** Scripts load on demand, idle States are reclaimed (TTL + LRU under a memory cap), the cap applies backpressure, and a notification hot-reloads with no restart.
- **Pluggable `SourceLoader` + metrics/logging/tracing.** Fetch scripts from any backend (file, DB, in-memory, caching, routing); opt into `Metrics`, `Logger`, and a per-stage `TraceHook` at zero cost when unset.
- **Sandboxed Lua 5.4 with exec-time & instruction limits.** A safe default library set (customizable via `Config.Libs`), plus `ExecTimeout` (wall-clock) and `MaxInstructions` (opcode) caps for untrusted scripts.

## Installation

```bash
go get github.com/htcom-code/go-lua-perf
```

The library lives at the module root; the package name is `luart`, so import it with an alias:

```go
import luart "github.com/htcom-code/go-lua-perf"
```

Public module — `go get` works directly via the module proxy. The API is `0.x` and may change between minor versions.

## Quick Start

```go
import (
	"context"
	"fmt"

	lua "github.com/htcom-code/lua-pure/lua"
	luart "github.com/htcom-code/go-lua-perf"
)

loader := luart.NewMapLoader() // a SourceLoader (where your cache/DB plugs in)
src := `function greet(name) return "hello, " .. name end`
loader.Set("greeter", src, luart.HashVersion(src), "1.0.0")

rt := luart.New(loader, luart.Config{MaxStates: 4})
defer rt.Close()

out, _ := rt.Run(context.Background(), "greeter", "greet", lua.LString("luart"))
fmt.Println(out[0].String()) // hello, luart
```

> [!IMPORTANT]
> **`SourceLoader` is the interface you implement** — write it to fetch sources from your external cache/DB/service:
> `Load(key string) (src, version, displayVersion string, err error)`
>
> `NewMapLoader` (and `Set` / `Loads`) is a **test/demo-only** in-memory implementation, not suitable for production (it keeps every source resident forever). Only the content-hash helper `HashVersion(src string) string` is meant for use beyond demos.
>
> **Full guide + File/DB/Memory/hybrid examples:** [docs/SourceLoader.md](docs/SourceLoader.md).

## Usage

The runtime is a small surface: construct with `New`, execute with `Run` /
`RunValues` / `RunWith`, reload with `Notify`, and stop with `Close` / `Shutdown`.
You implement `SourceLoader` to fetch scripts from your own cache/DB/service.

- **`Run`** — fastest path; read the returned values synchronously.
- **`RunValues`** — deep-copies results to Go values, safe to keep after the call.
- **`RunWith`** — consume results inside a handler while the State is still owned.

Runnable, test-covered examples — one folder per feature (hot reload, TTL, memory
budget, exec timeout, graceful shutdown, config loading, metrics, logging, tracing,
sandbox, custom libs, custom loaders) — live in **[examples/](examples/)**:

```bash
go run ./examples/basics
```

Full API reference: **[pkg.go.dev](https://pkg.go.dev/github.com/htcom-code/go-lua-perf)** (or `make doc`).

## Configuration

Set `luart.Config` in code, or load the numeric/duration fields from JSON/YAML/env
via the `luartconfig` subpackage. Key knobs:

| Concern | Fields |
|---|---|
| Concurrency cap | `MaxStates`, or `MemoryBudgetBytes` (derives the cap) |
| Idle reclaim | `IdleTTL`, `JanitorInterval` |
| Runaway guards | `ExecTimeout` (wall-clock), `MaxInstructions` (opcodes) |
| Sandbox / libraries | `Libs`, `ExtraLibs`, `IsolateGlobals` |
| Observability | `Metrics`, `Logger`, `Trace` |

- Field reference & loading (JSON/YAML/env, precedence): **[docs/config.md](docs/config.md)**
- Choosing values by workload: **[docs/tuning.md](docs/tuning.md)**
- Implementing a `SourceLoader`: **[docs/SourceLoader.md](docs/SourceLoader.md)**

## Performance

- **VM pool reuse ≈ 870×** faster than building a fresh State per call (~258 ns vs ~225 µs), and a **compile-cache hit is 0-alloc**.
- **Cache-hit execution is effectively size-independent** (~550 ns): the one-time compile is paid once per `key:version` and amortized across every run.
- **Compile cost stays within ~1–2× time / ~2× memory** of PUC-Lua's C compiler (lua-pure ports it in pure Go).

Full methodology and per-benchmark tables: **[docs/BENCHMARKS.md](docs/BENCHMARKS.md)**. Reproduce on your machine with `make bench` and `go run ./performance`.

## Documentation

- **API reference** — [pkg.go.dev](https://pkg.go.dev/github.com/htcom-code/go-lua-perf) (`make doc` / `make doc-web` locally)
- **Guides** — [config](docs/config.md) · [tuning](docs/tuning.md) · [SourceLoader](docs/SourceLoader.md) · [benchmarks](docs/BENCHMARKS.md)
- **Changelog** — [CHANGELOG.md](CHANGELOG.md)

## Status & Roadmap

`luart` is at **v0.0.1** (`0.x`) — usable, with an API that may still change between
minor versions. See **[ROADMAP.md](ROADMAP.md)** for direction and non-goals.

## Contributing

Contributions welcome — see **[CONTRIBUTING.md](CONTRIBUTING.md)** for the build/test
discipline (`make all` gate, per-file tests, benchmark guardrails) and the
architecture map. Bug reports / feature requests use the issue templates; Lua
**language** issues belong to the [lua-pure](https://github.com/htcom-code/lua-pure) engine.

## Security

luart can run untrusted Lua at scale, but the host owns the sandboxing and
resource-limit policy. See **[SECURITY.md](SECURITY.md)** for the threat model and how
to report a vulnerability privately.

## License

[MIT](LICENSE) © 2026 htjulia
