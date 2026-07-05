// Command genconfig writes the example luart config files (YAML + JSON) used by
// `make config`. It is a small, OS-independent generator (no shell printf/mkdir
// -p), so `make config` behaves identically on macOS, Linux, and Windows.
//
// Usage: go run ./internal/genconfig [dir]   (dir defaults to "configs")
//
// The field values and format are documented in docs/config.md — keep them in
// sync.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

const yamlContent = `# luart config — see docs/config.md. Durations: "300ms","30s","5m","1h".
# maxStates 0 => derived from memoryBudgetBytes / measured per-state cost.
# execTimeout 0 => no per-execution time limit (disabled).
maxStates: 16
memoryBudgetBytes: 0
idleTTL: 5m
janitorInterval: 30s
execTimeout: 0s
`

const jsonContent = `{
  "maxStates": 16,
  "memoryBudgetBytes": 0,
  "idleTTL": "5m",
  "janitorInterval": "30s",
  "execTimeout": "0s"
}
`

func main() {
	dir := "configs"
	if len(os.Args) > 1 && os.Args[1] != "" {
		dir = os.Args[1]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}

	yamlPath := filepath.Join(dir, "luart.example.yaml")
	jsonPath := filepath.Join(dir, "luart.example.json")
	if err := os.WriteFile(yamlPath, []byte(yamlContent), 0o644); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(jsonPath, []byte(jsonContent), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s and %s\n", yamlPath, jsonPath)
}
