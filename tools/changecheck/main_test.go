package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The gate must fire on these. Every one is a path from a change that really
// landed on main, so the list is a record rather than an invention.
func TestFiresOnBehaviourSomebodyCanSee(t *testing.T) {
	for _, p := range []string{
		"engine/internal/cli/ci.go",
		"engine/cmd/af/main.go",
		"web/apps/api/src/github/webhook.ts",
		"web/packages/db/src/client.ts",
		"console/components/ui.tsx",
		"ee/web/scim/src/routes.ts",
		"runner/src/execute.ts",
		"www/components/pages/product/Load.tsx",
		"www/app/globals.css",
		"api/waitlist/index.js",
		"schemas/manifest.v1.json",
		"deploy/docker/control-plane.Dockerfile",
		"deploy/helm/antifailure-control-plane/Chart.yaml",
		"install.sh",
	} {
		if _, ok := Requires(p); !ok {
			t.Errorf("%s changes what somebody sees and should need a fragment", p)
		}
	}
}

// The gate must stay quiet on these. The first block is what the naive version
// fired on, in this repository, on commits that are named in classify.go.
func TestStaysQuietOnEverythingElse(t *testing.T) {
	for _, p := range []string{
		"web/apps/api/test/harness.ts",            // e1ce6bbb
		"www/scripts/check-seo.mjs",               // 69ec57ad
		"engine/conformance/db.go",                // b4060e62
		"engine/internal/cli/ci_test.go",          // a test
		"web/apps/api/src/limits.test.ts",         // a test
		"console/lib/api.spec.ts",                 // a test
		"engine/internal/env/testdata/basic.yaml", // a fixture
		"engine/internal/testutil/fakes/db.go",    // a double
		"engine/internal/errors/codes.gen.go",     // generated
		"web/package-lock.json",                   // a dependency bump
		"engine/go.mod",
		"console/tsconfig.json", // Next rewrites this without being asked
		"www/.gitignore",
		"api/README.md",
		"docs/src/content/docs/reference/cli.md",
		"tools/gatecheck/main.go",
		".github/workflows/ci.yml",
		"justfile",
		"examples/github-workflow.yml",
		"infra/terraform/main.tf",
		"observability/alerts/antifailure.rules.yml",
		"deploy/cd/deploy.sh",
		"deploy/status/probe.sh",
		"deploy/blog-redirect/index.html",
		".changes/something.md",
		"README.md",
		"docs/plan/STATUS.md",
	} {
		if s, ok := Requires(p); ok {
			t.Errorf("%s does not change what anybody sees, and the gate would have demanded a fragment for it (%s)", p, s.why)
		}
	}
}

// A path matching no surface defaults to needing no fragment, which is the
// safe direction for findings and the unsafe one for coverage: a whole new
// product area could arrive and inherit silence. This is what stops that, and
// it fails on the day the directory is added rather than the day somebody
// notices the changelog is thin.
func TestEveryTopLevelPathIsClassified(t *testing.T) {
	root := repoRoot(t)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if name == ".git" {
			continue
		}
		if _, ok := notASurface[name]; ok {
			continue
		}
		if isSurfaceRoot(name) {
			continue
		}
		t.Errorf("%s is in the repository root and changecheck has no opinion about it.\n"+
			"Add it to surfaces in classify.go if a change there is something a user "+
			"can see, or to notASurface with the reason it is not.", name)
	}
	// The other direction: an exemption naming something that is gone reads as
	// a decision about a path that does not exist, and would silently cover a
	// future path that took the same name.
	for name := range notASurface {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("notASurface names %s and there is no such path any more", name)
		}
	}
	for _, s := range surfaces {
		p := strings.TrimSuffix(s.prefix, "/")
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Errorf("surfaces names %s and there is no such path any more", s.prefix)
		}
	}
}

func isSurfaceRoot(name string) bool {
	for _, s := range surfaces {
		if strings.TrimSuffix(s.prefix, "/") == name {
			return true
		}
		if strings.HasPrefix(s.prefix, name+"/") {
			return true
		}
	}
	return false
}

func TestARealFragmentParses(t *testing.T) {
	body := "# fixed\n\nA sentence about what changed.\n"
	if p := fragmentProblems(body); len(p) > 0 {
		t.Errorf("a valid fragment was rejected: %v", p)
	}
}

func TestAFragmentWithTwoCategoriesParses(t *testing.T) {
	body := "# added\n\nThe thing.\n\n# fixed\n\nThe other thing.\n"
	if p := fragmentProblems(body); len(p) > 0 {
		t.Errorf("a two-category fragment was rejected: %v", p)
	}
}

func TestFragmentProblemsAreNamed(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"a category nobody renders", "# docs\n\nSomething.\n", "not one of added"},
		{"no heading at all", "Something happened.\n", "not a category heading"},
		{"a heading with nothing under it", "# fixed\n\n# added\n\nOnly this one.\n", "no prose under it"},
		{"a second level heading", "# fixed\n\nProse.\n\n## Details\n\nMore.\n", "one level deep"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := fragmentProblems(tc.body)
			if len(got) == 0 {
				t.Fatalf("accepted a fragment it should have refused")
			}
			if !strings.Contains(strings.Join(got, " "), tc.want) {
				t.Errorf("said %q, which does not mention %q", got, tc.want)
			}
		})
	}
}

// Every fragment in the tree parses. This is the corpus the site renders, so a
// green here is the renderer's precondition rather than a statement about
// style.
func TestTheRealFragmentsAllParse(t *testing.T) {
	if err := validateFragments(filepath.Join(repoRoot(t), ".changes")); err != nil {
		t.Error(err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	// tools/changecheck -> tools -> root
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "justfile")); err != nil {
		t.Fatalf("expected the repository root at %s: %v", root, err)
	}
	return root
}
