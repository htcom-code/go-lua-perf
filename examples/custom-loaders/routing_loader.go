package main

import (
	"fmt"
	"strings"
)

// RoutingLoader (hybrid #2) dispatches by a key prefix to one of several backends,
// so a single Runtime can mix sources: "file:checkout" reads from disk, "db:pricing"
// from a database, "mem:healthcheck" from memory. The prefix is part of the key the
// caller passes to Run, so the chosen backend is explicit and stable.
//
// Since: 2026-06-08
type RoutingLoader struct {
	routes map[string]SourceLoader // prefix (without ':') → backend
}

// NewRoutingLoader builds a router from prefix→backend pairs, e.g.
// NewRoutingLoader(map[string]SourceLoader{"file": fl, "db": dbl, "mem": ml}).
func NewRoutingLoader(routes map[string]SourceLoader) *RoutingLoader {
	return &RoutingLoader{routes: routes}
}

// Load splits key on the first ':' into prefix and the backend-local key, then
// delegates. An unknown or missing prefix is an error. The backend receives the
// key WITHOUT the prefix (e.g. "file:checkout" → FileLoader.Load("checkout")).
func (l *RoutingLoader) Load(key string) (src, version, displayVersion string, err error) {
	prefix, rest, ok := strings.Cut(key, ":")
	if !ok {
		return "", "", "", fmt.Errorf("luart: key %q has no backend prefix (want \"<prefix>:<key>\")", key)
	}
	backend, ok := l.routes[prefix]
	if !ok {
		return "", "", "", fmt.Errorf("luart: no backend for prefix %q in key %q", prefix, key)
	}
	return backend.Load(rest)
}
