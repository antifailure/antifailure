package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tree writes a throwaway repository: one reference page, one Terraform file in
// the module, and optionally an exemption file.
func tree(t *testing.T, page, tf, exempt string) string {
	return treeWithChart(t, page, tf, "", exempt)
}

// treeWithChart is tree plus one Helm template, for the reader that has to tell
// an env entry from a comment mentioning one.
func treeWithChart(t *testing.T, page, tf, chart, exempt string) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(referencePath, page)
	write(modulePath+"/app.tf", tf)
	write(chartPath+"/templates/deployment.yaml", chart)
	if exempt != "" {
		write(exemptionsPath, exempt)
	}
	return root
}

// testRoutes is what main() builds, over a throwaway tree.
func testRoutes(t *testing.T, root string) []route {
	t.Helper()
	tf, err := settableVariables(root)
	if err != nil {
		t.Fatal(err)
	}
	chart, err := chartVariables(root)
	if err != nil {
		t.Fatal(err)
	}
	return []route{
		{key: targetTerraform, what: "the Terraform module", where: modulePath, sets: tf, floor: minSettable},
		{key: targetHelm, what: "the Helm chart", where: chartPath, sets: chart, floor: minChartSettable},
	}
}

const oneRow = "## Optional\n\n| Variable | Default | What it does |\n| --- | --- | --- |\n| `AF_WANTED` | unset | Something. |\n"

// THE SHAPES AN ENV BLOCK REALLY TAKES IN THIS MODULE.
//
// This is the reason the reader tracks brace depth rather than matching a whole
// block with one expression. Half of these blocks are dynamic, carry a for_each
// whose expression contains braces of its own, and put the name one level
// deeper inside a content block. A shallow read either stops at the first close
// brace it sees or swallows the block after it, and both of those failures are
// silent: they produce a variable that looks unset rather than an error.
func TestReadsEveryShapeAnEnvBlockTakes(t *testing.T) {
	for _, tc := range []struct{ name, tf string }{
		{"a plain env block", `
resource "azurerm_container_app" "this" {
  env {
    name  = "AF_WANTED"
    value = "8080"
  }
}`},
		{"a plain env block naming a secret", `
resource "azurerm_container_app" "this" {
  env {
    name        = "AF_WANTED"
    secret_name = "wanted"
  }
}`},
		{"a dynamic env block", `
resource "azurerm_container_app" "this" {
  dynamic "env" {
    for_each = var.thing == "" ? [] : [var.thing]
    content {
      name  = "AF_WANTED"
      value = env.value
    }
  }
}`},
		{"a dynamic env block whose for_each carries braces", `
resource "azurerm_container_app" "this" {
  dynamic "env" {
    for_each = var.app_id == "" ? {} : { a = 1 }
    content {
      name  = "AF_WANTED"
      value = env.value
    }
  }
}`},
		{"an env block after one that already closed", `
resource "azurerm_container_app" "this" {
  dynamic "env" {
    for_each = var.a == "" ? [] : [var.a]
    content {
      name  = "AF_OTHER"
      value = env.value
    }
  }
  env {
    name  = "AF_WANTED"
    value = "x"
  }
}`},
		{"terraform fmt's alignment on the equals sign", `
resource "azurerm_container_app" "this" {
  env {
    name        = "AF_WANTED"
    secret_name = "s"
  }
}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settable, err := settableVariables(tree(t, oneRow, tc.tf, ""))
			if err != nil {
				t.Fatal(err)
			}
			if settable["AF_WANTED"] == "" {
				t.Fatalf("AF_WANTED was not read out of %s", tc.name)
			}
		})
	}
}

// A name that is NOT an env block's is not a delivery. `name` is the commonest
// argument in this module: every resource has one, and so does every Key Vault
// secret.
func TestDoesNotCountANameThatIsNotAnEnvBlocks(t *testing.T) {
	tf := `
resource "azurerm_key_vault_secret" "owned" {
  name  = "AF_WANTED"
  value = "x"
}`
	settable, err := settableVariables(tree(t, oneRow, tf, ""))
	if err != nil {
		t.Fatal(err)
	}
	if where := settable["AF_WANTED"]; where != "" {
		t.Fatalf("a secret's own name was read as a delivered variable, at %s", where)
	}
}

// The reference names variables in prose constantly: every cell of the third
// column explains what happens when some OTHER variable is missing. A mention
// is not a definition, and counting one would make the gate demand an env block
// for a sentence.
func TestOnlyTheFirstCellOfARowDefinesAVariable(t *testing.T) {
	page := "## Optional\n\n| Variable | Default | What it does |\n| --- | --- | --- |\n" +
		"| `AF_WANTED` | unset | Needed together with `AF_MENTIONED_ONLY`. |\n" +
		"\nProse naming `AF_PROSE_ONLY` outside any table.\n"
	documented, err := documentedVariables(tree(t, page, "", ""))
	if err != nil {
		t.Fatal(err)
	}
	if documented["AF_WANTED"] == "" {
		t.Fatal("the first cell of the row was not read as a definition")
	}
	if where := documented["AF_MENTIONED_ONLY"]; where != "" {
		t.Fatalf("a variable named in the third column was read as a definition, at %s", where)
	}
	if where := documented["AF_PROSE_ONLY"]; where != "" {
		t.Fatalf("a variable named in prose was read as a definition, at %s", where)
	}
}

// A table that declares its variables are set somewhere else.
//
// The page says it with the column, not with the heading: a "Where it is set"
// column in place of a "Default" one, and a cell that answers it. Two sections
// are written that way today and the second arrived AFTER this gate did, which
// is the argument for reading the column rather than a list of titles.
func TestSkipsATableThatSaysTheVariableIsSetElsewhere(t *testing.T) {
	notHere := func(heading, name, where string) string {
		return "\n## " + heading + "\n\n| Variable | " + notSetHereColumn + " | What it is |\n" +
			"| --- | --- | --- |\n| `" + name + "` | " + where + " | Not read here. |\n"
	}
	page := oneRow +
		notHere("Set on the engine, not here", "AF_ON_THE_ENGINE", "On the engine, or in a CI job") +
		notHere("Read by a command, not by the server", "AF_IN_A_SHELL", "In the shell that runs the command") +
		"\n## Analytics\n\n| Variable | Default | What |\n| --- | --- | --- |\n" +
		"| `AF_AFTER_THE_SECTION` | unset | Read here again. |\n"

	documented, err := documentedVariables(tree(t, page, "", ""))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AF_ON_THE_ENGINE", "AF_IN_A_SHELL"} {
		if where := documented[name]; where != "" {
			t.Errorf("%s sits under a %q column and was still demanded of the module, at %s",
				name, notSetHereColumn, where)
		}
	}
	// The skip ENDS with the table. One that ran to the end of the file would
	// swallow the analytics table, which is where AF_SITE_ORIGIN and the
	// surrogate secret live: five of the variables this gate was written for.
	if documented["AF_AFTER_THE_SECTION"] == "" {
		t.Error("the skip did not end with the table, so every later one was ignored")
	}
	// And the row that opened the page is still there, so the skip did not
	// start early either.
	if documented["AF_WANTED"] == "" {
		t.Error("an ordinary table before the skipped one was ignored")
	}
}

// A "Default" table is an ordinary one however it is worded, and each case below
// is a DIFFERENT way a looser reading would wrongly skip one. They are separate
// because a substring test over the whole line passes the first and fails the
// second, so one of them alone would report a rule it is not testing.
func TestATableWithADefaultColumnIsNeverSkipped(t *testing.T) {
	for _, tc := range []struct{ name, page string }{
		{"the phrase appears in a data cell", "## Optional\n\n" +
			"| Variable | Default | What it does |\n| --- | --- | --- |\n" +
			"| `AF_WANTED` | unset | Where it is set is discussed at length here. |\n"},
		// The one that separates "the header's SECOND CELL is the column" from
		// "the header LINE mentions the words somewhere".
		{"the phrase appears in another header cell", "## Optional\n\n" +
			"| Variable | Default | Where it is set, and why that matters |\n| --- | --- | --- |\n" +
			"| `AF_WANTED` | unset | Something. |\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			documented, err := documentedVariables(tree(t, tc.page, "", ""))
			if err != nil {
				t.Fatal(err)
			}
			if documented["AF_WANTED"] == "" {
				t.Fatal("an ordinary table was skipped, so a variable nothing can set would pass unseen")
			}
		})
	}
}

// A HEADING ALWAYS RESETS THE SHAPE, and this is the case that proves the reset
// is load-bearing rather than decoration.
//
// A well-formed table declares itself in its header row, so on a tidy page the
// reset never decides anything. On a page somebody has edited badly -- a
// section whose header row was dropped -- it decides everything: without it the
// rows inherit the PREVIOUS table's "not set here" and the variables in them
// vanish from the gate silently, which is the one failure this whole tool
// exists to make impossible. With it they are read, and an unsettable one is
// reported loudly.
func TestAHeadingResetsTheTableShape(t *testing.T) {
	page := "\n## Set on the engine, not here\n\n| Variable | " + notSetHereColumn + " | What it is |\n" +
		"| --- | --- | --- |\n| `AF_ON_THE_ENGINE` | On the engine | Not read here. |\n" +
		"\n## Optional\n\n| `AF_WANTED` | unset | A row whose header row somebody deleted. |\n"
	documented, err := documentedVariables(tree(t, page, "", ""))
	if err != nil {
		t.Fatal(err)
	}
	if documented["AF_ON_THE_ENGINE"] != "" {
		t.Error("the not-set-here table was read after all")
	}
	if documented["AF_WANTED"] == "" {
		t.Fatal("a row under a later heading inherited the previous table's shape and was skipped silently")
	}
}

// ONE BREAK PER ASSERTION. Each case below is a different way the wire can be
// missing, and each one is asserted on its own so that a later check cannot be
// made unreachable by an earlier one failing first.
// knownRoutes is the route list every exemption test needs, since the second
// field is checked against it.
func knownRoutes() []route {
	return []route{{key: targetTerraform}, {key: targetHelm}}
}

func TestExemptions(t *testing.T) {
	t.Run("a row with a reason silences the finding", func(t *testing.T) {
		reasons, order, err := exemptions(tree(t, oneRow, "", "AF_WANTED\tterraform\tstamped at build time\n"), knownRoutes())
		if err != nil {
			t.Fatal(err)
		}
		if reasons[targetTerraform]["AF_WANTED"] != "stamped at build time" {
			t.Fatalf("the reason was not read: %q", reasons[targetTerraform]["AF_WANTED"])
		}
		if len(order) != 1 {
			t.Fatalf("expected one row, got %d", len(order))
		}
	})

	t.Run("a row with no reason is refused", func(t *testing.T) {
		_, _, err := exemptions(tree(t, oneRow, "", "AF_WANTED\tterraform\n"), knownRoutes())
		if err == nil {
			t.Fatal("a row with no reason was accepted, which is an allowance with no argument behind it")
		}
		if !strings.Contains(err.Error(), "no reason") {
			t.Fatalf("the error does not say what is wrong: %v", err)
		}
	})

	t.Run("a row whose reason is only whitespace is refused", func(t *testing.T) {
		_, _, err := exemptions(tree(t, oneRow, "", "AF_WANTED\tterraform\t   \n"), knownRoutes())
		if err == nil {
			t.Fatal("a row with a blank reason was accepted")
		}
	})

	t.Run("the same variable twice is refused", func(t *testing.T) {
		_, _, err := exemptions(tree(t, oneRow, "", "AF_WANTED\tterraform\tone\nAF_WANTED\tterraform\ttwo\n"), knownRoutes())
		if err == nil {
			t.Fatal("two rows for one variable were accepted, so one reason silently wins")
		}
	})

	t.Run("comments and blank lines are not rows", func(t *testing.T) {
		reasons, _, err := exemptions(tree(t, oneRow, "", "# why this file exists\n\nAF_WANTED\tterraform\tstamped\n"), knownRoutes())
		if err != nil {
			t.Fatal(err)
		}
		if len(reasons[targetTerraform]) != 1 {
			t.Fatalf("expected one row, got %d", len(reasons[targetTerraform]))
		}
	})

	t.Run("the same variable on two routes is two rows, not a duplicate", func(t *testing.T) {
		// THE REASON THE COLUMN EXISTS. AF_INSECURE_COOKIES is refused by the
		// Terraform module and set on purpose by the chart, so one variable
		// carries two opposite and both correct reasons.
		reasons, order, err := exemptions(
			tree(t, oneRow, "", "AF_WANTED\tterraform\tno ingress can carry it\nAF_WANTED\thelm\tlocal HTTP only\n"),
			knownRoutes())
		if err != nil {
			t.Fatal(err)
		}
		if reasons[targetTerraform]["AF_WANTED"] == reasons[targetHelm]["AF_WANTED"] {
			t.Fatal("the two routes share one reason, so the column is doing nothing")
		}
		if len(order) != 2 {
			t.Fatalf("expected two rows, got %d", len(order))
		}
	})

	t.Run("a route nothing reads is refused", func(t *testing.T) {
		// A row that looks applied and silences nothing fails as quietly as a
		// stale one, so it is an error rather than a skip.
		_, _, err := exemptions(tree(t, oneRow, "", "AF_WANTED\tansible\tsomebody's guess\n"), knownRoutes())
		if err == nil {
			t.Fatal("a row on an unknown route was accepted")
		}
		if !strings.Contains(err.Error(), "nothing reads") {
			t.Fatalf("the error does not say what is wrong: %v", err)
		}
	})

	t.Run("a missing file is no rows rather than an error", func(t *testing.T) {
		reasons, order, err := exemptions(tree(t, oneRow, "", ""), knownRoutes())
		if err != nil {
			t.Fatal(err)
		}
		if len(reasons[targetTerraform]) != 0 || len(order) != 0 {
			t.Fatal("a missing exemption file produced rows")
		}
	})
}

// THE TRIPWIRE ON THE READING, and it is the assertion that would have caught
// this whole class of gate failing open. A parser that stops matching returns
// an empty map, an empty map agrees with every other empty map, and the run
// prints a confident zero.
func TestFloorsRefuseAnImplausiblyQuietRead(t *testing.T) {
	full := map[string]string{}
	for i := 0; i < minDocumented; i++ {
		full[string(rune('A'+i))] = "somewhere"
	}
	plausible := func(n int) map[string]string {
		out := map[string]string{}
		for i := 0; i < n; i++ {
			out[string(rune('A'+i))] = "somewhere"
		}
		return out
	}
	routes := func(tf, chart int) []route {
		return []route{
			{key: targetTerraform, where: modulePath, sets: plausible(tf), floor: minSettable},
			{key: targetHelm, where: chartPath, sets: plausible(chart), floor: minChartSettable},
		}
	}

	t.Run("a reference that read as empty", func(t *testing.T) {
		if err := floors(map[string]string{}, routes(minSettable, minChartSettable)); err == nil {
			t.Fatal("zero documented variables passed the floor")
		}
	})
	t.Run("a module that read as empty", func(t *testing.T) {
		if err := floors(full, routes(0, minChartSettable)); err == nil {
			t.Fatal("zero settable variables passed the floor")
		}
	})
	t.Run("a chart that read as empty", func(t *testing.T) {
		// THE HALF THAT IS NEW, and it is not decoration. The chart reader
		// drops comments and matches one position, so a template written in a
		// shape it does not know returns an empty map, and an empty map is
		// consistent with every other empty map. The run would then print a
		// confident zero about a chart it never read.
		err := floors(full, routes(minSettable, 0))
		if err == nil {
			t.Fatal("zero chart variables passed the floor")
		}
		if !strings.Contains(err.Error(), chartPath) {
			t.Fatalf("the error does not name the surface it could not read: %v", err)
		}
	})
	t.Run("every surface read", func(t *testing.T) {
		if err := floors(full, routes(minSettable, minChartSettable)); err != nil {
			t.Fatalf("a plausible read was refused: %v", err)
		}
	})
}

// THE CHART READER, and every case here is a line that really appears in
// deploy/helm/antifailure-control-plane.
//
// A chart template is Go template source rather than YAML, so it cannot be
// parsed as YAML and a search for the name is what is left. A search for the
// name is wrong in a way that already had a live example when this was written:
// templates/deployment.yaml carries the comment "AF_MIGRATE is deliberately
// absent", and a scan for the string concludes that the chart delivers the one
// variable that file is refusing to deliver.
func TestTheChartReaderTellsAMentionFromAnEnvEntry(t *testing.T) {
	for _, tc := range []struct {
		name  string
		chart string
		want  bool
	}{
		{"a plain env entry", "            - name: AF_WANTED\n              value: \"1\"\n", true},
		{"a quoted name", "            - name: \"AF_WANTED\"\n", true},
		{"a secretKeyRef", "            - name: AF_WANTED\n              valueFrom:\n                secretKeyRef:\n                  name: s\n                  key: k\n", true},
		{"inside a conditional block", "            {{- if .Values.thing }}\n            - name: AF_WANTED\n              value: \"1\"\n            {{- end }}\n", true},
		{"a YAML comment saying it is absent", "            # AF_WANTED is deliberately absent.\n", false},
		{"a YAML comment beside a real entry", "            # AF_WANTED would be wrong here\n            - name: AF_OTHER\n", false},
		{"a Helm comment block", "{{/*\nAF_WANTED is refused, and here is the argument.\n*/}}\n", false},
		// THE CASE THE BLOCK RULE EXISTS FOR. A chart that documents how to add
		// a variable writes the entry out, indented, inside a comment. That
		// line is indistinguishable from a real entry to the anchor, so the
		// comment has to be removed before the anchor sees it.
		{"an env entry written out inside a Helm comment block", "{{/*\nAn extra one goes in as\n            - name: AF_WANTED\n              value: \"1\"\n*/}}\n", false},
		{"a commented out env entry", "            # - name: AF_WANTED\n            #   value: \"1\"\n", false},
		{"a one line Helm comment", "{{/* AF_WANTED is refused */}}\n", false},
		{"a name in prose after a real entry, in a Helm comment", "            - name: AF_OTHER\n{{/*\nAF_WANTED belongs to the Job.\n*/}}\n", false},
		{"the name as a Secret key rather than an env name", "                  key: AF_WANTED\n", false},
		{"the name in a value rather than a name", "            - name: AF_OTHER\n              value: AF_WANTED\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := treeWithChart(t, oneRow, "", tc.chart, "")
			got, err := chartVariables(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := got["AF_WANTED"]; ok != tc.want {
				t.Fatalf("AF_WANTED found=%v, wanted %v, from:\n%s", ok, tc.want, tc.chart)
			}
		})
	}
}

// A Helm comment that opens and never closes must not swallow the rest of the
// file, and one that opens and closes on the same line must not open at all.
// Both are silent: the first hides every entry below it, and the second would
// count a name in the sentence that follows.
func TestAHelmCommentEndsWhereItEnds(t *testing.T) {
	root := treeWithChart(t, oneRow, "",
		"{{/* AF_ONE is refused */}}\n            - name: AF_TWO\n"+
			"{{/*\nAF_THREE belongs elsewhere\n*/}}\n            - name: AF_FOUR\n", "")
	got, err := chartVariables(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []string{"AF_ONE", "AF_THREE"} {
		if _, ok := got[hidden]; ok {
			t.Errorf("%s is inside a comment and was counted as delivered", hidden)
		}
	}
	for _, real := range []string{"AF_TWO", "AF_FOUR"} {
		if _, ok := got[real]; !ok {
			t.Errorf("%s is a real env entry and was not found; a comment swallowed it", real)
		}
	}
}

// A chart with no templates at all is an error rather than an empty answer,
// for the reason the floors exist: an empty read agrees with everything.
func TestAChartWithNoTemplatesIsAnError(t *testing.T) {
	root := t.TempDir()
	if _, err := chartVariables(root); err == nil {
		t.Fatal("a missing chart read as a chart that sets nothing")
	}
}

// The gate on the REAL tree, which is the assertion that fails when somebody
// documents a variable and forgets the env block. Everything above proves the
// reader; this proves the answer.
func TestTheRepositoryItselfCanDeliverWhatItDocuments(t *testing.T) {
	root := "../.."
	documented, err := documentedVariables(root)
	if err != nil {
		t.Fatal(err)
	}
	routes := testRoutes(t, root)
	if err := floors(documented, routes); err != nil {
		t.Fatal(err)
	}
	exempt, order, err := exemptions(root, routes)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]route{}
	for _, r := range routes {
		byKey[r.key] = r
	}

	for _, r := range routes {
		for name, where := range documented {
			if r.sets[name] == "" && exempt[r.key][name] == "" {
				t.Errorf("%s is documented at %s and %s cannot set it", name, where, r.what)
			}
		}
		for name, where := range r.sets {
			if documented[name] == "" {
				t.Errorf("%s is set at %s and %s does not document it", name, where, referencePath)
			}
		}
	}
	for _, row := range order {
		if documented[row.name] == "" {
			t.Errorf("%s is exempt on %s and %s no longer documents it", row.name, row.target, referencePath)
		}
		if where := byKey[row.target].sets[row.name]; where != "" {
			t.Errorf("%s is exempt on %s and it is now set at %s", row.name, row.target, where)
		}
	}
}
