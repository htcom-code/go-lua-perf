// Example custom-libs adds a user-authored library to pooled States via
// Config.ExtraLibs — without restating the sandbox defaults. ExtraLibs run after
// the default libs (and after load/loadfile/dofile are stripped), so the sandbox
// stays intact while scripts gain the custom API. Each entry runs once per pooled
// State, on the goroutine that owns it, so shared Go state a lib closes over must
// be goroutine-safe (here a read-only allowlist map).
package main

import (
	"context"
	"fmt"
	"log"

	lua "github.com/htcom-code/lua-pure/lua"

	luart "github.com/htcom-code/go-lua-perf"
)

// hostConfig is read-only shared state the custom lib closes over — safe to read
// from any goroutine that owns a pooled State.
var hostConfig = map[string]string{"region": "kr", "tier": "pro"}

// openHostKit registers the custom library on a pooled State:
//   - global function clamp(x, lo, hi)
//   - module table `host` with host.get(key), reading the allowlisted config
func openHostKit(L *lua.LState) {
	L.Register("clamp", func(L *lua.LState) int {
		x, lo, hi := L.CheckInt(1), L.CheckInt(2), L.CheckInt(3)
		if x < lo {
			x = lo
		}
		if x > hi {
			x = hi
		}
		L.Push(lua.Int(x))
		return 1
	})

	host := lua.NewTable()
	host.SetStr("get", lua.NewGoFunc("host.get", func(L *lua.LState) int {
		L.Push(lua.MkString(hostConfig[L.CheckString(1)]))
		return 1
	}))
	L.SetGlobal("host", host.Value())
}

const script = `function summary(n)
  return "clamped=" .. clamp(n, 0, 100)
      .. " region=" .. host.get("region")
      .. " up=" .. string.upper("ok")
end`

// run compiles the script once and calls it on a pooled State that has both the
// default sandbox libs and the custom kit (clamp, host).
func run() (string, error) {
	loader := luart.NewMapLoader()
	loader.Set("app", script, luart.HashVersion(script), "1.0.0")

	rt := luart.New(loader, luart.Config{
		MaxStates: 4,
		// Libs stays at the default sandbox; ExtraLibs adds the custom kit on top.
		ExtraLibs: []func(*lua.LState){openHostKit},
	})
	defer rt.Close()

	out, err := rt.Run(context.Background(), "app", "summary", lua.Int(150))
	if err != nil {
		return "", err
	}
	return out[0].Str(), nil
}

func main() {
	s, err := run()
	if err != nil {
		log.Fatalf("run: %v", err)
	}
	fmt.Println(s) // clamped=100 region=kr up=OK
}
