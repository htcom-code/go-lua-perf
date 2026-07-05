package luart

import (
	"testing"
	"time"
)

// TestConfigValidate verifies Validate accepts sane values and rejects negative
// numeric/duration fields.
// Since: 2026-06-07
func TestConfigValidate(t *testing.T) {
	ok := []Config{
		{},
		{MaxStates: 8, IdleTTL: time.Minute, JanitorInterval: time.Second},
		{MemoryBudgetBytes: 1 << 20},
	}
	for i, c := range ok {
		if err := c.Validate(); err != nil {
			t.Fatalf("ok[%d] should validate: %v", i, err)
		}
	}

	bad := []Config{
		{MaxStates: -1},
		{IdleTTL: -time.Second},
		{JanitorInterval: -time.Second},
	}
	for i, c := range bad {
		if err := c.Validate(); err == nil {
			t.Fatalf("bad[%d] should fail validation: %+v", i, c)
		}
	}
}
