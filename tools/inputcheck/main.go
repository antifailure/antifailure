// Command inputcheck holds the self-hosting inputs to the promise version 1
// makes about them.
//
// A self hoster writes two files and never writes them again: a Helm values
// file and a tfvars file. Those files ARE their configuration, kept in their
// own repository, applied by their own pipeline. Until v1.0.0 the release
// notes said both surfaces were free to change in a minor release, and the
// consequence of that sentence is not cosmetic: every upgrade becomes a manual
// migration that nobody can automate, because no operator can know whether
// `pool_max` still exists in the version they are about to take.
//
// v1.0.0 turns that carve out into a promise, and a promise nothing checks is
// the same sentence with better manners. So this is the check.
//
// WHAT IS PROMISED, and it is deliberately narrower than "the inputs do not
// change":
//
//   - The NAME of an input. It is not removed and it is not renamed.
//   - Its TYPE. A string does not become a list.
//   - Whether it is REQUIRED. An optional input does not become required, and
//     a new input does not arrive required, because either one refuses an
//     existing tfvars file that was complete the day before.
//
// Terraform OUTPUTS are on the list too, by name only. Two runbook commands
// read one: self-hosting/azure.md pipes `terraform output -raw backend_hcl`
// into a backend configuration and rotating-secrets.md scopes a role
// assignment with `terraform output -raw key_vault_id`. A surface a document
// tells somebody to run is a surface, and renaming one breaks the instruction
// as surely as renaming a variable breaks a tfvars file.
//
// DEFAULTS ARE NOT PROMISED, and saying so is more useful than pretending.
// `image_tag` moves to the release being cut on every release; tools/tagsync
// exists to make sure it moves. A promise this file could not keep is worse
// than the carve out it replaces.
//
// HOW IT READS THE TWO SURFACES.
//
// The chart's inputs are the key paths in values.yaml, and the type of one is
// the type of the default sitting there, because that default is the only
// declaration a Helm chart makes. A map is descended into and a list is a leaf:
// the entries of `ingress.hosts` are Kubernetes' shape rather than ours, and
// promising the inside of a list would be promising somebody else's contract.
//
// Terraform declares both halves outright. The type is the `type` argument and
// an input is required exactly when its block carries no `default`. An output
// declares neither, so only its name is read and only its name is promised.
//
// The variables are read by tracking brace depth rather than by matching a
// block with one regular expression, and the reason is in this repository's
// contract: a pattern that cannot match looks exactly like a pattern that found
// nothing. Half of these blocks carry a `validation` block and a heredoc
// description with braces inside it, so a shallow read either stops early or
// swallows the next variable, and both of those failures are silent.
//
// A DIFFERENCE IS NOT AUTOMATICALLY A DEFECT, and the output says which kind
// it is looking at. Removing, renaming, retyping, or tightening an input is
// breaking and costs a major version. ADDING an optional input is compatible
// and allowed; it fails here only because the snapshot has to record it, and
// the fix is one command rather than an argument.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// The snapshot, committed beside this tool. A diff to it is the review.
const goldenPath = "tools/inputcheck/surface-v1.tsv"

// chartValues is the one Helm surface a self hoster writes.
const chartValues = "deploy/helm/antifailure-control-plane/values.yaml"

// The Terraform a self hoster writes a tfvars file against or reads an output
// from. Every one of these is named in docs/self-hosting/azure.md as something
// an operator runs or forks, which is what makes it a contract rather than an
// implementation detail.
var terraformDirs = []string{
	"infra/terraform/modules/control-plane",
	"infra/terraform/modules/foundation",
	"infra/terraform/modules/alerting",
	"infra/terraform/stacks/control-plane",
	"infra/terraform/stacks/tfstate",
}

// input is one thing somebody names from outside.
type input struct {
	// source is which file it lives in, shortened to something a person can
	// read in a failure message.
	source string
	// sort is `value` for a chart value, `variable` for a Terraform variable,
	// and `output` for a Terraform output. An output is on this list because
	// two documented runbook commands read one: self-hosting/azure.md pipes
	// `terraform output -raw backend_hcl` into a backend configuration, and
	// rotating-secrets.md scopes a role assignment with
	// `terraform output -raw key_vault_id`. A surface a document tells
	// somebody to run is a surface, whatever it is called in Terraform.
	sort string
	name string
	// kind is the declared type, normalised to one word for the chart and to
	// the literal Terraform type expression otherwise. An output declares
	// neither a type nor a requirement, so it carries a dash for both rather
	// than a value nobody wrote.
	kind     string
	required bool
}

func (i input) line() string {
	need := "optional"
	switch {
	case i.sort == "output":
		need = "-"
	case i.required:
		need = "required"
	}
	return strings.Join([]string{i.source, i.sort, i.name, i.kind, need}, "\t")
}

func (i input) key() string { return i.source + "\t" + i.sort + "\t" + i.name }

func main() {
	root := flag.String("root", ".", "repository root")
	update := flag.Bool("update", false, "rewrite the snapshot from the tree, for a change that is compatible")
	flag.Parse()
	if args := flag.Args(); len(args) > 0 {
		*root = args[0]
	}

	current, err := surface(*root)
	if err != nil {
		fail("%v", err)
	}
	if len(current) == 0 {
		// Not a pass. A check that found no subject and printed ok is the
		// failure this tool exists to prevent.
		fail("read no inputs at all from %s, so there is nothing to compare.\n"+
			"  Either the chart and the Terraform moved, or this check is now reading nothing.", *root)
	}

	if *update {
		if err := writeGolden(*root, current); err != nil {
			fail("%v", err)
		}
		fmt.Printf("inputcheck: wrote %s, %d inputs\n", goldenPath, len(current))
		return
	}

	golden, err := readGolden(*root)
	if err != nil {
		fail("%v", err)
	}

	breaking, additions := compare(golden, current)

	if len(breaking) > 0 {
		fmt.Fprintf(os.Stderr, "\ninputcheck: the self-hosting inputs changed in a way version 1 promised they would not.\n\n")
		for _, b := range breaking {
			fmt.Fprintf(os.Stderr, "  %s\n", b)
		}
		fmt.Fprintf(os.Stderr, "\n  These are what an operator's values file and tfvars file are written against.\n"+
			"  Breaking one costs a major version, and docs/reference/stability.md says so.\n"+
			"  If the change is genuinely wanted, it belongs in a 2.0 and not in a minor release.\n")
		os.Exit(1)
	}

	if len(additions) > 0 {
		fmt.Fprintf(os.Stderr, "\ninputcheck: %d new input(s), which is compatible. The snapshot has to record them.\n\n", len(additions))
		for _, a := range additions {
			fmt.Fprintf(os.Stderr, "  added     %s %s %s (%s)\n", a.source, a.sort, a.name, a.kind)
		}
		fmt.Fprintf(os.Stderr, "\n  go run ./tools/inputcheck -update .\n")
		os.Exit(1)
	}

	fmt.Printf("inputcheck: the %d values, variables and outputs v1.0.0 promised are all still here, unrenamed and unretyped\n", len(current))
}

// compare sorts the differences into the ones that cost a major version and
// the ones that only cost a snapshot refresh.
func compare(golden, current []input) (breaking []string, additions []input) {
	was := map[string]input{}
	for _, i := range golden {
		was[i.key()] = i
	}
	is := map[string]input{}
	for _, i := range current {
		is[i.key()] = i
	}

	// Gone, and each one is somebody's values file no longer applying.
	var removed []input
	for _, g := range golden {
		if _, ok := is[g.key()]; !ok {
			removed = append(removed, g)
		}
	}
	for _, c := range current {
		if _, ok := was[c.key()]; !ok {
			additions = append(additions, c)
		}
	}

	// A rename arrives here as a removal and an addition, and reporting it as
	// two unrelated facts makes the reader do the join. So do the join: an
	// addition in the same file with the same type is almost certainly the
	// same input under a new name, and saying that turns a puzzle into an
	// instruction.
	renamedTo := map[string]string{}
	renamedFrom := map[string]bool{}
	for _, r := range removed {
		for _, a := range additions {
			if a.source != r.source || a.kind != r.kind || a.required != r.required {
				continue
			}
			if _, taken := renamedTo[r.key()]; taken || renamedFrom[a.key()] {
				continue
			}
			renamedTo[r.key()] = a.name
			renamedFrom[a.key()] = true
		}
	}
	for _, r := range removed {
		if to, ok := renamedTo[r.key()]; ok {
			breaking = append(breaking, fmt.Sprintf(
				"renamed  %s %s %s is gone and %s appeared beside it.\n%s",
				r.source, r.sort, r.name, to, consequence(r)))
			continue
		}
		breaking = append(breaking, fmt.Sprintf(
			"removed  %s %s %s is gone.\n%s",
			r.source, r.sort, r.name, consequence(r)))
	}

	// Retyped and tightened, which are the two changes that keep the name and
	// break the file anyway.
	for _, c := range current {
		g, ok := was[c.key()]
		if !ok {
			continue
		}
		if g.kind != c.kind {
			breaking = append(breaking, fmt.Sprintf(
				"retyped  %s %s %s was %s and is now %s.\n"+
					"           A file carrying the old shape is refused rather than migrated.",
				c.source, c.sort, c.name, g.kind, c.kind))
		}
		if !g.required && c.required {
			breaking = append(breaking, fmt.Sprintf(
				"required %s %s %s was optional and is now required.\n"+
					"           Every existing file that reasonably left it out no longer applies.",
				c.source, c.sort, c.name))
		}
	}

	// A new input that arrives required is an addition on paper and a break in
	// practice: it refuses a file that was complete the day before.
	kept := additions[:0]
	for _, a := range additions {
		if renamedFrom[a.key()] {
			// Already reported as the far half of a rename. Saying it twice,
			// once as a rename and once as a new required input, makes a
			// reader look for two problems where there is one.
			continue
		}
		if a.required {
			breaking = append(breaking, fmt.Sprintf(
				"new      %s %s %s arrived with no default, so it is required.\n"+
					"           A new input has to be optional. Every file written before it existed\n"+
					"           omits it, and Terraform stops to prompt for a variable it cannot find.",
				a.source, a.sort, a.name))
			continue
		}
		kept = append(kept, a)
	}
	additions = kept

	sort.Strings(breaking)
	return breaking, additions
}

// consequence is what a reader loses, said in the terms of the surface that
// changed. A values key and a tfvars variable both fail SILENTLY, which is the
// part worth putting in front of somebody; an output fails a documented
// command instead.
func consequence(i input) string {
	if i.sort == "output" {
		return "           A runbook reads outputs by name. self-hosting/azure.md pipes one into a\n" +
			"           backend configuration and rotating-secrets.md scopes a role assignment with\n" +
			"           another. An output missing under the name a command asks for prints nothing."
	}
	return "           A file still naming it is not refused, it is IGNORED. Helm accepts a key no\n" +
		"           template reads and Terraform only warns about an undeclared variable, so the\n" +
		"           setting stops being in force without anybody being told."
}

// surface reads every input in the tree.
func surface(root string) ([]input, error) {
	found, err := valuesInputs(filepath.Join(root, chartValues))
	if err != nil {
		return nil, err
	}
	for _, dir := range terraformDirs {
		source := shortName(dir)
		vars, err := blockInputs(filepath.Join(root, dir, "variables.tf"), source, "variable")
		if err != nil {
			return nil, err
		}
		if len(vars) == 0 {
			return nil, fmt.Errorf("%s/variables.tf declares nothing, which cannot be right: either it moved or this check stopped reading it", dir)
		}
		outs, err := blockInputs(filepath.Join(root, dir, "outputs.tf"), source, "output")
		if err != nil {
			return nil, err
		}
		if len(outs) == 0 {
			return nil, fmt.Errorf("%s/outputs.tf declares nothing, which cannot be right: either it moved or this check stopped reading it", dir)
		}
		found = append(found, vars...)
		found = append(found, outs...)
	}
	sort.Slice(found, func(a, b int) bool {
		if found[a].source != found[b].source {
			return found[a].source < found[b].source
		}
		if found[a].sort != found[b].sort {
			return found[a].sort < found[b].sort
		}
		return found[a].name < found[b].name
	})
	return found, nil
}

// shortName is what a failure message calls a directory. The two path
// components that distinguish one module from another, and nothing else.
func shortName(dir string) string {
	parts := strings.Split(filepath.ToSlash(dir), "/")
	if len(parts) < 2 {
		return dir
	}
	return "terraform:" + strings.Join(parts[len(parts)-2:], "/")
}

// --- the chart ------------------------------------------------------------

// valuesInputs is every key path in values.yaml, with the type of the default
// sitting at it.
func valuesInputs(path string) ([]input, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the chart's values, which is half of what this checks: %v", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(source, &doc); err != nil {
		return nil, fmt.Errorf("%s is not valid YAML: %v", path, err)
	}
	if len(doc.Content) == 0 {
		return nil, fmt.Errorf("%s is empty", path)
	}
	var out []input
	walk(doc.Content[0], "", &out)
	return out, nil
}

// walk descends a mapping, recording a leaf for anything that is not a mapping
// with children.
//
// A list is a leaf on purpose. `ingress.hosts` carries Kubernetes' own shape,
// and reaching inside it would put somebody else's contract into ours.
func walk(node *yaml.Node, prefix string, out *[]input) {
	if node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		path := key.Value
		if prefix != "" {
			path = prefix + "." + key.Value
		}
		if value.Kind == yaml.MappingNode && len(value.Content) > 0 {
			walk(value, path, out)
			continue
		}
		// Every key in a values file has a default by construction, which is
		// what a values file is, so nothing the chart declares is required
		// here. The chart's own refusals live in _helpers.tpl and are about
		// a value being empty rather than about it being absent.
		*out = append(*out, input{source: "helm", sort: "value", name: path, kind: yamlType(value)})
	}
}

func yamlType(node *yaml.Node) string {
	switch node.Kind {
	case yaml.SequenceNode:
		return "list"
	case yaml.MappingNode:
		return "map"
	}
	switch node.Tag {
	case "!!str":
		return "string"
	case "!!int", "!!float":
		return "number"
	case "!!bool":
		return "bool"
	case "!!null":
		return "null"
	}
	return strings.TrimPrefix(node.Tag, "!!")
}

// --- terraform ------------------------------------------------------------

var (
	blockOpens   = regexp.MustCompile(`^(variable|output)\s+"([^"]+)"\s*\{`)
	typeArgument = regexp.MustCompile(`^type\s*=\s*(.+?)\s*$`)
	defaultOpens = regexp.MustCompile(`^default\s*=`)
	heredocOpens = regexp.MustCompile(`<<-?([A-Za-z_][A-Za-z0-9_]*)\s*$`)
	quotedSpan   = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
)

// variableInputs reads every variable block in one file.
//
// It normalises before it reads, and each of the three passes exists because
// the shallow version of this check gets a plausible wrong answer rather than
// an error. A heredoc description contains braces, so a reader that counts
// them walks off the end of the block and reads the NEXT variable's `default`
// as this one's, which turns a required input into an optional one in the
// snapshot. A brace inside an error message does the same. And a variable
// written on one line, which nine of these are, has its whole body on the line
// that opens it.
//
// So: heredoc bodies are dropped, comments and the insides of quoted strings
// are blanked, and every brace is put on a line of its own. What is left is a
// stream where depth is exactly what it looks like.
func blockInputs(path, source, want string) ([]input, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s, which declares part of what this promises: %v", path, err)
	}

	var cleaned []string
	heredoc := ""
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if heredoc != "" {
			if strings.TrimSpace(line) == heredoc {
				heredoc = ""
			}
			continue
		}
		line = strip(line)
		if m := heredocOpens.FindStringSubmatch(line); m != nil {
			heredoc = m[1]
		}
		cleaned = append(cleaned, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if heredoc != "" {
		return nil, fmt.Errorf("%s: a heredoc opened with %s never closes, so this read the file wrongly", path, heredoc)
	}

	body := strings.NewReplacer("{", "{\n", "}", "\n}\n").Replace(strings.Join(cleaned, "\n"))

	var (
		out     []input
		current *input
		depth   int
	)
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if current == nil {
			if m := blockOpens.FindStringSubmatch(line); m != nil && m[1] == want {
				current = &input{source: source, sort: want, name: m[2], required: true}
				depth = 1
			}
			continue
		}
		if depth == 1 && want == "variable" {
			if m := typeArgument.FindStringSubmatch(line); m != nil {
				declared := strings.TrimSpace(m[1])
				if declared == "" || strings.Count(declared, "(") != strings.Count(declared, ")") {
					// The type was a structural one, `object({...})`, and the
					// brace normalisation above cut it in half. Refusing is
					// the only honest answer: recording half a type would put
					// a wrong line in the snapshot and freeze it there.
					return nil, fmt.Errorf(
						"%s: variable %q declares a structural type. inputcheck records a type as one "+
							"expression and cannot read this one, so it refuses rather than record half of it",
						path, current.name)
				}
				current.kind = declared
			}
			if defaultOpens.MatchString(line) {
				current.required = false
			}
		}
		depth += strings.Count(line, "{") - strings.Count(line, "}")
		if depth <= 0 {
			if want == "output" {
				// An output declares no type and cannot be required. Only its
				// name is promised, and a dash says so rather than a value
				// nobody wrote.
				current.kind = "-"
				current.required = false
			}
			if current.kind == "" {
				return nil, fmt.Errorf("%s: variable %q declares no type, and every input this promises has to say what it is",
					path, current.name)
			}
			out = append(out, *current)
			current = nil
		}
	}
	if current != nil {
		return nil, fmt.Errorf("%s: %s %q never closes, so this read the file wrongly", path, want, current.name)
	}
	return out, nil
}

// strip removes comments and the contents of quoted strings, so that a brace
// inside an error message cannot move the depth and a `#` inside prose cannot
// open a comment.
//
// The name in `variable "x" {` survives, and it has to: blanking it too was
// the first version of this, and it read every file in the tree as declaring
// no variables at all. That is the shape of failure this whole tool is about,
// so it is worth one line of care here.
func strip(line string) string {
	if m := blockOpens.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
		return m[1] + " \"" + m[2] + "\" {" + strip(strings.TrimSpace(line)[len(m[0]):])
	}
	line = quotedSpan.ReplaceAllString(line, `""`)
	if at := strings.Index(line, "#"); at >= 0 {
		line = line[:at]
	}
	return line
}

// --- the snapshot ---------------------------------------------------------

const goldenHeader = `# The self-hosting configuration surface, as version 1.0.0 promised it.
#
# Five fields: where it lives, what it is, its name, its type, and whether it
# is required. What it is, is one of
#
#   value     a key in the Helm chart's values.yaml
#   variable  a Terraform variable, which is what a tfvars file names
#   output    a Terraform output, which is what a runbook command reads
#
# An output declares neither a type nor a requirement and carries a dash for
# both, because only its name is promised.
#
# tools/inputcheck holds the tree to this file, and docs/reference/stability.md
# is the promise this file is the evidence for.
#
# A LINE THAT DISAPPEARS OR CHANGES IS A MAJOR VERSION. Adding one is not, and
# ` + "`go run ./tools/inputcheck -update .`" + ` records the addition.
#
# Defaults are deliberately absent. They are not promised, they move, and
# tools/tagsync exists because image_tag's default has to move on every release.
`

func writeGolden(root string, inputs []input) error {
	var b strings.Builder
	b.WriteString(goldenHeader)
	for _, i := range inputs {
		b.WriteString(i.line())
		b.WriteString("\n")
	}
	return os.WriteFile(filepath.Join(root, goldenPath), []byte(b.String()), 0o644)
}

func readGolden(root string) ([]input, error) {
	source, err := os.ReadFile(filepath.Join(root, goldenPath))
	if err != nil {
		return nil, fmt.Errorf("reading the snapshot of the v1.0.0 surface: %v", err)
	}
	var out []input
	for n, line := range strings.Split(string(source), "\n") {
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 5 {
			return nil, fmt.Errorf("%s:%d: want five tab separated fields, got %d", goldenPath, n+1, len(parts))
		}
		out = append(out, input{
			source:   parts[0],
			sort:     parts[1],
			name:     parts[2],
			kind:     parts[3],
			required: parts[4] == "required",
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s records no inputs, so this check would pass over anything", goldenPath)
	}
	return out, nil
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "inputcheck: "+format+"\n", args...)
	os.Exit(1)
}
