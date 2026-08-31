package main

import (
	"strings"
)

// entry is one gate and the blocks that run it.
//
// The block names are what make a failure actionable: knowing that a gate is
// missing is half of it, and knowing that the justfile does run it, in a recipe
// `just gate` never calls, is the other half.
type entry struct {
	gate   gate
	blocks []string
}

// scan walks blocks and returns every gate found, keyed by its printed form.
func scan(blocks []block) map[string]*entry {
	found := map[string]*entry{}
	for _, b := range blocks {
		for _, g := range gatesInBlock(b) {
			key := g.String()
			e := found[key]
			if e == nil {
				e = &entry{gate: g}
				found[key] = e
			}
			if !contains(e.blocks, b.name) {
				e.blocks = append(e.blocks, b.name)
			}
		}
	}
	return found
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// gatesInBlock walks one block's lines in order, carrying the working
// directory forward the way a shell does.
//
// Three shapes name a directory and they do not mean the same thing. `cd X` on
// a line of its own, or at the head of a line, moves the shell and everything
// after it stays moved. `(cd X && ...)` is a subshell and moves nothing beyond
// its own line. A `(` on a line of its own opens the same scope across several
// lines, which is how both ci.yml and the justfile run the Python examples, and
// without that rule the `cd "$dir"` inside it would leak an unknown directory
// over every gate that follows.
func gatesInBlock(b block) []gate {
	dir := b.dir
	if dir == "" {
		dir = rootDir
	}

	var out []gate
	var scopes []string // directories to restore when a `(` region closes

	for _, raw := range b.lines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// A subshell opened on a line of its own, and closed on one.
		if trimmed == "(" {
			scopes = append(scopes, dir)
			continue
		}
		if trimmed == ")" || strings.HasPrefix(trimmed, ") ") {
			if n := len(scopes); n > 0 {
				dir = scopes[n-1]
				scopes = scopes[:n-1]
			}
			continue
		}

		// The directory this one line runs in. A `cd` at the head of a
		// parenthesised line applies to that line alone.
		lineDir := dir
		if m := leadingCd.FindStringSubmatch(trimmed); m != nil {
			moved := joinDir(dir, normalizeDir(m[1]))
			lineDir = moved
			if b.oneShell && !strings.HasPrefix(trimmed, "(") {
				dir = moved
			}
		}

		out = append(out, gatesIn(trimmed, lineDir)...)
	}
	return out
}
