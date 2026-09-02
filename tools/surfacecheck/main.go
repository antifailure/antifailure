// Command surfacecheck holds the Go half of the 1.0.0 stability promise to
// what the code actually is.
//
// The promise, from the release notes and from reference/stability, is two
// sentences. `engine/pkg/provider` and `engine/pkg/schema` are stable, along
// with the conformance suite that decides whether an implementation of them is
// conformant. Everything else is free to change in a minor release, and
// everything under `engine/internal` cannot be imported from outside the module
// at all.
//
// The second half of that is the toolchain's and needs nothing from anybody:
// the Go compiler refuses an import of a path containing an `internal` element
// from outside the subtree rooted at its parent, so no module but the engine
// can import engine/internal. That was verified rather than assumed, from the
// tools module, which shares a workspace with the engine and is still refused:
//
//	use of internal package github.com/antifailure/antifailure/engine/internal/secrets not allowed
//
// The FIRST half had nothing behind it, and three different things could go
// wrong in silence.
//
// A NEW IMPORTABLE PACKAGE. `engine/pkg/whatever` added in a minor release is
// importable by everybody the moment it lands, and whether it is covered by the
// promise is decided by nobody. So every importable package in the shipped
// modules is listed in engine/api/packages.txt with a classification and a
// reason, and one that is not listed fails here rather than defaulting to
// invisible. A whole new module is the same failure one layer up, so every
// go.mod in the repository has to be listed too, saying whether its packages
// ship.
//
// AN INCOMPATIBLE CHANGE TO A STABLE PACKAGE. engine/api/v1.0.0.txt is the
// exported surface of the stable packages as it stood at the tag. Adding to it
// is a minor release and passes. Removing an export, or changing a signature,
// or changing the value of an exported constant, is what a major version costs
// and it fails here.
//
// A TYPE THAT LEAKS. This is the one that was live. `provider.Database` is the
// interface a provider outside this repository implements, and its ConnString
// method returned `secrets.Value` from `engine/internal/secrets`. Both halves
// of the promise were kept and the combination was still broken: an out of
// module implementation cannot name the return type, and cannot import the
// package that would let it, so the interface the release notes call an
// integration surface could not be implemented from outside at all. It compiles
// here, it reviews as correct, and it fails on the first line of the first real
// provider anybody writes. Nothing in the tree said so, and the tag would have
// frozen it for the whole of version 1.
//
// So the third check reads every exported signature in a stable package and
// resolves every qualified identifier in it through that file's own imports.
// A reference to a package in this repository that is not itself stable is the
// leak, and it is reported with the file and line.
//
// WHAT THIS IS NOT. It reads the syntax tree, not the type checker, and that
// cuts both ways. A type that reaches a stable signature without being written
// down in it, through an unexported alias in the same package, is not something
// this sees. In the other direction it compares signatures as text, so moving a
// type to a differently named package and leaving an alias behind is a change
// here even though the compiler sees the same type; the report prints both
// spellings so a reader can tell that apart in a second. Erring strict is the
// right way round for a promise, and the trade buys a gate that runs in a
// second on any checkout with no build at all. Being approximate is fine here
// for the same reason it is fine in gatecheck; being silent is not.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	packagesFile = "engine/api/packages.txt"
	baselineFile = "engine/api/v1.0.0.txt"
)

func main() {
	updateBaseline := flag.Bool("update-baseline", false,
		"rewrite the baseline from the current tree. Legitimate at a major version and nowhere else: "+
			"the baseline is what version 1 promised, so regenerating it to make a failure go away deletes the promise.")
	flag.Parse()

	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}

	code, err := run(root, *updateBaseline, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "surfacecheck: %v\n", err)
		os.Exit(2)
	}
	os.Exit(code)
}

func run(root string, updateBaseline bool, out *os.File) (int, error) {
	packages, err := Load(root)
	if err != nil {
		return 0, err
	}
	if len(packages) == 0 {
		return 0, fmt.Errorf("found no importable package under %s, which means the walk is broken rather than the tree", root)
	}

	classes, err := ReadClasses(filepath.Join(root, packagesFile))
	if err != nil {
		return 0, err
	}

	problems, err := CheckModules(root)
	if err != nil {
		return 0, err
	}
	problems = append(problems, CheckInventory(packages, classes)...)
	problems = append(problems, CheckLeaks(packages, classes)...)

	if updateBaseline {
		if len(problems) > 0 {
			report(out, problems)
			fmt.Fprintln(out, "\nThe baseline was not rewritten: the surface it would record is not one that passes the checks above.")
			return 1, nil
		}
		if err := WriteBaseline(filepath.Join(root, baselineFile), packages, classes); err != nil {
			return 0, err
		}
		fmt.Fprintf(out, "surfacecheck: wrote %s\n", baselineFile)
		return 0, nil
	}

	baseline, err := ReadBaseline(filepath.Join(root, baselineFile))
	if err != nil {
		return 0, err
	}
	compat, additions := CheckCompatibility(packages, classes, baseline)
	problems = append(problems, compat...)

	if len(problems) > 0 {
		report(out, problems)
		return 1, nil
	}

	stable := 0
	exported := 0
	for _, pkg := range packages {
		if classes[pkg.ImportPath].Class == Stable {
			stable++
			exported += len(pkg.Exports())
		}
	}
	fmt.Fprintf(out, "surfacecheck: %d importable packages, %d stable holding %d exports, %d additions since v1.0.0\n",
		len(packages), stable, exported, additions)
	return 0, nil
}

func report(out *os.File, problems []Problem) {
	byKind := map[string][]Problem{}
	var kinds []string
	for _, p := range problems {
		if _, seen := byKind[p.Kind]; !seen {
			kinds = append(kinds, p.Kind)
		}
		byKind[p.Kind] = append(byKind[p.Kind], p)
	}
	fmt.Fprintf(out, "surfacecheck: %d problems\n", len(problems))
	for _, kind := range kinds {
		fmt.Fprintf(out, "\n%s\n", kind)
		list := byKind[kind]
		sort.Slice(list, func(i, j int) bool { return list[i].Message < list[j].Message })
		for _, p := range list {
			fmt.Fprintf(out, "  %s\n", p.Message)
		}
		fmt.Fprintf(out, "\n  %s\n", strings.ReplaceAll(byKind[kind][0].Remedy, "\n", "\n  "))
	}
}
