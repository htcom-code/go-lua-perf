[English](README.md) | **한국어**

# go-lua-perf (luart)

[![Go Reference](https://pkg.go.dev/badge/github.com/htcom-code/go-lua-perf.svg)](https://pkg.go.dev/github.com/htcom-code/go-lua-perf)
[![CI](https://github.com/htcom-code/go-lua-perf/actions/workflows/ci.yml/badge.svg)](https://github.com/htcom-code/go-lua-perf/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/htcom-code/go-lua-perf)](https://goreportcard.com/report/github.com/htcom-code/go-lua-perf)
[![Go 1.24+](https://img.shields.io/badge/go-1.24%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/dl/)
[![Lua 5.4](https://img.shields.io/badge/Lua-5.4-000080.svg)](https://www.lua.org/)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**`luart`** 는 Go 에서 Lua 스크립트를 **고성능·동시 안전**하게 실행한다 — `key:version` 별
바이트코드 캐시, 스크립트별 VM 풀링, 스스로 관리하는 동적 레지스트리를,
[lua-pure](https://github.com/htcom-code/lua-pure)(순수 Go PUC-Lua 5.4 엔진) 위에 얹었다.

- 모듈 `github.com/htcom-code/go-lua-perf` · Go 1.24+ · lua-pure v0.1.1 (Lua 5.4) · **v0.0.1**
- 코어 라이브러리는 lua-pure 외 의존성이 없다(설정 로더의 YAML 의존성은 `luartconfig` 에 격리).

## 특징 (Features)

- **바이트코드 캐시 — `key:version` 당 1회 컴파일.** 스크립트는 첫 실행에만 파싱·컴파일되고, 이후 모든 실행은 0-alloc 캐시 히트(소스 크기 무관).
- **동시성 안전 VM 풀링.** 스크립트마다 전용 Lua State 풀을 두고 goroutine 간 공유하지 않아, 수천 개의 동시 `Run` 이 예열된 VM 을 재사용한다.
- **스스로 관리하는 레지스트리 — 지연 로드·TTL/메모리 예산 회수·핫 리로드.** 온디맨드 로드, 유휴 State 회수(메모리 상한 하 TTL+LRU), 상한 backpressure, 알림 기반 재시작 없는 핫 리로드.
- **플러그인 `SourceLoader` + 메트릭/로깅/트레이싱.** 파일·DB·인메모리·캐싱·라우팅 등 임의 백엔드에서 스크립트 로드; `Metrics`·`Logger`·단계별 `TraceHook` 를 필요할 때만(0 비용) 켠다.
- **샌드박스 Lua 5.4 + 실행시간·명령어 상한.** 안전한 기본 라이브러리 집합(`Config.Libs` 로 커스터마이즈), 비신뢰 스크립트용 `ExecTimeout`(wall-clock)·`MaxInstructions`(opcode) 상한.

## 설치

```bash
go get github.com/htcom-code/go-lua-perf
```

라이브러리는 모듈 루트에 있고 패키지명은 `luart` 이므로 alias 로 import 한다:

```go
import luart "github.com/htcom-code/go-lua-perf"
```

public 모듈이라 `go get` 이 모듈 프록시로 바로 동작한다. API 는 `0.x` 라 마이너 버전 간 변경될 수 있다.

## 빠른 시작

```go
import (
	"context"
	"fmt"

	lua "github.com/htcom-code/lua-pure/lua"
	luart "github.com/htcom-code/go-lua-perf"
)

loader := luart.NewMapLoader() // SourceLoader (여기에 캐시/DB 를 끼운다)
src := `function greet(name) return "hello, " .. name end`
loader.Set("greeter", src, luart.HashVersion(src), "1.0.0")

rt := luart.New(loader, luart.Config{MaxStates: 4})
defer rt.Close()

out, _ := rt.Run(context.Background(), "greeter", "greet", lua.LString("luart"))
fmt.Println(out[0].String()) // hello, luart
```

## 사용법

런타임 표면은 작다: `New` 로 만들고, `Run` / `RunValues` / `RunWith` 로 실행하고,
`Notify` 로 리로드하고, `Close` / `Shutdown` 으로 멈춘다. 스크립트 소스는 직접 구현한
`SourceLoader` 로 가져온다.

- **`Run`** — 가장 빠른 경로; 반환값은 동기적으로만 읽는다.
- **`RunValues`** — 결과를 Go 값으로 깊은 복사(호출 이후에도 안전).
- **`RunWith`** — State 를 소유한 상태에서 핸들러 안에서 결과 소비.

기능별 폴더로 나뉜 실행형·테스트 포함 예제(핫 리로드·TTL·메모리 예산·실행 제한·우아한 종료·
설정 로딩·메트릭·로깅·트레이싱·샌드박스·커스텀 라이브러리·커스텀 로더)는
**[examples/](examples/)** 에 있다:

```bash
go run ./examples/basics
```

전체 API 레퍼런스: **[pkg.go.dev](https://pkg.go.dev/github.com/htcom-code/go-lua-perf)** (또는 `make doc`).

## 설정

`luart.Config` 를 코드로 설정하거나, 숫자/duration 필드는 `luartconfig` 서브패키지로
JSON/YAML/env 에서 로드한다. 주요 항목:

| 관심사 | 필드 |
|---|---|
| 동시성 상한 | `MaxStates`, 또는 `MemoryBudgetBytes`(상한 산정) |
| 유휴 회수 | `IdleTTL`, `JanitorInterval` |
| 폭주 가드 | `ExecTimeout`(wall-clock), `MaxInstructions`(opcode) |
| 샌드박스/라이브러리 | `Libs`, `ExtraLibs`, `IsolateGlobals` |
| 관측 가능성 | `Metrics`, `Logger`, `Trace` |

- 필드 레퍼런스·로딩(JSON/YAML/env, 우선순위): **[docs/config.ko.md](docs/config.ko.md)**
- 워크로드별 값 선택: **[docs/tuning.ko.md](docs/tuning.ko.md)**
- `SourceLoader` 구현: **[docs/SourceLoader.ko.md](docs/SourceLoader.ko.md)**

## 성능

- **VM 풀 재사용 ≈ 870×** — 매 호출마다 새 State 생성 대비(~258 ns vs ~225 µs), **컴파일 캐시 히트는 0-alloc**.
- **캐시 히트 실행은 사실상 크기 무관**(~550 ns): 1회 컴파일은 `key:version` 당 한 번만 내고 모든 실행에 걸쳐 상각된다.
- **컴파일 비용은 PUC-Lua(C) 대비 ~1–2× 시간 / ~2× 메모리** 수준(lua-pure 가 순수 Go 로 이식).

전체 방법론·벤치별 표: **[docs/BENCHMARKS.md](docs/BENCHMARKS.md)**. 직접 재현은 `make bench`, `go run ./performance`.

## 문서

- **API 레퍼런스** — [pkg.go.dev](https://pkg.go.dev/github.com/htcom-code/go-lua-perf) (로컬은 `make doc` / `make doc-web`)
- **가이드** — [config](docs/config.ko.md) · [tuning](docs/tuning.ko.md) · [SourceLoader](docs/SourceLoader.ko.md) · [benchmarks](docs/BENCHMARKS.md)
- **변경 이력** — [CHANGELOG.ko.md](CHANGELOG.ko.md)

## 상태 & 로드맵

`luart` 은 **v0.0.1**(`0.x`) — 사용 가능하지만 마이너 버전 간 API 가 바뀔 수 있다.
방향·비목표는 **[ROADMAP.md](ROADMAP.md)** 참조.

## 기여

기여를 환영한다 — 빌드/테스트 규율(`make all` 게이트, 파일별 테스트, 벤치 가드레일)과
아키텍처 지도는 **[CONTRIBUTING.md](CONTRIBUTING.md)** 참조. 버그 리포트·기능 요청은 이슈
템플릿을 쓴다. Lua **언어** 이슈는 [lua-pure](https://github.com/htcom-code/lua-pure) 엔진 소관이다.

## 보안

luart 은 비신뢰 Lua 를 대량 실행할 수 있지만, 샌드박싱·자원 제한 정책은 호스트가 책임진다.
위협 모델과 취약점 비공개 보고 방법은 **[SECURITY.md](SECURITY.md)** 참조.

## 라이선스

[MIT](LICENSE) © 2026 htjulia
