**English** | [한국어](config.ko.md)

# Configuration (`luartconfig`)

How to load a `luart.Runtime`'s settings from **JSON/YAML files** or **environment
variables**, and what each field means.

> For **why** to pick a given value (pool size, memory budget, TTL, exec timeout, by
> workload), see the [tuning guide](tuning.md).

- Only the **numeric / duration fields** are loadable from files or env vars.
- The remaining fields are **injected in code** (set on the returned `Config`), not
  loadable from files/env: `Libs` / `ExtraLibs` (Lua libraries to open),
  `IsolateGlobals`, `MaxInstructions` (per-`Run` opcode cap), `ConvertValue`,
  `Metrics`, `Logger`, and `Trace`.
- The loaders live in the subpackage `luartconfig`, not in the core `luart`
  (the YAML dependency is isolated there).

## Fields

| Field (`luart.Config`) | JSON/YAML key | Env var (`<PREFIX>` + ) | Type | Default | Description |
|---|---|---|---|---|---|
| `MaxStates` | `maxStates` | `MAX_STATES` | integer | `0` | Global cap on live VMs (States). **When `0`**, derived as `MemoryBudgetBytes ÷ measured perState` (minimum 1). |
| `MemoryBudgetBytes` | `memoryBudgetBytes` | `MEMORY_BUDGET_BYTES` | integer (bytes) | `0` | Memory budget. Used only when `MaxStates` is `0`. E.g. `8388608` (8 MiB). |
| `IdleTTL` | `idleTTL` | `IDLE_TTL` | duration string | `5m` | Script pools idle longer than this are reclaimed by the janitor. |
| `JanitorInterval` | `janitorInterval` | `JANITOR_INTERVAL` | duration string | `30s` | Janitor sweep period. |
| `ExecTimeout` | `execTimeout` | `EXEC_TIMEOUT` | duration string | `0` (disabled) | Per-execution hard cap. When `> 0`, each `Run` must finish within it (aborts runaway scripts such as infinite loops). `0` disables it (zero overhead). A cancelable ctx passed to `Run` is always honored regardless of this value. |

- **Duration strings** use Go's `time.ParseDuration` format: `"300ms"`, `"1.5s"`, `"30s"`, `"5m"`, `"1h"`.
- An empty (`""`) or zero value falls back to the default (at `luart.New`).
- Validation: `MaxStates`, `IdleTTL`, `JanitorInterval`, and `ExecTimeout` cannot be negative (rejected by `Config.Validate()`).
- `ExecTimeout` can only interrupt **pure-Lua loops**. Inside a C function / native tight loop there is no interruption point (opcode boundary), so it may not stop immediately.

## JSON

`luart.json`:
```json
{
  "maxStates": 16,
  "memoryBudgetBytes": 0,
  "idleTTL": "5m",
  "janitorInterval": "30s",
  "execTimeout": "0s"
}
```
```go
cfg, err := luartconfig.LoadJSON("luart.json")
```

> To parse a **JSON string** directly (remote store, flag, test, …) instead of a file, use `luartconfig.LoadJSONString(jsonStr)` (JSON only).

## YAML

`luart.yaml`:
```yaml
maxStates: 16
memoryBudgetBytes: 0
idleTTL: 5m
janitorInterval: 30s
execTimeout: 0s
```
```go
cfg, err := luartconfig.LoadYAML("luart.yaml")
```

> `luartconfig.Load("luart.yaml")` / `luartconfig.Load("luart.json")` auto-detects the format by **extension (.json/.yaml/.yml)**.

## Environment variables

Read with a prefix. Unset variables fall back to defaults.
```bash
export LUART_MAX_STATES=16
export LUART_MEMORY_BUDGET_BYTES=0
export LUART_IDLE_TTL=5m
export LUART_JANITOR_INTERVAL=30s
export LUART_EXEC_TIMEOUT=0s
```
```go
cfg, err := luartconfig.FromEnv("LUART_")
```

## Usage

```go
import (
	luart "github.com/htcom-code/go-lua-perf"
	"github.com/htcom-code/go-lua-perf/luartconfig"
)

cfg, err := luartconfig.Load("luart.yaml") // or LoadJSON / FromEnv
if err != nil {
	log.Fatal(err)
}

// Values that can't come from a file/env are injected in code here.
cfg.Logger = luart.NewSlogLogger(slog.Default())
// cfg.Metrics = myMetrics
// cfg.Trace = myTraceHook
// cfg.Libs = []func(*lua.LState){(*lua.LState).OpenBase, (*lua.LState).OpenString} // default: base/table/string/math/utf8/coroutine

rt := luart.New(loader, cfg)
defer rt.Close()
```

## Precedence merge (env > file > defaults)

`Resolve` reads **a file as the base, then overlays environment variables field by
field**. The precedence is `env > file > defaults`:

```go
// luart.yaml as the base; only fields with a set LUART_* env var are overridden
cfg, err := luartconfig.Resolve("luart.yaml", "LUART_")
```

- **Field-level merge**: an env var overrides only the field it sets. E.g. if the file provides all fields and only `LUART_MAX_STATES` is set, only `MaxStates` takes the env value; the rest stay from the file.
- A field set by neither source is left at its zero value, so **`luart.New`'s built-in default** applies.
- Passing `""` as `path` skips the file → `env > defaults`.

To use a **JSON string** as the base instead of a file, use `ResolveJSONString` — the precedence becomes `env > JSON string > defaults`:
```go
cfg, err := luartconfig.ResolveJSONString(`{"maxStates": 4, "idleTTL": "1m"}`, "LUART_")
// with LUART_MAX_STATES=20 → MaxStates=20 (env), IdleTTL=1m (string)
```

> If you only need one source, use a single loader (`LoadJSON` / `LoadJSONString` / `LoadYAML` / `Load` / `FromEnv`) — they build a `Config` from that one source without merging.

## Validation

`luartconfig.Load*` / `FromEnv` call `Config.Validate()` internally and return an error for
invalid values (negative numbers, unparseable durations). A hand-built `Config` can be
checked directly with `cfg.Validate()`.
