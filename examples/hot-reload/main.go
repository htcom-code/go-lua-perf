// Example hot-reload shows notification-driven drop-and-reload: when a script's
// source changes, Notify drops that script's pool so the next Run picks up the
// new version — with no Runtime restart. In-flight calls finish on the old
// version and are discarded on return.
package main

import (
	"context"
	"fmt"
	"log"

	lua "github.com/htcom-code/lua-pure/lua"

	luart "github.com/htcom-code/go-lua-perf"
)

// reloadDemo runs the "rate" script, swaps its source, notifies the runtime, and
// runs again — returning both results so the change is observable.
func reloadDemo() (before, after float64, err error) {
	loader := luart.NewMapLoader()
	v1 := `function rate(x) return x * 1.0 end`
	loader.Set("rate", v1, luart.HashVersion(v1), "1.0.0")

	rt := luart.New(loader, luart.Config{MaxStates: 4})
	defer rt.Close()

	if before, err = runRate(rt, 100); err != nil {
		return
	}

	// Simulate an external change: new source + new version, then Notify so the
	// pool is dropped and reloaded on the next Run.
	v2 := `function rate(x) return x * 1.5 end`
	loader.Set("rate", v2, luart.HashVersion(v2), "2.0.0")
	rt.Notify("rate", luart.HashVersion(v2), "2.0.0")

	after, err = runRate(rt, 100)
	return
}

func runRate(rt *luart.Runtime, x float64) (float64, error) {
	out, err := rt.Run(context.Background(), "rate", "rate", lua.Float(x))
	if err != nil {
		return 0, err
	}
	return out[0].AsFloat(), nil
}

func main() {
	before, after, err := reloadDemo()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("before reload: rate(100) = %.1f\n", before) // 100.0
	fmt.Printf("after  reload: rate(100) = %.1f\n", after)  // 150.0
}
