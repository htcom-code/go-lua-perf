[English](tuning.md) | **한국어**

# 튜닝 가이드 (`luart`)

워크로드에 맞춰 `luart.Config` 값을 **고르는 기준**과 아키텍처를 설명한다.
항목의 의미·기본값·로딩 방법은 [config.ko.md](config.ko.md), 전체 벤치 표는 [BENCHMARKS.md](BENCHMARKS.md) 참조 — 여기서는 **"어떤 값을 왜"** 에 집중한다.

## 1. 아키텍처 한눈에

`Runtime` 은 스크립트 키별로 프리로드된 `LState` 풀을 두고, 전역 상한 안에서 재사용·회수한다.

```
Run(ctx, key, fn, args)
  │
  ├─ getPool(key)           최초 1회만: loader.Load → CompileCache(key:version) → 풀 생성
  │                         (키별 sync.Once → 동시 첫 호출도 1회 컴파일)
  ├─ acquire(ctx)           ① idle 재사용(LIFO, 가장 따뜻한 캐시)
  │                         ② cap 미만 → newState + proto 프리로드(PCall 1회)
  │                         ③ cap 도달 → 전역에서 가장 오래된 idle State LRU eviction 후 생성
  │                         ④ 가용 idle 없음 → 슬롯 빌 때까지 백프레셔 대기(ctx 취소 가능)
  ├─ execute               [ExecTimeout/취소 ctx 있으면 SetContext] CallByParam → 결과 복사
  └─ release               SetTop(0) 후 idle 복귀 (drop/버전불일치/closed면 Close)

janitor(JanitorInterval 주기)   pool.lastUsed + IdleTTL 초과한 idle 풀을 통째 evict
전역 cap = MaxStates           (또는 MemoryBudgetBytes ÷ 측정 perState 로 파생)
```

- **proto 는 불변·공유**(`key:version` 캐시), `LState` 는 스크립트별 풀에서 재사용. 코드는 `manager.go` 의 `Run`/`acquire`/`release`/`janitor`.
- 핫 리로드는 외부 알림(`Notify`)으로 해당 풀만 drop → 다음 사용 시 재로드(in-flight 는 구버전으로 완료 후 폐기).

## 2. `MaxStates` vs `MemoryBudgetBytes` — 동시 VM 상한 정하기

전역에 동시에 살아있는 `LState` 총수를 제한하는 두 가지 방법이다(하나만 쓴다).

| 방식 | 언제 | 동작 |
|---|---|---|
| **`MaxStates > 0`** | 동시성 상한을 **직접 안다**(워커 수, 예상 동시 요청 등) | 그 값이 곧 전역 상한 |
| **`MaxStates == 0` + `MemoryBudgetBytes`** | **메모리로 묶고 싶다** | `New` 시점에 State 1개 평균 힙 비용(perState)을 8개 샘플로 측정해 `예산 ÷ perState` 로 산정(최소 1) |

- **산정 예**: 선택 라이브러리(base/table/string/math) 기준 perState ≈ **136.7 KiB** → **8 MiB 예산 ≈ 59 states**.

  | MemoryBudgetBytes | 대략 MaxStates (perState ≈137 KiB 가정) |
  |---|---|
  | 2 MiB | ~15 |
  | 8 MiB | ~59 |
  | 32 MiB | ~240 |

  실제 perState 는 `Libs` 구성에 따라 측정되므로, 라이브러리를 늘리면 같은 예산에서 state 수는 **자동으로 줄어든다**(과대산정 없음).
- **상한 도달 시**: 새 State 가 필요하면 전역에서 가장 오래된 **idle State 를 LRU eviction** 후 생성하고, 모두 사용 중이면 **백프레셔로 대기**(ctx 취소 시 즉시 에러). 즉 상한은 메모리를 지키되 요청을 떨구지 않고 줄세운다.
- **너무 낮으면** 백프레셔로 지연↑, **너무 높으면** 메모리↑. 시작점: 알면 동시 요청 수, 모르면 메모리 예산.

## 3. `IdleTTL` / `JanitorInterval` — 회수 공격성 정하기

janitor 가 `JanitorInterval` 마다 깨어나 `IdleTTL` 보다 오래 놀고 있는 풀을 통째로 닫는다.

- **짧은 IdleTTL** → 메모리 빨리 회수(유휴 스크립트가 많은 대량 환경 유리) / 대신 다음 사용 시 **콜드스타트**(NewState+프리로드 ≈ **74–93 µs**) 빈도↑.
- **긴 IdleTTL** → 웜 유지로 지연 안정 / 상주 메모리↑.
- `JanitorInterval` 은 회수 **반응속도 vs 스윕 비용** 트레이드오프. IdleTTL 보다 충분히 작게(기본 `IdleTTL=5m`, `JanitorInterval=30s`).
- 트래픽이 24/7 꾸준하면 길게(웜 유지), 산발적·버스트면 짧게(놀 때 회수).

## 4. `ExecTimeout` — 런어웨이 스크립트 방어

실행당 시간 하드캡. `> 0` 이면 각 `Run` 의 실행이 그 시간 내에 끝나도록 강제해 무한루프 등을 중단한다(`0` = 비활성, 오버헤드 0). 호출자가 넘긴 취소 가능 ctx 도 항상 적용된다.

- **순수 Lua 루프만** 중단 가능 — C 함수/네이티브 타이트 루프 안에는 중단 지점(opcode 경계)이 없어 즉시 멈추지 않을 수 있다.
- 신뢰할 수 없는/멀티테넌트 스크립트엔 **반드시** 설정. 신뢰 스크립트만 돌리면 0(무제한)도 무방.
- `MaxInstructions` 는 실행 opcode 수에 대한 직교적 상한(`ErrInstructionLimit` 반환) — wall-clock 없이도 CPU 폭주·타이트 루프를 막는 가드. [config.ko.md](config.ko.md) 참조.

## 5. `Libs` — 샌드박스 + 성능 동시 조정

풀 State 에 열 Lua 라이브러리 집합. 기본 `base/table/string/math/utf8/coroutine`(lua-pure 의 5.4 샌드박스 세트)로,
`os/io/package/debug` 는 **제외**되고 임의 코드·파일을 컴파일/실행하는 base 전역 `load/loadfile/dofile` 은 **제거**된다.

- 라이브러리를 적게 열수록 **빠르고(할당↓) 좁다(샌드박스)**: `Config.Libs` 를 기본 6종 대신 필요한 opener 만으로 지정한다. (빈 `Libs` 는 기본 집합으로 되돌아가므로, 원하는 최소 집합을 명시적으로 넘긴다.)
- 필요한 것만 추가하라. `os`/`io` 를 여는 순간 파일·프로세스 접근이 열려 샌드박스가 무너진다.

## 6. 워크로드별 프리셋

| 시나리오 | MaxStates / 예산 | IdleTTL | ExecTimeout | Libs |
|---|---|---|---|---|
| **소수 고정 스크립트 · 고QPS** | `MaxStates` 직접(동시 요청 수) | 길게(예: `1h`) — 웜 유지 | 0 또는 넉넉히 | 필요한 것만 |
| **대량 스크립트 · 산발 호출** | `MemoryBudgetBytes`(메모리로 묶기) | 짧게(예: `1m`) — 놀면 회수 | 워크로드별 | 최소 |
| **적대적 / 멀티테넌트** | `MaxStates` 보수적 | 짧게 | **반드시 설정**(짧게) | **최소**(base/string 등) |

## 7. 관측으로 조정하기

추측 대신 측정값으로 위 값을 조정한다.

| 신호(소스) | 의미 | 대응 |
|---|---|---|
| `acquire` 대기 시간↑ (`Config.Trace` 의 `acquire` 구간) | 백프레셔 — 상한이 부하에 비해 낮음 | `MaxStates`/예산↑ |
| evict 잦음 (`Metrics.OnEvict`) | 상한 대비 활성 스크립트 과다로 LRU 빈번 | 상한↑ 또는 활성 스크립트 수 점검 |
| 재컴파일 잦음 (`CompileCount()` 증가) | drop/리로드 또는 TTL 회수 후 재생성 빈번 | IdleTTL↑ 또는 리로드 빈도 점검 |
| `PoolStats()` 의 idle 과다 | 상주 메모리 낭비 | IdleTTL↓ 또는 상한↓ |

- 전역: `Stats()`(live/pools/max), 풀별: `PoolStats()`(idle/checkedOut/displayVersion), 컴파일: `CompileCount()`, 구간 비용: `Config.Trace TraceHook(stage,key,dur)`.
- 관측 인터페이스(`Metrics`/`Logger`/`Trace`)는 모두 opt-in — 미설정 시 핫패스 오버헤드 0.

---

> 참고: 값의 형식·기본값·JSON/YAML/env 로딩은 [config.ko.md](config.ko.md), 전체 벤치 표·방법론은 [BENCHMARKS.md](BENCHMARKS.md).
