package main

import (
	"os"
	"path/filepath"
	"testing"
)

// tree writes a throwaway repository with one Go file and one page.
func tree(t *testing.T, source, page string) string {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "engine", "internal", "cli")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "x.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	docs := filepath.Join(root, "docs", "src", "content", "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "p.md"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// Each case is a shape a variable really reaches a person through. The wrapped
// one is the reason this parses instead of grepping: the first version of this
// check was line oriented and returned a clean zero over AF_PORT_RANGE_START
// while looking straight at it, because the assignment and the string that
// names the variable are on different lines.
func TestSeesEveryShapeAVariableIsSpokenIn(t *testing.T) {
	for _, tc := range []struct {
		name, source string
	}{
		{"a Println argument", `package cli
func f(e *Env) { e.Out.Println("  Set AF_WANTED and try again.") }`},
		{"a Printf argument", `package cli
func f(e *Env) { e.Out.Printf("  Set %s.\n", "AF_WANTED") }`},
		{"an assignment to Remediation, wrapped across lines", `package cli
import "fmt"
func f(r *Result, n int) {
	r.Remediation = fmt.Sprintf(
		"Free some ports from %d upwards, or set AF_WANTED to a range that is free.", n)
}`},
		{"a Short field", `package cli
var c = Command{Short: "Uses AF_WANTED when set"}`},
		{"a Long field built by concatenation", `package cli
var c = Command{Long: "first half " +
	"and AF_WANTED in the second"}`},
		{"a cobra flag usage string", `package cli
func f(fs *Flags) { fs.StringVar(&v, "thing", "", "defaults to AF_WANTED") }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := tree(t, tc.source, "nothing documented here")
			spoken, err := spokenVariables(root)
			if err != nil {
				t.Fatal(err)
			}
			if spoken["AF_WANTED"] == "" {
				t.Fatalf("the scanner did not see AF_WANTED; it saw %v", spoken)
			}
		})
	}
}

// The other direction, so a scanner that matched everything would fail here.
// Without this the test above passes for a scanner that reports every string
// in the file, which would make the gate useless and noisy at once.
func TestIgnoresAVariableNobodyIsShown(t *testing.T) {
	root := tree(t, `package cli
import "os"

const MaskingKeyEnv = "AF_NEVER_SPOKEN"

func f() string { return os.Getenv("AF_NEVER_SPOKEN") }`, "nothing")
	spoken, err := spokenVariables(root)
	if err != nil {
		t.Fatal(err)
	}
	if spoken["AF_NEVER_SPOKEN"] != "" {
		t.Fatalf("a variable that is only read should not count as named at a user")
	}
}

func TestTestFilesAreNotAUserFacingSurface(t *testing.T) {
	root := tree(t, "package cli", "nothing")
	src := filepath.Join(root, "engine", "internal", "cli", "x_test.go")
	body := `package cli
func f(e *Env) { e.Out.Println("Set AF_ONLY_IN_A_TEST") }`
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	spoken, err := spokenVariables(root)
	if err != nil {
		t.Fatal(err)
	}
	if spoken["AF_ONLY_IN_A_TEST"] != "" {
		t.Fatal("a test file is not a surface a user reads")
	}
}

func TestDocumentedMeansNamedOnAPage(t *testing.T) {
	root := tree(t, "package cli", "You may set `AF_WANTED` to move it.")
	documented, err := documentedVariables(root)
	if err != nil {
		t.Fatal(err)
	}
	if !documented["AF_WANTED"] {
		t.Fatal("a variable named on a page should count as documented")
	}
	if documented["AF_NOT_THERE"] {
		t.Fatal("a variable no page names should not count as documented")
	}
}

// An exemption with no argument behind it cannot be told apart from somebody
// silencing a finding they did not understand, so the file refuses one.
func TestAnExemptionWithoutAReasonIsRefused(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tools", "docs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "variable-exemptions.tsv"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("# a comment\nAF_THING\n")
	if _, _, err := exemptions(root); err == nil {
		t.Fatal("a row with no reason should be refused")
	}

	write("# a comment\nAF_THING\t\n")
	if _, _, err := exemptions(root); err == nil {
		t.Fatal("a row with an empty reason should be refused")
	}

	write("# a comment\nAF_THING\tonly ever printed to somebody working on this repository\n")
	reasons, order, err := exemptions(root)
	if err != nil {
		t.Fatalf("a row with a reason should be accepted: %v", err)
	}
	if len(order) != 1 || reasons["AF_THING"] == "" {
		t.Fatalf("expected one reason, got %v", reasons)
	}
}

// A missing file is not an error: the gate is useful before anybody needs an
// exemption, and today nobody does.
func TestNoExemptionFileIsFine(t *testing.T) {
	reasons, order, err := exemptions(t.TempDir())
	if err != nil || len(reasons) != 0 || len(order) != 0 {
		t.Fatalf("an absent file should read as no exemptions, got %v %v %v", reasons, order, err)
	}
}
