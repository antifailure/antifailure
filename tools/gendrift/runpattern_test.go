package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A generator scoped with -run is only as good as the name in its pattern.
//
// The masking generator ran its whole package, which starts Postgres and
// ClickHouse containers, to rewrite one page. Scoping it to the one test that
// reads -update-transforms fixed that and brought a hazard of its own: `go test
// -run` with a pattern that matches nothing exits 0, prints "no tests to run"
// and writes nothing. Rename TestTransformReferenceIsCurrent and the generator
// keeps succeeding, the page is never rewritten again, and -generate reports a
// generator that ran.
//
// So a scoped command has to name its test exactly, as `-run '^Name$'`, and the
// package it runs in has to declare that test.

var (
	// scopedRun is the one -run shape this can check: a single anchored name.
	scopedRun = regexp.MustCompile(`-run '\^([A-Za-z0-9_]+)\$'`)
	// testIn is where a ledger command runs its tests, as `cd <dir> && go test <pkg>`.
	testIn = regexp.MustCompile(`^cd (\S+) && go test (\./\S+)`)
)

// unmatchedRunPatterns returns one sentence for each ledger command whose -run
// names no test declared in its package, and how many commands carried a -run.
func unmatchedRunPatterns(root string, gens []generator) ([]string, int, error) {
	var problems []string
	scoped := 0
	for _, g := range gens {
		if !strings.Contains(g.command, "-run") {
			continue
		}
		scoped++
		name := scopedRun.FindStringSubmatch(g.command)
		where := testIn.FindStringSubmatch(g.command)
		if name == nil || where == nil {
			problems = append(problems, fmt.Sprintf(
				"%q carries a -run this cannot check. Write it as `cd <dir> && go test ./<pkg> -run '^TestName$'`.",
				g.command))
			continue
		}
		dir := filepath.Join(root, where[1], filepath.FromSlash(where[2]))
		files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
		if err != nil {
			return nil, 0, err
		}
		declared := regexp.MustCompile(`(?m)^func ` + name[1] + `\(`)
		found := false
		for _, f := range files {
			body, err := os.ReadFile(f)
			if err != nil {
				return nil, 0, err
			}
			if declared.Match(body) {
				found = true
				break
			}
		}
		if !found {
			problems = append(problems, fmt.Sprintf(
				"%q runs only %s, and no _test.go file in %s declares it, so the generator runs nothing and writes nothing.",
				g.command, name[1], filepath.ToSlash(filepath.Join(where[1], where[2]))))
		}
	}
	return problems, scoped, nil
}

func TestEveryRunPatternInTheLedgerNamesATestThatExists(t *testing.T) {
	problems, scoped, err := unmatchedRunPatterns(filepath.Join("..", ".."), ledger)
	require.NoError(t, err)
	// The masking generator is scoped today. Finding none means this stopped
	// recognising the commands it exists to check, not that they are all fine.
	require.GreaterOrEqual(t, scoped, 1, "no ledger command carries a -run, so nothing here was checked")
	require.Empty(t, problems)
}

func TestARunPatternThatNamesNoTestIsRefused(t *testing.T) {
	root := t.TempDir()
	write(t, root, "engine/internal/x/x_test.go",
		"package x\n\nimport \"testing\"\n\nfunc TestTheRenamedOne(t *testing.T) {}\n")

	problems, _, err := unmatchedRunPatterns(root, []generator{
		{"cd engine && go test ./internal/x -run '^TestTheOriginalName$' -update-x", nil},
	})
	require.NoError(t, err)
	require.Len(t, problems, 1, "a -run naming a test the package does not declare was accepted")
	require.Contains(t, problems[0], "TestTheOriginalName")
	require.Contains(t, problems[0], "engine/internal/x")

	// The same package, named correctly, is accepted, so the refusal above is
	// about the name and not about the fixture.
	problems, _, err = unmatchedRunPatterns(root, []generator{
		{"cd engine && go test ./internal/x -run '^TestTheRenamedOne$' -update-x", nil},
	})
	require.NoError(t, err)
	require.Empty(t, problems)

	// An unanchored pattern is refused even when it spells a declared test,
	// because 'TestTheRenamedOne' also runs TestTheRenamedOneAgain and this
	// cannot say which of them writes the file.
	problems, _, err = unmatchedRunPatterns(root, []generator{
		{"cd engine && go test ./internal/x -run 'TestTheRenamedOne' -update-x", nil},
	})
	require.NoError(t, err)
	require.Len(t, problems, 1, "an unanchored -run was accepted without being checked")
}
