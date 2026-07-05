// Package luartconfig loads a luart.Config from JSON, YAML, or environment
// variables. It lives in a subpackage so the core luart package stays
// dependency-free (the YAML dependency is only pulled in when this package is
// imported).
//
// Only the serializable numeric/duration fields are loaded; Libs, Metrics, and
// Logger are code-injected on the returned Config by the caller.
//
// Sources can be combined with the precedence env > file > defaults via Resolve
// (or env > JSON string > defaults via ResolveJSONString). The merge is
// field-by-field: an env var overrides only the one field it sets, leaving the
// rest to the file (then to luart.New's built-in defaults).
//
// Since: 2026-06-07
package luartconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"

	luart "github.com/htcom-code/go-lua-perf"
)

// Spec is the serializable form of luart.Config. Durations are strings (e.g.
// "500ms", "30s") so JSON, YAML, and env all parse them the same way. The
// numeric fields are pointers so an unset field is distinguishable from a zero
// value, which is what lets sources merge field-by-field (see merge / Resolve).
type Spec struct {
	MaxStates         *int    `json:"maxStates,omitempty" yaml:"maxStates,omitempty"`
	MemoryBudgetBytes *uint64 `json:"memoryBudgetBytes,omitempty" yaml:"memoryBudgetBytes,omitempty"`
	IdleTTL           string  `json:"idleTTL,omitempty" yaml:"idleTTL,omitempty"`
	JanitorInterval   string  `json:"janitorInterval,omitempty" yaml:"janitorInterval,omitempty"`
	ExecTimeout       string  `json:"execTimeout,omitempty" yaml:"execTimeout,omitempty"`
}

// merge overlays the set fields of o onto s (o wins per field) and returns the
// result. A nil numeric pointer or empty duration string means "unset" and is
// left untouched, so o only overrides the fields it actually carries.
func (s Spec) merge(o Spec) Spec {
	if o.MaxStates != nil {
		s.MaxStates = o.MaxStates
	}
	if o.MemoryBudgetBytes != nil {
		s.MemoryBudgetBytes = o.MemoryBudgetBytes
	}
	if o.IdleTTL != "" {
		s.IdleTTL = o.IdleTTL
	}
	if o.JanitorInterval != "" {
		s.JanitorInterval = o.JanitorInterval
	}
	if o.ExecTimeout != "" {
		s.ExecTimeout = o.ExecTimeout
	}
	return s
}

// Config converts the Spec into a validated luart.Config (parsing durations).
// Unset fields stay at the zero value, so luart.New applies its own defaults.
func (s Spec) Config() (luart.Config, error) {
	var cfg luart.Config
	if s.MaxStates != nil {
		cfg.MaxStates = *s.MaxStates
	}
	if s.MemoryBudgetBytes != nil {
		cfg.MemoryBudgetBytes = *s.MemoryBudgetBytes
	}
	if s.IdleTTL != "" {
		d, err := time.ParseDuration(s.IdleTTL)
		if err != nil {
			return cfg, fmt.Errorf("config: idleTTL: %w", err)
		}
		cfg.IdleTTL = d
	}
	if s.JanitorInterval != "" {
		d, err := time.ParseDuration(s.JanitorInterval)
		if err != nil {
			return cfg, fmt.Errorf("config: janitorInterval: %w", err)
		}
		cfg.JanitorInterval = d
	}
	if s.ExecTimeout != "" {
		d, err := time.ParseDuration(s.ExecTimeout)
		if err != nil {
			return cfg, fmt.Errorf("config: execTimeout: %w", err)
		}
		cfg.ExecTimeout = d
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// ── Spec parsing (internal) ──

func specFromJSON(b []byte, src string) (Spec, error) {
	var s Spec
	if err := json.Unmarshal(b, &s); err != nil {
		return Spec{}, fmt.Errorf("config: parse JSON %s: %w", src, err)
	}
	return s, nil
}

func specFromYAML(b []byte, src string) (Spec, error) {
	var s Spec
	if err := yaml.Unmarshal(b, &s); err != nil {
		return Spec{}, fmt.Errorf("config: parse YAML %s: %w", src, err)
	}
	return s, nil
}

// specFromFile reads a file and parses it as JSON or YAML by extension.
func specFromFile(path string) (Spec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Spec{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	switch ext := filepath.Ext(path); ext {
	case ".json":
		return specFromJSON(b, path)
	case ".yaml", ".yml":
		return specFromYAML(b, path)
	default:
		return Spec{}, fmt.Errorf("config: unsupported file extension %q (want .json/.yaml/.yml)", ext)
	}
}

// envSpec reads a Spec from environment variables named prefix+FIELD. Unset
// variables leave the field unset (nil / "").
func envSpec(prefix string) (Spec, error) {
	var s Spec
	if v := os.Getenv(prefix + "MAX_STATES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Spec{}, fmt.Errorf("config: %sMAX_STATES: %w", prefix, err)
		}
		s.MaxStates = &n
	}
	if v := os.Getenv(prefix + "MEMORY_BUDGET_BYTES"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return Spec{}, fmt.Errorf("config: %sMEMORY_BUDGET_BYTES: %w", prefix, err)
		}
		s.MemoryBudgetBytes = &n
	}
	s.IdleTTL = os.Getenv(prefix + "IDLE_TTL")
	s.JanitorInterval = os.Getenv(prefix + "JANITOR_INTERVAL")
	s.ExecTimeout = os.Getenv(prefix + "EXEC_TIMEOUT")
	return s, nil
}

// ── single-source loaders ──

// LoadJSON reads a JSON config file into a luart.Config.
func LoadJSON(path string) (luart.Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return luart.Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	s, err := specFromJSON(b, path)
	if err != nil {
		return luart.Config{}, err
	}
	return s.Config()
}

// LoadJSONString parses an in-memory JSON string (not a file) into a
// luart.Config. JSON only — useful when the config comes from a remote store, a
// command-line flag, or a test rather than from disk.
//
// Since: 2026-06-07
func LoadJSONString(jsonStr string) (luart.Config, error) {
	s, err := specFromJSON([]byte(jsonStr), "<string>")
	if err != nil {
		return luart.Config{}, err
	}
	return s.Config()
}

// LoadYAML reads a YAML config file into a luart.Config.
func LoadYAML(path string) (luart.Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return luart.Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	s, err := specFromYAML(b, path)
	if err != nil {
		return luart.Config{}, err
	}
	return s.Config()
}

// Load reads a config file, choosing JSON or YAML by extension
// (.json → JSON; .yaml/.yml → YAML).
func Load(path string) (luart.Config, error) {
	s, err := specFromFile(path)
	if err != nil {
		return luart.Config{}, err
	}
	return s.Config()
}

// FromEnv builds a luart.Config from environment variables, each named
// prefix+FIELD: MAX_STATES, MEMORY_BUDGET_BYTES, IDLE_TTL, JANITOR_INTERVAL,
// EXEC_TIMEOUT. Unset variables leave the zero value (defaults apply at luart.New).
func FromEnv(prefix string) (luart.Config, error) {
	s, err := envSpec(prefix)
	if err != nil {
		return luart.Config{}, err
	}
	return s.Config()
}

// ── precedence resolvers (env > base > defaults) ──

// Resolve builds a luart.Config from a config file overlaid by environment
// variables, applying the precedence env > file > defaults: the file at path is
// the base, then each env var (prefix+FIELD) that is set overrides that one
// field. An empty path skips the file (env > defaults). Fields set by neither
// source stay at the zero value, so luart.New applies its built-in defaults.
//
// Since: 2026-06-07
func Resolve(path, envPrefix string) (luart.Config, error) {
	var base Spec
	if path != "" {
		s, err := specFromFile(path)
		if err != nil {
			return luart.Config{}, err
		}
		base = s
	}
	env, err := envSpec(envPrefix)
	if err != nil {
		return luart.Config{}, err
	}
	return base.merge(env).Config()
}

// ResolveJSONString is Resolve with an in-memory JSON string as the base
// instead of a file: the precedence is reconstructed to env > jsonStr >
// defaults. An empty jsonStr makes it env > defaults.
//
// Since: 2026-06-07
func ResolveJSONString(jsonStr, envPrefix string) (luart.Config, error) {
	var base Spec
	if jsonStr != "" {
		s, err := specFromJSON([]byte(jsonStr), "<string>")
		if err != nil {
			return luart.Config{}, err
		}
		base = s
	}
	env, err := envSpec(envPrefix)
	if err != nil {
		return luart.Config{}, err
	}
	return base.merge(env).Config()
}
