<!--
Thanks for contributing to go-lua-perf (luart)! Keep the title in Conventional
Commits form, e.g. `fix(pool): release State on Run panic`.
Delete any section that does not apply.
-->

## What & why

<!-- What does this change do, and what problem does it solve? -->

## Type of change

- [ ] `fix` — bug fix (no new API)
- [ ] `feat` — new capability (runtime, registry, config, SourceLoader, observability)
- [ ] `perf` — performance, no observable behaviour change
- [ ] `docs` — documentation only
- [ ] `refactor` / `chore` / `test` / `ci` — no behaviour change

## Concurrency & correctness

luart is concurrency-safe by contract — pooled States are never shared across
goroutines, and the registry is exercised under load.

- [ ] `make race` passes (`go test -race ./...`)
- [ ] Added/updated exception-case tests (nil/empty, cancellation/timeout,
      eviction under memory budget, hot reload, double-close, leak/invariant)
- [ ] No `lua.Value` reference escapes the State that owns it

## Performance (for `perf`, and anything touching the hot path)

The caching + pooling wins (load/cache-hit ≈ file-size-independent; pool reuse)
are the point of this library — guard them.

- [ ] `benchstat` before/after included below; no regression on the affected benchmark
- [ ] Allocation counts (`-benchmem`) checked where relevant

```
<!-- benchstat / go test -bench output -->
```

## Verification

The Makefile is the single source of truth.

- [ ] `make all` passes (vet → test → race → build)
- [ ] Godoc updated for any exported symbol (with `// Since: YYYY-MM-DD` on new API)
- [ ] Docs updated if behaviour/config changed (README, `docs/*`)

## Related

<!-- Closes #123, related issues. -->
