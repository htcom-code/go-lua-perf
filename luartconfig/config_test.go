package luartconfig

// Unit tests for the config loaders (JSON / YAML / env).
// Convention: basic behavior + exception cases (CONTRIBUTING.md).

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestLoadJSON verifies JSON loading and duration parsing.
// Since: 2026-06-07
func TestLoadJSON(t *testing.T) {
	path := writeFile(t, "c.json", `{
		"maxStates": 12,
		"memoryBudgetBytes": 8388608,
		"idleTTL": "500ms",
		"janitorInterval": "30s",
		"execTimeout": "2s"
	}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxStates != 12 || cfg.MemoryBudgetBytes != 8<<20 {
		t.Fatalf("got %+v", cfg)
	}
	if cfg.IdleTTL != 500*time.Millisecond || cfg.JanitorInterval != 30*time.Second {
		t.Fatalf("durations: ttl=%s jan=%s", cfg.IdleTTL, cfg.JanitorInterval)
	}
	if cfg.ExecTimeout != 2*time.Second {
		t.Fatalf("execTimeout: want 2s, got %s", cfg.ExecTimeout)
	}
}

// TestLoadYAML verifies YAML loading and duration parsing.
// Since: 2026-06-07
func TestLoadYAML(t *testing.T) {
	path := writeFile(t, "c.yaml", "maxStates: 4\nmemoryBudgetBytes: 1048576\nidleTTL: 1m\njanitorInterval: 2s\nexecTimeout: 500ms\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxStates != 4 || cfg.MemoryBudgetBytes != 1<<20 {
		t.Fatalf("got %+v", cfg)
	}
	if cfg.IdleTTL != time.Minute || cfg.JanitorInterval != 2*time.Second {
		t.Fatalf("durations: ttl=%s jan=%s", cfg.IdleTTL, cfg.JanitorInterval)
	}
	if cfg.ExecTimeout != 500*time.Millisecond {
		t.Fatalf("execTimeout: want 500ms, got %s", cfg.ExecTimeout)
	}
}

// TestFromEnv verifies environment-variable loading with a prefix.
// Since: 2026-06-07
func TestFromEnv(t *testing.T) {
	t.Setenv("LUART_MAX_STATES", "7")
	t.Setenv("LUART_MEMORY_BUDGET_BYTES", "2097152")
	t.Setenv("LUART_IDLE_TTL", "250ms")
	t.Setenv("LUART_JANITOR_INTERVAL", "5s")
	t.Setenv("LUART_EXEC_TIMEOUT", "1s")

	cfg, err := FromEnv("LUART_")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxStates != 7 || cfg.MemoryBudgetBytes != 2<<20 {
		t.Fatalf("got %+v", cfg)
	}
	if cfg.IdleTTL != 250*time.Millisecond || cfg.JanitorInterval != 5*time.Second {
		t.Fatalf("durations: ttl=%s jan=%s", cfg.IdleTTL, cfg.JanitorInterval)
	}
	if cfg.ExecTimeout != time.Second {
		t.Fatalf("execTimeout: want 1s, got %s", cfg.ExecTimeout)
	}
}

// TestFromEnv_Unset verifies unset variables leave zero values (defaults apply later).
// Since: 2026-06-07
func TestFromEnv_Unset(t *testing.T) {
	cfg, err := FromEnv("LUART_UNSET_")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxStates != 0 || cfg.IdleTTL != 0 || cfg.JanitorInterval != 0 {
		t.Fatalf("unset env should yield zero values, got %+v", cfg)
	}
}

// ── exception / edge cases ──

// TestLoad_UnsupportedExt verifies an unknown extension errors.
// Since: 2026-06-07
func TestLoad_UnsupportedExt(t *testing.T) {
	if _, err := Load("config.txt"); err == nil {
		t.Fatal("expected error for unsupported extension")
	}
}

// TestLoadJSON_MissingFile verifies a missing file errors.
// Since: 2026-06-07
func TestLoadJSON_MissingFile(t *testing.T) {
	if _, err := LoadJSON(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

// TestLoadJSON_ParseError verifies malformed JSON errors.
// Since: 2026-06-07
func TestLoadJSON_ParseError(t *testing.T) {
	path := writeFile(t, "bad.json", `{ "maxStates": `)
	if _, err := LoadJSON(path); err == nil {
		t.Fatal("expected JSON parse error")
	}
}

// TestSpecConfig_DurationError verifies an invalid duration string errors.
// Since: 2026-06-07
func TestSpecConfig_DurationError(t *testing.T) {
	if _, err := (Spec{IdleTTL: "nope"}).Config(); err == nil {
		t.Fatal("expected duration parse error")
	}
}

// TestSpecConfig_ValidateError verifies a negative MaxStates fails validation.
// Since: 2026-06-07
func TestSpecConfig_ValidateError(t *testing.T) {
	neg := -1
	if _, err := (Spec{MaxStates: &neg}).Config(); err == nil {
		t.Fatal("expected validation error for negative MaxStates")
	}
}

// TestSpecConfig_ExecTimeoutError verifies an invalid execTimeout duration errors.
// Since: 2026-06-07
func TestSpecConfig_ExecTimeoutError(t *testing.T) {
	if _, err := (Spec{ExecTimeout: "nope"}).Config(); err == nil {
		t.Fatal("expected duration parse error for execTimeout")
	}
}

// ── LoadJSONString ──

// TestLoadJSONString verifies parsing a JSON config from an in-memory string.
// Since: 2026-06-07
func TestLoadJSONString(t *testing.T) {
	cfg, err := LoadJSONString(`{"maxStates": 9, "idleTTL": "750ms"}`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxStates != 9 || cfg.IdleTTL != 750*time.Millisecond {
		t.Fatalf("got %+v", cfg)
	}
}

// TestLoadJSONString_ParseError verifies malformed JSON string errors.
// Since: 2026-06-07
func TestLoadJSONString_ParseError(t *testing.T) {
	if _, err := LoadJSONString(`{ "maxStates": `); err == nil {
		t.Fatal("expected JSON parse error")
	}
}

// ── precedence resolvers (env > base > defaults) ──

// TestResolve_EnvOverridesFile verifies env wins per field while unset env
// fields fall back to the file (field-level merge).
// Since: 2026-06-07
func TestResolve_EnvOverridesFile(t *testing.T) {
	path := writeFile(t, "c.json", `{
		"maxStates": 4,
		"memoryBudgetBytes": 1048576,
		"idleTTL": "1m",
		"janitorInterval": "2s",
		"execTimeout": "3s"
	}`)
	t.Setenv("LUART_MAX_STATES", "16") // overrides file's 4
	t.Setenv("LUART_IDLE_TTL", "5s")   // overrides file's 1m
	// MEMORY_BUDGET_BYTES, JANITOR_INTERVAL, EXEC_TIMEOUT unset → keep file values

	cfg, err := Resolve(path, "LUART_")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxStates != 16 { // from env
		t.Fatalf("MaxStates: want 16, got %d", cfg.MaxStates)
	}
	if cfg.IdleTTL != 5*time.Second { // from env
		t.Fatalf("IdleTTL: want 5s, got %s", cfg.IdleTTL)
	}
	if cfg.MemoryBudgetBytes != 1<<20 { // from file
		t.Fatalf("MemoryBudgetBytes: want 1MiB, got %d", cfg.MemoryBudgetBytes)
	}
	if cfg.JanitorInterval != 2*time.Second { // from file
		t.Fatalf("JanitorInterval: want 2s, got %s", cfg.JanitorInterval)
	}
	if cfg.ExecTimeout != 3*time.Second { // from file (env unset → merge keeps it)
		t.Fatalf("ExecTimeout: want 3s, got %s", cfg.ExecTimeout)
	}
}

// TestResolve_NoFile verifies an empty path yields env > defaults only.
// Since: 2026-06-07
func TestResolve_NoFile(t *testing.T) {
	t.Setenv("LUART_MAX_STATES", "3")
	cfg, err := Resolve("", "LUART_")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxStates != 3 {
		t.Fatalf("MaxStates: want 3, got %d", cfg.MaxStates)
	}
	if cfg.MemoryBudgetBytes != 0 || cfg.IdleTTL != 0 {
		t.Fatalf("unset fields should be zero, got %+v", cfg)
	}
}

// TestResolveJSONString_EnvOverridesString verifies env wins over the JSON
// string base, field by field.
// Since: 2026-06-07
func TestResolveJSONString_EnvOverridesString(t *testing.T) {
	t.Setenv("LUART_MAX_STATES", "20") // overrides the string's 4
	cfg, err := ResolveJSONString(`{"maxStates": 4, "idleTTL": "1m"}`, "LUART_")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxStates != 20 { // from env
		t.Fatalf("MaxStates: want 20, got %d", cfg.MaxStates)
	}
	if cfg.IdleTTL != time.Minute { // from string
		t.Fatalf("IdleTTL: want 1m, got %s", cfg.IdleTTL)
	}
}
