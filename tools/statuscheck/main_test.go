package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The header of a fixture, carrying the definitions the tool insists on.
const definitions = `# Status

| State | Means |
| --- | --- |
| **proven** | Exercised end to end against the real thing. |
| **written** | Passes against a fake. |
| **planned** | Specified, not built. |
| **mixed** | The parts differ, and the prose says which. |

`

func fixture(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "docs", "plan")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "STATUS.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// The four words this file used to carry outside its own set. `built` is the
// one worth naming: it is not a point on the scale at all, and it survived
// because nothing read the file.
func TestAWordOutsideTheSetFails(t *testing.T) {
	for _, state := range []string{"partial", "built", "in progress", "done", "enforced"} {
		root := fixture(t, definitions+"| 1.1 A thing | "+state+" | Some prose. |\n")
		var out strings.Builder
		if err := run(root, &out); err == nil {
			t.Errorf("state %q passed, and it is not one of the four:\n%s", state, out.String())
		}
	}
}

func TestTheFourDefinedStatesPass(t *testing.T) {
	for _, state := range []string{"proven", "written", "planned"} {
		root := fixture(t, definitions+"| 1.1 A thing | "+state+" | Some prose. |\n")
		var out strings.Builder
		if err := run(root, &out); err != nil {
			t.Errorf("state %q failed: %v\n%s", state, err, out.String())
		}
	}
}

// `mixed` without saying mixed of what is the fourth word again under a new
// spelling, so it is refused.
func TestMixedMustSayOfWhat(t *testing.T) {
	root := fixture(t, definitions+"| 1.1 A thing | mixed | Some of it works. |\n")
	var out strings.Builder
	if err := run(root, &out); err == nil {
		t.Fatalf("a mixed row naming no states passed:\n%s", out.String())
	}

	root = fixture(t, definitions+"| 1.1 A thing | mixed | The reader is proven; the writer is planned. |\n")
	out.Reset()
	if err := run(root, &out); err != nil {
		t.Fatalf("a mixed row naming two states failed: %v\n%s", err, out.String())
	}
}

// One state named is not enough. "proven" alone in the prose of a mixed row
// tells a reader nothing they did not already have.
func TestOneStateNamedIsNotEnough(t *testing.T) {
	root := fixture(t, definitions+"| 1.1 A thing | mixed | The reader is proven. |\n")
	var out strings.Builder
	if err := run(root, &out); err == nil {
		t.Fatalf("a mixed row naming one state passed:\n%s", out.String())
	}
}

// A word the tool accepts but the file never defines is the drift this exists
// to stop: the vocabulary and its documentation must move together.
func TestAnUndefinedStateIsRefused(t *testing.T) {
	body := strings.Replace(definitions,
		"| **mixed** | The parts differ, and the prose says which. |\n", "", 1)
	root := fixture(t, body+"| 1.1 A thing | proven | Some prose. |\n")
	var out strings.Builder
	if err := run(root, &out); err == nil {
		t.Fatalf("mixed is accepted but undefined, and that passed:\n%s", out.String())
	}
}

// A file with no rows means the matcher has stopped matching, and a gate that
// checks nothing must fail rather than report success. This repository has
// produced false green twice by reading an empty answer as a satisfied one.
func TestNoRowsIsAFailureNotASuccess(t *testing.T) {
	root := fixture(t, definitions+"Nothing here has a number.\n")
	var out strings.Builder
	if err := run(root, &out); err == nil {
		t.Fatalf("a file with no component rows passed:\n%s", out.String())
	}
}

// The gate table above the component tables uses words like "enforced" in its
// state column, and a gate that failed on it would be turned off within a day.
func TestTheGateTableIsNotAComponentTable(t *testing.T) {
	root := fixture(t, definitions+
		"| Gate | What it is | State |\n| --- | --- | --- |\n| G1 | Lint | enforced |\n"+
		"| 1.1 A thing | proven | Some prose. |\n")
	var out strings.Builder
	if err := run(root, &out); err != nil {
		t.Fatalf("the gate table was read as a component table: %v\n%s", err, out.String())
	}
}

// The real file, which is the only assertion that catches a regression in the
// document this tool exists for.
func TestTheRealStatusFilePasses(t *testing.T) {
	var out strings.Builder
	if err := run(filepath.Join("..", ".."), &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
}
