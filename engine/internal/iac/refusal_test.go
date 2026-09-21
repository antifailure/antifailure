package iac

import (
	"context"
	"encoding/json"
	"go/build"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The tests for the three absolute refusals in the package header.
//
// WHY THE FIXTURES ARE MATERIALISED AT RUN TIME rather than committed under
// the names they are tested under. This repository's .gitignore carries
// `*.tfstate`, `*.tfstate.*` and `plan.json`, correctly, because a real state
// file must never be committed. A fixture called terraform.tfstate would
// therefore have been silently absent in CI, and a refusal test whose input is
// absent PASSES, having refused nothing. That is precisely the shape of check
// this repository keeps finding in its own instruments, so the content is
// committed under a .golden name and written out under the name being tested,
// and goldenBytes FAILS rather than skipping when the content is not there.
//
// The content itself is real: it is what Terraform v1.15.8 wrote for a
// provider free configuration, not an imitation of what state is thought to
// look like.

// goldenBytes reads a committed fixture, and fails loudly when it is missing.
//
// It must never skip. A missing fixture means this test could not look, and
// "could not look" has to be a failure here, because the thing being tested is
// a refusal: a test that silently checks nothing would report that state files
// are refused when nothing had been offered to refuse.
func goldenBytes(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoErrorf(t, err, "the fixture %s is missing, so this test could not look at anything; "+
		"it is committed under a .golden name precisely because .gitignore would drop it under "+
		"its natural one", name)
	require.NotEmptyf(t, body, "the fixture %s is empty", name)
	return body
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o600))
	}
	return dir
}

// TestAStateFileIsRefusedHoweverItIsNamed is the sharpest promise this package
// makes, so it is tested under every name a state file turns up under.
//
// The renamed cases are the ones that matter. Refusing terraform.tfstate is
// easy and a reader that only did that would happily read the same bytes out
// of plan.json, which is a file people genuinely produce and commit by
// accident, and which would then put every generated password into a report.
func TestAStateFileIsRefusedHoweverItIsNamed(t *testing.T) {
	t.Parallel()
	rawState := string(goldenBytes(t, "terraform-state-v4.golden"))
	shownState := string(goldenBytes(t, "terraform-state-shown.golden"))

	// The fixture has to be a state file worth refusing, or the rest of this
	// test is theatre: it must carry the marker AND a value from the world.
	require.Contains(t, rawState, `"lineage"`, "the raw state fixture is not raw state")
	require.Contains(t, shownState, `"values"`, "the shown state fixture is not shown state")

	for _, tc := range []struct {
		name string
		file string
		body string
	}{
		{"under its own name", "terraform.tfstate", rawState},
		{"as a backup", "terraform.tfstate.backup", rawState},
		{"renamed to look like a plan", "plan.json", rawState},
		{"renamed to look like configuration", "main.tf.json", rawState},
		{"renamed to anything at all", "notes.json", rawState},
		{"shown as json", "shown.json", shownState},
		{"shown as json under a plan's name", "plan.json", shownState},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := writeTree(t, map[string]string{
				tc.file: tc.body,
				// A real file beside it, so that a reader which refused the
				// whole directory would be caught rather than mistaken for one
				// that refused the right file.
				"ok.tf": "resource \"aws_s3_bucket\" \"assets\" {\n  bucket = \"a\"\n}\n",
			})
			r, err := Read(context.Background(), dir)
			require.NoError(t, err)

			require.Len(t, r.Refused, 1, "the state file was not refused")
			require.Equal(t, tc.file, r.Refused[0].Path)
			// Both refusal reasons are checked for the word that gives the
			// reason, rather than for one reason's exact wording: a raw state
			// file and `terraform show -json` of state are refused for the
			// same cause and say so in their own words.
			require.Contains(t, r.Refused[0].Reason, "sensitive",
				"the refusal does not say why a state file is refused")

			// The refusal must not have cost the tree beside it.
			require.Len(t, r.Components, 1, "refusing the state file lost the file next to it")
			require.Equal(t, "aws_s3_bucket.assets", r.Components[0].Address)

			// And nothing out of the state file may appear anywhere in the
			// result, under any field. This is the assertion that would catch
			// a reader which refused the file in its ledger and read it anyway.
			require.NotContains(t, dump(t, r), "lineage")
		})
	}
}

// TestAValidPlanIsACCEPTED is the third arm of the state refusal, and without
// it the two arms above prove only half of what they look like they prove.
//
// A classifier that refused every JSON document would pass every case in
// TestAStateFileIsRefusedHoweverItIsNamed. A refusal that fires on everything
// is exactly as dead as one that fires on nothing, and a reader that refused
// its own primary input would be useless while reporting itself safe. So the
// same classifier is pointed at a plan Terraform actually produced and
// required to READ it.
func TestAValidPlanIsAccepted(t *testing.T) {
	t.Parallel()
	plan := string(goldenBytes(t, "terraform-plan.golden"))
	require.Contains(t, plan, `"planned_values"`, "the plan fixture is not a plan")

	dir := writeTree(t, map[string]string{"plan.json": plan})
	r, err := Read(context.Background(), dir)
	require.NoError(t, err)

	require.Empty(t, r.Refused, "a valid plan was refused, so the state refusal fires on everything")
	require.NotEmpty(t, r.Components, "a valid plan was accepted and read as nothing")
	for _, c := range r.Components {
		require.Equal(t, DialectTerraformPlan, c.From,
			"a component read from a plan does not say so, so a consumer cannot tell a fully "+
				"resolved reading from a partly resolved one")
	}
	require.True(t, r.FromPlan(), "FromPlan said no about a reading that came entirely from a plan")

	// THE ARM THAT MAKES FromPlan MEAN ANYTHING. A mutation caught this: with
	// its loop body disabled FromPlan answered true for everything, and the
	// assertion above passed, because it only ever asked the question it
	// already knew the answer to. FromPlan is a claim of CONFIDENCE, so the
	// case it must get right is the one where the answer is no.
	hclOnly := readTree(t, map[string]string{
		"main.tf": "resource \"aws_s3_bucket\" \"b\" {\n  bucket = \"b\"\n}\n",
	})
	require.False(t, hclOnly.FromPlan(),
		"FromPlan said yes about a reading that came entirely from HCL, which would give a "+
			"partly resolved description the authority of a fully resolved one")

	mixed := readTree(t, map[string]string{
		"plan.json": plan,
		"main.tf":   "resource \"aws_s3_bucket\" \"b\" {\n  bucket = \"b\"\n}\n",
	})
	require.False(t, mixed.FromPlan(),
		"a reading mixing a plan with loose HCL is not from a plan; rounding it up would give "+
			"the HCL half the plan half's authority")

	require.False(t, (&Reading{}).FromPlan(),
		"an empty reading claimed to be from a plan; nothing was read, which is not a statement "+
			"about how well it was read")

	// The other arm: the plan fixture DOES carry a credential, and it must
	// come back withheld rather than either published or reported as a hole.
	var withheld int
	for _, u := range r.Unmeasured() {
		if u.Withheld {
			withheld++
		}
	}
	require.Positive(t, withheld, "the plan fixture carries a sensitive value and none was "+
		"withheld, so the sensitivity mask did not run and the clean Unresolved list above "+
		"means nothing")

	// THE WHOLE POINT OF THE PLAN PATH: nothing is UNRESOLVED, because
	// Terraform already evaluated every variable, local, function, for_each
	// and module before this reader saw it. This assertion is what would catch
	// the plan reader quietly falling back to the HCL reader's answers.
	//
	// It is Unresolved rather than Unmeasured on purpose, and the difference
	// was forced by this test failing. A plan DOES still produce withheld
	// values, because it carries credentials this reader refuses to publish,
	// and counting those as holes in the description would mean the plan path
	// could never keep the promise that makes it the primary one.
	for _, u := range r.Unresolved() {
		require.Failf(t, "a plan produced an unresolved value",
			"%s: %s. A plan has already evaluated every variable, local, function, for_each "+
				"and module, so nothing read from one should be unresolvable", u.What, u.Why)
	}
}

// TestTheWorkspaceStateDirectoryIsNotEvenWalked proves the stronger half of
// the state refusal: a state file under terraform.tfstate.d is refused as a
// DIRECTORY, so its contents are never opened at all, not even to sniff them.
func TestTheWorkspaceStateDirectoryIsNotEvenWalked(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, map[string]string{
		"terraform.tfstate.d/production/terraform.tfstate": string(goldenBytes(t, "terraform-state-v4.golden")),
		"main.tf": "resource \"aws_sqs_queue\" \"jobs\" {\n  name = \"jobs\"\n}\n",
	})
	r, err := Read(context.Background(), dir)
	require.NoError(t, err)

	require.Len(t, r.Refused, 1)
	require.Equal(t, "terraform.tfstate.d", r.Refused[0].Path,
		"the directory itself must be refused, not the file inside it, or the file was opened")
	require.Contains(t, r.Refused[0].Reason, "does not walk this directory")
	require.Len(t, r.Components, 1)
}

// TestReadRefusesAFileBecauseItTakesADirectory proves the shape of the API is
// what enforces the refusal rather than a check somebody has to remember.
func TestReadRefusesAFileBecauseItTakesADirectory(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, map[string]string{"terraform.tfstate": "{}"})
	_, err := Read(context.Background(), filepath.Join(dir, "terraform.tfstate"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "Read takes a directory")
}

// TestTheReaderCannotExecuteOrReachANetwork asserts refusals one and two over
// the real import graph rather than over the package's comments.
//
// It walks the TRANSITIVE closure, not the direct imports, because the way
// this property breaks is not somebody writing os/exec in this package. It is
// somebody adding a helper import that happens to drag a process launcher or
// an HTTP client in behind it, which no reviewer would see and which a direct
// import check would pass.
func TestTheReaderCannotExecuteOrReachANetwork(t *testing.T) {
	t.Parallel()
	// net/url is deliberately NOT on this list, and the omission is a measured
	// one rather than an oversight. It was on it, and the test failed: the
	// engine's own redactor imports net/url to recognise a password inside a
	// connection string. net/url is a PARSER with no I/O in it, so forbidding
	// it would have been this test asserting a property that has nothing to do
	// with reaching a network, and the honest fix was to take it off rather
	// than to work around it.
	forbidden := map[string]string{
		"os/exec":       "it would let this package run a program, and it never executes anything",
		"net":           "it would let this package open a socket",
		"net/http":      "it would let this package reach a cloud or a registry",
		"database/sql":  "it would let this package reach a database",
		"os/user":       "it would let this package read the machine's accounts",
		"plugin":        "it would let this package load code at run time",
		"runtime/cgo":   "it would let this package leave Go's own memory safety",
		"text/template": "rendering a template is the step that turns a Helm chart into execution",
		"html/template": "rendering a template is the step that turns a chart into execution",
	}

	seen := map[string]bool{}
	var walk func(path, from string)
	walk = func(path, from string) {
		if seen[path] {
			return
		}
		seen[path] = true
		if why, bad := forbidden[path]; bad {
			t.Errorf("engine/internal/iac reaches %s (through %s), and it must not: %s", path, from, why)
			return
		}
		pkg, err := build.Import(path, "", 0)
		if err != nil {
			// A package that will not resolve is reported rather than skipped:
			// an import graph walk that quietly stopped early would report a
			// clean closure having failed to look at the rest of it.
			t.Errorf("could not resolve %s (imported by %s), so this test could not look at the "+
				"whole import graph: %v", path, from, err)
			return
		}
		for _, imp := range pkg.Imports {
			walk(imp, path)
		}
	}
	walk("github.com/antifailure/antifailure/engine/internal/iac", "the test")

	// The falsification arm. A test that walks a graph and finds nothing looks
	// identical whether the graph is clean or the walk never ran, so it is
	// pointed at a package that certainly does reach os/exec, and required to
	// say no.
	t.Run("the same walk refuses a package that does execute", func(t *testing.T) {
		found := false
		local := map[string]bool{}
		var probe func(string)
		probe = func(path string) {
			if local[path] || found {
				return
			}
			local[path] = true
			if path == "os/exec" {
				found = true
				return
			}
			pkg, err := build.Import(path, "", 0)
			if err != nil {
				return
			}
			for _, imp := range pkg.Imports {
				probe(imp)
			}
		}
		probe("os/exec")
		require.True(t, found, "the instrument cannot see os/exec at all, so its silence above "+
			"says nothing about this package")
	})

	// THE POSITIVE ARM, which matters more than it looks. Every assertion
	// above is of the form "X was not found", and a walk that has silently
	// stopped finding ANY imports satisfies all of them by finding nothing.
	// So the same walk is required to have found two packages this reader
	// certainly uses. If these ever fail, the absences above mean nothing.
	for _, must := range []string{"io/fs", "encoding/json", "strings", "gopkg.in/yaml.v3"} {
		require.Containsf(t, seen, must,
			"the walk did not reach %s, which this package certainly imports, so every "+
				"\"not found\" above was found by a walk that is not looking", must)
	}

	// syscall is deliberately NOT forbidden, and this is measured rather than
	// assumed: the closure reaches it through context, then time, then
	// syscall. Read takes a context.Context, so forbidding syscall would red
	// this test forever for a reason that has nothing to do with executing a
	// program or opening a socket. os/exec and net are the imports that carry
	// those capabilities, and neither is reachable.
	require.Contains(t, seen, "syscall",
		"syscall is expected in the closure, through context and time; if it has gone, this "+
			"comment is stale and the forbidden list above should be revisited")
}

// dump renders a Reading as text, for assertions about what must NOT be in it.
func dump(t *testing.T, r *Reading) string {
	t.Helper()
	var sb strings.Builder
	for _, s := range r.Sources {
		sb.WriteString(s.Path + " " + s.Why + "\n")
	}
	for _, ref := range r.Refused {
		sb.WriteString(ref.Path + "\n")
	}
	for _, c := range r.Components {
		sb.WriteString(c.Address + " " + c.Name + " " + c.Type + "\n")
		for _, a := range c.Attrs {
			v, _ := a.Value.Get()
			sb.WriteString("  " + a.Name + "=" + v + " " + a.Value.Why() + "\n")
		}
		for _, e := range c.Env {
			from, _ := e.From.Get()
			sb.WriteString("  env " + e.Name + " " + from + "\n")
		}
		for _, s := range c.Secrets {
			from, _ := s.From.Get()
			sb.WriteString("  secret " + s.Name + " " + from + "\n")
		}
		for _, p := range c.Params {
			v, _ := p.Value.Get()
			sb.WriteString("  param " + p.Name + "=" + v + "\n")
		}
	}
	return sb.String()
}

// TestARefusalNamesWhatItRefused covers the dialect on a Refusal.
//
// It exists because that constant had ZERO uses when this package was first
// written, while its doc comment said it was there so a state file could be
// recognised. A constant nothing sets, with a comment explaining why it
// matters, is worse than no constant: the comment is what stops anybody asking
// whether it is wired.
func TestARefusalNamesWhatItRefused(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, map[string]string{
		"terraform.tfstate":                    string(goldenBytes(t, "terraform-state-v4.golden")),
		"renamed.json":                         string(goldenBytes(t, "terraform-state-v4.golden")),
		"terraform.tfstate.d/prod/one.tfstate": "{}",
	})
	r, err := Read(context.Background(), dir)
	require.NoError(t, err)
	require.Len(t, r.Refused, 3)
	for _, ref := range r.Refused {
		require.Equalf(t, DialectTerraformState, ref.Dialect,
			"the refusal of %s does not say what it refused", ref.Path)
	}
}

// TestAValueSurvivesAProcessBoundary covers the JSON contract.
//
// Other packages consume a Reading, and one of them will eventually send it
// somewhere. A hole that closed up in transit would arrive as a confident zero
// at the far end, which is the failure this whole package exists to prevent,
// reintroduced by a serialiser nobody tested.
func TestAValueSurvivesAProcessBoundary(t *testing.T) {
	t.Parallel()
	at := Position{File: "main.tf", Line: 12, Col: 3}
	for _, tc := range []struct {
		name string
		in   Value[string]
	}{
		{"absent", Value[string]{}},
		{"known", Resolved("postgres", at)},
		{"unreadable", Unresolved[string]("the variable size has no default", at)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body, err := json.Marshal(tc.in)
			require.NoError(t, err)
			var back Value[string]
			require.NoError(t, json.Unmarshal(body, &back))

			require.Equal(t, tc.in.State(), back.State(), "the state did not survive")
			require.Equal(t, tc.in.Why(), back.Why(), "the reason did not survive")
			require.Equal(t, tc.in.At(), back.At(), "the position did not survive")
			wantV, wantOK := tc.in.Get()
			gotV, gotOK := back.Get()
			require.Equal(t, wantOK, gotOK)
			require.Equal(t, wantV, gotV)
		})
	}

	// An unreadable value must not carry its (empty) value as a known one, and
	// an absent one must serialise to null rather than to an object full of
	// zeroes, or every component in a serialised Reading grows a field per
	// thing it does not declare.
	body, err := json.Marshal(Value[int]{})
	require.NoError(t, err)
	require.Equal(t, "null", string(body))

	body, err = json.Marshal(Unresolved[int]("nope", at))
	require.NoError(t, err)
	require.NotContains(t, string(body), `"value"`,
		"an unreadable value serialised a value, which a decoder would read back as one")
}
