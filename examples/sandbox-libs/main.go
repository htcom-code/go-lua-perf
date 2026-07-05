// Example sandbox-libs shows controlling which Lua standard libraries a script
// can reach via Config.Libs. The default set (base/table/string/math/utf8/
// coroutine) omits os/io/package/debug and removes load/loadfile/dofile, so an
// untrusted script cannot touch the host or run arbitrary code. Opting a
// library in (here os) is explicit and per-Runtime.
package main

import (
	"context"
	"fmt"
	"log"

	lua "github.com/htcom-code/lua-pure/lua"

	luart "github.com/htcom-code/go-lua-perf"
)

// usesOS calls os.time(), which only works if the os library is opened.
const usesOS = `function now() return os.time() end`

// sandboxed runs the script under the DEFAULT libs (no os) — it should error
// because the global os table is not present.
func sandboxed() error {
	loader := luart.NewMapLoader()
	loader.Set("clock", usesOS, luart.HashVersion(usesOS), "1.0.0")

	rt := luart.New(loader, luart.Config{MaxStates: 1}) // default Libs: base/table/string/math/utf8/coroutine
	defer rt.Close()

	_, err := rt.Run(context.Background(), "clock", "now")
	return err
}

// allowed runs the same script with os explicitly opened — it should succeed.
func allowed() error {
	loader := luart.NewMapLoader()
	loader.Set("clock", usesOS, luart.HashVersion(usesOS), "1.0.0")

	rt := luart.New(loader, luart.Config{
		MaxStates: 1,
		Libs:      []func(*lua.LState){(*lua.LState).OpenBase, (*lua.LState).OpenOS},
	})
	defer rt.Close()

	_, err := rt.Run(context.Background(), "clock", "now")
	return err
}

func main() {
	if err := sandboxed(); err != nil {
		fmt.Printf("sandboxed (default libs): blocked as expected: %v\n", err)
	} else {
		log.Fatal("expected the sandboxed script to fail (os should be unavailable)")
	}

	if err := allowed(); err != nil {
		log.Fatalf("expected the os-enabled script to succeed: %v", err)
	}
	fmt.Println("os-enabled script: succeeded")
}
