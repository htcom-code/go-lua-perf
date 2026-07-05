// Example custom-loaders shows how to implement the luart.SourceLoader interface
// for real backends — a file tree, a database, an in-memory store — plus two
// hybrid patterns (a caching wrapper and a prefix router). The full guide is
// docs/SourceLoader.md.
//
// Since: 2026-06-08
package main

import (
	"fmt"
	"os"
	"path/filepath"

	luart "github.com/htcom-code/go-lua-perf"
)

// FileLoader reads each script from "<dir>/<key>.lua" on disk. The version is the
// content hash of the bytes, so editing a file and calling Notify with the new
// hash triggers a drop-and-reload. displayVersion is left empty (the runtime falls
// back to the hash prefix).
//
// Since: 2026-06-08
type FileLoader struct{ dir string }

// NewFileLoader returns a loader rooted at dir.
func NewFileLoader(dir string) *FileLoader { return &FileLoader{dir: dir} }

// Load reads "<dir>/<key>.lua". A missing file is reported as an error (not an
// empty source). Safe for concurrent use: os.ReadFile is goroutine-safe and the
// loader holds no mutable state.
func (l *FileLoader) Load(key string) (src, version, displayVersion string, err error) {
	b, err := os.ReadFile(filepath.Join(l.dir, key+".lua"))
	if err != nil {
		return "", "", "", fmt.Errorf("luart: script %q not found: %w", key, err)
	}
	src = string(b)
	return src, luart.HashVersion(src), "", nil
}
