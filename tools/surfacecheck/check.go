package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Class is what the release notes promise about a package.
type Class string

const (
	// Stable means breaking it costs a major version.
	Stable Class = "stable"
	// Unstable means it may change in a minor release. It is a real answer and
	// the common one; what is not an answer is leaving the question open.
	Unstable Class = "unstable"
)

// Classification is one line of engine/api/packages.txt.
type Classification struct {
	Class  Class
	Reason string
	Line   int
}

// Problem is one finding, with what to do about it. The remedy is on the
// finding rather than in the reader's head because the person who trips this
// gate is usually not the person who wrote it.
type Problem struct {
	Kind    string
	Message string
	Remedy  string
}

const (
	kindModules       = "Go modules this tool has no opinion about:"
	kindInventory     = "Importable packages that nothing classifies:"
	kindStale         = "Classified packages that no longer exist:"
	kindLeak          = "Stable exports naming a type from a package that is not stable:"
	kindIncompatible  = "Changes to the surface version 1 promised:"
	kindBaselineStale = "Baseline entries for packages that are no longer stable:"
)

const (
	remedyModules = "Add each one to moduleDirs in tools/surfacecheck/surface.go, saying whether its packages ship\n" +
		"and why. A module nobody listed is a module whose packages are classified nowhere, which is the\n" +
		"same silence one directory up."
	remedyInventory = "Add each one to " + packagesFile + " with a classification and a reason.\n" +
		"stable means breaking it costs a major version and it must appear in the release notes and in\n" +
		"reference/stability. unstable means it may change in a minor release, which is the ordinary\n" +
		"answer. Neither is the wrong answer here; leaving it unanswered is."
	remedyStale = "Remove the line from " + packagesFile + ". A classification for a package that is gone is a\n" +
		"promise about nothing, and it hides the day the package comes back under a different shape."
	remedyLeak = "A caller outside this module has to be able to NAME every type an exported signature mentions.\n" +
		"A type from engine/internal cannot be named there at all, and a type from a package that is not\n" +
		"stable can be named today and gone in the next minor release. Move the type into a stable\n" +
		"package, or stop naming it in the stable signature. This is the defect that made\n" +
		"provider.Database impossible to implement from outside the repository."
	remedyIncompatible = "This is what a major version costs. Adding an export is compatible and passes here; removing one,\n" +
		"changing a signature, or changing the value of an exported constant is not.\n" +
		"If version 2 is genuinely being cut, `go run ./tools/surfacecheck -update-baseline .` rewrites the\n" +
		"baseline. Running it for any other reason deletes the promise rather than keeping it."
	remedyBaselineStale = "A package cannot stop being stable inside a major version. Either put it back in the stable set in\n" +
		packagesFile + ", or this is a version 2 and the baseline is being cut afresh."
)

// ReadClasses parses engine/api/packages.txt.
func ReadClasses(path string) (map[string]Classification, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	out := map[string]Classification{}
	scanner := bufio.NewScanner(file)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Fields(text)
		if len(fields) < 3 {
			return nil, fmt.Errorf("%s:%d: a line is <stable|unstable> <import path> <reason>, and this one is %q", path, line, text)
		}
		class := Class(fields[0])
		if class != Stable && class != Unstable {
			return nil, fmt.Errorf("%s:%d: %q is not stable or unstable", path, line, fields[0])
		}
		reason := strings.TrimSpace(strings.Join(fields[2:], " "))
		// The reason is what somebody reads before promoting a package or
		// breaking one. A one word reason is the same as none.
		if len(reason) < 20 {
			return nil, fmt.Errorf("%s:%d: %s carries no reason worth reading, so nobody can judge a change to it", path, line, fields[1])
		}
		if _, dup := out[fields[1]]; dup {
			return nil, fmt.Errorf("%s:%d: %s is classified twice", path, line, fields[1])
		}
		out[fields[1]] = Classification{Class: class, Reason: reason, Line: line}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s classifies nothing, which is a parser fault rather than an empty repository", path)
	}
	return out, nil
}

// CheckInventory reports a package nothing classifies, and a classification for
// a package that is gone. Both directions, because a register that only grows
// becomes a list of exemptions nobody rereads.
func CheckInventory(packages []*Package, classes map[string]Classification) []Problem {
	var problems []Problem
	seen := map[string]bool{}
	for _, pkg := range packages {
		seen[pkg.ImportPath] = true
		if _, ok := classes[pkg.ImportPath]; !ok {
			problems = append(problems, Problem{
				Kind:    kindInventory,
				Message: fmt.Sprintf("%s  (%s)", pkg.ImportPath, pkg.Dir),
				Remedy:  remedyInventory,
			})
		}
	}
	var stale []string
	for path := range classes {
		if !seen[path] {
			stale = append(stale, path)
		}
	}
	sort.Strings(stale)
	for _, path := range stale {
		problems = append(problems, Problem{
			Kind:    kindStale,
			Message: fmt.Sprintf("%s  (%s:%d)", path, packagesFile, classes[path].Line),
			Remedy:  remedyStale,
		})
	}
	return problems
}

// CheckLeaks reports an exported thing in a stable package whose signature
// names a type from a package in this repository that is not stable.
func CheckLeaks(packages []*Package, classes map[string]Classification) []Problem {
	var problems []Problem
	for _, pkg := range packages {
		if classes[pkg.ImportPath].Class != Stable {
			continue
		}
		for _, entry := range pkg.Exports() {
			for _, ref := range dedupe(entry.Refs) {
				target := RefPackage(ref)
				if !Ours(target) || classes[target].Class == Stable {
					continue
				}
				why := "is not classified stable"
				if _, known := classes[target]; !known {
					why = "cannot be imported from outside this module at all"
				}
				problems = append(problems, Problem{
					Kind: kindLeak,
					Message: fmt.Sprintf("%s.%s names %s, which %s\n    %s:%d",
						pkg.ImportPath, entry.Key, ref, why, entry.Pos.Filename, entry.Pos.Line),
					Remedy: remedyLeak,
				})
			}
		}
	}
	return problems
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// ReadBaseline reads engine/api/v1.0.0.txt into "importpath\tkey" -> signature.
func ReadBaseline(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for i, line := range strings.Split(string(data), "\n") {
		text := strings.TrimSpace(line)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("%s:%d: expected three tab separated fields, got %q", path, i+1, line)
		}
		out[parts[0]+"\t"+parts[1]] = parts[2]
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s records nothing, so it would pass any surface at all", path)
	}
	return out, nil
}

// CheckCompatibility compares the stable packages against the baseline and
// returns the incompatible changes, plus a count of the compatible additions.
func CheckCompatibility(packages []*Package, classes map[string]Classification, baseline map[string]string) ([]Problem, int) {
	current := map[string]Entry{}
	stablePaths := map[string]bool{}
	for _, pkg := range packages {
		if classes[pkg.ImportPath].Class != Stable {
			continue
		}
		stablePaths[pkg.ImportPath] = true
		for _, entry := range pkg.Exports() {
			current[pkg.ImportPath+"\t"+entry.Key] = entry
		}
	}

	var problems []Problem
	additions := 0
	var keys []string
	for key := range baseline {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		importPath := key[:strings.Index(key, "\t")]
		name := key[strings.Index(key, "\t")+1:]
		if !stablePaths[importPath] {
			problems = append(problems, Problem{
				Kind:    kindBaselineStale,
				Message: importPath,
				Remedy:  remedyBaselineStale,
			})
			continue
		}
		entry, ok := current[key]
		if !ok {
			problems = append(problems, Problem{
				Kind:    kindIncompatible,
				Message: fmt.Sprintf("%s.%s is gone. v1.0.0 had:\n    %s", importPath, name, baseline[key]),
				Remedy:  remedyIncompatible,
			})
			continue
		}
		if entry.Sig != baseline[key] {
			problems = append(problems, Problem{
				Kind: kindIncompatible,
				Message: fmt.Sprintf("%s.%s changed shape\n    v1.0.0: %s\n       now: %s\n    %s:%d",
					importPath, name, baseline[key], entry.Sig, entry.Pos.Filename, entry.Pos.Line),
				Remedy: remedyIncompatible,
			})
		}
	}

	// Deduplicate the stale-baseline finding: one line per package, not one
	// per export in it.
	problems = dedupeProblems(problems)

	// An added export is a minor release. An added METHOD on an exported
	// interface is not, and the difference is who the surface is for.
	//
	// provider.Database and provider.Runtime exist to be IMPLEMENTED, outside
	// this repository, by somebody whose code we never see. A method added to
	// either one leaves every caller compiling and stops every implementation
	// compiling, on the release they take to get an unrelated fix. The change
	// reads as an addition in the diff and lands as a removal in their build,
	// which is exactly the class of thing that has to fail here rather than
	// there.
	var added []string
	for key := range current {
		if _, known := baseline[key]; known {
			continue
		}
		additions++
		added = append(added, key)
	}
	sort.Strings(added)
	for _, key := range added {
		if !strings.HasPrefix(current[key].Sig, "method ") {
			continue
		}
		owner := key[:strings.LastIndex(key, ".")]
		if !strings.HasSuffix(baseline[owner], " interface") {
			continue
		}
		problems = append(problems, Problem{
			Kind: kindIncompatible,
			Message: fmt.Sprintf("%s is new on an interface v1.0.0 published for implementing\n    %s\n    %s:%d",
				strings.Replace(key, "\t", ".", 1), current[key].Sig,
				current[key].Pos.Filename, current[key].Pos.Line),
			Remedy: remedyIncompatible,
		})
	}
	return problems, additions
}

func dedupeProblems(in []Problem) []Problem {
	seen := map[string]bool{}
	var out []Problem
	for _, p := range in {
		k := p.Kind + "\x00" + p.Message
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, p)
	}
	return out
}

// WriteBaseline records the stable packages' exported surface.
func WriteBaseline(path string, packages []*Package, classes map[string]Classification) error {
	var lines []string
	for _, pkg := range packages {
		if classes[pkg.ImportPath].Class != Stable {
			continue
		}
		for _, entry := range pkg.Exports() {
			lines = append(lines, entry.Line(pkg.ImportPath))
		}
	}
	sort.Strings(lines)

	var b strings.Builder
	b.WriteString(baselineHeader)
	for _, line := range lines {
		b.WriteString(line)
		b.WriteString("\n")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

const baselineHeader = `# The exported surface of the stable Go packages, as it stood at v1.0.0.
#
# Generated by tools/surfacecheck, compared by it on every run, and NOT
# regenerated as part of ` + "`just generate`" + `. That is the point of it: this file is
# what version 1 promised, so a change to the tree that disagrees with it is
# supposed to fail rather than to update it.
#
# Adding an export is compatible and does not appear here. Removing one,
# changing a signature, or changing the value of an exported constant fails, and
# the remedy is a major version rather than a rewrite of this file.
#
# One line per thing a caller can name, tab separated: import path, the name as
# a caller writes it, and the shape it has to keep. A constant longer than a
# line is recorded as a digest, which still changes when the value does.
`

// CheckModules reports a Go module in the repository that moduleDirs says
// nothing about.
//
// The layer above the package inventory. Listing packages is worth nothing if a
// whole module can appear and never be walked, and the failure would look
// exactly like a repository with no new packages in it.
func CheckModules(root string) ([]Problem, error) {
	found, err := FindModules(root)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("found no go.mod under %s, which means the walk is broken rather than the tree", root)
	}
	known := KnownModules()
	var problems []Problem
	for _, dir := range found {
		if !known[dir] {
			problems = append(problems, Problem{
				Kind:    kindModules,
				Message: dir,
				Remedy:  remedyModules,
			})
		}
	}
	for dir := range known {
		if !slices.Contains(found, dir) {
			problems = append(problems, Problem{
				Kind:    kindStale,
				Message: fmt.Sprintf("%s is listed in moduleDirs and holds no go.mod", dir),
				Remedy:  remedyStale,
			})
		}
	}
	sort.Slice(problems, func(i, j int) bool { return problems[i].Message < problems[j].Message })
	return problems, nil
}
