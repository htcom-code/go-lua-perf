package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	lua "github.com/htcom-code/lua-pure/lua"
)

// TestGenScript_Basic 는 genScript 가 요청한 개수만큼 함수를 내고 그 결과가
// lua-pure 로 정상 컴파일·실행되며 fI() == I 임을 검증한다.
//
// Since: 2026-06-19
func TestGenScript_Basic(t *testing.T) {
	const n = 500
	src := genScript(n)
	if got := strings.Count(src, "function f"); got != n {
		t.Fatalf("function count: got %d, want %d", got, n)
	}
	L := lua.NewState()
	defer L.Close()
	if _, err := L.DoString(src, "gen"); err != nil {
		t.Fatalf("DoString: %v", err)
	}
	for _, i := range []int{1, n / 2, n} {
		rets, err := L.Call(L.GetGlobal("f"+itoa(i)), nil, 1)
		if err != nil {
			t.Fatalf("call f%d: %v", i, err)
		}
		if got := int(rets[0].AsInt()); got != i {
			t.Fatalf("f%d(): got %d, want %d", i, got, i)
		}
	}
}

// TestGenScript_Empty 는 경계 케이스 — 함수 0개면 빈 소스를 내는지 확인한다.
//
// Since: 2026-06-19
func TestGenScript_Empty(t *testing.T) {
	if src := genScript(0); src != "" {
		t.Fatalf("genScript(0): got %q, want empty", src)
	}
}

// TestMeasureGo_Sane 는 measureGo 가 작은 스크립트에 대해 양수의
// 컴파일 시간·상주 메모리를 ok=true 로 돌려주는지 확인한다.
//
// Since: 2026-06-19
func TestMeasureGo_Sane(t *testing.T) {
	r := measureGo([]byte(genScript(1_000)), 2)
	if !r.ok {
		t.Fatal("measureGo: ok=false")
	}
	if r.compileMs <= 0 || r.retainMB <= 0 {
		t.Fatalf("measureGo: nonsensical result %+v", r)
	}
}

// TestMeasurePUC_AgreesOrSkips 는 lua 바이너리가 있으면 measure.lua 가 두 수치를
// 파싱 가능한 형태로 내는지 확인하고, 없으면 건너뛴다(예외/환경 경계).
//
// Since: 2026-06-19
func TestMeasurePUC_AgreesOrSkips(t *testing.T) {
	luaBin, err := exec.LookPath("lua")
	if err != nil {
		t.Skip("lua binary not on PATH; skipping PUC-Lua check")
	}
	// 컴파일 가능한 소스인지 사전 점검(파서가 같은 바이트를 받는다는 전제).
	if _, err := lua.CompileReader(bytes.NewReader([]byte(genScript(100))), "t"); err != nil {
		t.Fatalf("compile: %v", err)
	}
	dir := mustScriptDir(t)
	path := t.TempDir() + "/small.lua" // temp dir so we don't touch the committed scripts/
	if err := ensureScript(path, 100, false); err != nil {
		t.Fatalf("ensureScript: %v", err)
	}
	r := measurePUC(luaBin, dir+"/measure.lua", path, 2)
	if !r.ok {
		t.Fatal("measurePUC: ok=false despite lua present")
	}
	if r.compileMs < 0 || r.retainMB < 0 {
		t.Fatalf("measurePUC: negative result %+v", r)
	}
}

func mustScriptDir(t *testing.T) string {
	t.Helper()
	d, err := scriptDir()
	if err != nil {
		t.Fatalf("scriptDir: %v", err)
	}
	return d
}

// itoa is a tiny helper to avoid importing strconv just for the test.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}
