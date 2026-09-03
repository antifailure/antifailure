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
	if exempt != "" {
		write(exemptionsPath, exempt)
	}
	return root
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

// The one section whose variables the control plane never reads from its own
// environment. The page states it; this reads the statement rather than keeping
// a second copy of it in a TSV.
func TestSkipsTheSectionTheReferenceSaysIsNotSetHere(t *testing.T) {
	page := oneRow + "\n## " + engineSection + "\n\n| Variable | Where | What |\n| --- | --- | --- |\n" +
		"| `AF_ON_THE_ENGINE` | On the engine | Not read here. |\n" +
		"\n## Analytics\n\n| Variable | Default | What |\n| --- | --- | --- |\n" +
		"| `AF_AFTER_THE_SECTION` | unset | Read here again. |\n"
	documented, err := documentedVariables(tree(t, page, "", ""))
	if err != nil {
		t.Fatal(err)
	}
	if where := documented["AF_ON_THE_ENGINE"]; where != "" {
		t.Fatalf("a variable under %q was read as one this module must set, at %s", engineSection, where)
	}
	// The section ENDS at the next heading. A skip that ran to the end of the
	// file would swallow the analytics table, which is where AF_SITE_ORIGIN and
	// the surrogate secret live -- five of the variables this gate was written
	// for.
	if documented["AF_AFTER_THE_SECTION"] == "" {
		t.Fatal("the skip did not stop at the next heading, so every later table was ignored")
	}
}

// ONE BREAK PER ASSERTION. Each case below is a different way the wire can be
// missing, and each one is asserted on its own so that a later check cannot be
// made unreachable by an earlier one failing first.
func TestExemptions(t *testing.T) {
	t.Run("a row with a reason silences the finding", func(t *testing.T) {
		reasons, order, err := exemptions(tree(t, oneRow, "", "AF_WANTED\tstamped at build time\n"))
		if err != nil {
			t.Fatal(err)
		}
		if reasons["AF_WANTED"] != "stamped at build time" {
			t.Fatalf("the reason was not read: %q", reasons["AF_WANTED"])
		}
		if len(order) != 1 {
			t.Fatalf("expected one row, got %d", len(order))
		}
	})

	t.Run("a row with no reason is refused", func(t *testing.T) {
		_, _, err := exemptions(tree(t, oneRow, "", "AF_WANTED\n"))
		if err == nil {
			t.Fatal("a row with no reason was accepted, which is an allowance with no argument behind it")
		}
		if !strings.Contains(err.Error(), "no reason") {
			t.Fatalf("the error does not say what is wrong: %v", err)
		}
	})

	t.Run("a row whose reason is only whitespace is refused", func(t *testing.T) {
		_, _, err := exemptions(tree(t, oneRow, "", "AF_WANTED\t   \n"))
		if err == nil {
			t.Fatal("a row with a blank reason was accepted")
		}
	})

	t.Run("the same variable twice is refused", func(t *testing.T) {
		_, _, err := exemptions(tree(t, oneRow, "", "AF_WANTED\tone\nAF_WANTED\ttwo\n"))
		if err == nil {
			t.Fatal("two rows for one variable were accepted, so one reason silently wins")
		}
	})

	t.Run("comments and blank lines are not rows", func(t *testing.T) {
		reasons, _, err := exemptions(tree(t, oneRow, "", "# why this file exists\n\nAF_WANTED\tstamped\n"))
		if err != nil {
			t.Fatal(err)
		}
		if len(reasons) != 1 {
			t.Fatalf("expected one row, got %d", len(reasons))
		}
	})

	t.Run("a missing file is no rows rather than an error", func(t *testing.T) {
		reasons, order, err := exemptions(tree(t, oneRow, "", ""))
		if err != nil {
			t.Fatal(err)
		}
		if len(reasons) != 0 || len(order) != 0 {
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
	settable := map[string]string{}
	for i := 0; i < minSettable; i++ {
		settable[string(rune('A'+i))] = "somewhere"
	}

	t.Run("a reference that read as empty", func(t *testing.T) {
		if err := floors(map[string]string{}, settable); err == nil {
			t.Fatal("zero documented variables passed the floor")
		}
	})
	t.Run("a module that read as empty", func(t *testing.T) {
		if err := floors(full, map[string]string{}); err == nil {
			t.Fatal("zero settable variables passed the floor")
		}
	})
	t.Run("both surfaces read", func(t *testing.T) {
		if err := floors(full, settable); err != nil {
			t.Fatalf("a plausible read was refused: %v", err)
		}
	})
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
	settable, err := settableVariables(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := floors(documented, settable); err != nil {
		t.Fatal(err)
	}
	exempt, order, err := exemptions(root)
	if err != nil {
		t.Fatal(err)
	}

	for name, where := range documented {
		if settable[name] == "" && exempt[name] == "" {
			t.Errorf("%s is documented at %s and no supported deploy can set it", name, where)
		}
	}
	for name, where := range settable {
		if documented[name] == "" {
			t.Errorf("%s is set at %s and %s does not document it", name, where, referencePath)
		}
	}
	for _, name := range order {
		if documented[name] == "" {
			t.Errorf("%s is exempt and %s no longer documents it", name, referencePath)
		}
		if where := settable[name]; where != "" {
			t.Errorf("%s is exempt and the module now sets it at %s", name, where)
		}
	}
}
