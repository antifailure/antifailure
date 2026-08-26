package mockpack

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// Built in packs ship with the engine so that a common provider works with no
// setup at all. They are the same JSON format anybody can write, rather than a
// privileged path, which is the difference between an extension mechanism and
// a hardcoded list with a plugin story bolted on.

//go:embed packs/*.json
var builtinFS embed.FS

// Builtin returns the packs that ship with the engine.
func Builtin() ([]Pack, error) {
	entries, err := fs.ReadDir(builtinFS, "packs")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	// Sorted, so the order two machines load them in is the same and a
	// specificity tie resolves identically.
	sort.Strings(names)

	out := make([]Pack, 0, len(names))
	for _, name := range names {
		body, readErr := builtinFS.ReadFile("packs/" + name)
		if readErr != nil {
			return nil, readErr
		}
		p, parseErr := Parse(body)
		if parseErr != nil {
			return nil, fmt.Errorf("the built in pack %s is not valid: %w", name, parseErr)
		}
		out = append(out, p)
	}
	return out, nil
}

// BuiltinJSON returns the raw pack files, for handing to the sidecar.
func BuiltinJSON() (map[string]string, error) {
	entries, err := fs.ReadDir(builtinFS, "packs")
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		body, readErr := builtinFS.ReadFile("packs/" + e.Name())
		if readErr != nil {
			return nil, readErr
		}
		out[strings.TrimSuffix(e.Name(), ".json")] = string(body)
	}
	return out, nil
}
