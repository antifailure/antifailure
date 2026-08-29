package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// minimal is a schema small enough to read and complete enough to exercise
// every branch the renderer has: a required field, a default, a constraint, an
// enum, a reference, a list of references, and an enumerated value list with a
// description each.
const minimal = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "Test",
  "description": "A schema for the renderer's own tests. It has one of everything.",
  "type": "object",
  "required": ["name"],
  "properties": {
    "name": {"type": "string", "description": "What it is called.", "maxLength": 40},
    "mode": {"type": "string", "enum": ["fast", "slow"], "default": "fast"},
    "widget": {"$ref": "#/$defs/widget"},
    "widgets": {"type": "array", "items": {"$ref": "#/$defs/widget"}},
    "kind": {
      "description": "Which sort it is.",
      "oneOf": [
        {"const": "one", "description": "The first sort."},
        {"const": "two", "description": "The second sort."}
      ]
    },
    "pattern": {"type": "string", "pattern": "^a|b$"}
  },
  "$defs": {
    "widget": {
      "description": "A widget, which is the thing this schema is about.",
      "type": "object",
      "properties": {"size": {"type": "integer", "minimum": 1}}
    }
  }
}`

func renderMinimal(t *testing.T) string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(minimal), &doc); err != nil {
		t.Fatal(err)
	}
	page, err := render(doc, "test.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func TestRenderCarriesEverySchemaFactIntoThePage(t *testing.T) {
	page := renderMinimal(t)

	for _, want := range []string{
		`title: "Test schema"`,
		"generated from `schemas/test.v1.json`",
		"| `name` | string | **yes** | What it is called. Max length 40. |",
		"| `mode` | `fast`, `slow` | no | Defaults to `fast`. |",
		"[widget](#widget)",
		"list of [widget](#widget)",
		"### Values for `kind`",
		"| `one` | The first sort. |",
		"| `size` | integer | no | Minimum 1. |",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page does not contain %q\n---\n%s", want, page)
		}
	}
}

// A property that is a bare reference has no prose of its own, and an empty
// Notes cell tells a reader nothing. The referenced object's opening sentence
// fills it.
func TestABareReferenceBorrowsItsTargetsDescription(t *testing.T) {
	page := renderMinimal(t)
	if !strings.Contains(page, "A widget, which is the thing this schema is about.") {
		t.Errorf("the reference cell is empty:\n%s", page)
	}
}

// A pipe inside a pattern would end the table column and shift every cell
// after it, which turns one regular expression into a broken page.
func TestAPipeInAPatternDoesNotBreakTheTable(t *testing.T) {
	page := renderMinimal(t)
	if !strings.Contains(page, "^a\\|b$") {
		t.Errorf("the pattern was not escaped:\n%s", page)
	}
	for _, line := range strings.Split(page, "\n") {
		if strings.HasPrefix(line, "| `pattern`") {
			if got := strings.Count(line, "|") - strings.Count(line, "\\|"); got != 5 {
				t.Errorf("the pattern row has %d unescaped pipes, want 5: %s", got, line)
			}
		}
	}
}

// A definition with no description would render a section that opens on a
// table, and would leave an empty cell in the row that points at it. The
// schema is the only place that sentence can come from, so the generator
// refuses rather than shipping the gap.
func TestADefinitionWithoutADescriptionIsRefused(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(minimal), &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc["$defs"].(map[string]any)["widget"].(map[string]any), "description")

	if _, err := render(doc, "test.v1.json"); err == nil {
		t.Fatal("a definition with no description was accepted")
	} else if !strings.Contains(err.Error(), "widget") {
		t.Errorf("the refusal does not name the definition: %v", err)
	}
}

// Data under schemas/ is skipped rather than rendered. The policy vectors are
// a conformance corpus, not a contract with a shape worth tabulating, and the
// absence of $schema is the file saying so itself.
func TestFilesWithoutASchemaKeywordAreSkipped(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "schemas"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(root, "schemas", name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("real.v1.json", minimal)
	write("vectors.json", `{"note": "not a schema", "cases": []}`)

	if err := run(root); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(filepath.Join(root, outDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "real-v1.md" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("rendered %v, want just real-v1.md", names)
	}
}

// A page whose schema was deleted is a reference to something that no longer
// exists, which is the one failure a generated page is supposed to make
// impossible.
func TestAPageWhoseSchemaIsGoneIsRemoved(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "schemas"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "schemas", "real.v1.json"), []byte(minimal), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, outDir), 0o755); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(root, outDir, "removed-v1.md")
	if err := os.WriteFile(orphan, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := run(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Error("the orphaned page survived")
	}
}

// Pointing the generator at a tree with no schemas is an error rather than a
// quiet success, for the same reason every other gate here refuses an empty
// scan: a green run over nothing proves nothing.
func TestGeneratingFromNothingIsAnError(t *testing.T) {
	if err := run(t.TempDir()); err == nil {
		t.Fatal("a tree with no schemas was accepted")
	}
}
