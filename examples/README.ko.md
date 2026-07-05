[English](README.md) | **한국어**

# 예제 (Examples)

소비자가 쓰듯 **`luart` 라이브러리를 직접 import 해서** 사용하는, 작고 독립적인 프로그램 모음 —
공개 기능 1개당 폴더 1개. 모두 실행 가능하고 테스트가 붙어 있다.

> [`cmd/`](../cmd/) 와는 다르다. `cmd/` 는 내부 동작 원리(바이트코드 캐싱·풀링 기법)를 인라인으로
> 재구현한 저수준 데모다. `examples/` 는 "라이브러리를 어떻게 쓰는가", `cmd/` 는 "라이브러리가 어떻게
> 동작하는가" 이다.

예제 실행:

```bash
go run ./examples/basics
```

각 폴더에 `main_test.go` 가 있어 `go test ./examples/...`(및 `make all`)로 함께 검증된다.

## 권장 읽는 순서

`basics` 부터 시작하고, 필요한 기능을 골라 본다.

| 예제 | 내용 | 핵심 API |
|---|---|---|
| [basics](basics/) | 스크립트 로드 → 인자 넣어 함수 실행 → 반환값 읽기 | `New`, `Run`, `NewMapLoader`/`Set`, `HashVersion` |
| [hot-reload](hot-reload/) | 외부 변경 시 drop-and-reload(재시작 없음) | `Notify` / `NotifyChanges` |
| [ttl-eviction](ttl-eviction/) | `IdleTTL` 초과 유휴 풀을 janitor가 회수 | `Config.IdleTTL`, `JanitorInterval`, `Stats` |
| [memory-budget](memory-budget/) | 메모리 예산으로 VM 상한(파생 `MaxStates` + LRU) | `Config.MemoryBudgetBytes` |
| [exec-timeout](exec-timeout/) | 하드캡/호출자 deadline으로 런어웨이 스크립트 중단 | `Config.ExecTimeout`, `Run(ctx, …)` |
| [graceful-shutdown](graceful-shutdown/) | in-flight 드레인 후 종료; 이후 `ErrClosed` | `Shutdown(ctx)` vs `Close()` |
| [config-loading](config-loading/) | JSON/YAML/env에서 우선순위로 `Config` 구성 | `luartconfig.ResolveJSONString` / `FromEnv` / `Load` |
| [metrics](metrics/) | 라이프사이클 이벤트 카운트(compile/build/reuse/…) | `Config.Metrics` |
| [logging](logging/) | 이벤트를 `log/slog` 로 라우팅 | `Config.Logger`, `NewSlogLogger` |
| [trace-profiling](trace-profiling/) | 요청 구간별 시간 측정(프로파일링) | `Config.Trace` (`TraceHook`) |
| [observability](observability/) | 대시보드용 읽기 전용 조회 | `Stats`, `PoolStats`, `CompileCount` |
| [sandbox-libs](sandbox-libs/) | 스크립트가 접근 가능한 Lua 표준 라이브러리 제어 | `Config.Libs` |
| [custom-libs](custom-libs/) | 사용자 작성 라이브러리(Go 함수 + 모듈 테이블)를 샌드박스 위에 추가 | `Config.ExtraLibs` |
| [custom-loaders](custom-loaders/) | File/DB/Memory + 캐싱·라우팅 백엔드로 `SourceLoader` 구현 | `SourceLoader`, `HashVersion` ([가이드](../docs/SourceLoader.ko.md)) |

전체 공개 API 개요는 [루트 README](../README.ko.md), 워크로드별 설정값 선택은
[튜닝 가이드](../docs/tuning.ko.md) 참조.
