package luart

import (
	"context"
	"fmt"

	lua "github.com/htcom-code/lua-pure/lua"
)

// maxConvertDepth bounds RunValues table recursion so a deeply nested — or cyclic
// — table fails with an error instead of overflowing the stack. A cyclic table
// cannot be represented as an acyclic Go []any / map[any]any anyway.
const maxConvertDepth = 100

// RunValues executes the key script's entryFn and deep-copies its results into Go
// values, so the caller never holds a value tied to a pooled State (the safe,
// convenient counterpart to Run). It runs the copy inside RunWith, while the
// State still owns the values.
//
// Conversion: nil→nil, boolean→bool, integer→int64, float→float64, string→string,
// table→[]any for a clean 1..n sequence or map[any]any otherwise (recursive; map
// keys must be scalar/string). function/userdata/thread are delegated to
// Config.ConvertValue, or return an error when it is unset (so a State-bound value
// is never handed back).
func (m *Runtime) RunValues(ctx context.Context, key, entryFn string, args ...lua.Value) ([]any, error) {
	var out []any
	err := m.RunWith(ctx, key, entryFn, func(L *lua.LState, rets []lua.Value) error {
		vs := make([]any, len(rets))
		for i, rv := range rets {
			cv, err := m.convertValue(L, rv, 0)
			if err != nil {
				return err
			}
			vs[i] = cv
		}
		out = vs
		return nil
	}, args...)
	return out, err
}

// convertValue materializes one Lua value into a Go value (see RunValues).
func (m *Runtime) convertValue(L *lua.LState, v lua.Value, depth int) (any, error) {
	switch {
	case v.IsNil():
		return nil, nil
	case v.IsBool():
		return v.AsBool(), nil
	case v.IsInt():
		return v.AsInt(), nil
	case v.IsFloat():
		return v.AsFloat(), nil
	case v.IsString():
		return v.Str(), nil
	case v.IsTable():
		if depth >= maxConvertDepth {
			return nil, fmt.Errorf("luart: table nesting exceeds %d levels (cyclic or too deep to convert)", maxConvertDepth)
		}
		return m.convertTable(L, v.AsTable(), depth+1)
	default: // function, userdata, thread — bound to the State, not copyable as data
		if m.cfg.ConvertValue != nil {
			return m.cfg.ConvertValue(L, v)
		}
		return nil, fmt.Errorf("luart: cannot convert a %s return value to a Go value (set Config.ConvertValue)", luaTypeName(v))
	}
}

// convertTable copies a Lua table to a Go []any (clean 1..n sequence) or
// map[any]any (otherwise).
func (m *Runtime) convertTable(L *lua.LState, t *lua.Table, depth int) (any, error) {
	type kv struct{ k, v lua.Value }
	var entries []kv
	for k, v, ok := t.Next(lua.Nil); ok; k, v, ok = t.Next(k) {
		entries = append(entries, kv{k, v})
	}

	// A clean sequence has exactly the keys 1..n (n = entry count), all distinct.
	n := int64(len(entries))
	isSeq := n > 0
	if isSeq {
		seen := make(map[int64]bool, n)
		for _, e := range entries {
			if !e.k.IsInt() {
				isSeq = false
				break
			}
			ki := e.k.AsInt()
			if ki < 1 || ki > n || seen[ki] {
				isSeq = false
				break
			}
			seen[ki] = true
		}
	}

	if isSeq {
		arr := make([]any, n)
		for _, e := range entries {
			cv, err := m.convertValue(L, e.v, depth)
			if err != nil {
				return nil, err
			}
			arr[e.k.AsInt()-1] = cv
		}
		return arr, nil
	}

	mp := make(map[any]any, len(entries))
	for _, e := range entries {
		kc, err := convertKey(e.k)
		if err != nil {
			return nil, err
		}
		vc, err := m.convertValue(L, e.v, depth)
		if err != nil {
			return nil, err
		}
		mp[kc] = vc
	}
	return mp, nil
}

// convertKey converts a Lua table key to a comparable Go value usable as a map
// key. Only scalars and strings are allowed; a reference-typed key would be
// non-comparable (or State-bound), so it is an error.
func convertKey(k lua.Value) (any, error) {
	switch {
	case k.IsBool():
		return k.AsBool(), nil
	case k.IsInt():
		return k.AsInt(), nil
	case k.IsFloat():
		return k.AsFloat(), nil
	case k.IsString():
		return k.Str(), nil
	default:
		return nil, fmt.Errorf("luart: %s table key cannot convert to a Go map key", luaTypeName(k))
	}
}

// luaTypeName is a Go-side name for a Lua value's type (for error messages).
func luaTypeName(v lua.Value) string {
	switch {
	case v.IsNil():
		return "nil"
	case v.IsBool():
		return "boolean"
	case v.IsNumber():
		return "number"
	case v.IsString():
		return "string"
	case v.IsTable():
		return "table"
	case v.IsFunction():
		return "function"
	case v.IsUserData():
		return "userdata"
	case v.IsThread():
		return "thread"
	default:
		return "value"
	}
}
