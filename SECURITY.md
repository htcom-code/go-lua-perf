# Security Policy

## Reporting a vulnerability

Please report security issues **privately** — do not open a public issue for an
unpatched vulnerability.

- Use GitHub's **"Report a vulnerability"** (Security → Advisories) on this
  repository:
  <https://github.com/htcom-code/go-lua-perf/security/advisories/new>, or
- email the maintainer at **htjulia1@gmail.com**.

Include a description, the affected version/commit, and a minimal reproducer
(a failing Go test is ideal). We'll acknowledge the report and work with you on a
fix and coordinated disclosure.

Vulnerabilities in the **Lua engine itself** (VM/compiler, chunk loading,
`string.pack`, sandbox escape at the language level) belong to
[lua-pure](https://github.com/htcom-code/lua-pure/security) — luart embeds it.

## Supported versions

luart is pre-1.0; fixes land on the active line, and the public API may change
between minor versions.

| Version | Supported |
|---|---|
| 0.0.x   | ✅ |

## Running untrusted Lua — threat model

luart can run untrusted Lua at scale (bytecode cache + per-script VM pool +
dynamic registry), but **the host owns the policy.** luart adds runtime controls
on top of the lua-pure engine; the engine-level caveats
([lua-pure SECURITY.md](https://github.com/htcom-code/lua-pure/blob/main/SECURITY.md))
still apply.

- **Sandbox the library set.** With `Config.Libs` unset, pooled States open the
  lua-pure 5.4 safe set (`base/table/string/math/utf8/coroutine`) and remove
  `load`/`loadfile`/`dofile` — no `io`, `os`, `debug`, or `package`. If you set
  `Config.Libs`, you choose exactly what opens; **do not add `io`/`os`/`debug`
  for untrusted scripts.** `SkipOpenLibs` opens nothing by default.
- **Bound CPU and wall-clock — two orthogonal limits.** `Config.ExecTimeout` is
  a per-execution wall-clock cap, and a cancelable `ctx` passed to `Run` is also
  honored; `Config.MaxInstructions` caps executed Lua opcodes per `Run`
  (returns `ErrInstructionLimit`). Both guard **pure-Lua** loops — they are
  checked between VM instructions. `0` disables each (zero overhead).
- **A blocking or spinning Go callback escapes those limits.** A callback blocked
  *inside* a Go call (channel receive, network read, syscall) — or spinning on
  pure Go CPU — is not interrupted until control returns to the VM, so it can pin
  the goroutine **and its pool slot** indefinitely. Make callbacks cancellable
  (read the `ctx` and `select` on `Done()`) and keep them short.
- **The memory budget caps pool size, not per-script allocation.**
  `Config.MemoryBudgetBytes` derives `MaxStates` (budget ÷ measured per-State
  cost) and drives idle-LRU eviction + backpressure at the cap. It bounds how
  many live States exist — it is **not** a per-script memory quota; a hostile
  script can still allocate heavily within a State. Run untrusted workloads under
  OS/container memory limits too.
- **Don't let a State's values escape its owner.** Pooled States are never shared
  across goroutines. `RunWith` hands you results **while the State is still
  owned** — read or call them inside the handler, but never let a reference
  `lua.Value` (table/function/userdata) escape to another goroutine or outlive
  the call. Materialize data you need to keep.
- **Cached bytecode is keyed by `key:version`.** A `SourceLoader` that returns a
  stale or attacker-controlled `version` for a changed script can serve stale
  compiled code from cache. Ensure your loader's version reflects content
  (e.g. `HashVersion`) so hot reload and cache invalidation are sound.

## Scope

In scope: luart runtime bugs with security impact — a pooled State leaking across
goroutines, an eviction/backpressure flaw, a cache-key collision serving wrong
bytecode, or `ExecTimeout`/`MaxInstructions` failing to stop a pure-Lua runaway.

Out of scope: resource exhaustion from a script run in a deliberately fully-opened
state (that's a configuration choice — sandbox it), and Lua language/VM issues
(report those to lua-pure).
