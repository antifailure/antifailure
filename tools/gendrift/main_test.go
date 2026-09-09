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

	var writers []string
	for _, g := range ledger {
		if g.command == embedder {
			continue
		}
		for _, p := range g.paths {
			if strings.HasPrefix(p, "docs/") {
				writers = append(writers, g.command)
				break
			}
		}
	}
	// A rule with nothing to compare passes for the wrong reason. Six pages
	// under docs/ are generated today and the ledger is where they are
	// declared, so finding fewer than that means this test stopped seeing the
	// thing it exists to order.
	require.GreaterOrEqual(t, len(writers), 5,
		"the ledger should name at least five generators writing under docs/, found %d", len(writers))

	body, err := os.ReadFile(filepath.Join("..", "..", "justfile"))
	require.NoError(t, err)

	for _, recipe := range []string{"generate:", "_generated:"} {
		t.Run(recipe, func(t *testing.T) {
			lines := recipeBody(t, string(body), recipe)

			embedAt := -1
			for i, l := range lines {
				if strings.Contains(l, embedder) {
					embedAt = i
					break
				}
			}
			require.NotEqual(t, -1, embedAt, "%s never runs %s", recipe, embedder)

			found := 0
			for _, w := range writers {
				for i, l := range lines {
					if !strings.Contains(l, w) {
						continue
					}
					found++
					require.Less(t, i, embedAt,
						"%s runs %q at line %d of the recipe, after %s at line %d.\n"+
							"docsembed embeds the page that generator writes, so this pass embeds the previous wording.\n"+
							"Move %s below every generator that writes under docs/.",
						recipe, w, i, embedder, embedAt, embedder)
					break
				}
			}
			require.GreaterOrEqual(t, found, 5,
				"%s should run at least five of the ledger's docs writers, found %d", recipe, found)
		})
	}
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
