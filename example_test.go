package luart_test

import (
	"context"
	"fmt"

	lua "github.com/htcom-code/lua-pure/lua"

	luart "github.com/htcom-code/go-lua-perf"
)

// ExampleRuntime shows the basic public API: load a script from a SourceLoader,
// run it from the pooled runtime, and clean up.
func ExampleRuntime() {
	loader := luart.NewMapLoader()
	src := `function greet(name) return "hello, " .. name end`
	loader.Set("greeter", src, luart.HashVersion(src), "1.0.0")

	rt := luart.New(loader, luart.Config{MaxStates: 4})
	defer rt.Close()

	out, err := rt.Run(context.Background(), "greeter", "greet", lua.MkString("luart"))
	if err != nil {
		panic(err)
	}
	fmt.Println(out[0].Str())
	// Output: hello, luart
}

// ExampleRuntime_notify shows notification-driven hot reload: after the source
// changes, Notify drops the pool and the next Run uses the new version.
func ExampleRuntime_notify() {
	loader := luart.NewMapLoader()
	v1 := `function f() return "v1" end`
	loader.Set("s", v1, luart.HashVersion(v1), "1.0.0")

	rt := luart.New(loader, luart.Config{MaxStates: 4})
	defer rt.Close()
	ctx := context.Background()

	out, _ := rt.Run(ctx, "s", "f")
	fmt.Println(out[0].Str())

	v2 := `function f() return "v2" end`
	loader.Set("s", v2, luart.HashVersion(v2), "2.0.0")
	rt.Notify("s", luart.HashVersion(v2), "2.0.0")

	out, _ = rt.Run(ctx, "s", "f")
	fmt.Println(out[0].Str())
	// Output:
	// v1
	// v2
}
