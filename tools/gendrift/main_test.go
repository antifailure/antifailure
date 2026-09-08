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
