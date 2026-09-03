package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tf writes one variables.tf and reads it back.
func tf(t *testing.T, body string) []input {
	t.Helper()
	path := filepath.Join(t.TempDir(), "variables.tf")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := blockInputs(path, "terraform:test", "variable")
	if err != nil {
		t.Fatalf("reading variables: %v", err)
	}
	return got
}

func names(got []input) string {
	var out []string
	for _, i := range got {
		need := "optional"
		if i.required {
			need = "required"
		}
		out = append(out, i.name+" "+i.kind+" "+need)
	}
	return strings.Join(out, "; ")
}

// The four shapes a variable is really written in here, and three of them are
// shapes a shallow reader gets wrong QUIETLY rather than loudly. A heredoc
// description and a validation block both carry braces, so a reader that
// counts them walks off the end of one block and reads the next variable's
// `default` as this one's, which turns a required input into an optional one
// in the snapshot and freezes the wrong promise.
func TestReadsEveryShapeAVariableIsWrittenIn(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{
			"a block with a type and a default",
			`variable "pool_max" {
  type    = number
  default = 10
}`,
			"pool_max number optional",
		},
		{
			"a block with no default, which is required",
			`variable "subscription_id" {
  type = string
}`,
			"subscription_id string required",
		},
		{
			"the whole variable on one line, which nine of the real ones are",
			`variable "location" { type = string }`,
			"location string required",
		},
		{
			"one line carrying a default too",
			`variable "tags" { type = map(string)
  default = {} }`,
			"tags map(string) optional",
		},
		{
			"a heredoc description carrying braces, then another variable",
			`variable "first" {
  type    = string
  default = ""
  description = <<-EOT
    A brace { and its partner } inside prose, and a ${interpolation} too.
    default = "this sentence is not an argument"
  EOT
}

variable "second" {
  type = bool
}`,
			"first string optional; second bool required",
		},
		{
			"a validation block, whose braces are not the variable's",
			`variable "guarded" {
  type = string
  validation {
    condition     = can(regex("^[a-z]{3}$", var.guarded))
    error_message = "Three letters. A brace { in a message must not end this block."
  }
}

variable "after" {
  type    = number
  default = 1
}`,
			"guarded string required; after number optional",
		},
		{
			"a map default, whose braces close before the variable does",
			`variable "tags" {
  type    = map(string)
  default = {}
}`,
			"tags map(string) optional",
		},
		{
			"a comment that names an argument it is not",
			`variable "commented" {
  # default = 3 would be wrong here, and this line says so rather than does it.
  type = list(string)
}`,
			"commented list(string) required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := names(tf(t, tc.body)); got != tc.want {
				t.Errorf("read %q, want %q", got, tc.want)
			}
		})
	}
}

// Outputs are read from the same files by name alone. Two runbook commands
// read one, so a rename there breaks a documented instruction as surely as a
// renamed variable breaks a tfvars file.
func TestReadsOutputsByNameAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outputs.tf")
	body := `output "backend_hcl" {
  value       = "..."
  description = "Braces { } and a validation-looking block must not confuse this."
}

output "location" { value = azurerm_resource_group.this.location }

variable "not_an_output" {
  type = string
}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := blockInputs(path, "terraform:test", "output")
	if err != nil {
		t.Fatal(err)
	}
	if want := "backend_hcl - optional; location - optional"; names(got) != want {
		t.Errorf("read %q, want %q", names(got), want)
	}
	for _, o := range got {
		if o.sort != "output" {
			t.Errorf("%s came back as %q rather than an output", o.name, o.sort)
		}
	}
}

// A type this cannot read is refused rather than recorded as half of itself.
// Recording half a type would put a wrong line in the snapshot and then freeze
// it, which is worse than declining to read the file.
func TestRefusesATypeItCannotRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "variables.tf")
	body := `variable "shaped" {
  type = object({
    a = string
  })
}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := blockInputs(path, "terraform:test", "variable"); err == nil {
		t.Fatal("read a structural type without complaining, so the snapshot would hold half of one")
	}
}

// A variable with no type is refused too. The promise is about types, so an
// input that does not declare one cannot be part of it.
func TestRefusesAVariableWithNoType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "variables.tf")
	if err := os.WriteFile(path, []byte("variable \"loose\" {\n  default = 1\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := blockInputs(path, "terraform:test", "variable"); err == nil {
		t.Fatal("accepted a variable with no declared type")
	}
}

// The chart's inputs are key paths, the type of one is the type of its
// default, and a list is a leaf. `ingress.hosts` carries Kubernetes' own
// shape, so descending into it would put somebody else's contract inside ours.
func TestValuesAreKeyPathsAndListsAreLeaves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "values.yaml")
	body := `replicaCount: 2
config:
  appBaseUrl: ""
  poolMax: 10
  insecureCookies: false
  signinAllowlist: null
ingress:
  hosts:
    - host: cp.example.com
      paths:
        - path: /
podAnnotations: {}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := valuesInputs(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "replicaCount number optional; config.appBaseUrl string optional; " +
		"config.poolMax number optional; config.insecureCookies bool optional; " +
		"config.signinAllowlist null optional; ingress.hosts list optional; " +
		"podAnnotations map optional"
	if names(got) != want {
		t.Errorf("read\n  %s\nwant\n  %s", names(got), want)
	}
}

// A promise nobody can lose track of: what breaks and what does not.
//
// The rows are the whole contract. Removing, renaming, retyping and tightening
// cost a major version; adding an optional input does not, and a check that
// treated an addition as a break would be deleted within a week for crying
// wolf at compatible work.
func TestSortsChangesIntoBreakingAndNot(t *testing.T) {
	golden := []input{
		{source: "helm", sort: "value", name: "config.poolMax", kind: "number"},
		{source: "terraform:m", sort: "variable", name: "pool_max", kind: "number"},
		{source: "terraform:m", sort: "variable", name: "app_base_url", kind: "string"},
		{source: "terraform:m", sort: "variable", name: "signin_allowlist", kind: "list(string)", required: true},
	}

	for _, tc := range []struct {
		name    string
		current []input
		wants   string
		clean   bool
	}{
		{
			name:    "nothing changed",
			current: golden,
			clean:   true,
		},
		{
			name: "an input is gone",
			current: []input{
				{source: "terraform:m", sort: "variable", name: "pool_max", kind: "number"},
				{source: "terraform:m", sort: "variable", name: "app_base_url", kind: "string"},
				{source: "terraform:m", sort: "variable", name: "signin_allowlist", kind: "list(string)", required: true},
			},
			wants: "removed  helm value config.poolMax",
		},
		{
			name: "an input is renamed, which is a removal and an addition that belong together",
			current: []input{
				{source: "helm", sort: "value", name: "config.connectionPoolMax", kind: "number"},
				{source: "terraform:m", sort: "variable", name: "pool_max", kind: "number"},
				{source: "terraform:m", sort: "variable", name: "app_base_url", kind: "string"},
				{source: "terraform:m", sort: "variable", name: "signin_allowlist", kind: "list(string)", required: true},
			},
			wants: "renamed  helm value config.poolMax is gone and config.connectionPoolMax",
		},
		{
			name: "an input changes type",
			current: []input{
				{source: "helm", sort: "value", name: "config.poolMax", kind: "number"},
				{source: "terraform:m", sort: "variable", name: "pool_max", kind: "string"},
				{source: "terraform:m", sort: "variable", name: "app_base_url", kind: "string"},
				{source: "terraform:m", sort: "variable", name: "signin_allowlist", kind: "list(string)", required: true},
			},
			wants: "retyped  terraform:m variable pool_max was number and is now string",
		},
		{
			name: "an optional input becomes required",
			current: []input{
				{source: "helm", sort: "value", name: "config.poolMax", kind: "number"},
				{source: "terraform:m", sort: "variable", name: "pool_max", kind: "number", required: true},
				{source: "terraform:m", sort: "variable", name: "app_base_url", kind: "string"},
				{source: "terraform:m", sort: "variable", name: "signin_allowlist", kind: "list(string)", required: true},
			},
			wants: "required terraform:m variable pool_max was optional and is now required",
		},
		{
			name: "a new input arrives required, which refuses a file that was complete yesterday",
			current: append(append([]input{}, golden...),
				input{source: "terraform:m", sort: "variable", name: "tenant_id", kind: "string", required: true}),
			wants: "new      terraform:m variable tenant_id arrived with no default",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			breaking, additions := compare(golden, tc.current)
			if tc.clean {
				if len(breaking) != 0 || len(additions) != 0 {
					t.Fatalf("reported %v and %v over an unchanged surface", breaking, additions)
				}
				return
			}
			if len(breaking) != 1 {
				t.Fatalf("want exactly one finding, got %d: %v", len(breaking), breaking)
			}
			if !strings.Contains(breaking[0], tc.wants) {
				t.Errorf("finding was\n  %s\nwanted it to contain\n  %s", breaking[0], tc.wants)
			}
		})
	}
}

// Adding an optional input is compatible, and the check has to say so rather
// than call it a break. It still fails, because the snapshot has to record the
// addition, and the message is the difference between the two.
func TestAnAddedOptionalInputIsNotBreaking(t *testing.T) {
	golden := []input{{source: "helm", sort: "value", name: "config.poolMax", kind: "number"}}
	current := append(append([]input{}, golden...),
		input{source: "helm", sort: "value", name: "config.requestTimeoutSeconds", kind: "number"})

	breaking, additions := compare(golden, current)
	if len(breaking) != 0 {
		t.Fatalf("called a compatible addition breaking: %v", breaking)
	}
	if len(additions) != 1 || additions[0].name != "config.requestTimeoutSeconds" {
		t.Fatalf("want the one addition reported, got %v", additions)
	}
}

// The real tree, read end to end, and it has to agree with the snapshot beside
// it. This is the test that fails when somebody renames an input, which is the
// whole point of the tool.
func TestTheCommittedSnapshotMatchesTheTree(t *testing.T) {
	root := "../.."
	current, err := surface(root)
	if err != nil {
		t.Fatalf("reading the tree: %v", err)
	}
	golden, err := readGolden(root)
	if err != nil {
		t.Fatalf("reading the snapshot: %v", err)
	}
	breaking, additions := compare(golden, current)
	for _, b := range breaking {
		t.Errorf("%s", b)
	}
	for _, a := range additions {
		t.Errorf("%s %s is new and the snapshot does not record it. go run ./tools/inputcheck -update .", a.source, a.name)
	}
}
