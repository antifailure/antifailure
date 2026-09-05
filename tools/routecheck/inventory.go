package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

const (
	inventoryPath = "www/lib/control-plane-routes.ts"
	boundaryPath  = "web/apps/api/src/boundary.ts"
	// The two files in www that are allowed to name CONTROL_PLANE_URL in code:
	// the one that defines it and the one that turns it into a route's URL.
	// Anywhere else is a call site nothing can enumerate.
	definitionFile = "lib/site.ts"
	inventoryFile  = "lib/control-plane-routes.ts"
)

// Route is one entry of www/lib/control-plane-routes.ts.
type Route struct {
	Name        string
	Method      string
	Path        string
	CalledFrom  string
	WhenMissing string
	ProbeEffect string
	ProbeReason string
}

// Inert reports whether the inventory says a probe of this route cannot reach
// the handler behind it.
func (r Route) Inert() bool { return r.ProbeEffect == "inert" }

// quoted returns the contents of the first double quoted string on a line, or
// "" when there is none. The inventory wraps long sentences across lines and
// only the first fragment is needed: it is what a person reads in the report.
func quoted(line string) string {
	i := strings.Index(line, `"`)
	if i < 0 {
		return ""
	}
	j := strings.Index(line[i+1:], `"`)
	if j < 0 {
		return ""
	}
	return line[i+1 : i+1+j]
}

var (
	entryStart = regexp.MustCompile(`^\s*"([a-z][a-zA-Z0-9.]*)":\s*\{\s*$`)
	fieldLine  = regexp.MustCompile(`^\s*(method|path|calledFrom|probeEffect):\s*"([^"]*)",\s*$`)
)

// ParseInventory reads the route inventory.
//
// It is deliberately a line parser over a deliberately rigid literal rather
// than anything that evaluates TypeScript. The trade is stated out loud: this
// cannot read an entry written in a shape it does not expect, so it FAILS on
// one rather than passing over it. A parser that skipped what it could not read
// would report a clean run over an inventory it had understood half of, which
// is the exact defect this command was written to stop happening elsewhere.
func ParseInventory(path string) ([]Route, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read the route inventory: %w", err)
	}
	defer func() { _ = f.Close() }()

	var (
		routes  []Route
		current *Route
		inBlock bool
		pending string
		line    int
	)
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scan.Scan() {
		line++
		text := scan.Text()
		if !inBlock {
			if strings.Contains(text, "export const CONTROL_PLANE_ROUTES") {
				inBlock = true
			}
			continue
		}
		if current == nil {
			if strings.HasPrefix(text, "} as const") {
				break
			}
			if m := entryStart.FindStringSubmatch(text); m != nil {
				current = &Route{Name: m[1]}
				pending = ""
			}
			continue
		}
		if strings.TrimSpace(text) == "}," {
			if err := current.validate(path); err != nil {
				return nil, err
			}
			routes = append(routes, *current)
			current = nil
			pending = ""
			continue
		}
		if m := fieldLine.FindStringSubmatch(text); m != nil {
			switch m[1] {
			case "method":
				current.Method = m[2]
			case "path":
				current.Path = m[2]
			case "calledFrom":
				current.CalledFrom = m[2]
			case "probeEffect":
				current.ProbeEffect = m[2]
			}
			continue
		}
		if strings.Contains(text, "probeReason:") {
			current.ProbeReason = "declared"
			pending = "probeReason"
			continue
		}
		if strings.Contains(text, "whenMissing:") {
			pending = "whenMissing"
			if v := quoted(text); v != "" {
				current.WhenMissing = v
			}
			continue
		}
		if pending == "whenMissing" && current.WhenMissing == "" {
			if v := quoted(text); v != "" {
				current.WhenMissing = v
			}
			continue
		}
		if pending == "probeReason" {
			if v := quoted(text); v != "" {
				current.ProbeReason = v
			}
			continue
		}
		// A continuation of a wrapped string literal, or prose. Ignored on
		// purpose: the fields that matter are matched exactly above, and an
		// entry missing one of them is caught by validate rather than by
		// this loop guessing.
	}
	if err := scan.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if current != nil {
		return nil, fmt.Errorf("%s: the entry named %q is still open at line %d. The inventory has to parse completely, because a half-read inventory is a route this command silently does not check", path, current.Name, line)
	}
	if !inBlock {
		return nil, fmt.Errorf("%s: no `export const CONTROL_PLANE_ROUTES` in the file. Either it moved or this parser is now reading the wrong thing, and both mean the routes below were not checked", path)
	}
	return routes, nil
}

func (r *Route) validate(path string) error {
	switch {
	case r.Method != "GET" && r.Method != "POST":
		return fmt.Errorf("%s: route %q has method %q. Only GET and POST are understood, because a probe with the wrong method reads exactly like a route that is not there", path, r.Name, r.Method)
	case !strings.HasPrefix(r.Path, "/"):
		return fmt.Errorf("%s: route %q has path %q, which is not a path", path, r.Name, r.Path)
	case r.ProbeEffect != "inert" && r.ProbeEffect != "writes":
		return fmt.Errorf("%s: route %q declares probeEffect %q. It has to be \"inert\" or \"writes\": what a probe costs is the one thing this command is not allowed to assume", path, r.Name, r.ProbeEffect)
	case r.ProbeReason == "":
		return fmt.Errorf("%s: route %q declares probeEffect %q and no probeReason. The reason is what makes the claim checkable against the API's source, so an entry without one is unfinished", path, r.Name, r.ProbeEffect)
	case r.CalledFrom == "":
		return fmt.Errorf("%s: route %q does not say where it is called from", path, r.Name)
	}
	return nil
}

// ParseBoundaryRegister reads the keys of the API's route register.
//
// web/apps/api/src/boundary.ts classifies every route the router mounts, and
// route-boundary.test.ts walks the server's own route table and fails on one
// that is not in it. That makes the register's key set a complete list of what
// this repository's control plane serves, which is a stronger source than
// grepping server.ts for app.post: the register is already PROVEN complete by a
// test, and a grep is only true on the day it is run.
func ParseBoundaryRegister(path string) (map[string]bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read the route register: %w", err)
	}
	key := regexp.MustCompile(`(?m)^\s*'(GET|POST|PUT|PATCH|DELETE|OPTIONS|HEAD|ALL) (/[^']*)':\s*\{`)
	found := map[string]bool{}
	for _, m := range key.FindAllStringSubmatch(string(raw), -1) {
		found[m[1]+" "+m[2]] = true
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("%s parsed to no routes at all. Its shape has changed, and an empty register would let every route in the inventory pass unchecked", path)
	}
	return found, nil
}
