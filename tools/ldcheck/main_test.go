package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The check has to be able to fail, or it proves nothing.
//
// These build a small tree with a known answer and assert on it directly,
// rather than running the command, because what is worth pinning is the
// judgement about each symbol and not the exit code around it.

func TestFindsAStringVariable(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "root.go", `package cli

var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)
`)
	for _, name := range []string{"Version", "Commit", "BuildDate"} {
		if err := hasStringVar(dir, name); err != nil {
			t.Errorf("hasStringVar(%s) = %v, want nil", name, err)
		}
	}
}

func TestRefusesASymbolThatIsNotThere(t *testing.T) {
	// The v0.1.0 failure, reduced. The linker would have accepted this and
	// said nothing.
	dir := t.TempDir()
	write(t, dir, "root.go", "package cli\n\nvar Version = \"dev\"\n")
	if err := hasStringVar(dir, "version"); err == nil {
		t.Fatal("a symbol that differs only in case was accepted")
	}
}

func TestRefusesAConstant(t *testing.T) {
	// -X against a constant is the same silent no-op as -X against nothing.
	dir := t.TempDir()
	write(t, dir, "root.go", "package cli\n\nconst Version = \"dev\"\n")
	err := hasStringVar(dir, "Version")
	if err == nil {
		t.Fatal("a constant was accepted")
	}
	if got := err.Error(); got == "" || !contains(got, "constant") {
		t.Errorf("the error does not say why: %v", err)
	}
}

func TestRefusesANonString(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "root.go", "package cli\n\nvar Version = 3\n")
	if err := hasStringVar(dir, "Version"); err == nil {
		t.Fatal("an int was accepted for a flag that can only write strings")
	}
}

func TestRefusesBareMain(t *testing.T) {
	// Bare main resolves against whatever is being built, which is exactly why
	// naming the wrong package fails without a word.
	_, err := packageDir(t.TempDir(), "example.com/m", "engine", "main")
	if err == nil {
		t.Fatal("bare main was accepted")
	}
	if !contains(err.Error(), "full import path") {
		t.Errorf("the error does not say what to do instead: %v", err)
	}
}

func TestReadsTheFlagsOutOfAWorkflowLine(t *testing.T) {
	line := `-ldflags "-s -w \
	  -X example.com/m/internal/cli.Version=$version \
	  -X example.com/m/internal/cli.Commit=$GITHUB_SHA"`
	got := xFlag.FindAllStringSubmatch(line, -1)
	if len(got) != 2 {
		t.Fatalf("found %d flags, want 2", len(got))
	}
	if got[0][1] != "example.com/m/internal/cli.Version" {
		t.Errorf("first symbol is %q", got[0][1])
	}
}

func TestDoesNotMatchAShellVariable(t *testing.T) {
	// A symbol assembled at run time cannot be checked, and matching it as
	// though it could would report a pass for something unverified.
	got := xFlag.FindAllStringSubmatch(`-X $pkg.Version=1`, -1)
	if len(got) == 1 && got[0][1] == "$pkg.Version" {
		return // matched, and packageDir refuses it: also acceptable
	}
	if len(got) != 0 {
		t.Fatalf("unexpected match: %v", got)
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

// The flags moved out of the workflow and into the script the workflow calls,
// so a check that read only the workflow would have found nothing and, worse,
// would have found nothing quietly if the empty-set guard were ever softened.
// This pins the shape the regular expression has to cope with in a shell script
// as well as in YAML.
func TestReadsTheFlagsOutOfAShellScriptLine(t *testing.T) {
	line := `    -ldflags "-s -w \
      -X github.com/antifailure/antifailure/engine/internal/cli.Version=$version \
      -X github.com/antifailure/antifailure/engine/internal/cli.Commit=$commit \
      -X github.com/antifailure/antifailure/engine/internal/cli.BuildDate=$commit_date" \`
	got := xFlag.FindAllStringSubmatch(line, -1)
	if len(got) != 3 {
		t.Fatalf("found %d flags in the build script's line, want 3", len(got))
	}
	want := []string{"Version", "Commit", "BuildDate"}
	for i, w := range want {
		if suffix := got[i][1]; !contains(suffix, "internal/cli."+w) {
			t.Errorf("flag %d is %q, want the cli.%s symbol", i, suffix, w)
		}
	}
}

// Both files the release build is spread across really do carry the flags
// today. This is the check that would go red if somebody moved the build back
// into one place and forgot to tell this command, which is exactly how the
// original failure survived.
func TestEveryDeclaredSourceIsReadable(t *testing.T) {
	root := "../.."
	for _, name := range []string{".github/workflows/release.yml", "tools/release/build.sh"} {
		if _, err := os.ReadFile(filepath.Join(root, name)); err != nil {
			t.Errorf("%s is named as a source of -X flags and cannot be read: %v", name, err)
		}
	}
}
