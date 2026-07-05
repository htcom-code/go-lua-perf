// Command performance compares Lua script compile cost between lua-pure (the
// pure-Go engine luart is built on) and PUC-Lua (the reference C implementation,
// the `lua` binary on PATH), on identical source files.
//
// It generates scripts of several sizes into performance/scripts/, then for each
// measures, on both engines:
//   - compile time: parse + compile the source into an executable form
//     (lua-pure: lua.CompileReader → *Proto; PUC-Lua: load()),
//     reported as the best (min) of several reps;
//   - retained memory: heap held by that compiled artifact afterwards.
//
// This surfaces how the pure-Go compiler's compile cost and retained memory scale
// with script size relative to PUC-Lua — which is why luart caches each compiled
// proto per key:version and pools VMs to pay that cost only once.
//
// Run:
//
//	go run ./performance              # full comparison
//	go run ./performance -reps 3      # fix the rep count
//	go run ./performance -keep        # reuse existing scripts (don't regenerate)
//
// If the `lua` binary is not on PATH, the PUC-Lua columns are reported as N/A and
// only the lua-pure numbers are shown.
//
// Since: 2026-06-19
package main

import (
	"bytes"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	lua "github.com/htcom-code/lua-pure/lua"
)

// sizeSpec is one row of the comparison: a human label and the number of trivial
// global functions to emit. "Trivial functions" is a deliberate worst case for
// per-prototype overhead; real scripts expand less, but the engine-vs-engine
// ratio is what this tool isolates.
type sizeSpec struct {
	label  string
	nfuncs int
}

// defaultSizes are the scales used in the README performance table.
var defaultSizes = []sizeSpec{
	{"64KB", 2_000},
	{"256KB", 8_000},
	{"512KB", 16_000},
	{"1MB", 30_000},
}

// result holds one engine's measurement for one size.
type result struct {
	compileMs float64
	retainMB  float64
	ok        bool // false when the engine was unavailable (e.g. no `lua` binary)
}

func main() {
	reps := flag.Int("reps", 0, "fixed rep count for the timed loop (0 = adaptive: more reps for fast cases)")
	keep := flag.Bool("keep", false, "reuse existing scripts instead of regenerating them")
	flag.Parse()

	dir, err := scriptDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "performance:", err)
		os.Exit(1)
	}
	scriptsDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "performance:", err)
		os.Exit(1)
	}

	luaBin, _ := exec.LookPath("lua")
	luaVer := luaVersion(luaBin)
	fmt.Printf("lua-pure vs PUC-Lua — compile cost on identical scripts\n")
	fmt.Printf("  goarch=%s/%s  PUC-Lua=%s\n\n", runtime.GOOS, runtime.GOARCH, luaVer)

	header := fmt.Sprintf("%-7s | %12s %11s | %12s %11s | %8s %8s",
		"size", "luapure(ms)", "luapure(MB)", "PUC(ms)", "PUC(MB)", "x time", "x mem")
	fmt.Println(header)
	fmt.Println(strings.Repeat("-", len(header)))

	for _, s := range defaultSizes {
		path := filepath.Join(scriptsDir, fmt.Sprintf("f%d.lua", s.nfuncs))
		if err := ensureScript(path, s.nfuncs, *keep); err != nil {
			fmt.Fprintln(os.Stderr, "performance:", err)
			os.Exit(1)
		}
		src, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "performance:", err)
			os.Exit(1)
		}

		g := measureGo(src, *reps)
		p := measurePUC(luaBin, filepath.Join(dir, "measure.lua"), path, *reps)

		timeX, memX := "N/A", "N/A"
		if p.ok && p.compileMs > 0 {
			timeX = fmt.Sprintf("%.0fx", g.compileMs/p.compileMs)
			memX = fmt.Sprintf("%.0fx", g.retainMB/p.retainMB)
		}
		pucMs, pucMB := "    N/A", "    N/A"
		if p.ok {
			pucMs = fmt.Sprintf("%12.1f", p.compileMs)
			pucMB = fmt.Sprintf("%11.1f", p.retainMB)
		}
		fmt.Printf("%-7s | %12.1f %11.1f | %s %s | %8s %8s\n",
			s.label, g.compileMs, g.retainMB, pucMs, pucMB, timeX, memX)
	}

	if luaBin == "" {
		fmt.Printf("\nNote: `lua` not found on PATH — PUC-Lua columns are N/A. Install Lua (e.g. `brew install lua`) to compare.\n")
	}
	fmt.Printf("\nCompile time = parse+compile only (not execution). Retained = heap held by the compiled proto.\n")
}

// scriptDir returns the directory holding this command's sources (so the tool
// works regardless of the caller's working directory), falling back to ".".
func scriptDir() (string, error) {
	// The measure.lua file lives next to this program. When run via `go run
	// ./performance`, the working directory is the module root, so look there
	// first, then the current directory.
	for _, cand := range []string{"performance", "."} {
		if _, err := os.Stat(filepath.Join(cand, "measure.lua")); err == nil {
			return cand, nil
		}
	}
	return "performance", nil
}

// genScript returns a Lua source defining nfuncs trivial global functions
// (fI returns I). Kept simple and deterministic so both engines compile the
// exact same bytes and a caller can verify fI() == I.
func genScript(nfuncs int) string {
	var b strings.Builder
	b.Grow(nfuncs * 34)
	for i := 1; i <= nfuncs; i++ {
		fmt.Fprintf(&b, "function f%d() return %d end\n", i, i)
	}
	return b.String()
}

// ensureScript writes the script for nfuncs to path unless keep is set and the
// file already exists.
func ensureScript(path string, nfuncs int, keep bool) error {
	if keep {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
	}
	return os.WriteFile(path, []byte(genScript(nfuncs)), 0o644)
}

// measureGo compiles src with lua-pure and returns the best compile time
// and the retained proto size. reps==0 selects an adaptive loop (repeat until
// ~500ms cumulative or 8 reps), so fast cases get many samples and slow ones get
// few.
func measureGo(src []byte, reps int) result {
	best := math.MaxFloat64
	var cumulative time.Duration
	for i := 0; ; i++ {
		runtime.GC()
		t0 := time.Now()
		proto, err := lua.CompileReader(bytes.NewReader(src), "perf")
		if err != nil {
			return result{ok: false}
		}
		d := time.Since(t0)
		runtime.KeepAlive(proto)
		if ms := float64(d.Microseconds()) / 1000; ms < best {
			best = ms
		}
		cumulative += d
		if reps > 0 {
			if i+1 >= reps {
				break
			}
		} else if i+1 >= 8 || cumulative >= 500*time.Millisecond {
			break
		}
	}

	// Retained: heap delta from holding one compiled proto, post-GC.
	runtime.GC()
	runtime.GC()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	proto, _ := lua.CompileReader(bytes.NewReader(src), "perf")
	runtime.GC()
	runtime.ReadMemStats(&m1)
	runtime.KeepAlive(proto)

	return result{compileMs: best, retainMB: float64(m1.HeapAlloc-m0.HeapAlloc) / 1e6, ok: true}
}

// measurePUC runs measure.lua under the `lua` binary and parses its
// "<compile_ms> <retained_mb>" line. Returns ok=false if lua is unavailable or
// the run fails.
func measurePUC(luaBin, measureLua, scriptPath string, reps int) result {
	if luaBin == "" {
		return result{ok: false}
	}
	r := reps
	if r <= 0 {
		r = 5
	}
	out, err := exec.Command(luaBin, measureLua, scriptPath, fmt.Sprint(r)).Output()
	if err != nil {
		return result{ok: false}
	}
	var ms, mb float64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%f %f", &ms, &mb); err != nil {
		return result{ok: false}
	}
	return result{compileMs: ms, retainMB: mb, ok: true}
}

// luaVersion returns the `lua -v` banner (first line) or "not found".
func luaVersion(luaBin string) string {
	if luaBin == "" {
		return "not found"
	}
	out, err := exec.Command(luaBin, "-v").CombinedOutput()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
}
