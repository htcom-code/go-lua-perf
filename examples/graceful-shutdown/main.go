// Example graceful-shutdown shows the Close vs Shutdown split (mirroring
// net/http.Server): Close stops immediately, Shutdown drains in-flight calls
// first (up to the ctx deadline). After either, Run returns luart.ErrClosed.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	luart "github.com/htcom-code/go-lua-perf"
)

// shutdownDemo runs a script, gracefully shuts the Runtime down, then attempts
// another Run — returning the post-shutdown error (expected ErrClosed).
func shutdownDemo() (afterErr error, err error) {
	loader := luart.NewMapLoader()
	src := `function ping() return "pong" end`
	loader.Set("svc", src, luart.HashVersion(src), "1.0.0")

	rt := luart.New(loader, luart.Config{MaxStates: 4})

	if _, err = rt.Run(context.Background(), "svc", "ping"); err != nil {
		return
	}

	// Graceful drain: stop the janitor, close idle States, wait for in-flight
	// calls to finish (or until ctx is done).
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = rt.Shutdown(ctx); err != nil {
		return
	}

	_, afterErr = rt.Run(context.Background(), "svc", "ping")
	return
}

func main() {
	afterErr, err := shutdownDemo()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Run after Shutdown: %v\n", afterErr)
	if !errors.Is(afterErr, luart.ErrClosed) {
		log.Fatalf("expected ErrClosed, got %v", afterErr)
	}
	fmt.Println("graceful shutdown complete")
}
