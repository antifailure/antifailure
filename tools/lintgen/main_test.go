package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// generate runs the whole command against a temporary tree and returns the
// register it wrote, which is the file the promise rests on.
func generate(t *testing.T, catalog string, seedRegister string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	paths := struct{ catalog, goSrc, docs, jsonSrc, register string }{
		filepath.Join(dir, "lintcatalog.yaml"),
		filepath.Join(dir, "findings.gen.go"),
		filepath.Join(dir, "lint-findings.md"),
		filepath.Join(dir, "lint-findings.v1.json"),
		filepath.Join(dir, "findings.register.json"),
	}
	if err := os.WriteFile(paths.catalog, []byte(catalog), 0o644); err != nil {
		t.Fatal(err)
	}
	if seedRegister != "" {
		if err := os.WriteFile(paths.register, []byte(seedRegister), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	err := run(paths.catalog, paths.goSrc, paths.docs, paths.jsonSrc, paths.register, false)
	if err != nil {
		return "", err
	}
	body, readErr := os.ReadFile(paths.register)
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(body), nil
}

const oneRule = `findings:
  - id: LINT-001
    rule: no_lock_timeout
    title: "no lock_timeout"
`

// The whole reason for a separate identifier: the name moves and the number
// does not, and regenerating after a rename must not disturb the register.
func TestARenamedRuleKeepsItsIdentifier(t *testing.T) {
	before, err := generate(t, oneRule, "")
	if err != nil {
		t.Fatal(err)
	}
	renamed := strings.Replace(oneRule, "no_lock_timeout", "migration_sets_no_lock_timeout", 1)
	after, err := generate(t, renamed, before)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(after, "LINT-001") {
		t.Fatalf("the identifier did not survive a rename: %s", after)
	}
	if strings.Contains(after, "LINT-002") {
		t.Fatalf("a rename handed out a second identifier: %s", after)
	}
	// first_named is history and is written once, so the register still says
	// what the rule was called when the number was assigned.
	if !strings.Contains(after, "no_lock_timeout\"") {
		t.Fatalf("the register lost what LINT-001 was first assigned to: %s", after)
	}
}

// The register only grows. Dropping an entry from the catalogue frees its
// number, which is the one thing an identifier must never be.
func TestAWithdrawnIdentifierIsRefused(t *testing.T) {
	seed := `{"assigned":[{"id":"LINT-001","first_named":"no_lock_timeout"},
		{"id":"LINT-002","first_named":"drop_table"}]}`
	_, err := generate(t, oneRule, seed)
	if err == nil {
		t.Fatal("dropping LINT-002 from the catalogue was accepted")
	}
	if !strings.Contains(err.Error(), "LINT-002") {
		t.Fatalf("the refusal does not name the identifier that went: %v", err)
	}
}

func TestADuplicateIsRefused(t *testing.T) {
	_, err := generate(t, oneRule+`  - id: LINT-001
    rule: drop_table
    title: "table dropped"
`, "")
	if err == nil || !strings.Contains(err.Error(), "assigned twice") {
		t.Fatalf("a duplicated identifier was accepted: %v", err)
	}
}

func TestAMalformedCatalogueIsRefused(t *testing.T) {
	for name, catalog := range map[string]string{
		"no findings":     "findings:\n",
		"a bad shape":     "findings:\n  - id: LINT1\n    rule: a\n    title: b\n",
		"no rule":         "findings:\n  - id: LINT-001\n    rule: \"\"\n    title: b\n",
		"no title":        "findings:\n  - id: LINT-001\n    rule: a\n    title: \"\"\n",
		"a shouty rule":   "findings:\n  - id: LINT-001\n    rule: DropTable\n    title: b\n",
		"a repeated rule": oneRule + "  - id: LINT-002\n    rule: no_lock_timeout\n    title: b\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := generate(t, catalog, ""); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

// A retired entry keeps its number spoken for and leaves the generated map, so
// nothing can report the finding and nothing can be handed the number again.
func TestARetiredEntryKeepsItsNumberAndLeavesTheCode(t *testing.T) {
	dir := t.TempDir()
	catalog := filepath.Join(dir, "lintcatalog.yaml")
	goSrc := filepath.Join(dir, "findings.gen.go")
	body := oneRule + `  - id: LINT-002
    rule: drop_table
    title: "table dropped"
    retired: "the rule was folded into LINT-001"
`
	if err := os.WriteFile(catalog, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	err := run(catalog, goSrc, filepath.Join(dir, "d.md"), filepath.Join(dir, "c.json"),
		filepath.Join(dir, "r.json"), false)
	if err != nil {
		t.Fatal(err)
	}
	code, readErr := os.ReadFile(goSrc)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(code), `"drop_table": "LINT-002"`) {
		t.Fatal("a retired rule is still in the generated map")
	}
	if !strings.Contains(string(code), `"LINT-002"`) {
		t.Fatal("a retired identifier left assignedFindingIDs, so it can be handed out again")
	}
}
