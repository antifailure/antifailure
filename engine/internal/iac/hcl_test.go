package iac

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// evalOne reads one attribute out of a one resource module, which is the
// shortest way to ask "what does this expression come out as".
func evalOne(t *testing.T, expr string, opts ...Option) Attr {
	t.Helper()
	r := readTree(t, map[string]string{"main.tf": `
variable "known" {
  type    = string
  default = "yes"
}
variable "unset" {
  type = string
}
locals {
  region = "eu-west-2"
  nested = "${local.region}-a"
}
resource "aws_s3_bucket" "b" {
  subject = ` + expr + `
}
`}, opts...)
	c := componentAt(t, r, "aws_s3_bucket.b")
	for _, a := range c.Attrs {
		if a.Name == "subject" {
			return a
		}
	}
	// The file may have been refused whole, which is a legitimate outcome the
	// caller asserts on, so this reports rather than fails.
	return Attr{}
}

// TestTheLexerReadsTheThingsRealConfigurationsAreMadeOf.
func TestTheLexerReadsTheThingsRealConfigurationsAreMadeOf(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		expr string
		want string
	}{
		{"a plain string", `"hello"`, "hello"},
		{"an escape", `"a\tb\n"`, "a\tb\n"},
		{"a unicode escape", `"caf\u00e9"`, "café"},
		{"an escaped interpolation", `"\${not_interpolated}"`, "${not_interpolated}"},
		{"a number", `8080`, "8080"},
		{"a boolean", `true`, "true"},
		{"a variable with a default", `var.known`, "yes"},
		{"a local", `local.region`, "eu-west-2"},
		{"a local built from another local", `local.nested`, "eu-west-2-a"},
		{"an interpolation", `"bucket-${var.known}"`, "bucket-yes"},
		{"two interpolations", `"${local.region}/${var.known}"`, "eu-west-2/yes"},
		{"an interpolation holding a string", `"${var.known}-x"`, "yes-x"},
		{"a parenthesised reference", `(var.known)`, "yes"},
		{"a heredoc", "<<EOT\nline one\nline two\nEOT", "line one\nline two\n"},
		{"an indented heredoc", "<<-EOT\n    indented\n      more\n    EOT", "indented\n  more\n"},
		// The first line is NOT the least indented one here, which is what
		// makes this case able to tell "strip the smallest indentation" apart
		// from "strip the first line's indentation". With only the case above,
		// both rules give the same answer and the test proves nothing.
		{"an indented heredoc whose first line is the deepest", "<<-EOT\n      more\n    indented\n    EOT", "  more\nindented\n"},
		{"a heredoc with an interpolation", "<<EOT\nregion ${local.region}\nEOT", "region eu-west-2\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := evalOne(t, tc.expr)
			got, ok := a.Value.Get()
			require.Truef(t, ok, "%s did not resolve: %s", tc.name, a.Value.Why())
			require.Equal(t, tc.want, got)
		})
	}
}

// TestAnExpressionThisReaderCannotEvaluateSaysWhichOne.
//
// Every one of these is a real shape from real configurations, and the point
// of each assertion is the WORDING: a reason has to name the thing standing
// between the reader and the value, because that is what makes it actionable,
// and it must never quote the expression, because an expression can hold a
// credential written in line.
func TestAnExpressionThisReaderCannotEvaluateSaysWhichOne(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		expr string
		says string
	}{
		{"a variable with no default", `var.unset`, "variable unset has no default"},
		{"a variable that is not declared", `var.nowhere`, "is not declared in this root module"},
		{"a function call", `join(",", var.known)`, "calls join"},
		{"a conditional", `var.known == "yes" ? "a" : "b"`, "is a conditional"},
		{"a data source", `data.aws_caller_identity.me.account_id`, "a data source only a plan"},
		{"a module output", `module.network.subnet_id`, "module.network"},
		{"another resource's attribute", `aws_vpc.main.id`, "only exists after an apply"},
		{"a for_each instance", `each.value`, "which instance of a for_each"},
		{"the count index", `count.index`, "count.index"},
		{"the path", `path.module`, "path.module"},
		{"the workspace with none named", `terraform.workspace`, "no workspace was named"},
		{"a template directive", `"%{ if true }a%{ endif }"`, "template directive"},
		{"an operator", `var.known + 1`, "combines values with +"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := evalOne(t, tc.expr)
			require.Equalf(t, Unreadable, a.Value.State(),
				"%s resolved to something, which means this reader guessed", tc.name)
			require.Containsf(t, a.Value.Why(), tc.says,
				"the reason for %s does not name what stood in the way", tc.name)
			require.Positivef(t, a.Value.At().Line, "%s has no line, so it is not actionable", tc.name)
		})
	}
}

// TestTheWorkspaceResolvesWhenTheCallerNamesOne is the other arm of the
// workspace case above: the option has to change the answer, or it is a field
// nobody should have added.
func TestTheWorkspaceResolvesWhenTheCallerNamesOne(t *testing.T) {
	t.Parallel()
	a := evalOne(t, `"${terraform.workspace}-assets"`, WithWorkspace("production"))
	got, ok := a.Value.Get()
	require.True(t, ok, "naming a workspace did not resolve terraform.workspace: %s", a.Value.Why())
	require.Equal(t, "production-assets", got)
}

// TestAVarFileChangesTheAnswerOnlyWhenItIsAskedFor covers both halves of the
// tfvars rule.
func TestAVarFileChangesTheAnswerOnlyWhenItIsAskedFor(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"main.tf": `
variable "size" {
  type = string
}
resource "aws_s3_bucket" "b" {
  bucket = var.size
}
`,
		"production.tfvars": `size = "large"`,
	}

	// Not asked for: the value must stay unreadable, and the file must say why
	// it was not used. A reader that helped itself to every tfvars in the tree
	// would describe staging's production.
	plain := readTree(t, files)
	require.Equal(t, Unreadable, attrOf(t, plain, "aws_s3_bucket.b", "bucket").State())
	var src Source
	for _, s := range plain.Sources {
		if s.Path == "production.tfvars" {
			src = s
		}
	}
	require.False(t, src.Read)
	require.Contains(t, src.Why, "was not named for this read")

	// Asked for: it resolves.
	asked := readTree(t, files, WithVarFiles("production.tfvars"))
	got, ok := attrOf(t, asked, "aws_s3_bucket.b", "bucket").Get()
	require.True(t, ok, "a variable file that WAS named did not change the answer")
	require.Equal(t, "large", got)
}

func attrOf(t *testing.T, r *Reading, address, name string) Value[string] {
	t.Helper()
	for _, a := range componentAt(t, r, address).Attrs {
		if a.Name == name {
			return a.Value
		}
	}
	t.Fatalf("%s has no attribute %s", address, name)
	return Value[string]{}
}

// TestAFileTheLexerCannotReadIsRefusedWHOLE is the containment for the one
// failure mode a hand written tokenizer has that a library does not.
//
// A mis-parse is worse than a hole, so the rule is that a file the lexer is
// unsure about contributes NOTHING. The second half of each case is the half
// that matters: the file BESIDE the broken one is still read in full, so
// failing closed costs one file and never the tree.
func TestAFileTheLexerCannotReadIsRefusedWhole(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		broken string
		says   string
	}{
		{"an unclosed block", "resource \"aws_s3_bucket\" \"a\" {\n  bucket = \"x\"\n", "never closed"},
		{"an unclosed string", "resource \"aws_s3_bucket\" \"a\" {\n  bucket = \"x\n}\n", "line break inside a quoted string"},
		{"an unclosed comment", "/* resource \"aws_s3_bucket\" \"a\" {\n", "never closed"},
		{"an unclosed interpolation", "resource \"a\" \"b\" {\n  c = <<EOT\n${var.x\nEOT\n}\n", "${ and never closed"},
		{"an unclosed heredoc", "resource \"a\" \"b\" {\n  c = <<EOT\nbody\n}\n", "never closed by a line saying EOT"},
		{"an unknown escape", "resource \"a\" \"b\" {\n  c = \"a\\qb\"\n}\n", "escape \\q"},
		{"a stray closing brace", "}\nresource \"a\" \"b\" {\n}\n", "closing brace with no block open"},
		{"an attribute with no value", "resource \"a\" \"b\" {\n  c =\n}\n", "nothing after its ="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := readTree(t, map[string]string{
				"broken.tf": tc.broken,
				"fine.tf":   "resource \"aws_sqs_queue\" \"jobs\" {\n  name = \"jobs\"\n}\n",
			})

			var broken Source
			for _, s := range r.Sources {
				if s.Path == "broken.tf" {
					broken = s
				}
			}
			require.False(t, broken.Read, "a file the lexer could not read reported itself read")
			require.Containsf(t, broken.Why, tc.says, "the refusal does not say what was wrong")

			// Nothing from the broken file may have reached the reading. This
			// is the assertion that catches a partial parse: a lexer that
			// recovered would have produced aws_s3_bucket.a from most of the
			// cases above.
			for _, c := range r.Components {
				require.NotEqualf(t, "broken.tf", c.At.File,
					"a file the lexer refused still contributed %s", c.Address)
			}
			// And the file beside it is untouched.
			require.Equal(t, "aws_sqs_queue.jobs", componentAt(t, r, "aws_sqs_queue.jobs").Address)
		})
	}
}

// TestALocalThatRefersToItselfStopsRatherThanRecursing. Without the guard this
// is a stack overflow, which takes the whole process down instead of reporting
// a value that could not be read, and a reader of other people's
// infrastructure meets malformed input as a matter of course.
func TestALocalThatRefersToItselfStopsRatherThanRecursing(t *testing.T) {
	t.Parallel()
	r := readTree(t, map[string]string{"main.tf": `
locals {
  a = local.b
  b = local.a
}
resource "aws_s3_bucket" "b" {
  bucket = local.a
}
`})
	v := attrOf(t, r, "aws_s3_bucket.b", "bucket")
	require.Equal(t, Unreadable, v.State())
	require.Contains(t, v.Why(), "defined in terms of itself")
}

// TestOneUnresolvableElementDoesNotBlankTheList is the decode boundary rule
// applied to expressions: a list with one computed entry keeps the rest.
func TestOneUnresolvableElementDoesNotBlankTheList(t *testing.T) {
	t.Parallel()
	r := readTree(t, map[string]string{"main.tf": `
variable "unset" {
  type = string
}
resource "aws_s3_bucket" "b" {
  tags = {
    env     = "production"
    owner   = var.unset
    region  = "eu-west-2"
  }
}
`})
	c := componentAt(t, r, "aws_s3_bucket.b")
	byName := map[string]Attr{}
	for _, a := range c.Attrs {
		byName[a.Name] = a
	}
	require.Equal(t, "production", byName["tags.env"].Value.Or(""),
		"one unresolvable entry blanked the whole object")
	require.Equal(t, "eu-west-2", byName["tags.region"].Value.Or(""))
	require.Equal(t, Unreadable, byName["tags.owner"].Value.State())
}

// TestTheReaderDescribesThisRepositorysOwnInfrastructure is the dogfood, and
// it is the test that has found every defect worth finding in this package.
//
// It reads the real control plane module, which carries dynamic blocks,
// conditional counts, locals, function calls and cross resource references,
// and asserts the facts a person can check by opening the files. It is not a
// golden file: a golden would have to be regenerated every time the
// infrastructure changed, and then it would assert only that this reader
// still does whatever it did last time.
func TestTheReaderDescribesThisRepositorysOwnInfrastructure(t *testing.T) {
	t.Parallel()
	r, err := Read(context.Background(), "../../../infra/terraform/modules/control-plane")
	require.NoError(t, err)

	require.NotEmpty(t, r.Sources)
	for _, s := range r.Sources {
		require.Truef(t, s.Read, "%s was not read: %s", s.Path, s.Why)
	}
	require.Empty(t, r.Refused)

	// The Postgres, which is the fact a fidelity report most needs.
	db := componentAt(t, r, "azurerm_postgresql_flexible_server.this")
	require.Equal(t, KindPostgres, db.Kind)
	require.Equal(t, "postgres", db.Engine.Or(""))
	require.Equal(t, "17", db.Version.Or(""),
		"the control plane's Postgres version is stated in database.tf and this reader lost it")
	require.Equal(t, "B_Standard_B1ms", attrOf(t, r, "azurerm_postgresql_flexible_server.this", "sku_name").Or(""))
	require.Equal(t, "false", attrOf(t, r, "azurerm_postgresql_flexible_server.this", "public_network_access_enabled").Or(""),
		"the control plane's database has no public endpoint and this reader did not see that")

	// The server parameter, which lives in a SEPARATE resource pointing back
	// at the server, and which a reader that only looked inside the server
	// block would report as none.
	require.NotEmpty(t, db.Params, "the server parameter set on this database was lost, because it "+
		"is declared as its own resource rather than as an attribute")

	// The application, its environment and its secrets.
	app := componentAt(t, r, "azurerm_container_app.this")
	require.Equal(t, KindService, app.Kind)
	require.Greater(t, len(app.Env), 20,
		"the control plane declares dozens of environment variables and this reader found %d", len(app.Env))
	require.NotEmpty(t, app.Secrets)
	require.NotEmpty(t, app.Probes)

	// NOT ONE environment VALUE may be in the reading, only names. This is the
	// assertion that would catch the whole class of leak at once.
	for _, e := range app.Env {
		require.NotContainsf(t, e.Name, "=", "an environment entry carries a value: %q", e.Name)
	}

	// And the credential controls, on the real file rather than on a fixture
	// written to suit them.
	require.Equal(t, Withheld,
		attrOf(t, r, "azurerm_postgresql_flexible_server.this", "administrator_password").State(),
		"the real database's administrator_password was carried")

	// The unmeasured list must be real on both counts: not empty, because this
	// module genuinely cannot be fully resolved without running Terraform, and
	// every entry must carry a reason and a position or it is not actionable.
	um := r.Unmeasured()
	require.NotEmpty(t, um, "reading a module full of variables with no defaults produced no "+
		"unmeasured items at all, which means holes are being reported as values")
	for _, u := range um {
		require.NotEmptyf(t, u.Why, "%s has no reason", u.What)
		require.NotEmptyf(t, u.At.File, "%s has no file", u.What)
		require.NotContainsf(t, strings.ToLower(u.Why), "unsupported",
			"%s reads as a gap in the parser rather than a fact about the configuration", u.What)
	}
}
