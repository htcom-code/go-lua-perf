// Example trace-profiling shows Config.Trace: a TraceHook receives per-request,
// per-stage timings (load, compile, acquire, build, execute, release). Leaving
// Trace nil has zero overhead (no time.Now calls); this opts in for development
// profiling.
package main

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	luart "github.com/htcom-code/go-lua-perf"
)

// traceDemo runs a script once with a TraceHook and returns the per-stage
// durations it observed. A cold first run touches every stage.
func traceDemo() (map[string]time.Duration, error) {
	loader := luart.NewMapLoader()
	src := `function work() return 42 end`
	loader.Set("work", src, luart.HashVersion(src), "1.0.0")

	var mu sync.Mutex
	stages := make(map[string]time.Duration)
	hook := func(stage, key string, dur time.Duration) {
		mu.Lock()
		stages[stage] += dur
		mu.Unlock()
	}

	rt := luart.New(loader, luart.Config{MaxStates: 2, Trace: hook})
	defer rt.Close()

	if _, err := rt.Run(context.Background(), "work", "work"); err != nil {
		return nil, err
	}
	return stages, nil
}

func main() {
	stages, err := traceDemo()
	if err != nil {
		log.Fatal(err)
	}
	names := make([]string, 0, len(stages))
	for s := range stages {
		names = append(names, s)
	}
	sort.Strings(names)
	for _, s := range names {
		fmt.Printf("%-8s %v\n", s, stages[s])
	}
}
