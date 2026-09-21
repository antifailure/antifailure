package iac

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// THE ONE PLACE A FILE IS CHOSEN, which is why the state refusal lives here.
//
// Every file that reaches a reader in this package comes through classify
// first, and classify checks for a state file before it checks for anything
// else. There is no second route: Read takes a directory, no exported function
// takes a path, and none of the readers is exported. So "the reader refuses a
// state file" is a property of the shape of this package rather than a rule
// somebody has to remember at each call site.

// stateDir is the directory Terraform keeps per workspace state in. Everything
// under it is state, whatever the files inside are called.
const stateDir = "terraform.tfstate.d"

// skipDirs are directories the walk does not descend into.
//
// `.terraform` is the interesting one and it is skipped ON PURPOSE. It holds
// modules Terraform downloaded from a registry, and reading them would mean
// this reader's answer depended on whether somebody had run `terraform init`
// in the checkout it was pointed at: the same tree would describe production
// differently on two machines. A module from a registry is reported as unread
// with that as its reason, which is the same answer on every machine.
var skipDirs = map[string]bool{
	".git":         true,
	".terraform":   true,
	"node_modules": true,
	"vendor":       true,
}

func walk(ctx context.Context, root string, cfg *config, out *Reading) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("iac: cannot read %s: %w", root, err)
	}
	if !info.IsDir() {
		// Read takes a directory. A caller that reached here with a file has
		// bypassed the signature, so this is a programming error rather than a
		// reading with a refusal in it.
		return fmt.Errorf("iac: %s is a file; Read takes a directory, so that a state file "+
			"cannot be handed to this reader in the first place", root)
	}

	// Pass one: see every file, classify it, refuse what must be refused.
	// Nothing is parsed yet, because the terraform reader needs a whole
	// directory at once and cannot be given files one at a time.
	byDir := map[string][]*candidate{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch {
			case rel == ".":
				return nil
			case d.Name() == stateDir:
				// Refused as a directory rather than walked, so that a state
				// file inside it is never opened at all, not even to sniff it.
				out.Refused = append(out.Refused, Refusal{
					Path:    rel,
					Dialect: DialectTerraformState,
					Reason:  refuseStateDir,
				})
				return fs.SkipDir
			case skipDirs[d.Name()]:
				out.Sources = append(out.Sources, Source{
					Path: rel,
					Read: false,
					Why:  skipReason(d.Name()),
				})
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		c := &candidate{path: path, rel: rel}
		classifyByName(c)
		if c.refused != "" {
			out.Refused = append(out.Refused, Refusal{
				Path: rel, Dialect: DialectTerraformState, Reason: c.refused,
			})
			return nil
		}
		if c.dialect == "" && !c.sniff {
			// Not a file this reader has anything to say about. It is not
			// listed as unread, because listing every README and .png as
			// unmeasured would bury the files that genuinely could not be
			// read.
			return nil
		}
		if fi, statErr := d.Info(); statErr == nil && fi.Size() > cfg.maxBytes {
			out.Sources = append(out.Sources, Source{
				Path:    rel,
				Dialect: c.dialect,
				Read:    false,
				Why: fmt.Sprintf("the file is %d bytes, above the %d byte ceiling this reader "+
					"will read into memory", fi.Size(), cfg.maxBytes),
			})
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			out.Sources = append(out.Sources, Source{
				Path: rel, Dialect: c.dialect, Read: false,
				Why: "the file could not be opened: " + readErr.Error(),
			})
			return nil
		}
		c.body = body
		classifyByContent(c)
		if c.refused != "" {
			// The body is dropped on the floor here rather than carried
			// anywhere: a refused file's contents leave this function.
			c.body = nil
			out.Refused = append(out.Refused, Refusal{
				Path: rel, Dialect: DialectTerraformState, Reason: c.refused,
			})
			return nil
		}
		if c.dialect == "" {
			return nil
		}
		dir := filepath.Dir(rel)
		byDir[dir] = append(byDir[dir], c)
		return nil
	})
	if err != nil {
		return fmt.Errorf("iac: cannot walk %s: %w", root, err)
	}

	dirs := make([]string, 0, len(byDir))
	for dir := range byDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	var kustomizations []*candidate
	for _, dir := range dirs {
		files := byDir[dir]
		sort.SliceStable(files, func(i, j int) bool { return files[i].rel < files[j].rel })
		kustomizations = append(kustomizations, readDir(cfg, files, out)...)
	}
	// Kustomize last, on purpose. An overlay changes components that were
	// declared in files this walk may not have reached yet, so applying it
	// before every document is read would apply it to a Reading that was still
	// filling up, and the components read afterwards would keep the base's
	// values while looking exactly like the ones that had been corrected.
	for _, c := range kustomizations {
		readKustomize(c, out)
	}
	return nil
}

// candidate is one file the walk is considering, and what the classifier
// decided about it.
type candidate struct {
	path    string
	rel     string
	body    []byte
	dialect Dialect
	// sniff says the name alone was not enough and the content decides.
	sniff bool
	// refused is the reason this file is refused, empty when it is not.
	refused string
}

const (
	refuseStateFile = "a Terraform state file holds every value a provider returned, " +
		"including every password and every attribute marked sensitive, in plain text; " +
		"this reader never opens one"
	refuseStateDir = "terraform.tfstate.d holds per workspace state files, and a state file " +
		"holds secrets in plain text; this reader never opens one, so it does not walk this directory"
)

func skipReason(name string) string {
	if name == ".terraform" {
		return "this directory holds modules Terraform downloaded, so reading it would make " +
			"the answer depend on whether terraform init had been run in this checkout"
	}
	return "this directory holds dependencies rather than infrastructure this repository declares"
}

// classifyByName decides what it can from the path alone.
//
// The state check is first and it is by NAME as well as by content, because a
// state file can be arbitrarily large and refusing it by name means never
// opening it at all.
func classifyByName(c *candidate) {
	base := filepath.Base(c.rel)
	lower := strings.ToLower(base)
	switch {
	case strings.HasSuffix(lower, ".tfstate"), strings.HasSuffix(lower, ".tfstate.backup"):
		c.refused = refuseStateFile
		return
	case strings.Contains(filepath.ToSlash(c.rel), "/"+stateDir+"/"), strings.HasPrefix(filepath.ToSlash(c.rel), stateDir+"/"):
		c.refused = refuseStateDir
		return
	}
	switch {
	case strings.HasSuffix(lower, ".tf.json"):
		// JSON, so it has to be sniffed: a state file renamed to main.tf.json
		// is still a state file.
		c.dialect, c.sniff = DialectTerraformJSON, true
	case strings.HasSuffix(lower, ".tf"):
		c.dialect = DialectTerraform
	case strings.HasSuffix(lower, ".tfvars"), strings.HasSuffix(lower, ".tfvars.json"):
		c.dialect = DialectTerraform
		c.sniff = strings.HasSuffix(lower, ".json")
	case lower == "chart.yaml", lower == "chart.yml":
		c.dialect = DialectHelm
	case lower == "kustomization.yaml", lower == "kustomization.yml":
		c.dialect = DialectKustomize
	case strings.HasSuffix(lower, ".json"):
		// Any JSON at all is sniffed, because the only way to be sure a file
		// is not a state file is to look at what is in it.
		c.sniff = true
	case strings.HasSuffix(lower, ".yaml"), strings.HasSuffix(lower, ".yml"):
		c.dialect, c.sniff = DialectKubernetes, true
	}
}

// classifyByContent decides what the name could not, and refuses a state file
// whatever it is called.
//
// The markers were MEASURED against Terraform v1.15.8's own output rather than
// remembered, by running a plan and an apply over a provider free configuration
// and printing the top level keys of each artefact:
//
//	raw state (terraform.tfstate): check_results, lineage, outputs, resources,
//	                               serial, terraform_version, version
//	`terraform show -json` of state: format_version, terraform_version, values
//	`terraform show -json` of a plan: applyable, complete, configuration,
//	                               errored, format_version, output_changes,
//	                               planned_values, relevant_attributes,
//	                               resource_changes, terraform_version,
//	                               timestamp, variables
//
// So `lineage` and `serial` name raw state, a top level `values` names state
// rendered as JSON, and a plan has neither while having `planned_values`. The
// evidence is kept beside this lane's notes; the fixtures in testdata are the
// real files those commands produced, not hand written imitations.
func classifyByContent(c *candidate) {
	if !c.sniff {
		return
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(c.body, &top); err != nil {
		// Not a JSON object. A .yaml file reaches here, which is expected: it
		// keeps whatever dialect the name gave it.
		if c.dialect == DialectTerraformJSON || c.dialect == DialectKubernetes {
			return
		}
		c.dialect = ""
		return
	}
	if reason := stateMarkers(top); reason != "" {
		c.refused = reason
		return
	}
	switch {
	case has(top, "planned_values"), has(top, "resource_changes"):
		c.dialect = DialectTerraformPlan
	case c.dialect != "":
		// A .tf.json or a .yaml keeps what the name said.
	case has(top, "apiVersion") && has(top, "kind"):
		c.dialect = DialectKubernetes
	default:
		c.dialect = ""
	}
}

// stateMarkers names the state file this content is, or returns empty.
//
// It errs towards refusing. A file carrying a state marker is refused even if
// it also carries a plan marker, because the cost of refusing a plan by
// mistake is a missing measurement that says so, and the cost of reading a
// state file by mistake is a password in a report.
func stateMarkers(top map[string]json.RawMessage) string {
	switch {
	case has(top, "lineage"), has(top, "serial"):
		return refuseStateFile
	case has(top, "values") && !has(top, "planned_values"):
		return "this is `terraform show -json` run against STATE rather than against a plan " +
			"file: its top level `values` holds what the providers actually returned, " +
			"including sensitive attributes, so this reader refuses it"
	}
	return ""
}

func has(m map[string]json.RawMessage, key string) bool {
	_, ok := m[key]
	return ok
}

// readDir hands one directory's files to the readers that understand them.
//
// Terraform is read a directory at a time because that is what a root module
// is: variables declared in one file are referenced from another, and a reader
// given one file at a time would report as unreadable every value the file
// beside it defines.
func readDir(cfg *config, files []*candidate, out *Reading) []*candidate {
	var tf, kustomizations []*candidate
	for _, c := range files {
		switch c.dialect {
		case DialectTerraform, DialectTerraformJSON:
			tf = append(tf, c)
		case DialectTerraformPlan:
			readPlan(c, out)
		case DialectKubernetes:
			readKube(c, out)
		case DialectKustomize:
			kustomizations = append(kustomizations, c)
		case DialectHelm:
			out.Sources = append(out.Sources, Source{
				Path: c.rel, Dialect: DialectHelm, Read: false,
				Why: "this is a Helm chart, and rendering one means running a template engine " +
					"whose `lookup` function reads from a live cluster; this reader neither " +
					"executes nor reaches a cluster, so the chart is named rather than read",
			})
		}
	}
	if len(tf) > 0 {
		readTerraform(cfg, tf, out)
	}
	return kustomizations
}
