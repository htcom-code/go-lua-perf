[English](config.md) | **한국어**

# 설정 가이드 (`luartconfig`)

`luart.Runtime` 의 동작값을 **JSON·YAML 파일** 또는 **환경변수**로 불러오는 방법과 항목을 설명한다.

> 어떤 값을 **왜** 고르는지(풀 크기·메모리 예산·TTL·실행 시간 제한의 워크로드별 기준)는 [튜닝 가이드](tuning.ko.md) 참조.

- 파일/환경변수로 설정 가능한 것은 **숫자·기간(duration) 필드뿐**이다.
- 나머지 필드는 **코드에서 주입**하는 값이라(로드 후 코드에서 설정) 파일/환경변수 대상이 아니다: `Libs`/`ExtraLibs`(로드할 Lua 라이브러리), `IsolateGlobals`, `MaxInstructions`(Run당 opcode 상한), `ConvertValue`, `Metrics`, `Logger`, `Trace`.
- 로더는 코어 `luart` 가 아니라 서브패키지 `luartconfig` 에 있다(YAML 의존성 격리).

## 설정 항목

| 필드 (`luart.Config`) | JSON/YAML 키 | 환경변수 (`<PREFIX>` + ) | 타입 | 기본값 | 설명 |
|---|---|---|---|---|---|
| `MaxStates` | `maxStates` | `MAX_STATES` | 정수 | `0` | 전역 최대 VM(State) 수. **`0` 이면** `MemoryBudgetBytes ÷ 측정 perState` 로 자동 산정(최소 1). |
| `MemoryBudgetBytes` | `memoryBudgetBytes` | `MEMORY_BUDGET_BYTES` | 정수(바이트) | `0` | 메모리 예산. `MaxStates` 가 `0` 일 때만 사용. 예: `8388608`(8MiB). |
| `IdleTTL` | `idleTTL` | `IDLE_TTL` | 기간 문자열 | `5m` | 이 시간 넘게 미사용인 스크립트 풀을 janitor 가 정리. |
| `JanitorInterval` | `janitorInterval` | `JANITOR_INTERVAL` | 기간 문자열 | `30s` | janitor 정리 주기. |
| `ExecTimeout` | `execTimeout` | `EXEC_TIMEOUT` | 기간 문자열 | `0`(무제한) | 실행당 시간 하드캡. `> 0` 이면 각 `Run` 의 실행이 이 시간 내에 끝나도록 강제(무한루프 등 런어웨이 스크립트 중단). `0` 이면 비활성(오버헤드 0). 호출자가 넘긴 취소 가능 ctx 는 이 값과 무관하게 항상 적용. |

- **기간 문자열**은 Go `time.ParseDuration` 형식: `"300ms"`, `"1.5s"`, `"30s"`, `"5m"`, `"1h"`.
- 비워 두거나(`""`) 0 이면 기본값이 적용된다(`luart.New` 시점).
- 검증: `MaxStates`·`IdleTTL`·`JanitorInterval`·`ExecTimeout` 은 음수일 수 없다(`Config.Validate()` 가 거부).
- `ExecTimeout` 은 **순수 Lua 루프**만 중단할 수 있다. C 함수/네이티브 타이트 루프 안에는 중단 지점(opcode 경계)이 없어 즉시 멈추지 않을 수 있다.

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

> 파일이 아니라 **JSON 문자열**(원격 저장소·플래그·테스트 등)에서 바로 읽으려면 `luartconfig.LoadJSONString(jsonStr)` 를 쓴다(JSON 전용).

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

> `luartconfig.Load("luart.yaml")` / `luartconfig.Load("luart.json")` 처럼 **확장자(.json/.yaml/.yml)로 자동 판별**도 가능하다.

## 환경변수

접두사(prefix)를 붙여 읽는다. 설정 안 한 변수는 기본값이 적용된다.
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

## 사용 예

```go
import (
	luart "github.com/htcom-code/go-lua-perf"
	"github.com/htcom-code/go-lua-perf/luartconfig"
)

cfg, err := luartconfig.Load("luart.yaml") // 또는 LoadJSON / FromEnv
if err != nil {
	log.Fatal(err)
}

// 파일/환경변수로 못 받는 값은 여기서 코드로 주입
cfg.Logger = luart.NewSlogLogger(slog.Default())
// cfg.Metrics = myMetrics
// cfg.Trace = myTraceHook
// cfg.Libs = []func(*lua.LState){(*lua.LState).OpenBase, (*lua.LState).OpenString} // 기본: base/table/string/math/utf8/coroutine

rt := luart.New(loader, cfg)
defer rt.Close()
```

## 우선순위 병합 (env > file > 기본값)

`Resolve` 는 **파일을 base 로 읽고 그 위에 환경변수를 필드 단위로 덮어쓴다.** 우선순위는 `env > file > 기본값`:

```go
// luart.yaml 을 base 로, LUART_* 환경변수가 있는 필드만 덮어씀
cfg, err := luartconfig.Resolve("luart.yaml", "LUART_")
```

- **필드 단위 병합**: 환경변수는 자신이 설정한 필드만 덮어쓴다. 예) 파일이 4개 필드를 모두 주고 `LUART_MAX_STATES` 만 설정돼 있으면 `MaxStates` 만 환경변수 값, 나머지는 파일 값.
- 어느 소스에서도 설정 안 된 필드는 그대로 두어 **`luart.New` 의 기본값**이 적용된다.
- `path` 를 `""` 로 주면 파일을 건너뛰어 `env > 기본값` 이 된다.

파일 대신 **JSON 문자열**을 base 로 쓰려면 `ResolveJSONString` — 우선순위가 `env > JSON 문자열 > 기본값` 으로 재구성된다:
```go
cfg, err := luartconfig.ResolveJSONString(`{"maxStates": 4, "idleTTL": "1m"}`, "LUART_")
// LUART_MAX_STATES=20 이면 → MaxStates=20(env), IdleTTL=1m(문자열)
```

> 한 소스만 필요하면 단일 로더(`LoadJSON` / `LoadJSONString` / `LoadYAML` / `Load` / `FromEnv`)를 그대로 쓰면 된다 — 이들은 병합 없이 그 소스 하나로 `Config` 를 만든다.

## 검증

`luartconfig.Load*` / `FromEnv` 는 내부에서 `Config.Validate()` 를 호출해 잘못된 값(음수, 파싱 불가 기간)을 오류로 돌려준다. 직접 만든 `Config` 도 `cfg.Validate()` 로 검사할 수 있다.
