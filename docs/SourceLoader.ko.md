[English](SourceLoader.md) | **한국어**

# 소스 로딩 (`SourceLoader`)

`SourceLoader`는 `luart`를 쓰기 위해 사용 개발자가 직접 구현하는 단 하나의 인터페이스로,
런타임에게 **스크립트 소스를 어디서 가져올지**(파일 트리·DB·캐시·서비스)를 알려준다.
`New(loader, cfg)`가 이 인터페이스를 받고, 런타임은 스크립트가 처음 사용될 때 `Load`를 지연
호출한다(키당 1회, 동시 접근에도 1회 보장).

런타임은 소스 문자열을 보유하지 않는다 — 컴파일 시점에 한 번 읽고 버린다. **소스 상주는 로더의
책임이다.** 내장 `NewMapLoader`는 모든 소스를 영구 메모리 상주시키므로(테스트·데모용으로 적합)
운영용 로더는 필요 시점에 가져와서 미사용 소스가 메모리에 남지 않게 해야 한다. [루트
README](../README.md)의 *메모리 모델* 노트 참조.

## 1. 계약

메서드 하나를 구현한다. 헬퍼 하나는 선택이다.

| 메서드 / 헬퍼 | 시그니처 | 반환 | 규칙 |
|---|---|---|---|
| `SourceLoader.Load` (직접 구현) | `Load(key string)` | `src, version, displayVersion string, err error` | 스크립트 본문·엔진 `version`·사람용 라벨을 반환하거나, 알 수 없는 `key`엔 **non-nil 에러** 반환. **goroutine-safe** 필수. |
| `HashVersion` (헬퍼) | `HashVersion(src string)` | `string` | `src`의 sha256-hex. 백엔드가 버전을 주지 않을 때 `version` 생성에 사용. |

**`version` vs `displayVersion`:**

| 필드 | 역할 | 효과 |
|---|---|---|
| `version` | 엔진 변경 키(콘텐츠 해시 권장) | 컴파일 캐시 키(`key:version`)이자 핫 리로드 동력: `Notify`에 **바뀐** 버전이 오면 풀 drop, **같은** 버전은 멱등(재컴파일·drop 없음). 소스가 바뀌면 *반드시* 바뀌어야 한다. |
| `displayVersion` | 사람용 라벨(예: `"1.0.0"`) | 표시용 — `PoolStats`·로그에 노출. 라벨만 바뀌면 리로드 없이 라벨만 갱신. 비어 있어도 되며, 런타임이 버전 해시 접두로 폴백한다. |

**지켜야 할 규칙:**
- 알 수 없는 키엔 **빈 문자열이 아니라 에러를 반환**한다(예: `fmt.Errorf("luart: script %q not found", key)`).
- `Load`는 **런타임 잠금 밖**에서 실행되므로 블로킹 I/O(디스크·DB·네트워크)도 괜찮다.
- 동일 소스 ⇒ 동일 `version` ⇒ 멱등. `version`은 콘텐츠에서 결정적으로 도출한다(또는 백엔드가 콘텐츠 해시를 저장).

아래 예제는 [`examples/custom-loaders/`](../examples/custom-loaders/)의 검증된 소스다 — `go run ./examples/custom-loaders`로 실행한다.

## 2. 파일 기반 로더

`<dir>/<key>.lua`를 읽는다. 콘텐츠 해시가 버전이므로, 파일을 수정하고 새 해시로 알리면 리로드가
트리거된다.

```go
type FileLoader struct{ dir string }

func NewFileLoader(dir string) *FileLoader { return &FileLoader{dir: dir} }

func (l *FileLoader) Load(key string) (src, version, displayVersion string, err error) {
	b, err := os.ReadFile(filepath.Join(l.dir, key+".lua"))
	if err != nil {
		return "", "", "", fmt.Errorf("luart: script %q not found: %w", key, err)
	}
	src = string(b)
	return src, luart.HashVersion(src), "", nil
}
```

## 3. DB 기반 로더

표준 `database/sql` 패키지를 쓰고 **특정 드라이버를 import 하지 않으므로**, 백엔드(Postgres·MySQL·SQLite
…)는 사용자가 고른다 — 드라이버로 `*sql.DB`를 열어 넘기면 된다. `sql.ErrNoRows`는 not-found 에러로
매핑한다.

```go
type DBLoader struct {
	db    *sql.DB
	query string
}

func NewDBLoader(db *sql.DB) *DBLoader {
	return &DBLoader{
		db:    db,
		query: `SELECT src, version, display FROM scripts WHERE key = ?`,
	}
}

func (l *DBLoader) Load(key string) (src, version, displayVersion string, err error) {
	var display sql.NullString
	err = l.db.QueryRow(l.query, key).Scan(&src, &version, &display)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", fmt.Errorf("luart: script %q not found", key)
	}
	if err != nil {
		return "", "", "", fmt.Errorf("luart: load %q: %w", key, err)
	}
	return src, version, display.String, nil
}
```

예상 스키마(writer가 `version`에 콘텐츠 해시를 넣는다):

```sql
CREATE TABLE scripts (
    key     TEXT PRIMARY KEY,
    src     TEXT NOT NULL,
    version TEXT NOT NULL, -- engine version: a content hash the writer sets
    display TEXT           -- human label, e.g. "1.0.0" (may be empty)
);
```

> 예제의 `DBLoader`는 컴파일 검증만 되고 테스트에서 **실행되지 않는다** — 실행하려면 구체 SQL
> 드라이버 import(새 모듈 의존성)가 필요하기 때문이다. 실제 서비스에서는 진짜 `*sql.DB`를 열어
> `NewDBLoader`에 넘긴다.

## 4. 인메모리 로더

`RWMutex`로 보호되는 `map`. 내장 `luart.MapLoader`와 같은 형태다 — 테스트·데모엔 `MapLoader`를
쓰고, 시작 시 설정에서 시드하고 싶을 때 직접 작성한다. `MapLoader`처럼 모든 소스를 상주시키므로
대규모 카탈로그가 아니라 작고 고정된 스크립트 집합에 적합하다.

```go
type MemoryLoader struct {
	mu   sync.RWMutex
	recs map[string]memRec
}

type memRec struct{ src, version, displayVersion string }

func NewMemoryLoader() *MemoryLoader {
	return &MemoryLoader{recs: make(map[string]memRec)}
}

func (l *MemoryLoader) Set(key, src, displayVersion string) {
	l.mu.Lock()
	l.recs[key] = memRec{src: src, version: luart.HashVersion(src), displayVersion: displayVersion}
	l.mu.Unlock()
}

func (l *MemoryLoader) Load(key string) (src, version, displayVersion string, err error) {
	l.mu.RLock()
	rec, ok := l.recs[key]
	l.mu.RUnlock()
	if !ok {
		return "", "", "", fmt.Errorf("luart: script %q not found", key)
	}
	return rec.src, rec.version, rec.displayVersion, nil
}
```

## 5. 캐싱 래퍼 (혼합)

임의의 로더를 인메모리 캐시로 감싼다 — 키의 첫 `Load`는 백엔드(파일·DB·네트워크)를 치고, 이후
`Load`는 메모리에서 제공된다. 느린 백엔드를 지연 상태로 두면서 상주를 실제 사용된 키로 한정한다 —
모든 걸 들고 있는 `MapLoader`와 다른 점이다.

```go
type SourceLoader interface {
	Load(key string) (src, version, displayVersion string, err error)
}

type CachingLoader struct {
	backend SourceLoader
	mu      sync.RWMutex
	cache   map[string]cacheRec
}

type cacheRec struct{ src, version, displayVersion string }

func NewCachingLoader(backend SourceLoader) *CachingLoader {
	return &CachingLoader{backend: backend, cache: make(map[string]cacheRec)}
}

func (l *CachingLoader) Load(key string) (src, version, displayVersion string, err error) {
	l.mu.RLock()
	rec, ok := l.cache[key]
	l.mu.RUnlock()
	if ok {
		return rec.src, rec.version, rec.displayVersion, nil
	}

	src, version, displayVersion, err = l.backend.Load(key)
	if err != nil {
		return "", "", "", err // 에러는 캐시하지 않음 → 일시적 실패는 재시도 가능
	}
	l.mu.Lock()
	l.cache[key] = cacheRec{src: src, version: version, displayVersion: displayVersion}
	l.mu.Unlock()
	return src, version, displayVersion, nil
}

func (l *CachingLoader) Invalidate(key string) {
	l.mu.Lock()
	delete(l.cache, key)
	l.mu.Unlock()
}
```

> **무효화는 핫 리로드와 짝을 이룬다.** 런타임은 drop 이후에만 `Load`를 다시 호출하므로, 캐시
> 엔트리는 저절로 재페치되지 않는다. 기반 소스가 바뀌면 `Notify` **전에** 반드시 `Invalidate(key)`를
> 호출해야 한다. 그러지 않으면 drop 후 리로드가 캐시의 낡은 소스를 그대로 돌려준다.

## 6. 라우팅 로더 (혼합)

키 접두어로 분배해 하나의 런타임이 소스를 혼합한다: `file:checkout`은 디스크, `db:pricing`은 DB,
`mem:healthcheck`은 메모리에서 읽는다. 접두어는 `Run`에 넘기는 키의 일부라 선택된 백엔드가 명시적이다.
백엔드는 접두어를 **뺀** 키를 받는다.

```go
type RoutingLoader struct {
	routes map[string]SourceLoader // prefix (without ':') → backend
}

func NewRoutingLoader(routes map[string]SourceLoader) *RoutingLoader {
	return &RoutingLoader{routes: routes}
}

func (l *RoutingLoader) Load(key string) (src, version, displayVersion string, err error) {
	prefix, rest, ok := strings.Cut(key, ":")
	if !ok {
		return "", "", "", fmt.Errorf("luart: key %q has no backend prefix (want \"<prefix>:<key>\")", key)
	}
	backend, ok := l.routes[prefix]
	if !ok {
		return "", "", "", fmt.Errorf("luart: no backend for prefix %q in key %q", prefix, key)
	}
	return backend.Load(rest)
}
```

## 7. 배선 & 핫 리로드

로더를 조합해 가장 바깥 것을 `New`에 넘긴다. 외부 변경 시 백엔드를 갱신하고, 캐시가 있으면
무효화한 뒤, 새 버전으로 `Notify`하면 풀이 drop 되어 다음 `Run`에서 리로드된다.

```go
cachedFile := NewCachingLoader(NewFileLoader(dir))
mem := NewMemoryLoader()
mem.Set("healthcheck", `function status() return "ok" end`, "1.0.0")

router := NewRoutingLoader(map[string]SourceLoader{
	"file": cachedFile,
	"mem":  mem,
})

rt := luart.New(router, luart.Config{MaxStates: 4})
defer rt.Close()

out, _ := rt.Run(ctx, "file:checkout", "discount", lua.LNumber(100))

// 파일 기반 스크립트의 외부 변경:
os.WriteFile(filepath.Join(dir, "checkout.lua"), []byte(checkoutV2), 0o644)
cachedFile.Invalidate("checkout") // 백엔드 로컬 키(접두어 없음)
rt.Notify("file:checkout", luart.HashVersion(checkoutV2), "2.0.0")

out, _ = rt.Run(ctx, "file:checkout", "discount", lua.LNumber(100)) // 새 버전
```

전체 실행 가능한 프로그램은 [`examples/custom-loaders/`](../examples/custom-loaders/),
리로드만 집중한 데모는 [`examples/hot-reload/`](../examples/hot-reload/) 참조.

---

> 함께 보기: [루트 README](../README.md)의 공개 API 개요; 설정 항목 형식은 [config.ko.md](config.ko.md);
> 풀 크기·예산·TTL 선택은 [튜닝 가이드](tuning.ko.md).
