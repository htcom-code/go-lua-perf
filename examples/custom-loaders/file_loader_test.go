package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	luart "github.com/htcom-code/go-lua-perf"
)

// writeLargeScript 은 nfuncs 개의 작은 전역 함수(fI() == I)로 이루어진 "큰"
// Lua 스크립트를 dir/<name>.lua 에 쓰고, 그 절대 경로와 바이트 크기를 돌려준다.
// 함수가 fI 라는 이름으로 전역에 등록되므로 rt.Run(key, "fI") 로 임의의 함수를
// 직접 호출해 결과(== I)를 결정적으로 검증할 수 있다.
//
// 왜 "단일 거대 테이블/청크"가 아니라 "많은 함수"인가: PUC-Lua(및 그 순수 Go
// 이식인 lua-pure)는 한 함수/청크 단위에 상수·지역변수 수, 테이블 생성자 필드
// 수 등의 상한이 있어 하나의 거대한 함수/테이블로는 큰 파일을 만들 수 없다.
// 본문이 작은 함수를 많이 두면 진짜로 큰 "정상" 스크립트에 가깝고, 컴파일 비용도
// 총 구문 수에 거의 선형으로 늘어(컴파일러가 선형) 파일 크기 대비 측정이
// 의미를 갖는다.
//
// Since: 2026-06-19
func writeLargeScript(t testing.TB, dir, name string, nfuncs int) (path string, size int64) {
	t.Helper()
	var b strings.Builder
	b.Grow(nfuncs * 34) // 대략적인 사전 할당으로 빌더 재할당 감소
	for i := 1; i <= nfuncs; i++ {
		fmt.Fprintf(&b, "function f%d() return %d end\n", i, i)
	}
	path = filepath.Join(dir, name+".lua")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write large script: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat large script: %v", err)
	}
	return path, fi.Size()
}

// callF 는 큰 스크립트 안의 fI 함수를 실행해 반환값을 정수로 돌려준다.
//
// Since: 2026-06-19
func callF(t testing.TB, rt *luart.Runtime, key string, i int) int {
	t.Helper()
	out, err := rt.Run(context.Background(), key, fmt.Sprintf("f%d", i))
	if err != nil {
		t.Fatalf("Run f%d: %v", i, err)
	}
	return int(out[0].AsInt())
}

// TestFileLoader_LargeFile 는 큰 스크립트 파일이 FileLoader 를 통해 읽혀
// 정상적으로 컴파일·실행되는지 검증한다. 처음(첫째)·중간·마지막 함수를 모두
// 호출해 파일 전체가 빠짐없이 컴파일됐는지 확인한다.
//
// Since: 2026-06-19
func TestFileLoader_LargeFile(t *testing.T) {
	const nfuncs = 8_000 // 약 256 KB 규모의 소스
	dir := t.TempDir()
	_, size := writeLargeScript(t, dir, "big", nfuncs)
	t.Logf("generated script: %d KiB (%d functions)", size/1024, nfuncs)

	loader := NewFileLoader(dir)

	// 계약 점검: Load 가 소스·버전을 반환하고 버전이 내용 해시와 일치하는지.
	src, version, _, err := loader.Load("big")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if int64(len(src)) != size {
		t.Fatalf("loaded size: got %d, want %d", len(src), size)
	}
	if want := luart.HashVersion(src); version != want {
		t.Fatalf("version: got %q, want content hash %q", version, want)
	}

	rt := luart.New(loader, luart.Config{MaxStates: 2})
	defer rt.Close()

	// 파일 전체가 로드·컴파일됐는지: 처음·중간·마지막 함수를 호출.
	for _, i := range []int{1, nfuncs / 2, nfuncs} {
		if got := callF(t, rt, "big", i); got != i {
			t.Fatalf("f%d(): got %d, want %d", i, got, i)
		}
	}
}

// TestFileLoader_LargeFile_Memory 는 큰 파일 한 개를 로드→컴파일→실행하는 데
// 드는 메모리 사용량(힙 증가분·총 할당량·malloc 횟수)을 측정해 로그로 남긴다.
// 컴파일이 무거워(super-linear) 느리므로 -short 에서는 건너뛴다.
//
// Since: 2026-06-19
func TestFileLoader_LargeFile_Memory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-file memory test in -short mode")
	}
	const nfuncs = 30_000 // 약 1 MB 규모의 소스
	dir := t.TempDir()
	_, size := writeLargeScript(t, dir, "big", nfuncs)
	t.Logf("generated script: %d KiB (%d functions)", size/1024, nfuncs)

	loader := NewFileLoader(dir)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	rt := luart.New(loader, luart.Config{MaxStates: 2})
	defer rt.Close()
	if got := callF(t, rt, "big", nfuncs); got != nfuncs { // 로드+컴파일+실행
		t.Fatalf("f%d(): got %d, want %d", nfuncs, got, nfuncs)
	}

	runtime.ReadMemStats(&after)
	t.Logf("memory for load+compile+run of %d KiB: heap +%.2f MiB, total alloc +%.2f MiB, mallocs +%d",
		size/1024,
		float64(after.HeapAlloc-before.HeapAlloc)/(1<<20),
		float64(after.TotalAlloc-before.TotalAlloc)/(1<<20),
		after.Mallocs-before.Mallocs)
}

// TestFileLoader_LargeFile_Missing 는 큰 파일 시나리오의 예외 경계 —
// 존재하지 않는 키를 빈 소스가 아니라 에러로 보고하는지 검증한다.
//
// Since: 2026-06-19
func TestFileLoader_LargeFile_Missing(t *testing.T) {
	loader := NewFileLoader(t.TempDir())
	if _, _, _, err := loader.Load("does-not-exist"); err == nil {
		t.Fatal("Load(missing): expected error, got nil")
	}
}

// largeFileSizes 는 벤치마크에서 쓰는 스크립트 규모(함수 개수)의 표.
// CompileRun 벤치가 매 반복 컴파일하므로 컴파일 시간이 과하지 않도록 상한을 둔다.
var largeFileSizes = []struct {
	name   string
	nfuncs int
}{
	{"64KB", 2_000},
	{"256KB", 8_000},
	{"512KB", 16_000},
}

// BenchmarkFileLoader_Load 는 디스크에서 큰 파일을 읽고 내용 해시를 계산하는
// FileLoader.Load 한 번의 비용(시간·할당)을 규모별로 측정한다. b.SetBytes 로
// 처리량(MB/s)도 함께 보고된다.
//
// Since: 2026-06-19
func BenchmarkFileLoader_Load(b *testing.B) {
	for _, s := range largeFileSizes {
		b.Run(s.name, func(b *testing.B) {
			dir := b.TempDir()
			_, size := writeLargeScript(b, dir, "big", s.nfuncs)
			loader := NewFileLoader(dir)
			b.SetBytes(size)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, _, err := loader.Load("big"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkFileLoader_CompileRun 은 큰 파일을 읽어 파싱·컴파일하고 처음
// 실행하기까지의 전체 비용을 측정한다 — 큰 파일의 비용이 컴파일에 집중됨을
// 보인다. CompileCache 가 키별로 한 번만 컴파일하므로, 컴파일 비용을 매 반복에
// 포함시키려고 Runtime 을 반복마다 새로 만든다(생성·정리는 타이머에서 제외).
//
// Since: 2026-06-19
func BenchmarkFileLoader_CompileRun(b *testing.B) {
	for _, s := range largeFileSizes {
		b.Run(s.name, func(b *testing.B) {
			dir := b.TempDir()
			_, size := writeLargeScript(b, dir, "big", s.nfuncs)
			loader := NewFileLoader(dir)
			ctx := context.Background()
			b.SetBytes(size)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				rt := luart.New(loader, luart.Config{MaxStates: 1})
				b.StartTimer()
				if _, err := rt.Run(ctx, "big", "f1"); err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
				rt.Close()
				b.StartTimer()
			}
		})
	}
}

// BenchmarkFileLoader_RunCached 는 컴파일 캐시·VM 풀이 덥혀진(warm) 뒤의 반복
// 실행 비용을 측정한다 — 큰 파일이라도 캐시 적중 후에는 파싱·컴파일 비용이
// 사라지고 실행만 남아, 파일 크기와 무관하게 일정함을 보인다.
//
// Since: 2026-06-19
func BenchmarkFileLoader_RunCached(b *testing.B) {
	for _, s := range largeFileSizes {
		b.Run(s.name, func(b *testing.B) {
			dir := b.TempDir()
			writeLargeScript(b, dir, "big", s.nfuncs)
			rt := luart.New(NewFileLoader(dir), luart.Config{MaxStates: 4})
			defer rt.Close()
			ctx := context.Background()
			if _, err := rt.Run(ctx, "big", "f1"); err != nil { // warm-up
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := rt.Run(ctx, "big", "f1"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
