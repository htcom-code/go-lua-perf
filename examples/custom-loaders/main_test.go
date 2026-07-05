package main

import (
	"database/sql"
	"testing"
)

func TestLoadersDemo(t *testing.T) {
	before, after, health, err := loadersDemo()
	if err != nil {
		t.Fatalf("loadersDemo: %v", err)
	}
	if health != "ok" {
		t.Fatalf("mem:healthcheck status: got %q, want \"ok\"", health)
	}
	if before != 90 {
		t.Fatalf("discount before reload: got %.1f, want 90", before)
	}
	if after != 80 {
		t.Fatalf("discount after reload: got %.1f, want 80 (new version not picked up)", after)
	}
}

// TestDBLoaderConstructs only compile-checks DBLoader and confirms it satisfies the
// SourceLoader contract. It is not run against a real database — that would require
// importing a concrete SQL driver and thus a new module dependency.
func TestDBLoaderConstructs(t *testing.T) {
	var _ SourceLoader = NewDBLoader((*sql.DB)(nil))
}
