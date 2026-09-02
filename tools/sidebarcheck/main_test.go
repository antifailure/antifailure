package main

import (
	"strings"
	"testing"
)

func ordered(path string, n string) page {
	return page{path: path, order: n, hasOrder: n != ""}
}

// The order this repository actually shipped, so a green run here means the
// real tree's shape passes rather than some shape invented for the test.
func realShape() map[string][]page {
	return map[string][]page{
		"concepts": {ordered("concepts/goldens.md", "1"), ordered("concepts/masking.md", "2")},
		"self-hosting": {
			ordered("self-hosting/control-plane.md", "1"),
			ordered("self-hosting/rotating-secrets.md", "7"),
		},
		"self-hosting/runbooks": {
			ordered("self-hosting/runbooks/index.md", "8"),
			ordered("self-hosting/runbooks/availability.md", "9"),
		},
		// A directory with no orders at all is a deliberate arrangement, not an
		// omission: reference/schemas is two generated pages that sort last.
		"reference/schemas": {ordered("reference/schemas/events-v1.md", ""), ordered("reference/schemas/manifest-v1.md", "")},
	}
}

func TestAcceptsTheShapeTheDocumentationActuallyHas(t *testing.T) {
	problems, pages, groups := analyse(realShape())
	if len(problems) != 0 {
		t.Fatalf("expected no findings, got %v", problems)
	}
	if pages != 8 || groups != 4 {
		t.Fatalf("counted %d pages in %d groups, want 8 in 4", pages, groups)
	}
}

// Each of these is a defect that was really in the tree when the gate was
// written. A rule nobody has watched fail is decoration, so every rule gets
// its own failing case rather than one case that trips several at once.
func TestFindsEachDefectItWasWrittenFor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string][]page)
		want   string
	}{
		{
			// 27 of 78 pages were in this state. Starlight breaks the tie on
			// file name, so the reader sees an alphabetical order that nobody
			// chose and the author cannot see from the page they are editing.
			name:   "two pages claiming the same number",
			mutate: func(g map[string][]page) { g["concepts"][1].order = "1" },
			want:   "both claim sidebar.order 1",
		},
		{
			// self-hosting really carried a 3.5, wedged between two pages that
			// both claimed 3.
			name:   "a fractional order",
			mutate: func(g map[string][]page) { g["self-hosting"][1].order = "3.5" },
			want:   "not a whole number",
		},
		{
			name: "a page with no order among siblings that have one",
			mutate: func(g map[string][]page) {
				g["concepts"][1].order, g["concepts"][1].hasOrder = "", false
			},
			want: "has no sidebar.order while",
		},
		{
			// This is the one that is not obvious, and it happened while the
			// sidebar was being renumbered: Starlight puts a nested group at
			// the smallest order of its children, so numbering the runbooks
			// from 1 lifted the whole group above every self-hosting page.
			name:   "a nested group numbered from below its parent",
			mutate: func(g map[string][]page) { g["self-hosting/runbooks"][0].order = "1" },
			want:   "renders at position 1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := realShape()
			tc.mutate(g)
			problems, _, _ := analyse(g)
			if len(problems) == 0 {
				t.Fatalf("the gate did not fire")
			}
			found := false
			for _, p := range problems {
				if strings.Contains(p, tc.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("no finding mentioned %q; got %v", tc.want, problems)
			}
		})
	}
}

// The frontmatter is read on its own because the documentation is full of
// manifest examples, and `order:` appears inside them. Reading the whole file
// would report a page that declares nothing, which is the false finding that
// would get this gate deleted.
func TestReadsOnlyTheFrontmatter(t *testing.T) {
	doc := "---\ntitle: Load\nsidebar:\n  order: 12\n---\n\n```yaml\nload:\n  order: 99\n```\n"
	fm := frontmatter(doc)
	if strings.Contains(fm, "99") {
		t.Fatalf("frontmatter reached into the body: %q", fm)
	}
	m := orderLine.FindStringSubmatch(fm)
	if m == nil || m[1] != "12" {
		t.Fatalf("wanted order 12 from the frontmatter, got %v", m)
	}
	if frontmatter("no frontmatter here\n") != "" {
		t.Fatal("a file with no frontmatter should yield nothing")
	}
}
