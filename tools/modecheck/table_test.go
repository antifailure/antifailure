package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tree(t *testing.T) *schemaTree {
	t.Helper()
	tr, err := loadSchema(filepath.Join("..", "..", "schemas", "manifest.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func table(t *testing.T, body string) []finding {
	t.Helper()
	return CheckTable("manifest.md", body, tree(t))
}

func oneRow(t *testing.T, body string) finding {
	t.Helper()
	got := table(t, body)
	if len(got) != 1 {
		t.Fatalf("want one finding, got %d: %+v", len(got), got)
	}
	return got[0]
}

func noRows(t *testing.T, body string) {
	t.Helper()
	if got := table(t, body); len(got) != 0 {
		t.Fatalf("want no findings, got %d: %+v", len(got), got)
	}
}

const egressTable = "## `egress`\n\n| Key | Notes |\n| --- | --- |\n"

// The three rows this was written for, each quoted from the page it shipped on.
func TestTheHistoricalRowsAreFound(t *testing.T) {
	for name, tc := range map[string]struct{ body, missing string }{
		"egress.default": {
			egressTable + "| `default` | `block` (default) or `allow`. |\n",
			"capture, mock, sandbox, synth",
		},
		"database.provider": {
			"## `database`\n\n| Key | Notes |\n| --- | --- |\n" +
				"| `provider` | `docker` (default) or `neon`. |\n",
			"supabase, dblab",
		},
		// Nested under two headings, with a prose heading in between. See
		// TestAProseHeadingDoesNotEndTheSectionAboveIt for why that matters.
		"services.build.strategy": {
			"## `services`\n\n### What a service is given\n\n### `build`\n\n| Key | Notes |\n| --- | --- |\n" +
				"| `strategy` | `auto` (default), `dockerfile`, or `buildpack`. |\n",
			"image",
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := oneRow(t, tc.body)
			if !strings.Contains(f.why, "Missing: "+tc.missing) {
				t.Errorf("want missing %q, got: %s", tc.missing, f.why)
			}
			if !strings.Contains(f.why, name) {
				t.Errorf("the finding should name the schema path %q: %s", name, f.why)
			}
		})
	}
}

func TestACompleteRowIsSilent(t *testing.T) {
	noRows(t, egressTable+
		"| `default` | Any mode: `block` (default), `allow`, `capture`, `mock`, `sandbox` or `synth`. |\n")
}

// The bug that made this tool report success while checking nothing.
//
// The reference puts "### What a service is given" between "## `services`" and
// "### `build`". A flat current-section variable was cleared by that prose
// heading, so "### `build`" resolved as a top level key named build, which does
// not exist, and the strategy row was never checked. The tool printed zero
// findings and looked like it worked.
func TestAProseHeadingDoesNotEndTheSectionAboveIt(t *testing.T) {
	with := "## `services`\n\n### What a service is given\n\n### `build`\n\n| Key | Notes |\n| --- | --- |\n" +
		"| `strategy` | `auto`, `dockerfile`, or `buildpack`. |\n"
	without := "## `services`\n\n### `build`\n\n| Key | Notes |\n| --- | --- |\n" +
		"| `strategy` | `auto`, `dockerfile`, or `buildpack`. |\n"
	a, b := table(t, with), table(t, without)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("the prose heading changed the verdict: with=%d without=%d", len(a), len(b))
	}
	if a[0].why != b[0].why {
		t.Errorf("with = %q\nwithout = %q", a[0].why, b[0].why)
	}
}

// A table under a heading that is not a manifest key describes something the
// schema has no property for, so its rows are not schema claims.
func TestATableUnderAProseHeadingIsNotChecked(t *testing.T) {
	noRows(t, "## When the manifest is wrong\n\n| Key | Notes |\n| --- | --- |\n"+
		"| `default` | `block` or `allow`. |\n")
}

// Everything below is a cell that must stay silent. Each is real text from the
// reference, and each is a finding an obvious version of this rule produces.
func TestCellsThatAreNotClaimsAreSilent(t *testing.T) {
	for name, body := range map[string]string{
		// The nine policy rows. The section states the enumeration once in its
		// preamble and the rows sensibly do not repeat it. A rule demanding
		// every enum row restate its values would fire on all nine and make
		// the page worse to read while calling it correctness.
		"a row that describes the key instead of listing values": "## `policy`\n\n" +
			"| Key | Default | The finding |\n| --- | --- | --- |\n" +
			"| `migration_rewrite` | `warn` | Postgres rewrote a table. |\n",

		// The false alarm that produced the read-the-last-cell rule. Reading
		// everything after the key cell counted the Default column's `warn` as
		// part of an enumeration, and "more often or slower" supplied the or.
		"a Default column is not an enumeration": "## `policy`\n\n" +
			"| Key | Default | The finding |\n| --- | --- | --- |\n" +
			"| `query_regression` | `warn` | A statement runs more often or slower than the baseline. |\n",

		// Naming one value without joining it to another is a mention.
		"one value mentioned in passing": "## `database`\n\n| Key | Notes |\n| --- | --- |\n" +
			"| `provider` | Leave unset for `docker`. |\n",

		// An enum of integers the page sensibly describes rather than lists.
		"a described numeric range": "## `database`\n\n| Key | Notes |\n| --- | --- |\n" +
			"| `version` | Postgres major, default 17. |\n",

		// A key with no closed set at all.
		"a key with no enum": "## `database`\n\n| Key | Notes |\n| --- | --- |\n" +
			"| `url_env` | The variable services receive the connection string in. |\n",

		// A key the schema does not have. The page is allowed to document
		// things that are not manifest properties.
		"a row that is not a schema property": "## `egress`\n\n| Key | Notes |\n| --- | --- |\n" +
			"| `nonesuch` | `block` or `allow`. |\n",
	} {
		t.Run(name, func(t *testing.T) { noRows(t, body) })
	}
}

// "database" is both the top level block and a property of the fidelity
// requirement, with entirely different keys under each. Resolving a heading by
// searching the schema for any property of that name would have to guess which
// table it was enforcing; resolving by the heading nesting does not.
func TestTheHeadingNestingDisambiguatesARepeatedName(t *testing.T) {
	tr := tree(t)
	top := tr.propertiesAt([]string{"database"})
	if _, has := top["provider"]; !has {
		t.Fatalf("the top level database block should have a provider key, got %d keys", len(top))
	}
	sub := tr.propertiesAt([]string{"database", "subset"})
	if _, has := sub["provider"]; has {
		t.Error("database.subset should not resolve to the database block's own keys")
	}
	if len(sub) == 0 {
		t.Error("database.subset should resolve to something")
	}
}

// An array of objects is one table in the reference and an array in the
// schema, so the walk has to step through items or every nested table is
// unreachable.
func TestAnArrayOfObjectsResolvesToItsItems(t *testing.T) {
	props := tree(t).propertiesAt([]string{"services"})
	if _, has := props["kind"]; !has {
		t.Fatalf("services should resolve through items to a service's keys, got %d", len(props))
	}
}

// The reference tables are the whole reason this exists, so the real page is
// checked as well as the synthetic rows above.
func TestTheRealReferencePasses(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "docs", "src", "content", "docs", "reference", "manifest.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got := CheckTable("manifest.md", string(body), tree(t)); len(got) != 0 {
		t.Fatalf("the reference should be complete, got %+v", got)
	}
}

// A JSON number arrives as a float64. Rendering it the obvious way gives
// "14.000000", which matches nothing on a page and would make this check go
// quiet on every numeric enum while still reporting success. database.version
// is the one that would have gone quiet.
func TestAnIntegerEnumIsRenderedTheWayThePageWritesIt(t *testing.T) {
	props := tree(t).propertiesAt([]string{"database"})
	got := tree(t).valuesFor(props["version"])
	if len(got) == 0 {
		t.Fatal("database.version declares a closed set and none was read")
	}
	for _, v := range got {
		if strings.ContainsAny(v, ".eE") {
			t.Errorf("version %q is not written the way the reference writes it", v)
		}
	}
	// And the rule still fires on a numeric enum listed short.
	f := oneRow(t, "## `database`\n\n| Key | Notes |\n| --- | --- |\n"+
		"| `version` | `14` or `15`. |\n")
	if !strings.Contains(f.why, "16") {
		t.Errorf("want the missing majors named, got: %s", f.why)
	}
}
