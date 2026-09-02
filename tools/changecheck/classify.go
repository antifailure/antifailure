// Command changecheck refuses a change to something a user can see unless it
// says what changed.
//
// CONTRIBUTING.md has said "Every pull request adds a changelog fragment under
// `.changes/`. A missing fragment fails a gate" since the first week, and no
// gate existed. README.md names the four categories a fragment's first line
// may take, and nothing read them. The pull request template carries a
// checkbox for it, and a checkbox is not a check.
//
// This repository has watched that exact sentence rot once before. The
// Developer Certificate of Origin was required by the same document, nothing
// enforced it, and 65 of the first 80 commits carried no trailer. A rule
// nobody checks stops being a rule, quietly, and the way you find out is by
// counting afterwards.
//
// The counting here: 125 fragments exist and 20 first-parent changes since the
// convention became a habit touched something a user can see. Eight of those
// twenty landed with no fragment at all. That is what discipline held by habit
// decays to inside a week.
//
// WHY THIS IS NOT THE OBVIOUS GATE. The obvious version fails any change with
// no new file in `.changes/`, and it is wrong. Run it over this repository's
// own history and it fires on a lint fix in engine/internal/oracle/path.go, on
// four double hyphens removed from two component files, and on a check script
// under www/scripts that ships nothing. Every one of those is a real finding
// that is not a real problem, and a gate whose findings are not all real is a
// gate people route around, then ignore, then delete. So the question this
// asks is not "did anything change" but "did anything a user could NOTICE
// change", and everything else passes in silence.
//
// WHAT COUNTS, and the reasoning is in surfaces below rather than here. The
// engine, the control plane and its console, the enterprise edition, the agent
// runner, the site, the site's API, the schemas a customer writes manifests
// against, the container and the chart a self-hosted install runs, and the
// installer. Documentation, the gates in tools/, CI, examples, our own cloud
// under infra/, our own dashboards, tests, fixtures, generated files, module
// and lock files: none of those.
//
// The classification is TOTAL at the top level. Every entry in the repository
// root is either a declared surface or a declared exemption carrying its
// reason, and a new one that is neither fails TestEveryTopLevelPathIsClassified
// rather than being silently waved through. An unclassified path defaulting to
// "no fragment needed" is the shape this repository calls a green gate over a
// subject it never examined, and the test is what stops it happening by
// omission.
//
// WHAT IT DELIBERATELY DOES NOT DO. It does not read the diff to decide
// whether a change was only comments. That idea was built and measured before
// it was rejected: of the twenty product-touching changes since the convention
// began, exactly one was comment-only, and that one already carried a
// fragment. It would have prevented none of the false findings above, because
// neither of them was a comment: one was a lint fix and one was user-facing
// copy. A guard that catches nothing it was written for is worse than no
// guard, because it makes the tool harder to reason about while reading as
// though it were doing something.
//
// The escape hatch is a `Changelog-None:` trailer with a reason, on any commit
// in the range. A trailer rather than a label, because a label lives on GitHub
// where `just changecheck` cannot see it and where the history cannot keep it,
// and because a reason a person had to type is the part that makes an
// exemption reviewable.
package main

import (
	"path"
	"strings"
)

// surface is a part of the repository whose contents are behaviour somebody
// outside this team could notice, and the reason it is one.
//
// A prefix ending in "/" matches everything under it; anything else matches
// that path exactly. The reason is printed when the gate fires, because
// "engine/internal/cli/ci.go needs a fragment" is an instruction and "the CLI
// changed, and the CLI is what a customer runs" is an explanation.
type surface struct {
	prefix string
	why    string
}

var surfaces = []surface{
	{"engine/", "the CLI and the engine a customer runs in their own CI"},
	{"web/", "the hosted control plane's API"},
	{"console/", "the hosted control plane's console"},
	{"ee/", "the enterprise edition"},
	{"runner/", "the agent runner that drives the browser"},
	{"www/", "the public site"},
	{"api/", "the site's own API"},
	{"schemas/", "the manifest and event contracts a customer writes against"},
	{"deploy/docker/", "the container a self-hosted control plane runs"},
	{"deploy/helm/", "the chart a self-hosted control plane installs from"},
	{"install.sh", "how the CLI gets onto a machine"},
}

// notASurface is everything else in the repository root, with the reason it
// carries no changelog obligation.
//
// This exists to be complete rather than to be consulted: the gate's decision
// needs only surfaces, and a path matching none of them is exempt. What the
// list buys is the test that every top-level entry appears in one of the two,
// so a new directory has to be classified by whoever adds it instead of
// inheriting silence.
var notASurface = map[string]string{
	".changes":               "the fragments themselves",
	".dockerignore":          "build context, invisible to anybody running the result",
	".editorconfig":          "editor settings",
	".gitattributes":         "how git stores files",
	".githooks":              "hooks a contributor opts into",
	".github":                "CI, and the workflows are not the product",
	".gitignore":             "what git ignores",
	".golangci.yml":          "linter configuration",
	".govulncheck.yaml":      "vulnerability suppressions, which carry their own expiry gate",
	".npmaudit.yaml":         "advisory suppressions, same",
	".vale.ini":              "prose style configuration",
	"CHANGELOG.md":           "what the fragments become, so requiring one to edit it would be circular",
	"CODE_OF_CONDUCT.md":     "documentation",
	"CONTRIBUTING.md":        "documentation",
	"LICENSE":                "the licence text",
	"LICENSING.md":           "the licence text, in the file LICENSE cannot hold it in",
	"README.md":              "documentation",
	"SECURITY.md":            "documentation",
	"THIRD_PARTY_NOTICES.md": "generated from the dependency set",
	"antifailure.yaml":       "this repository's own dogfood manifest, not a shipped one",
	"assets":                 "brand images used outside the product",
	"cspell.json":            "the spelling dictionary",
	"deploy":                 "only deploy/docker and deploy/helm ship; cd, status and blog-redirect are our own operations",
	"docs":                   "documentation, which has its own gates for links, claims and prose",
	"examples":               "examples, which the docs gates compile",
	"go.work":                "the Go workspace",
	"go.work.sum":            "a lock file",
	"infra":                  "our own cloud, not anything a customer installs",
	"justfile":               "the gates themselves",
	"lychee.toml":            "link checker configuration",
	"masking.yaml":           "this repository's own dogfood masking policy",
	"observability":          "our own dashboards and alert rules",
	"tools":                  "the gates themselves",
}

// exemptWithin names the things inside a surface that are not behaviour.
//
// Each entry earned its place from a change on main that the naive rule would
// have failed:
//
//   - a directory segment of test, tests, testdata, __tests__, fixtures,
//     testutil or mocks. web/apps/api/test/harness.ts is a harness, and
//     e1ce6bbb changed nothing else.
//   - conformance and chaos, which are suites rather than product. b4060e62,
//     61ecc708 and 67723e65 each touched engine/conformance alone.
//   - a scripts directory. 69ec57ad edited www/scripts/check-seo.mjs, which is
//     a gate that runs before a deploy and ships no bytes.
//   - a module or lock file. A dependency bump that changes behaviour changes
//     code as well, and the code is what this reads. Next 16 also rewrites
//     tsconfig.json on its own, which is not a change anybody made.
//   - a generated file, which is a copy of a source that is itself classified.
//   - markdown, which is documentation wherever it sits.
var exemptDirs = map[string]bool{
	"test": true, "tests": true, "testdata": true, "__tests__": true,
	"fixtures": true, "testutil": true, "mocks": true, "scripts": true,
	"conformance": true, "chaos": true, "node_modules": true,
}

var exemptFiles = map[string]bool{
	"go.mod": true, "go.sum": true, "package.json": true,
	"package-lock.json": true, "tsconfig.json": true, ".gitignore": true,
}

// Requires reports whether a path is behaviour somebody outside this team
// could notice, and which surface says so.
func Requires(p string) (surface, bool) {
	p = strings.TrimPrefix(path.Clean(p), "./")
	if p == "" || p == "." {
		return surface{}, false
	}

	var match surface
	found := false
	for _, s := range surfaces {
		if strings.HasSuffix(s.prefix, "/") {
			if strings.HasPrefix(p, s.prefix) {
				// Longest prefix wins, so deploy/docker/ beats nothing and a
				// future narrower surface beats a wider one without the order
				// of this slice deciding anything.
				if len(s.prefix) > len(match.prefix) {
					match, found = s, true
				}
			}
			continue
		}
		if p == s.prefix {
			return s, true
		}
	}
	if !found {
		return surface{}, false
	}

	segments := strings.Split(p, "/")
	base := segments[len(segments)-1]
	for _, dir := range segments[:len(segments)-1] {
		if exemptDirs[dir] {
			return surface{}, false
		}
	}
	if exemptFiles[base] || isTestFile(base) || isGenerated(base) {
		return surface{}, false
	}
	if strings.HasSuffix(base, ".md") {
		return surface{}, false
	}
	return match, true
}

// isTestFile recognises the two naming conventions in this tree: Go's
// _test.go suffix, and the .test./.spec. infix TypeScript and JavaScript use.
func isTestFile(base string) bool {
	if strings.HasSuffix(base, "_test.go") {
		return true
	}
	return strings.Contains(base, ".test.") || strings.Contains(base, ".spec.")
}

// isGenerated recognises the suffix this repository puts on generated Go.
// A generated file is a copy of a source that is itself classified, so
// requiring a fragment for it would ask for one twice or, worse, ask for one
// when `just generate` was the only thing that ran.
func isGenerated(base string) bool {
	return strings.HasSuffix(base, ".gen.go")
}
