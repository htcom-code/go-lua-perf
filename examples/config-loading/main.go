// Example config-loading shows building a luart.Config from external sources via
// the luartconfig subpackage: a base JSON string overlaid by environment
// variables, using the precedence resolver (env > base > defaults). The file
// loaders (Load/LoadJSON/LoadYAML) work the same way against a path.
package main

import (
	"fmt"
	"log"
	"os"

	luart "github.com/htcom-code/go-lua-perf"
	"github.com/htcom-code/go-lua-perf/luartconfig"
)

// resolveDemo loads a base JSON config, overrides one field via an env var, and
// returns the resolved Config. Precedence is env > json > defaults: the env var
// overrides only the field it sets, leaving the rest to the JSON base.
func resolveDemo() (luart.Config, error) {
	const baseJSON = `{"maxStates": 8, "idleTTL": "5m"}`

	os.Setenv("LUART_MAX_STATES", "16") // simulate a deployment override
	defer os.Unsetenv("LUART_MAX_STATES")

	return luartconfig.ResolveJSONString(baseJSON, "LUART_")
}

func main() {
	cfg, err := resolveDemo()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("MaxStates (env override): %d\n", cfg.MaxStates) // 16
	fmt.Printf("IdleTTL   (from JSON):    %s\n", cfg.IdleTTL)   // 5m0s

	// The resolved Config plugs straight into luart.New.
	rt := luart.New(luart.NewMapLoader(), cfg)
	defer rt.Close()
	fmt.Println("config applied to a new Runtime")
}
