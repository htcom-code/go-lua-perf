// Example exec-timeout shows two ways a runaway script is stopped: a server-side
// hard cap via Config.ExecTimeout, and a caller deadline via the context passed
// to Run. Both abort a pure-Lua infinite loop (the timeout interrupts pure-Lua
// loops). Both surface as context.DeadlineExceeded.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	luart "github.com/htcom-code/go-lua-perf"
)

const runaway = `function spin() while true do end end`

// timeoutDemo runs an infinite-loop script under an ExecTimeout and returns the
// error (expected: context.DeadlineExceeded).
func timeoutDemo() error {
	loader := luart.NewMapLoader()
	loader.Set("spin", runaway, luart.HashVersion(runaway), "1.0.0")

	rt := luart.New(loader, luart.Config{MaxStates: 2, ExecTimeout: 50 * time.Millisecond})
	defer rt.Close()

	_, err := rt.Run(context.Background(), "spin", "spin")
	return err
}

// ctxDemo runs the same script but relies on a caller context deadline instead
// of ExecTimeout (also expected: context.DeadlineExceeded).
func ctxDemo() error {
	loader := luart.NewMapLoader()
	loader.Set("spin", runaway, luart.HashVersion(runaway), "1.0.0")

	rt := luart.New(loader, luart.Config{MaxStates: 2})
	defer rt.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := rt.Run(ctx, "spin", "spin")
	return err
}

func main() {
	err := timeoutDemo()
	fmt.Printf("ExecTimeout aborted runaway script: %v\n", err)
	if !errors.Is(err, context.DeadlineExceeded) {
		log.Fatalf("expected DeadlineExceeded, got %v", err)
	}

	err = ctxDemo()
	fmt.Printf("context deadline aborted runaway script: %v\n", err)
	if !errors.Is(err, context.DeadlineExceeded) {
		log.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}
