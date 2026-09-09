package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// repoWithLedger is a git repository carrying every path the ledger claims,
// committed, so that a test can dirty exactly one of them and nothing else.
//
// The whole ledger has to exist because checkLedger refuses a tree missing one
// of its paths, and that refusal is itself under test below. A fixture that
// quietly satisfied it would make every other case here pass for the wrong
// reason.
func repoWithLedger(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	}
	run("init", "-q")

	for _, g := range ledger {
		for _, p := range g.paths {
			// A ledger entry is a file or a directory and the tool must
			// accept either, so a path with no extension is made a directory
			// with one file under it. That is what schemadoc and the hud
			// golden frames actually are.
			full := filepath.Join(root, filepath.FromSlash(p))
			if filepath.Ext(p) == "" {
				require.NoError(t, os.MkdirAll(full, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(full, "one.txt"), []byte("one\n"), 0o644))
				continue
			}
			require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
			require.NoError(t, os.WriteFile(full, []byte("generated\n"), 0o644))
		}
	}
	run("add", "-A")
	run("commit", "-qm", "the generated tree")
	return root
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
}

// A clean tree passes, and says how much it looked at.
//
// The count is in the message on purpose: a gate that prints "ok" having
// examined nothing reads the same as one that examined everything, and this
// repository has shipped both.
func TestACleanTreeReportsHowManyPathsItCompared(t *testing.T) {
	root := repoWithLedger(t)
	var out bytes.Buffer
	require.NoError(t, run(root, true, &out))
	require.Contains(t, out.String(), "generated paths match their generators")
	require.Contains(t, out.String(), strconv.Itoa(countPaths()))
}

// The whole point: the message names the ONE command to run.
//
// pages.gen.go is the specific file eight pull requests drifted on, and
// docsembed is the second of thirteen generators. A tool that answered
// "run just generate" would send a lane to several minutes of npm and Docker
// for a one second fix.
func TestADriftedFileNamesTheGeneratorThatOwnsIt(t *testing.T) {
	root := repoWithLedger(t)
	write(t, root, "engine/internal/docs/pages.gen.go", "drifted\n")

	var out bytes.Buffer
	err := run(root, true, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "engine/internal/docs/pages.gen.go")
	require.Contains(t, err.Error(), "go run ./tools/docsembed")
	require.NotContains(t, err.Error(), "go run ./tools/proxysrc")
}

// The pairing a reader of the justfile would get wrong.
//
// stream.register.json sits beside events.v1.json in every list in this
// repository and a different command writes each. If this ever reports
// -update-schema for the register, the table has been rewritten from memory
// rather than from the generators.
func TestTheEventRegisterAndTheEventSchemaHaveDifferentGenerators(t *testing.T) {
	root := repoWithLedger(t)
	write(t, root, "engine/internal/events/stream.register.json", "drifted\n")

	var out bytes.Buffer
	err := run(root, true, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "go run ./tools/eventcheck -freeze .")
	require.NotContains(t, err.Error(), "-update-schema")
}

// A generator writing a file that was never committed is drift too.
//
// `git diff` cannot see this case at all, because an untracked file is not in
// the index, so a comparison built on diff alone passes and the artifact never
// reaches the tree.
func TestANewFileAGeneratorWroteIsDriftEvenThoughItIsUntracked(t *testing.T) {
	root := repoWithLedger(t)
	write(t, root, "docs/src/content/docs/reference/schemas/manifest-v2.md", "new\n")

	var out bytes.Buffer
	err := run(root, true, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "manifest-v2.md")
	require.Contains(t, err.Error(), "go run ./tools/schemadoc .")
}

// A directory entry owns what is under it and not what merely starts the same.
//
// Without the separator, `docs/.../reference/schemas` would claim a sibling
// called `schemas-old`, and a real edit would be reported as generated drift
// with a command that does not fix it.
func TestADirectoryEntryDoesNotClaimASiblingWithTheSamePrefix(t *testing.T) {
	root := repoWithLedger(t)
	write(t, root, "docs/src/content/docs/reference/schemas-old/note.md", "mine\n")

	var out bytes.Buffer
	err := run(root, true, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no generator in tools/gendrift")
	require.NotContains(t, err.Error(), "go run ./tools/schemadoc .")
}

// strict is the difference between the two callers, and it has to be real.
//
// CI runs on a clean checkout, where an unowned change means a generator has
// started writing somewhere nothing watches. A developer's gate runs in the
// middle of an edit, where the same change is the edit. If strict did nothing,
// `just gate` would fail on every uncommitted line and would be turned off.
func TestAnUnownedChangeFailsOnlyInStrictMode(t *testing.T) {
	root := repoWithLedger(t)
	write(t, root, "engine/internal/env/env.go", "somebody is editing this\n")

	var strictOut bytes.Buffer
	require.Error(t, run(root, true, &strictOut))

	var looseOut bytes.Buffer
	require.NoError(t, run(root, false, &looseOut))
	require.Contains(t, looseOut.String(), "match their generators")
}

// A ledger row pointing at nothing is a refusal, never a quiet pass.
//
// This is the failure mode of every check in this repository that went blind:
// the artifact was renamed, the row was not, and the comparison went on
// printing a number about a file it could no longer see.
func TestALedgerPathMissingFromTheTreeIsARefusal(t *testing.T) {
	root := repoWithLedger(t)
	require.NoError(t, os.Remove(filepath.Join(root, "engine/internal/docs/pages.gen.go")))

	var out bytes.Buffer
	err := run(root, true, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nothing compares it")
	require.Contains(t, err.Error(), "pages.gen.go")
}

// The ledger describes THIS repository, not a fixture.
//
// Everything above runs against a tree the test built, so all of it would keep
// passing if the ledger drifted from the real one. This is the assertion that
// notices, and it is the reason the tool is safe to put in front of the gate
// that eleven pull requests failed.
func TestEveryLedgerPathExistsInThisRepository(t *testing.T) {
	root := "../.."
	for _, g := range ledger {
		for _, p := range g.paths {
			_, err := os.Stat(filepath.Join(root, filepath.FromSlash(p)))
			require.NoError(t, err, "%s, claimed by `%s`", p, g.command)
		}
	}
	require.Greater(t, countPaths(), 15)
}

// The local gate reports the drifted artifact and stays quiet about your work.
//
// This is the case that makes the two guards on `strict` different. A
// developer with a stale generated file AND uncommitted edits is the ordinary
// state of `just gate`, and it must name the artifact and say nothing about
// the edits. Reporting the edits is how a gate becomes noise and then becomes
// skipped, which is the reason none of the eleven pull requests had run it.
func TestTheLocalGateNamesTheDriftedArtifactAndNotYourEdits(t *testing.T) {
	root := repoWithLedger(t)
	write(t, root, "engine/internal/docs/pages.gen.go", "drifted\n")
	write(t, root, "engine/internal/env/env.go", "somebody is editing this\n")

	var out bytes.Buffer
	err := run(root, false, &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "go run ./tools/docsembed")
	require.NotContains(t, err.Error(), "engine/internal/env/env.go")
	require.NotContains(t, err.Error(), "no generator in tools/gendrift")
}

// The generator that EMBEDS the documentation runs after every generator that
// WRITES a documentation page.
//
// docsembed packs every page under docs/ into
// engine/internal/docs/pages.gen.go, and six of those pages are themselves
// generated. Run it before one of them and it embeds the PREVIOUS wording, so
// a single pass of `just generate` cannot converge: the tree it leaves behind
// fails gendrift while every generator in it has just reported success. That
// is the worst shape a gate failure can take, because the obvious reading is
// that a generated file is corrupt rather than that the recipe is ordered
// wrong.
//
// This is a real ordering that was wrong. docsembed sat sixth, ahead of
// schemadoc, -update-reference, -update-transforms and -update-frames, and it
// went unnoticed because it is INVISIBLE until one of those pages actually
// changes. A lane adding two manifest keys found it.
//
// The rule is derived from the ledger rather than written down twice: anything
// the ledger says writes under docs/ has to come first. So adding a seventh
// generated page is enough to extend this, and no one has to remember the
// list.
func TestDocsembedRunsAfterEveryGeneratorThatWritesADocsPage(t *testing.T) {
	const embedder = "go run ./tools/docsembed"

	// The order is asserted on the LEDGER now, and not on two copies of it in
	// the justfile. It used to read the `generate` and `_generated` recipes,
	// which each wrote the generators out in full, and it could say nothing at
	// all about ci.yml, which wrote them out a third time and ran docsembed
	// FOURTH of twelve. Every caller runs the ledger through `-generate`, so
	// the ledger's order is the order all three of them use, and that is what
	// this holds.
	embedAt := -1
	var writers []int
	for i, g := range ledger {
		if g.command == embedder {
			embedAt = i
			continue
		}
		for _, p := range g.paths {
			if strings.HasPrefix(p, "docs/") {
				writers = append(writers, i)
				break
			}
		}
	}
	require.NotEqual(t, -1, embedAt, "the ledger does not run %s at all", embedder)

	// A rule with nothing to compare passes for the wrong reason. Six pages
	// under docs/ are generated today and the ledger is where they are
	// declared, so finding fewer than that means this test stopped seeing the
	// thing it exists to order.
	require.GreaterOrEqual(t, len(writers), 5,
		"the ledger should name at least five generators writing under docs/, found %d", len(writers))

	for _, i := range writers {
		require.Less(t, i, embedAt,
			"the ledger runs %q at position %d, after %s at position %d.\n"+
				"docsembed embeds the page that generator writes, so this pass embeds the previous wording.\n"+
				"Move %s below every generator that writes under docs/.",
			ledger[i].command, i, embedder, embedAt, embedder)
	}
}

// TestTheLedgerIsTheOnlyPlaceTheGeneratorsAreListed is the gate on the failure
// that produced -generate.
//
// The list was written out three times. The ledger named fifteen generators,
// `just _generated` ran fifteen and ci.yml ran twelve, so three generated
// files were rewritten by nothing on a clean checkout and could never be
// reported by a tool whose only question is `git status`.
// engine/internal/manifest/manifest.v1.json was one of them and sat stale on
// main for thirteen commits with the step called "Generated files are current"
// green on every one of them.
//
// A second copy of the list is what has to become impossible, so this refuses
// one. It reads every caller and requires that each reaches the generators
// through the ledger and spells none of them out itself.
func TestTheLedgerIsTheOnlyPlaceTheGeneratorsAreListed(t *testing.T) {
	root := filepath.Join("..", "..")

	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	require.NoError(t, err)
	justfile, err := os.ReadFile(filepath.Join(root, "justfile"))
	require.NoError(t, err)

	callers := map[string]string{
		// Comments removed first. ci.yml explains this very defect in prose
		// and names `go run ./tools/docsembed` while doing so, and a check
		// that cannot tell a command from a sentence about a command would
		// have to be deleted for being unsatisfiable.
		".github/workflows/ci.yml": withoutComments(string(workflow)),
		"justfile _generated":      withoutComments(strings.Join(recipeBody(t, string(justfile), "_generated:"), "\n")),
		"justfile generate":        withoutComments(strings.Join(recipeBody(t, string(justfile), "generate:"), "\n")),
	}

	for name, body := range callers {
		t.Run(name, func(t *testing.T) {
			require.Contains(t, body, "go run ./tools/gendrift -generate .",
				"%s does not run the ledger, so it is running some other list of generators", name)

			for _, g := range ledger {
				// gendrift's own row is `go run ./tools/docsembed` and so on;
				// a caller naming one of them is a caller keeping a second
				// copy of the list, which is the defect.
				require.NotContains(t, body, g.command,
					"%s spells out %q itself. That is a second copy of the ledger, and the\n"+
						"two copies disagreed for thirteen commits with every check green.\n"+
						"Add the generator to the ledger in tools/gendrift and let -generate run it.",
					name, g.command)
			}
		})
	}
}

// TestGenerateRunsEveryLedgerCommandInOrder proves -generate does the thing its
// callers now trust it to do.
//
// A mode that named the commands and ran none of them would be the same defect
// one layer down, and it would look identical from CI: a step that passes
// having done nothing, followed by a comparison of a tree nobody rewrote.
func TestGenerateRunsEveryLedgerCommandInOrder(t *testing.T) {
	root := t.TempDir()

	restore := ledger
	t.Cleanup(func() { ledger = restore })
	ledger = []generator{
		{"echo first >> ran.txt", []string{"ran.txt"}},
		{"echo second >> ran.txt", []string{"ran.txt"}},
		{"echo third >> ran.txt", []string{"ran.txt"}},
	}

	var out bytes.Buffer
	require.NoError(t, generateAll(root, &out))

	body, err := os.ReadFile(filepath.Join(root, "ran.txt"))
	require.NoError(t, err, "no generator wrote anything, so none of them ran")
	require.Equal(t, "first\nsecond\nthird\n", string(body),
		"the generators did not all run, or did not run in the ledger's order")
	require.Contains(t, out.String(), "ran 3 generators",
		"the summary does not say how many generators ran")
}

// TestAFailingGeneratorSaysNothingWasCompared is the distinction that cost
// three lanes an afternoon.
//
// A step named for a comparison, which died in a generator before reaching it,
// reported staleness it had never measured. Running the generators and
// comparing them are two questions and they have to be able to fail with
// different words, which is why -generate is a separate mode rather than a
// line folded into the comparison.
func TestAFailingGeneratorSaysNothingWasCompared(t *testing.T) {
	root := t.TempDir()

	restore := ledger
	t.Cleanup(func() { ledger = restore })
	ledger = []generator{
		{"echo first >> ran.txt", []string{"ran.txt"}},
		{"exit 3", []string{"ran.txt"}},
		{"echo third >> ran.txt", []string{"ran.txt"}},
	}

	var out bytes.Buffer
	err := generateAll(root, &out)
	require.Error(t, err, "a generator exiting 3 was reported as success")
	require.Contains(t, err.Error(), "`exit 3` failed",
		"the failure does not name the generator that failed")
	require.Contains(t, out.String(), "Nothing\nhas been compared",
		"the output does not say that this is a generator failure and not a stale file")

	body, err := os.ReadFile(filepath.Join(root, "ran.txt"))
	require.NoError(t, err)
	require.Equal(t, "first\n", string(body),
		"it carried on past a failed generator, so the tree it leaves is half written")
}

// withoutComments drops whole line comments, in both YAML and just, which use
// the same `#`.
//
// It is deliberately whole line only. A trailing comment on a `run:` line
// would survive, and that is the safe direction: this feeds a NotContains, so
// keeping too much can only fail a tree that is fine, which somebody would
// then fix, while dropping too much would pass a tree that is not.
func withoutComments(body string) string {
	var out []string
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// recipeBody returns the indented lines of one justfile recipe.
func recipeBody(t *testing.T, justfile, header string) []string {
	t.Helper()
	all := strings.Split(justfile, "\n")
	start := -1
	for i, l := range all {
		if l == header {
			start = i + 1
			break
		}
	}
	require.NotEqual(t, -1, start, "recipe %s not found in the justfile", header)

	var out []string
	for _, l := range all[start:] {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t") {
			break
		}
		out = append(out, l)
	}
	require.NotEmpty(t, out, "recipe %s is empty", header)
	return out
}
