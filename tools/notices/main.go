// Command notices generates the third party attribution file.
//
// Generated from what is actually linked rather than maintained by hand,
// because a list maintained by hand attributes the wrong people the moment
// somebody adds a dependency and forgets. MIT does not require a NOTICE file;
// the Apache licensed dependencies still require attribution, and getting that
// wrong is the kind of quiet unfairness nobody notices until it matters.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

type module struct {
	Path     string
	Version  string
	Indirect bool
	Main     bool
}

func main() {
	mods, err := linked()
	if err != nil {
		fmt.Fprintln(os.Stderr, "notices:", err)
		os.Exit(1)
	}

	var b strings.Builder
	b.WriteString("# Third party notices\n\n")
	b.WriteString("Antifailure is MIT licensed, except for the `ee/` directory, which is\n")
	b.WriteString("licensed under the Antifailure Enterprise License. This file lists the\n")
	b.WriteString("dependencies the binary links, and is generated from them rather than\n")
	b.WriteString("maintained by hand, so it cannot go stale.\n\n")
	b.WriteString("Run `go run ./tools/notices` to regenerate it.\n\n")

	fmt.Fprintf(&b, "## Go modules (%d)\n\n", len(mods))
	for _, m := range mods {
		fmt.Fprintf(&b, "- `%s` %s\n", m.Path, m.Version)
	}
	b.WriteString("\n## Node packages\n\n")
	b.WriteString("The agent runner depends on Playwright, which is Apache 2.0 licensed,\n")
	b.WriteString("and on its own transitive dependencies. Run `npm ls --all` inside\n")
	b.WriteString("`runner/` for the full tree of whatever version is installed.\n")

	fmt.Print(b.String())
}

// linked returns the modules the engine actually builds against.
//
// The build list rather than the requirement list, because a module that is
// required and not linked is not something the binary distributes, and
// attributing it would be as wrong as omitting one that is.
func linked() ([]module, error) {
	cmd := exec.Command("go", "list", "-deps", "-json", "./cmd/af")
	cmd.Dir = "engine"
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing dependencies: %w", err)
	}

	seen := map[string]module{}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for {
		var pkg struct {
			Standard bool
			Module   *module
		}
		if err := dec.Decode(&pkg); err != nil {
			break
		}
		// The standard library needs no attribution, and the main module is
		// this project.
		if pkg.Standard || pkg.Module == nil || pkg.Module.Main {
			continue
		}
		seen[pkg.Module.Path] = *pkg.Module
	}

	mods := make([]module, 0, len(seen))
	for _, m := range seen {
		mods = append(mods, m)
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].Path < mods[j].Path })
	return mods, nil
}
