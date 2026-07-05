# Go dev/test environment — AI Rule #6: build only after tests pass.
# See docs/golang-ai-rule.md for the full rules.

.PHONY: vet test race bench build all config performance memprof doc doc-web

# Directory for generated example config files (override: make config CONFIG_DIR=...).
CONFIG_DIR ?= configs

# Port for the local doc web server (override: make doc-web DOC_PORT=9090).
DOC_PORT ?= 8080

# performance: which extra args to pass the compile-cost tool (e.g. make performance PERF_ARGS="-keep").
PERF_ARGS ?=

# memprof: package + benchmark to profile, and where the heap profile lands.
# Override e.g. make memprof MEMPROF_PKG=. MEMPROF_BENCH=BenchmarkDynamicRun.
MEMPROF_PKG   ?= ./cmd/pool-preload
MEMPROF_BENCH ?= .
MEMPROF_OUT   ?= mem.prof

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

bench:
	go test -bench=. -benchmem ./...

# build depends on test, so it only builds after tests pass (Rule #6).
build: vet test
	go build ./...

# all: vet -> test -> race -> build (full pre-release verification)
all: vet test race build

# config: generate example luart config files (YAML + JSON) with default values.
# Delegates to a small Go generator so it works identically on macOS, Linux, and
# Windows (no POSIX-shell printf/mkdir -p). Fields/format: docs/config.md.
config:
	go run ./internal/genconfig $(CONFIG_DIR)

# doc: print the package's godoc (all exported symbols) to the terminal.
doc:
	go doc -all .

# doc-web: browse the full API in a local pkgsite server (Go's modern doc web).
# No install needed (go run); open the printed URL. Override the port with DOC_PORT.
doc-web:
	@echo "Serving docs at http://localhost:$(DOC_PORT)/github.com/htcom-code/go-lua-perf  (Ctrl-C to stop)"
	go run golang.org/x/pkgsite/cmd/pkgsite@latest -http=localhost:$(DOC_PORT) .

# performance: lua-pure vs PUC-Lua compile cost (parse+compile time + retained
# memory) on identical scripts. Needs `lua` on PATH for the PUC columns (else N/A).
# See performance/README.md. Pass flags via PERF_ARGS (e.g. -keep, -reps 3).
performance:
	go run ./performance $(PERF_ARGS)

# memprof: Go-side memory proof — capture a heap profile from a benchmark and print
# the top allocators by total bytes allocated (alloc_space). Shows where Go memory
# goes; the default pool-preload bench makes the with-pool vs without-pool cost
# concrete (VM creation: newRegistry/stringConcat dominate). Profile -> $(MEMPROF_OUT).
memprof:
	go test -run='^$$' -bench=$(MEMPROF_BENCH) -benchmem -memprofile=$(MEMPROF_OUT) $(MEMPROF_PKG)
	go tool pprof -top -sample_index=alloc_space -nodecount=12 $(MEMPROF_OUT)
