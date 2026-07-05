package luart

import "fmt"

// Validate checks the numeric/duration fields of a Config. It is used by the
// config loaders (luartconfig) and is safe to call directly. Libs, Metrics,
// and Logger are code-injected and not validated here.
// Since: 2026-06-07
func (c Config) Validate() error {
	if c.MaxStates < 0 {
		return fmt.Errorf("luart: MaxStates must be >= 0, got %d", c.MaxStates)
	}
	if c.IdleTTL < 0 {
		return fmt.Errorf("luart: IdleTTL must be >= 0, got %s", c.IdleTTL)
	}
	if c.JanitorInterval < 0 {
		return fmt.Errorf("luart: JanitorInterval must be >= 0, got %s", c.JanitorInterval)
	}
	if c.ExecTimeout < 0 {
		return fmt.Errorf("luart: ExecTimeout must be >= 0, got %s", c.ExecTimeout)
	}
	return nil
}
