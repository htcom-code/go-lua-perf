package main

import (
	"log"
	"sync"

	lua "github.com/htcom-code/lua-pure/lua"
)

// BytecodeCache is a thread-safe cache of compiled "bytecode prototypes".
// It caches the immutable *lua.Proto (independent of any LState), not an
// LFunction.
type BytecodeCache struct {
	mu    sync.RWMutex
	cache map[string]*lua.Proto
}

func NewBytecodeCache() *BytecodeCache {
	return &BytecodeCache{cache: make(map[string]*lua.Proto)}
}

// GetOrCompile returns the cached prototype or compiles a new one.
func (bc *BytecodeCache) GetOrCompile(key, sourceCode string) (*lua.Proto, error) {
	bc.mu.RLock()
	proto, ok := bc.cache[key]
	bc.mu.RUnlock()
	if ok {
		return proto, nil
	}

	bc.mu.Lock()
	defer bc.mu.Unlock()

	// Double-checking
	if proto, ok = bc.cache[key]; ok {
		return proto, nil
	}

	// Parse + compile to a bytecode prototype (*lua.Proto) in one call.
	proto, err := lua.CompileString(sourceCode, key)
	if err != nil {
		return nil, err
	}

	bc.cache[key] = proto
	return proto, nil
}

func main() {
	bc := NewBytecodeCache()
	luaSource := `print("Hello from cached Lua!")`

	// 1. Try the cache and fetch the prototype.
	proto, err := bc.GetOrCompile("hello_script", luaSource)
	if err != nil {
		log.Fatal(err)
	}

	// 2. Create a standalone VM (with the standard libraries) to run it.
	L := lua.NewState(lua.WithOpenLibs())
	defer L.Close()

	// 3. Execute the compiled prototype on this State.
	if _, err := L.CallProto(proto, 0); err != nil {
		log.Fatal(err)
	}
}
