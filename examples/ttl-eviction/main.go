// Example ttl-eviction shows the background janitor reclaiming idle script pools:
// a pool unused for longer than Config.IdleTTL is evicted whole (its pooled
// States closed), freeing memory automatically. JanitorInterval controls how
// often the sweep runs.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	luart "github.com/htcom-code/go-lua-perf"
)

// evictDemo runs a script (creating a pool), waits past the IdleTTL, and returns
// the pool count right after the run and after the janitor has swept.
func evictDemo() (poolsAfterRun, poolsAfterTTL int, err error) {
	loader := luart.NewMapLoader()
	src := `function noop() return 1 end`
	loader.Set("job", src, luart.HashVersion(src), "1.0.0")

	// Short TTL + frequent janitor so the demo finishes quickly. In production
	// these are seconds/minutes.
	rt := luart.New(loader, luart.Config{
		MaxStates:       4,
		IdleTTL:         50 * time.Millisecond,
		JanitorInterval: 20 * time.Millisecond,
	})
	defer rt.Close()

	if _, err = rt.Run(context.Background(), "job", "noop"); err != nil {
		return
	}
	poolsAfterRun = rt.Stats().Pools

	// Wait long enough for the idle pool to exceed IdleTTL and be swept.
	time.Sleep(200 * time.Millisecond)
	poolsAfterTTL = rt.Stats().Pools
	return
}

func main() {
	afterRun, afterTTL, err := evictDemo()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("pools after run: %d\n", afterRun) // 1
	fmt.Printf("pools after TTL: %d\n", afterTTL) // 0 (janitor reclaimed it)
}
