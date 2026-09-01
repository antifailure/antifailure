package manifest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Which fields of the github block anything actually reads.
//
// All four are validated, defaulted, and printed by af explain, and none of
// them changes what happens. Setting `fork_policy: always` does not make a fork
// run. Removing `close` from `teardown_on` does not stop a closed pull request
// being torn down. That is a real gap and it is written down in
// docs/reference/manifest.md, with the reason: the hosted control plane never
// reads the customer's manifest, because a control plane that read it would
// have to fetch the repository, and that is the boundary the whole product
// rests on.
//
// This test exists so the gap cannot grow quietly in either direction. A new
// field added to the block with no classification fails here, and a field that
// gains a real reader fails here too, which is the direction that matters:
// somebody wiring one up and forgetting to correct a documentation table that
// says nothing reads it would leave the page wrong in the more dangerous way,
// telling a reader their setting is inert when it is not.
//
// WHAT IT CANNOT SEE, written next to the assertion rather than in a report: it
// reads this Go module and nothing else. A reader in the control plane, in the
// runner, or in a workflow file is invisible to it. Those are all across a
// process boundary from the manifest, so a reader there would have to be fed
// the value by this module, which is a reference here and would be caught.

// displayOnly are the fields nothing acts on, with what happens instead.
//
// Keyed by the Go field name rather than the YAML key, so a rename that misses
// this map fails to compile against the reflection below rather than silently
// exempting a field that no longer exists.
var displayOnly = map[string]string{
	"Mode": "printed by af explain; whether a control plane is involved is decided by " +
		"the workflow, which has the address of one or does not",
	"Comment": "printed by af explain; the comment is maintained by the control plane when " +
		"there is one and by the workflow's own step when there is not",
	"ForkPolicy": "printed by af explain; the control plane always requires a maintainer to " +
		"approve the exact commit, which is label behaviour, because it cannot read this file",
	"TeardownOn": "printed by af explain; teardown is always asked for on close, merge, " +
		"supersession and timeout, because a run that is stopping leaks its environment " +
		"if nothing cleans up after it",
}

// Where a field being NAMED does not count as being read.
//
// normalize.go writes the defaults and explain.go prints them, so a reference
// in either is exactly the "validated, defaulted and displayed" state this
// whole test is about. Counting those would make every field look live.
var notAReader = map[string]bool{
	"normalize.go": true,
	"explain.go":   true,
}

func TestEveryGitHubFieldIsEitherReadOrRecordedAsDisplayOnly(t *testing.T) {
	fields := reflect.TypeOf(schema.GitHub{})
	require.Greater(t, fields.NumField(), 0, "the github block has no fields; this test is looking at the wrong type")

	readers := gitHubFieldReaders(t)

	var problems []string
	for i := 0; i < fields.NumField(); i++ {
		name := fields.Field(i).Name
		where := readers[name]
		_, exempt := displayOnly[name]

		switch {
		case len(where) > 0 && exempt:
			problems = append(problems, name+" is recorded as display only and is read by "+
				strings.Join(where, ", ")+". Correct the table in docs/reference/manifest.md, "+
				"which currently tells a reader this setting does nothing.")
		case len(where) == 0 && !exempt:
			problems = append(problems, name+" is read by nothing and is not recorded as display "+
				"only. Wire it up, or add it to displayOnly with what happens instead, and put it "+
				"in the table in docs/reference/manifest.md.")
		}
	}

	// And the other direction: an exemption for a field that no longer exists
	// is an exemption nobody will ever remove.
	present := map[string]bool{}
	for i := 0; i < fields.NumField(); i++ {
		present[fields.Field(i).Name] = true
	}
	for name := range displayOnly {
		if !present[name] {
			problems = append(problems, name+" is recorded as display only and is not a field of schema.GitHub")
		}
	}

	sort.Strings(problems)
	require.Empty(t, problems, "%s", strings.Join(problems, "\n  "))
}

// gitHubFieldReaders finds every file that reads a field of the github block as
// a selector, ignoring the two files that write the defaults and print them.
func gitHubFieldReaders(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	root := filepath.Join("..", "..")

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "testdata" || info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if notAReader[filepath.Base(path)] {
			return nil
		}

		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			// A file this cannot parse is a file it cannot speak for, and
			// saying so beats reporting an absence it did not establish.
			t.Fatalf("could not parse %s: %v", path, parseErr)
		}

		// `.GitHub.Field`, which is the only shape a reader can have: the block
		// is reached through the manifest and never held in a variable of its
		// own anywhere in this tree.
		ast.Inspect(file, func(n ast.Node) bool {
			outer, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			inner, ok := outer.X.(*ast.SelectorExpr)
			if !ok || inner.Sel.Name != "GitHub" {
				return true
			}
			rel, _ := filepath.Rel(root, path)
			out[outer.Sel.Name] = append(out[outer.Sel.Name], rel)
			return true
		})
		return nil
	})
	require.NoError(t, err)
	return out
}
