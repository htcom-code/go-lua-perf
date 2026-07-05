package luart

// Unit tests for convert.go — RunValues deep-copy conversion (scalars, sequence
// vs map tables, nesting, non-data delegation, recursion guard).
// Convention: basic behavior + exception cases (CONTRIBUTING.md).

import (
	"context"
	"reflect"
	"testing"
	"time"

	lua "github.com/htcom-code/lua-pure/lua"
)

const convertSrc = `
	function scalars() return 7, "hi", true, nil, 2.5 end
	function seq() return {"a", "b", "c"} end
	function nested() return { n = 1, f = 2.5, s = "x", ok = true, list = {10, 20, 30}, sub = { y = "z" } } end
	function fn() return function() return 1 end end
	function cyclic() local t = {}; t.self = t; return t end`

func newConvertRuntime(t *testing.T, cfg Config) *Runtime {
	t.Helper()
	loader := NewMapLoader()
	loader.Set("k", convertSrc, "v1", "")
	cfg.IdleTTL = time.Hour
	cfg.JanitorInterval = time.Hour
	if cfg.MaxStates == 0 {
		cfg.MaxStates = 2
	}
	rt := New(loader, cfg)
	t.Cleanup(rt.Close)
	return rt
}

// TestRunValuesScalars verifies scalar/number/nil conversion and the int64 vs
// float64 split.
// Since: 2026-06-28
func TestRunValuesScalars(t *testing.T) {
	rt := newConvertRuntime(t, Config{})
	out, err := rt.RunValues(context.Background(), "k", "scalars")
	if err != nil {
		t.Fatalf("RunValues: %v", err)
	}
	want := []any{int64(7), "hi", true, nil, 2.5}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("got %#v, want %#v", out, want)
	}
}

// TestRunValuesSequence verifies a clean 1..n table becomes a []any.
// Since: 2026-06-28
func TestRunValuesSequence(t *testing.T) {
	rt := newConvertRuntime(t, Config{})
	out, err := rt.RunValues(context.Background(), "k", "seq")
	if err != nil {
		t.Fatalf("RunValues: %v", err)
	}
	want := []any{[]any{"a", "b", "c"}}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("got %#v, want %#v", out, want)
	}
}

// TestRunValuesNestedMap verifies a mixed-key table becomes a recursive map[any]any
// with nested sequence and sub-map.
// Since: 2026-06-28
func TestRunValuesNestedMap(t *testing.T) {
	rt := newConvertRuntime(t, Config{})
	out, err := rt.RunValues(context.Background(), "k", "nested")
	if err != nil {
		t.Fatalf("RunValues: %v", err)
	}
	want := []any{map[any]any{
		"n":    int64(1),
		"f":    2.5,
		"s":    "x",
		"ok":   true,
		"list": []any{int64(10), int64(20), int64(30)},
		"sub":  map[any]any{"y": "z"},
	}}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("got %#v, want %#v", out, want)
	}
}

// TestRunValuesFunctionErrorsWithoutHook verifies a non-data return errors safely
// when Config.ConvertValue is unset (never hands back a State-bound value).
// Since: 2026-06-28
func TestRunValuesFunctionErrorsWithoutHook(t *testing.T) {
	rt := newConvertRuntime(t, Config{})
	if _, err := rt.RunValues(context.Background(), "k", "fn"); err == nil {
		t.Fatal("expected an error converting a function return without Config.ConvertValue")
	}
}

// TestRunValuesFunctionWithHook verifies Config.ConvertValue handles non-data
// values.
// Since: 2026-06-28
func TestRunValuesFunctionWithHook(t *testing.T) {
	rt := newConvertRuntime(t, Config{
		ConvertValue: func(L *lua.LState, lv lua.Value) (any, error) {
			if lv.IsFunction() {
				return "FN", nil
			}
			return nil, nil
		},
	})
	out, err := rt.RunValues(context.Background(), "k", "fn")
	if err != nil {
		t.Fatalf("RunValues: %v", err)
	}
	if want := []any{"FN"}; !reflect.DeepEqual(out, want) {
		t.Fatalf("got %#v, want %#v", out, want)
	}
}

// TestRunValuesCyclicGuard verifies a cyclic table fails via the depth guard
// instead of overflowing the stack.
// Since: 2026-06-28
func TestRunValuesCyclicGuard(t *testing.T) {
	rt := newConvertRuntime(t, Config{})
	if _, err := rt.RunValues(context.Background(), "k", "cyclic"); err == nil {
		t.Fatal("expected a depth-limit error converting a cyclic table")
	}
}
