[English](CHANGELOG.md) | **한국어**

# 변경 이력

이 프로젝트의 주요 변경 사항을 여기 기록한다.
형식은 [Keep a Changelog](https://keepachangelog.com/ko/1.1.0/)를 따르고,
버전은 [유의적 버전(SemVer)](https://semver.org/lang/ko/)을 따른다.

버전이 `0.x` 인 동안에는 마이너 릴리스 사이에 공개 API 가 바뀔 수 있다.

## [0.0.1] - 2026-07-05

**`luart`** 의 첫 공개 릴리스 — Go 에서 Lua 스크립트를 고성능·동시 안전하게 실행하기 위한
런타임으로, [lua-pure](https://github.com/htcom-code/lua-pure)(순수 Go PUC-Lua 5.4 이식)
위에 얹혀 있다. Go 1.24+ 필요. 코어 라이브러리는 lua-pure 외 의존성이 없고, 설정 로더의
YAML 의존성은 `luartconfig` 서브패키지에 격리돼 있다.

### 런타임 (Runtime)

- `New(loader SourceLoader, cfg Config) *Runtime` — 지연 로딩·풀링 런타임(백그라운드
  janitor 포함).
- `Run(ctx, key, entryFn, args…) ([]lua.Value, error)` — `key:version` 별로 한 번만
  컴파일하고, 스크립트별 풀에서 프리로드된 State 를 재사용한다.
- `RunWith(ctx, key, entryFn, handle, args…)` — State 를 아직 소유한 상태에서 결과를
  소비한다(`handle` 안에서는 어떤 반환 타입도 안전하게 읽거나 호출 가능).
- `RunValues(...) ([]any, error)` — 결과를 Go 값으로 깊은 복사해 호출 이후에도 쓸 수 있게
  한다. 데이터가 아닌 값은 `Config.ConvertValue` 훅을 거친다.
- 라이프사이클: `Close()`(즉시), `Shutdown(ctx)`(우아한 드레인).

### 캐싱 & 풀링

- `key:version` 별 바이트코드 캐시(`CompileCache`): 스크립트는 최초 사용 시에만 파싱·컴파일되고,
  이후 실행은 캐시 히트(사실상 0-alloc, 크기 무관)다.
- 스크립트별 Lua State 풀을 두고 호출 간 재사용한다. State 는 goroutine 간에 공유하지
  않으며, 부하 상황에서도 단일 소유자 불변식이 유지된다.

### 동적 레지스트리

- 최초 `Run` 에서 지연 로드, TTL 유휴 회수(`IdleTTL` / `JanitorInterval`).
- 메모리 예산 기반 `MaxStates` 산정(`MemoryBudgetBytes ÷ 측정된 State당 비용`)과 상한에서의
  전역 유휴-LRU 회수 및 FIFO backpressure(도착 순서, ctx 인지 — 손실 웨이크업이나 폴링 없음).
- 알림 기반 핫 리로드: `Notify(key, version, displayVersion)` / `NotifyChanges([]Change)`
  가 풀을 드롭하고, 다음 `Run` 이 다시 로드한다. 진행 중이던 State 는 옛 버전으로 끝난 뒤 폐기된다.

### 소스 & 설정

- `SourceLoader` 인터페이스 + `MapLoader`(인메모리), `HashVersion(src)`(내용 해시 버전).
  File / DB / Memory / 캐싱 / 라우팅 예제 로더는 `examples/custom-loaders` 에 있다
  (`docs/SourceLoader.md` 참조).
- `luartconfig` 서브패키지: `LoadJSON` / `LoadYAML` / `Load`(확장자 판별) / `FromEnv`,
  그리고 `Resolve`(우선순위 env > 파일/문자열 > 기본값), `Config.Validate()`.

### 실행 제한 & 안전

- `Config.ExecTimeout` — 실행당 wall-clock 하드캡. `Run` 에 넘긴 취소 가능 `ctx` 도 항상
  적용된다(`0` = 비활성, 오버헤드 0; 순수 Lua 루프 한정).
- `Config.MaxInstructions` — `Run` 당 opcode 상한으로 순수 Lua CPU 폭주를 막는다.
  `ExecTimeout`(wall-clock)과 직교적이며, 초과 시 `ErrInstructionLimit` 를 반환한다.
- `Config.IsolateGlobals` — 각 `Run` 을 새 `_ENV` 아래에서 실행해, 스크립트의 전역 쓰기가
  같은 풀 State 를 재사용하는 다음 호출로 새지 않게 한다.
- 풀 State 는 보호 모드로 실행된다: 패닉하는 Go 콜백은 잡을 수 있는 에러로 회수되고 State 는
  재사용 가능한 상태로 되돌아간다.

### 라이브러리

- 기본 라이브러리 집합은 lua-pure 의 안전한 Lua 5.4 부분집합
  (`base/table/string/math/utf8/coroutine`; `load`/`loadfile`/`dofile` 제거;
  `os`/`io`/`package`/`debug` 없음).
- `Config.Libs` 로 열 라이브러리를 정확히 지정하고, `Config.ExtraLibs` 로 샌드박스 기본값
  이후에 커스텀 라이브러리(Go 함수, 모듈 테이블, `L.Preload` 지연 `require`)를 추가한다.

### 관측 가능성 (opt-in, 미설정 시 오버헤드 0)

- `Metrics` 인터페이스(no-op 기본), `Logger` 인터페이스 + `NewSlogLogger`, 단계별
  `TraceHook`. 스냅샷은 `Stats()`, `PoolStats()`, `CompileCount()`.

### 툴링 & 문서

- 단일 출처인 `Makefile`(`make all` = vet → test → race → build); GitHub Actions CI 가 이를 미러링.
- `examples/`(공개 기능별 폴더), `cmd/` 기법 데모, `performance/`(lua-pure vs PUC-Lua 컴파일 비용 비교).
- README(en/ko), `docs/config.md`, `docs/tuning.md`, `docs/SourceLoader.md`(각각 한국어 `.ko.md`).

[0.0.1]: https://github.com/htcom-code/go-lua-perf/releases/tag/v0.0.1
