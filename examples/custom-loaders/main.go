package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	lua "github.com/htcom-code/lua-pure/lua"

	luart "github.com/htcom-code/go-lua-perf"
)

// loadersDemo wires three backends behind one RoutingLoader: a file-backed script
// (cached) reached as "file:checkout" and an in-memory script reached as
// "mem:healthcheck". It runs both, then edits the file on disk, invalidates the
// cache, and Notifies — so the next Run picks up the new version. It returns the
// before/after results so the reload is observable.
func loadersDemo() (before, after float64, health string, err error) {
	// File backend: write checkout.lua into a temp dir.
	dir, err := os.MkdirTemp("", "luart-scripts")
	if err != nil {
		return 0, 0, "", err
	}
	defer os.RemoveAll(dir)
	checkoutV1 := `function discount(price) return price * 0.9 end` // 10% off
	if err = os.WriteFile(filepath.Join(dir, "checkout.lua"), []byte(checkoutV1), 0o644); err != nil {
		return 0, 0, "", err
	}
	cachedFile := NewCachingLoader(NewFileLoader(dir))

	// Memory backend: a tiny health-check script.
	mem := NewMemoryLoader()
	mem.Set("healthcheck", `function status() return "ok" end`, "1.0.0")

	// One router over both, dispatched by key prefix.
	router := NewRoutingLoader(map[string]SourceLoader{
		"file": cachedFile,
		"mem":  mem,
	})

	rt := luart.New(router, luart.Config{MaxStates: 4})
	defer rt.Close()

	ctx := context.Background()
	if before, err = runDiscount(ctx, rt, 100); err != nil {
		return
	}
	if health, err = runStatus(ctx, rt); err != nil {
		return
	}

	// External change: rewrite the file, invalidate the cache entry (backend-local
	// key, no prefix), then Notify with the new content hash so the pool is dropped.
	checkoutV2 := `function discount(price) return price * 0.8 end` // 20% off
	if err = os.WriteFile(filepath.Join(dir, "checkout.lua"), []byte(checkoutV2), 0o644); err != nil {
		return
	}
	cachedFile.Invalidate("checkout")
	rt.Notify("file:checkout", luart.HashVersion(checkoutV2), "2.0.0")

	after, err = runDiscount(ctx, rt, 100)
	return
}

func runDiscount(ctx context.Context, rt *luart.Runtime, price float64) (float64, error) {
	out, err := rt.Run(ctx, "file:checkout", "discount", lua.Float(price))
	if err != nil {
		return 0, err
	}
	return out[0].AsFloat(), nil
}

func runStatus(ctx context.Context, rt *luart.Runtime) (string, error) {
	out, err := rt.Run(ctx, "mem:healthcheck", "status")
	if err != nil {
		return "", err
	}
	return out[0].Str(), nil
}

func main() {
	before, after, health, err := loadersDemo()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("mem:healthcheck status()      = %q\n", health)                 // "ok"
	fmt.Printf("file:checkout discount(100)   = %.1f (before)\n", before)      // 90.0
	fmt.Printf("file:checkout discount(100)   = %.1f (after reload)\n", after) // 80.0
}
