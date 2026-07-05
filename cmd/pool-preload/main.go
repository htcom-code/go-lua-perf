package main

import (
	"fmt"
	"log"
	"sync"

	lua "github.com/htcom-code/lua-pure/lua"
)

var luaVMPool sync.Pool

func init() {
	// 1. The Lua source to run — defines the global function process.
	luaSource := `
		function process(heavyData)
			return "Processed: " .. heavyData
		end
	`

	// 2. Compile to bytecode exactly once (shared by all pooled States — safe, immutable).
	proto, err := compileLuaSource("process_script", luaSource)
	if err != nil {
		log.Fatalf("lua compile failed: %v", err)
	}

	// 3. Initialize the sync.Pool.
	luaVMPool = sync.Pool{
		New: func() interface{} {
			L := lua.NewState(lua.WithOpenLibs())

			// Run the chunk once → the global process gets defined (= preload).
			if _, err := L.CallProto(proto, 0); err != nil {
				log.Fatalf("preload failed: %v", err)
			}

			return L
		},
	}
}

// compileLuaSource converts text into a bytecode prototype.
func compileLuaSource(key, source string) (*lua.Proto, error) {
	return lua.CompileString(source, key)
}

// ExecuteLuaTask calls the preloaded process function in a goroutine-safe way.
func ExecuteLuaTask(inputParam string) (string, error) {
	// 1. Get a (preloaded) VM instance from the pool.
	L := luaVMPool.Get().(*lua.LState)

	// 2. Return it after use (global mutations persist; Call leaves no residual
	//    data stack to clear).
	defer luaVMPool.Put(L)

	// 3. Fetch the preloaded global function (process itself, not the chunk).
	fn := L.GetGlobal("process")

	// 4. Pass the parameter and run, requesting one result.
	rets, err := L.Call(fn, []lua.Value{lua.MkString(inputParam)}, 1)
	if err != nil {
		return "", err
	}

	// 5. Extract the result.
	return rets[0].Str(), nil
}

func main() {
	var wg sync.WaitGroup
	for i := 1; i <= 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			data := fmt.Sprintf("Data-%d", id)
			result, err := ExecuteLuaTask(data)
			if err != nil {
				log.Printf("[%d] error: %v", id, err)
				return
			}
			fmt.Printf("[%d] result: %s\n", id, result)
		}(i)
	}
	wg.Wait()
}
