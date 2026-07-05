// Example logging shows routing luart's structured events into log/slog via
// Config.Logger + NewSlogLogger. The default Logger is a no-op; this opts in so
// pool loads, drops, and load/compile failures land in your logs.
package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"log/slog"

	luart "github.com/htcom-code/go-lua-perf"
)

// loggingDemo runs a script with an slog logger writing into buf, and returns
// nothing but the side effect: buf captures luart's structured log lines.
func loggingDemo(buf *bytes.Buffer) error {
	loader := luart.NewMapLoader()
	src := `function hi() return "hi" end`
	loader.Set("hi", src, luart.HashVersion(src), "1.0.0")

	logger := luart.NewSlogLogger(slog.New(slog.NewTextHandler(buf, nil)))
	rt := luart.New(loader, luart.Config{MaxStates: 2, Logger: logger})
	defer rt.Close()

	_, err := rt.Run(context.Background(), "hi", "hi")
	return err
}

func main() {
	var buf bytes.Buffer
	if err := loggingDemo(&buf); err != nil {
		log.Fatal(err)
	}
	// e.g. level=INFO msg="luart: pool loaded" key=hi version=... display=1.0.0
	fmt.Print(buf.String())
}
