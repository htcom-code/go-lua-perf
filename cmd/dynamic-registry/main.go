// Command dynamic-registry is a thin demo of the reusable luart runtime:
// lazy load from an external source, per-script preloaded VM pools, TTL idle
// eviction, memory-capped pool size, and notification-driven hot reload
// (drop-and-reload) with content-hash versions + human-readable displayVersion.
package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	lua "github.com/htcom-code/lua-pure/lua"

	luart "github.com/htcom-code/go-lua-perf"
)

func main() {
	// Stand in for the external cache — sources are loaded only when first used.
	// version is the content hash (engine), displayVersion is the human label.
	loader := luart.NewMapLoader()
	greetV1 := `function run(name) return "v1 hello, " .. name end`
	double := `function run(n) return tostring(n * 2) end`
	loader.Set("greet", greetV1, luart.HashVersion(greetV1), "1.0.0")
	loader.Set("double", double, luart.HashVersion(double), "1.0.0")

	// 8MB budget → MaxStates derived from the measured per-state cost.
	rt := luart.New(loader, luart.Config{
		MemoryBudgetBytes: 8 << 20,
		IdleTTL:           500 * time.Millisecond,
		JanitorInterval:   200 * time.Millisecond,
	})
	defer rt.Close()

	ctx := context.Background()
	fmt.Printf("MaxStates=%d (8MB budget / measured perState)\n", rt.Stats().MaxStates)

	// Concurrent execution across goroutines — lazily loaded on first use.
	var wg sync.WaitGroup
	for i := 1; i <= 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			g, err := rt.Run(ctx, "greet", "run", lua.MkString(fmt.Sprintf("u%d", id)))
			if err != nil {
				log.Printf("[%d] greet: %v", id, err)
				return
			}
			d, err := rt.Run(ctx, "double", "run", lua.Int(int64(id)))
			if err != nil {
				log.Printf("[%d] double: %v", id, err)
				return
			}
			fmt.Printf("[%d] %s | double=%s\n", id, g[0].Str(), d[0].Str())
		}(i)
	}
	wg.Wait()
	fmt.Printf("loads=%d, compiles=%d, live states=%d\n", loader.Loads(), rt.CompileCount(), rt.Stats().LiveStates)
	for _, ps := range rt.PoolStats() {
		fmt.Printf("  pool %-7s display=%s hash=%s idle=%d\n", ps.Key, ps.DisplayVersion, ps.VersionShort, ps.Idle)
	}

	// Hot reload: the external side changes greet to v2 and notifies (Notify) →
	// the pool is dropped → reloaded on next use.
	greetV2 := `function run(name) return "v2 hi, " .. name end`
	loader.Set("greet", greetV2, luart.HashVersion(greetV2), "2.0.0")
	rt.Notify("greet", luart.HashVersion(greetV2), "2.0.0")
	g, _ := rt.Run(ctx, "greet", "run", lua.MkString("world"))
	fmt.Printf("after hot reload (Notify): %s (compiles=%d)\n", g[0].Str(), rt.CompileCount())
	for _, ps := range rt.PoolStats() {
		if ps.Key == "greet" {
			fmt.Printf("  pool greet display=%s hash=%s\n", ps.DisplayVersion, ps.VersionShort)
		}
	}

	// TTL idle eviction: idling briefly lets the janitor reclaim pools (live = 0).
	time.Sleep(900 * time.Millisecond)
	fmt.Printf("after TTL sweep: pools=%d, live states=%d\n", rt.Stats().Pools, rt.Stats().LiveStates)
}
