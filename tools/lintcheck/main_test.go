package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The point of this command is that it says no. A check nobody has watched
// refuse is a check nobody knows the shape of, so each direction it is
// supposed to catch gets a tree that breaks exactly that one thing.

// tree writes a repository just large enough for run to read, with the pieces
// each case wants to break passed in.
func tree(t *testing.T, catalog, register, generated, rules string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "engine", "internal", "insights")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"lintcatalog.yaml":       catalog,
		"findings.register.json": register,
		"findings.gen.go":        generated,
		"lint.go":                rules,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const (
	goodCatalog = `findings:
  - id: LINT-001
    rule: no_lock_timeout
    title: "no lock_timeout"
  - id: LINT-002
    rule: drop_table
    title: "table dropped"
`
	goodRegister = `{"assigned":[{"id":"LINT-001"},{"id":"LINT-002"}]}`
	goodGen      = `package insights

var findingIDs = map[Rule]FindingID{
	"no_lock_timeout": "LINT-001",
	"drop_table":      "LINT-002",
}
`
	goodRules = `package insights

type Rule string

const (
	RuleNoLockTimeout Rule = "no_lock_timeout"
	RuleDropTable     Rule = "drop_table"
)
`
)

func problems(t *testing.T, root string) string {
	t.Helper()
	got, _, err := run(root)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return strings.Join(got, "\n")
}

func TestAgreementIsQuiet(t *testing.T) {
	root := tree(t, goodCatalog, goodRegister, goodGen, goodRules)
	if got := problems(t, root); got != "" {
		t.Fatalf("a tree that agrees reported: %s", got)
	}
}

// The direction the promise itself lives on. An identifier that was handed out
// and is no longer in the catalogue is free to be handed to something else,
// which is worse than a name that changed.
func TestAWithdrawnIdentifierIsRefused(t *testing.T) {
	catalog := strings.Replace(goodCatalog, "LINT-002", "LINT-009", 1)
	gen := strings.Replace(goodGen, "LINT-002", "LINT-009", 1)
	got := problems(t, tree(t, catalog, goodRegister, gen, goodRules))
	if !strings.Contains(got, "LINT-002 was assigned and is gone") {
		t.Fatalf("a withdrawn identifier was not reported: %s", got)
	}
}

// A rule that can reach a user carrying nothing to match on.
func TestARuleWithNoIdentifierIsRefused(t *testing.T) {
	rules := strings.Replace(goodRules, ")", "\tRuleTruncate Rule = \"truncate\"\n)", 1)
	got := problems(t, tree(t, goodCatalog, goodRegister, goodGen, rules))
	if !strings.Contains(got, "truncate (RuleTruncate) can be reported and has no identifier") {
		t.Fatalf("an unidentified rule was not reported: %s", got)
	}
}

// A catalogue entry, a reference page row and a published catalogue line for a
// finding nothing can produce.
func TestAnEntryForNoRuleIsRefused(t *testing.T) {
	catalog := goodCatalog + `  - id: LINT-003
    rule: drop_schema
    title: "schema dropped"
`
	register := `{"assigned":[{"id":"LINT-001"},{"id":"LINT-002"},{"id":"LINT-003"}]}`
	got := problems(t, tree(t, catalog, register, goodGen, goodRules))
	if !strings.Contains(got, "LINT-003 (drop_schema) is in the catalogue and no rule") {
		t.Fatalf("a dead entry was not reported: %s", got)
	}
}

// Retirement is how an identifier stays spoken for. Marking a live rule retired
// tells a reader the finding is gone while it still fires.
func TestARetiredMarkerOnALiveRuleIsRefused(t *testing.T) {
	catalog := goodCatalog + "    retired: \"folded into LINT-001\"\n"
	got := problems(t, tree(t, catalog, goodRegister, goodGen, goodRules))
	if !strings.Contains(got, "LINT-002 (drop_table) is marked retired and the rule still exists") {
		t.Fatalf("a stale retirement was not reported: %s", got)
	}
}

// The generated map is the only one of the three files a running binary reads,
// so a stale one ships identifiers the documentation does not list.
func TestAStaleGeneratedMapIsRefused(t *testing.T) {
	gen := strings.Replace(goodGen, `"drop_table":      "LINT-002"`, `"drop_table":      "LINT-007"`, 1)
	got := problems(t, tree(t, goodCatalog, goodRegister, gen, goodRules))
	if !strings.Contains(got, "drop_table carries LINT-007") {
		t.Fatalf("a stale generated map was not reported: %s", got)
	}
}

func TestADuplicatedIdentifierIsRefused(t *testing.T) {
	catalog := strings.Replace(goodCatalog, "LINT-002", "LINT-001", 1)
	got := problems(t, tree(t, catalog, goodRegister, goodGen, goodRules))
	if !strings.Contains(got, "LINT-001 is assigned twice") {
		t.Fatalf("a duplicate was not reported: %s", got)
	}
}

// A tree this cannot read must fail loudly rather than reporting agreement,
// which is how a check quietly stops measuring anything.
func TestAnEmptyTreeIsAnErrorRatherThanAPass(t *testing.T) {
	root := tree(t, "findings:\n", goodRegister, goodGen, goodRules)
	if _, _, err := run(root); err == nil {
		t.Fatal("an empty catalogue passed")
	}
	root = tree(t, goodCatalog, goodRegister, goodGen, "package insights\n")
	if _, _, err := run(root); err == nil {
		t.Fatal("a package with no rules passed")
	}
}
