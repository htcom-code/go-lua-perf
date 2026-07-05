// Example observability shows the read-only introspection API: Stats (global
// gauges), PoolStats (per-script snapshot incl. displayVersion), and
// CompileCount (distinct key:version compiled). Use these for dashboards and
// health checks.
package main

import (
	"context"
	"fmt"
	"log"
	"sort"

	luart "github.com/htcom-code/go-lua-perf"
)

// observeDemo loads two scripts, runs each once, and returns the compile count,
// the sorted pool keys, and the global Stats snapshot.
func observeDemo() (compiles int64, keys []string, stats luart.Stats, err error) {
	loader := luart.NewMapLoader()
	a := `function f() return 1 end`
	b := `function g() return 2 end`
	loader.Set("a", a, luart.HashVersion(a), "1.0.0")
	loader.Set("b", b, luart.HashVersion(b), "2.0.0")

	rt := luart.New(loader, luart.Config{MaxStates: 4})
	defer rt.Close()

	if _, err = rt.Run(context.Background(), "a", "f"); err != nil {
		return
	}
	if _, err = rt.Run(context.Background(), "b", "g"); err != nil {
		return
	}

	compiles = rt.CompileCount()
	stats = rt.Stats()
	for _, ps := range rt.PoolStats() {
		keys = append(keys, ps.Key)
	}
	sort.Strings(keys)
	return
}

func main() {
	compiles, keys, stats, err := observeDemo()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("compiles: %d\n", compiles) // 2 (distinct key:version)
	fmt.Printf("pools:    %v\n", keys)     // [a b]
	fmt.Printf("stats:    %+v\n", stats)   // {Pools:2 LiveStates:2 MaxStates:4}
}
