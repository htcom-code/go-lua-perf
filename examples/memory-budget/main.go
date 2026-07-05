// Example memory-budget shows capping total live VMs by a memory budget instead
// of a fixed count: with Config.MaxStates unset, the Runtime derives MaxStates
// from MemoryBudgetBytes ÷ measured per-state cost, then enforces it with LRU
// eviction + back-pressure under load.
package main

import (
	"context"
	"fmt"
	"log"
	"sync"

	luart "github.com/htcom-code/go-lua-perf"
)

var keys = []string{"a", "b", "c", "d", "e"}

// budgetDemo creates a budget-bounded Runtime, hammers several scripts
// concurrently, and returns the derived cap and whether live States stayed
// within it the whole time.
func budgetDemo() (maxStates int, liveWithinCap bool, err error) {
	loader := luart.NewMapLoader()
	for _, k := range keys {
		src := fmt.Sprintf(`function run() return %q end`, k)
		loader.Set(k, src, luart.HashVersion(src), "1.0.0")
	}

	// ~2 MB budget → a handful of States (exact count depends on the machine).
	rt := luart.New(loader, luart.Config{MemoryBudgetBytes: 2 * 1024 * 1024})
	defer rt.Close()

	maxStates = rt.Stats().MaxStates
	liveWithinCap = true

	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, e := rt.Run(context.Background(), keys[i%len(keys)], "run"); e != nil {
				mu.Lock()
				err = e
				mu.Unlock()
				return
			}
			if rt.Stats().LiveStates > maxStates {
				mu.Lock()
				liveWithinCap = false
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	return
}

func main() {
	maxStates, ok, err := budgetDemo()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("MaxStates derived from budget: %d\n", maxStates)
	fmt.Printf("live States stayed within cap: %v\n", ok)
}
