package docs_test

// The G8 token scan is a shell script because the plan names it as one, and a
// shell script with no test is a gate nobody has ever seen fail. These drive
// the real script as a subprocess, which is the only way to find out what it
// does rather than what it is meant to do.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// script is the scan, resolved from this file so the test does not depend on
// which directory it was started in.
func script(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("forbidden.sh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("the scan is not where the test expects it: %v", err)
	}
	return abs
}

type run struct {
	code   int
	output string
}

func scan(t *testing.T, env []string, args ...string) run {
	t.Helper()
	cmd := exec.Command(script(t), args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	code := 0
	var exit *exec.ExitError
	if err != nil {
		if !asExit(err, &exit) {
			t.Fatalf("running the scan: %v\n%s", err, out)
		}
		code = exit.ExitCode()
	}
	return run{code: code, output: string(out)}
}

func asExit(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	if ok {
		*target = e
	}
	return ok
}

// write puts one document in a scratch directory and returns the directory.
func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "page.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Every rule catches something. Table driven so that adding a rule without a
// case is visible, and so that a rule that silently stops matching is a red
// test rather than a green scan.
func TestEveryRuleCatchesItsToken(t *testing.T) {
	cases := []struct {
		name string
		body string
		why  string
	}{
		{"todo", "Set the key.\n\nTODO: explain rotation.\n", "unfinished note"},
		{"tbd", "Limits are TBD.\n", "unfinished note"},
		{"fixme", "FIXME this section is wrong.\n", "unfinished note"},
		{"wip", "Providers (WIP)\n", "unfinished note"},
		{"lorem", "Lorem ipsum dolor sit amet.\n", "filler text"},
		{"coming soon", "Kubernetes support is coming soon.\n", "a promise instead of a page"},
		{"insert", "Run `af up --token [insert your token]`.\n", "an unfilled slot"},
		{"bracket placeholder", "Use [placeholder] here.\n", "an unfilled slot"},
		{"template slot", "Welcome to {{COMPANY_NAME}}.\n", "an unfilled slot"},
		{"upper placeholder", "Set KEY to PLACEHOLDER.\n", "an unfilled slot"},
		{"xxx", "Your key looks like xxx.\n", "an unfilled slot"},
		{"object object", "It renders [Object Object].\n", "an unfilled slot"},
		{"hack", "This is a hack until the provider lands.\n", "work marked as not the real thing"},
		{"temporary workaround", "A temporary workaround is to restart it.\n", "work marked as not the real thing"},
		{"internal host", "Point it at build01.corp for now.\n", "an address that resolves only inside a private network"},
		{"dotlocal host", "Reach it on registry.local.\n", "an address that resolves only inside a private network"},
		{"guid", "Subscription 8f14e45f-ceea-467a-9f4b-1a2b3c4d5e6f is the one.\n", "a subscription or tenant identifier"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := scan(t, nil, write(t, c.body))
			if got.code == 0 {
				t.Fatalf("the scan passed a document containing a forbidden token:\n%s", c.body)
			}
			if !strings.Contains(got.output, c.why) {
				t.Errorf("reported something other than %q:\n%s", c.why, got.output)
			}
		})
	}
}

// The other half of the control. A page with none of the tokens passes, and a
// page using the product's own vocabulary passes, because a gate that fires on
// accurate documentation is answered by making the documentation worse.
func TestCleanAndProductVocabularyPass(t *testing.T) {
	bodies := map[string]string{
		"clean": "The proxy refuses anything the policy does not allow.\n",
		"product vocabulary": strings.Join([]string{
			"The container gets a placeholder credential in STRIPE_SECRET_KEY.",
			"The migration runs against a temporary server and shuts it down.",
			"A temporary export beats a file, because somebody who typed it meant it.",
			"Reach the service on localhost or 127.0.0.1.",
		}, "\n") + "\n",
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			got := scan(t, nil, write(t, body))
			if got.code != 0 {
				t.Errorf("a clean document was rejected:\n%s", got.output)
			}
		})
	}
}

// A name in the extra list is caught. That file is the only mechanism for
// personal and customer names, which no pattern can find.
func TestANameInTheExtraListIsCaught(t *testing.T) {
	extra := filepath.Join(t.TempDir(), "extra.txt")
	if err := os.WriteFile(extra, []byte("# names\nContoso\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := write(t, "Deployed for Contoso last quarter.\n")
	got := scan(t, []string{"AF_FORBIDDEN_EXTRA=" + extra}, dir)
	if got.code == 0 {
		t.Fatal("a listed name was not caught")
	}
	if !strings.Contains(got.output, "a name that is not the product's") {
		t.Errorf("reported the wrong rule:\n%s", got.output)
	}
}

// An exemption that excuses nothing is reported. Left alone it is a licence
// nobody granted, for a hit that stopped existing.
func TestAStaleExemptionIsReported(t *testing.T) {
	ex := filepath.Join(t.TempDir(), "ex.tsv")
	body := "docs/nothing.md\tunfinished note\tthe page this excused was deleted\n"
	if err := os.WriteFile(ex, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got := scan(t, []string{"AF_FORBIDDEN_EXEMPTIONS=" + ex}, write(t, "Nothing wrong here.\n"))
	if got.code == 0 {
		t.Fatal("a stale exemption was accepted")
	}
	if !strings.Contains(got.output, "matches nothing") {
		t.Errorf("did not explain the staleness:\n%s", got.output)
	}
}

// An exemption with no reason is refused. Reading the file has to say why a
// hit was allowed, not only that somebody allowed it.
func TestAnExemptionWithoutAReasonIsRefused(t *testing.T) {
	ex := filepath.Join(t.TempDir(), "ex.tsv")
	if err := os.WriteFile(ex, []byte("docs/page.md\tunfinished note\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := scan(t, []string{"AF_FORBIDDEN_EXEMPTIONS=" + ex}, write(t, "Fine.\n"))
	if got.code == 0 {
		t.Fatal("an exemption with no reason was accepted")
	}
	if !strings.Contains(got.output, "no reason") {
		t.Errorf("did not explain the refusal:\n%s", got.output)
	}
}

// Pointing the scan at nothing is an error rather than a pass. A gate that
// reports success because it looked in an empty directory is worse than no
// gate: it is a green check that proves nothing.
func TestScanningNothingIsAnError(t *testing.T) {
	got := scan(t, nil, filepath.Join(t.TempDir(), "does-not-exist"))
	if got.code == 0 {
		t.Fatal("scanning a path that does not exist reported success")
	}
	if !strings.Contains(got.output, "looking in the wrong place") {
		t.Errorf("did not explain what was wrong:\n%s", got.output)
	}
}

// The repository itself passes. This is the gate, and it runs here as well as
// in CI so that a page added with a forbidden token fails before it is pushed.
func TestTheRepositoryHasNoForbiddenTokens(t *testing.T) {
	got := scan(t, nil)
	if got.code != 0 {
		t.Errorf("the documentation has forbidden tokens:\n%s", got.output)
	}
}
