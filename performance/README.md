# performance — lua-pure vs PUC-Lua compile cost

A standalone tool that compiles the **same** Lua source files with two engines and
prints a side-by-side table:

- **lua-pure** — the pure-Go engine `luart` is built on (`lua.CompileReader` →
  `*Proto`).
- **PUC-Lua** — the reference C implementation (the `lua` binary on your `PATH`),
  measured in-process via [`measure.lua`](measure.lua) (`load()` + `collectgarbage("count")`).

For each size it reports **compile time** (parse + compile only, not execution)
and **retained memory** (heap held by the compiled prototype afterwards).

## Run

```bash
go run ./performance            # full comparison (regenerates scripts)
go run ./performance -reps 3    # fix the rep count instead of adaptive
go run ./performance -keep      # reuse existing scripts/ instead of regenerating
```

If `lua` is not on `PATH`, the PUC-Lua columns show `N/A` and only the lua-pure
numbers are printed. Install it with e.g. `brew install lua`.

The test scripts live in [`scripts/`](scripts/) and are committed so the
comparison is reproducible — `fN.lua` defines `N` trivial global functions
(`function fI() return I end`). A run regenerates them deterministically (same
bytes, no git diff); use `-keep` to skip regeneration.

## What it shows (Apple Silicon, darwin/arm64, PUC-Lua 5.4.8)

Because lua-pure ports PUC-Lua's 5.4 compiler in pure Go, compile cost stays close
to the reference C implementation — and both scale **linearly** with source size:

| size  | lua-pure (ms) | lua-pure (MB) | PUC (ms) | PUC (MB) | × time | × mem |
|-------|--------------:|--------------:|---------:|---------:|-------:|------:|
| 64KB  |           1.4 |           1.0 |      1.0 |      0.4 |     1× |    2× |
| 256KB |           6.7 |           3.9 |      3.9 |      1.7 |     2× |    2× |
| 512KB |          14.6 |           7.8 |      7.5 |      3.4 |     2× |    2× |
| 1MB   |          27.2 |          14.4 |     14.5 |      6.4 |     2× |    2× |

Your absolute numbers will vary by machine; the meaningful figure is the
engine-vs-engine ratio (~1–2× time, ~2× retained memory), which stays flat across
sizes because neither compiler is super-linear.

## Why this matters for `luart`

The compile cost is real but it is a **one-time** cost. `luart` pays it **once per
`key:version`**: the compiled `*Proto` is cached and reused, and pooled VMs execute
it without recompiling. A warm cache hit runs in ~550 ns / ~136 B regardless of
script size (see the `BenchmarkFileLoader_*` benchmarks in
[`examples/custom-loaders`](../examples/custom-loaders)). So the practical guidance
is: keep individual scripts to a sensible size, warm the cache, and the compile
cost amortizes to nothing across subsequent runs.

> **Caveat — synthetic worst case.** "Many trivial functions" maximizes
> per-prototype overhead (debug line info, source names, etc.), so the absolute
> expansion factor is far larger than a real script of the same byte size would
> show. The engine-vs-engine *ratio* is the meaningful number here, not the
> absolute MB.
