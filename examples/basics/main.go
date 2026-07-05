// Example basics is the smallest end-to-end use of the luart library: register a
// script with a SourceLoader, create a Runtime, and Run a Lua function with an
// argument, reading back its return value.
package main

import (
	"context"
	"fmt"
	"log"

	lua "github.com/htcom-code/lua-pure/lua"

	luart "github.com/htcom-code/go-lua-perf"
)

// greet loads a one-function script and calls it with name, returning the Lua
// string result. It is the unit the test exercises.
func greet(name string) (string, error) {
	loader := luart.NewMapLoader() // in-memory SourceLoader (a DB/cache plugs in here)
	src := `function greet(who) return "hello, " .. who end`
	loader.Set("greeter", src, luart.HashVersion(src), "1.0.0")

	rt := luart.New(loader, luart.Config{MaxStates: 4})
	defer rt.Close()

	out, err := rt.Run(context.Background(), "greeter", "greet", lua.MkString(name))
	if err != nil {
		return "", err
	}
	if len(out) == 0 {
		return "", fmt.Errorf("script returned no values")
	}
	return out[0].Str(), nil
}

func main() {
	msg, err := greet("luart")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(msg) // hello, luart
}
