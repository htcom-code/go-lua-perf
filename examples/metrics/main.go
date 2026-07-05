// Example metrics shows wiring a Config.Metrics sink: the Runtime calls
// OnCompile/OnBuild/OnReuse/OnEvict/OnDrop at lifecycle points. The default is a
// no-op (zero overhead), so this is purely opt-in — pair it with your metrics
// system (Prometheus, statsd, …).
package main

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"

	lua "github.com/htcom-code/lua-pure/lua"

	luart "github.com/htcom-code/go-lua-perf"
)

// counters is a minimal concurrency-safe Metrics implementation.
type counters struct {
	compile, build, reuse, evict, drop atomic.Int64
}

func (c *counters) OnCompile(string) { c.compile.Add(1) }
func (c *counters) OnBuild(string)   { c.build.Add(1) }
func (c *counters) OnReuse(string)   { c.reuse.Add(1) }
func (c *counters) OnEvict(string)   { c.evict.Add(1) }
func (c *counters) OnDrop(string)    { c.drop.Add(1) }

// metricsDemo runs one script several times through a single pooled State and
// returns the collected counters: one compile, one build, the rest reuse.
func metricsDemo() (*counters, error) {
	loader := luart.NewMapLoader()
	src := `function inc(x) return x + 1 end`
	loader.Set("inc", src, luart.HashVersion(src), "1.0.0")

	m := &counters{}
	rt := luart.New(loader, luart.Config{MaxStates: 1, Metrics: m})
	defer rt.Close()

	for i := 0; i < 5; i++ {
		if _, err := rt.Run(context.Background(), "inc", "inc", lua.Int(int64(i))); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func main() {
	m, err := metricsDemo()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("compile=%d build=%d reuse=%d evict=%d drop=%d\n",
		m.compile.Load(), m.build.Load(), m.reuse.Load(), m.evict.Load(), m.drop.Load())
}
