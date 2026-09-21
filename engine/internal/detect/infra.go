package detect

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Finding where a repository's infrastructure as code lives.
//
// WHAT THIS DOES NOT DO, said first because the boundary is the whole design.
// It does not read what the infrastructure DECLARES. It does not know that a
// stack holds a Postgres, a queue or three instances of a worker, and it never
// opens a resource block. Reading an application's declared production is
// engine/internal/iac's job, and this is the analyzer that tells it where to
// look. Keeping the two apart is what lets the locating half be exact and
// bounded while the reading half grows.
//
// THE ONE INFERENCE IT MAKES, and the reason it is not just a glob. Every
// directory holding a .tf file is a Terraform module, and most of them are not
// ROOT modules. A root module is the unit that is planned and applied on its
// own; a child module is one some other module calls. Drafting every directory
// with a .tf file in it would point a comparison at a library of building
// blocks and report an application whose production is twelve subnets, so the
// child modules have to be taken out. A child module is knowable exactly:
// something called it, with `module "x" { source = "./somewhere" }`, and that
// is a bounded scan rather than a guess.
//
// WHY IT NEVER DRAFTS A WORKSPACE OR A VARIABLE FILE. Which workspace holds
// production, and which of prod.tfvars, staging.tfvars and dev.tfvars
// describes it, is not stated anywhere in the tree. A file named
// staging.tfvars is not evidence about production, and drafting it would put a
// guess where the manifest is at its most load bearing: everything else in the
// document describes the copy and can be checked against the copy, while this
// section describes production and nothing downstream can check it. A wrong
// variable file would silently compare a twin against staging and report a
// number nobody could tell was about the wrong thing. So both keys are left
// out, and af init discloses that it left them out.

// KindInfra is a directory that declares infrastructure: a Terraform root
// module, named by its path relative to the repository root.
//
// Its own kind rather than a note, because the merger writes it into a
// manifest section and a note is something only a human reads.
const KindInfra Kind = "infra"

// MaxInfraRoots is how many root modules will be drafted.
//
// Fifty, matching the maxItems on infrastructure.paths in
// schemas/manifest.v1.json, and the two have to agree: af init validates the
// draft it rendered against the real repository before writing it, so a
// fifty first path would not be a long manifest, it would be af init refusing
// to write anything at all on a repository it had read correctly. Above the
// bound the section is left out and a note says why, which is a manifest
// somebody can finish rather than a command that failed.
const MaxInfraRoots = 50

// InfraAnalyzer finds the Terraform root modules in a repository.
type InfraAnalyzer struct{}

// Name identifies the analyzer.
func (*InfraAnalyzer) Name() string { return "infrastructure" }

// localModuleSource matches a module block's local source argument.
//
// Anchored to the whole assignment rather than to the word "source", because
// a registry source ("hashicorp/consul/aws") and a Git source are not
// directories in this repository and must not be mistaken for one. Only a
// path beginning ./ or ../ is local, which is Terraform's own rule.
var localModuleSource = regexp.MustCompile(`(?m)^\s*source\s*=\s*"(\.{1,2}/[^"]*)"`)

// moduleBlockOpen matches the line that opens a module block.
var moduleBlockOpen = regexp.MustCompile(`(?m)^\s*module\s+"[^"]*"\s*\{`)

// Analyze reports every Terraform root module in the repository.
func (a *InfraAnalyzer) Analyze(_ context.Context, r *Repo) ([]Finding, error) {
	files := r.WithExtension(".tf")
	if len(files) == 0 {
		return nil, nil
	}

	// Every directory holding Terraform, and every directory something calls
	// as a module. The difference is what gets drafted.
	holds := map[string]bool{}
	called := map[string]bool{}
	for _, p := range files {
		dir := dirOf(p)
		holds[dir] = true
		body, ok := r.ReadString(p)
		if !ok {
			continue
		}
		for _, target := range localModuleTargets(body, dir) {
			called[target] = true
		}
	}

	roots := make([]string, 0, len(holds))
	for dir := range holds {
		if called[dir] || underModulesDirectory(dir) {
			continue
		}
		roots = append(roots, dir)
	}
	sort.Strings(roots)

	if len(roots) == 0 {
		// Terraform is here and every directory of it is called by another,
		// which happens in a repository that publishes modules rather than
		// applying them. A note rather than silence: "we found no
		// infrastructure" and "we found only building blocks" are different
		// facts, and only the second tells the reader that the section is
		// theirs to write by hand.
		return []Finding{{
			Kind: KindNote, Subject: "infrastructure", Analyzer: a.Name(),
			Confidence: High, Evidence: files[0],
			Detail: "Every Terraform directory here is called as a module by another, so none of " +
				"them is applied on its own. Name the root module under infrastructure.paths by hand.",
		}}, nil
	}
	if len(roots) > MaxInfraRoots {
		return []Finding{{
			Kind: KindNote, Subject: "infrastructure", Analyzer: a.Name(),
			Confidence: High, Evidence: files[0],
			Detail: fmt.Sprintf(
				"This repository has %d Terraform root modules and a manifest may name %d. "+
					"Name the ones that build production under infrastructure.paths by hand.",
				len(roots), MaxInfraRoots),
		}}, nil
	}

	out := make([]Finding, 0, len(roots))
	for _, dir := range roots {
		// The repository root itself is a legal place to keep Terraform, and
		// "" is not a path a manifest can carry, so it is written as the
		// current directory.
		declared := dir
		if declared == "" {
			declared = "."
		}
		out = append(out, Finding{
			Kind: KindInfra, Subject: declared, Value: string(infraTerraform),
			Confidence: High, Evidence: firstFileIn(files, dir), Analyzer: a.Name(),
			Detail: fmt.Sprintf("%s holds Terraform and nothing calls it as a module, so it is applied on its own.",
				orRepositoryRoot(declared)),
		})
	}
	return out, nil
}

// infraTerraform is the source value this analyzer reports.
//
// A private constant of the same string schema.InfraTerraform holds, so that
// the detect package does not import the schema for one word while the value
// it writes is still checked against the schema's own vocabulary by
// TestInfraFindingsCarryASourceTheSchemaKnows.
const infraTerraform = "terraform"

// localModuleTargets returns the directories a module block in this file
// calls, resolved against the calling file's own directory.
//
// The brace count is what keeps a `source` argument belonging to something
// else out of the answer. A provider or a resource can carry a `source` too,
// and while none of them takes a relative directory today, a scan that read
// every source line in the file would silently start excluding root modules
// the first time one did, and a root module quietly dropped from the draft is
// invisible: the manifest would simply not mention a stack.
func localModuleTargets(body, dir string) []string {
	var out []string
	for _, block := range moduleBlocks(body) {
		for _, m := range localModuleSource.FindAllStringSubmatch(block, -1) {
			out = append(out, path.Clean(path.Join(dir, m[1])))
		}
	}
	return out
}

// moduleBlocks returns the text of each module block in a Terraform file.
//
// Brace counting rather than a single regex, because a module block spans
// lines and contains blocks of its own. Strings are not parsed: a brace inside
// a quoted string would miscount, and the consequence is bounded to this
// analyzer reading one block as longer or shorter than it is, never to
// anything being executed.
func moduleBlocks(body string) []string {
	var out []string
	for _, loc := range moduleBlockOpen.FindAllStringIndex(body, -1) {
		depth := 0
		end := -1
		for i := loc[1] - 1; i < len(body); i++ {
			switch body[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					end = i
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			// Unbalanced, which means the file does not parse as Terraform
			// either. Read to the end rather than dropping it, so a truncated
			// file still contributes its module calls.
			end = len(body) - 1
		}
		out = append(out, body[loc[0]:end+1])
	}
	return out
}

// underModulesDirectory reports whether a path sits under a directory named
// modules or module.
//
// The convention every Terraform repository uses, and the one case the call
// graph cannot answer: a module published for another repository to consume is
// called by nothing HERE, so it looks like a root module to a scan that only
// reads this tree. Without this rule a repository that ships modules alongside
// its own stack drafts both.
func underModulesDirectory(dir string) bool {
	for _, seg := range strings.Split(dir, "/") {
		if seg == "modules" || seg == "module" {
			return true
		}
	}
	return false
}

// firstFileIn names a file inside dir, so that a finding can point at
// something a reader can open.
func firstFileIn(files []string, dir string) string {
	for _, p := range files {
		if dirOf(p) == dir {
			return p
		}
	}
	return dir
}

// orRepositoryRoot renders the current directory as what it is, for a sentence
// somebody reads.
func orRepositoryRoot(dir string) string {
	if dir == "." {
		return "The repository root"
	}
	return dir
}
