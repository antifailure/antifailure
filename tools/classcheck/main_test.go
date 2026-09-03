package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The stylesheet Tailwind emits, cut down to what these tests need. The order
// is the real one: an arbitrary colour before every text-black/N, and
// text-black/N ascending by opacity, so text-black comes before text-black/70.
const css = `@font-face{font-family:Inter;src:url(x.woff2)}` +
	`.text-\[\#9B9EA5\]{color:#9b9ea5}` +
	`.text-\[\#1A1A1A\]{color:#1a1a1a}` +
	`.bg-\[\#F7F7F8\]{color:ignored;background-color:#f7f7f8}` +
	`.text-black{color:var(--color-black)}` +
	`@supports (color:color-mix(in lab, red, red)){.text-black\/70{color:color-mix(in oklab, black 70%, transparent)}}` +
	`.text-black\/70{color:#000000b3}` +
	`.bg-white{background-color:#fff}` +
	`@media (min-width:768px){.md\:text-black{color:#000}}` +
	`.hover\:text-black:hover{color:#000}`

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func tree(t *testing.T, html string) string {
	t.Helper()
	root := t.TempDir()
	// A fixture has to look like a repository, not just like a build output.
	// The gate finds its applications by looking for the config file that makes
	// one, so a tree with a .next and no next.config is not an application it
	// has any reason to read.
	write(t, root, "www/next.config.ts", "export default {};\n")
	write(t, root, "www/.next/static/chunks/a.css", css)
	write(t, root, "www/.next/server/app/index.html", html)
	return root
}

func TestParsesUnconditionalRulesOnly(t *testing.T) {
	m := parseCSS(css)
	for _, want := range []string{"text-black", "text-black/70", "bg-white", "bg-[#F7F7F8]", "text-[#1A1A1A]"} {
		if _, ok := m[want]; !ok {
			t.Fatalf("%q missing from the parsed stylesheet: %v", want, m)
		}
	}
	// A breakpoint and a hover are meant to override a base and must not be
	// treated as rivals of one, or every hover:text-* on the site is a finding.
	for _, no := range []string{"md:text-black", "hover:text-black"} {
		if _, ok := m[no]; ok {
			t.Fatalf("%q should not be treated as an unconditional utility", no)
		}
	}
	if m["text-black"].at >= m["text-black/70"].at {
		t.Fatal("text-black must be recorded as emitted before text-black/70")
	}
	if m["bg-white"].prop != "background-color" {
		t.Fatalf("bg-white property = %q", m["bg-white"].prop)
	}
}

// The defect the gate exists for: the nav marked the current page with
// text-black over text-black/70 and the marker never applied.
func TestOverrideThatLosesIsReported(t *testing.T) {
	root := tree(t, `<a class="px-2 text-black/70 hover:text-black text-black">Pricing</a>`)
	code, out := run(root)
	if code == 0 {
		t.Fatalf("a losing override must fail the gate, got:\n%s", out)
	}
	if !strings.Contains(out, "text-black is written here and never applies") {
		t.Fatalf("the finding should name the dead class:\n%s", out)
	}
	if !strings.Contains(out, "text-black/70 wins on color") {
		t.Fatalf("the finding should name what beat it:\n%s", out)
	}
}

// The inverse is how overriding is supposed to look and must stay silent, or
// this gate reports every override on the site and gets muted.
func TestOverrideThatWinsIsNotReported(t *testing.T) {
	root := tree(t, `<span class="text-black bg-white text-black/70">14 removed</span>`)
	code, out := run(root)
	if code != 0 {
		t.Fatalf("a winning override is not a defect:\n%s", out)
	}
	if !strings.Contains(out, "0 classes that never apply") {
		t.Fatalf("unexpected summary:\n%s", out)
	}
}

func TestHoverAndBreakpointAreNotRivals(t *testing.T) {
	root := tree(t, `<a class="text-black/70 hover:text-black md:text-black">x</a>`)
	if code, out := run(root); code != 0 {
		t.Fatalf("a variant is not in the race:\n%s", out)
	}
}

func TestRepeatedClassIsRedundantNotDead(t *testing.T) {
	root := tree(t, `<div class="bg-white px-2 bg-white">x</div>`)
	if code, out := run(root); code != 0 {
		t.Fatalf("the same class twice is redundant, not dead:\n%s", out)
	}
}

// A property each class sets separately must not be cross compared, or a text
// colour would be judged against a background colour.
func TestDifferentPropertiesDoNotCollide(t *testing.T) {
	root := tree(t, `<div class="bg-white text-black">x</div>`)
	if code, out := run(root); code != 0 {
		t.Fatalf("different properties are not rivals:\n%s", out)
	}
}

func TestArbitraryColourLosesToEveryBlackAlpha(t *testing.T) {
	root := tree(t, `<span class="text-black/70 text-[#1A1A1A]">live</span>`)
	code, out := run(root)
	if code == 0 {
		t.Fatalf("an arbitrary colour after a text-black/N is dead, got:\n%s", out)
	}
	if !strings.Contains(out, "text-[#1A1A1A] is written here and never applies") {
		t.Fatalf("wrong finding:\n%s", out)
	}
}

func TestExemptionSilencesAndMustCarryAReason(t *testing.T) {
	root := tree(t, `<a class="text-black/70 text-black">Pricing</a>`)
	write(t, root, "tools/docs/classcheck-exemptions.tsv", "text-black\ttext-black/70\tdeliberate, for the test\n")
	if code, out := run(root); code != 0 {
		t.Fatalf("an exemption should silence the finding:\n%s", out)
	}
	write(t, root, "tools/docs/classcheck-exemptions.tsv", "text-black\ttext-black/70\n")
	if code, out := run(root); code == 0 || !strings.Contains(out, "needs a dead class") {
		t.Fatalf("an exemption with no reason must be refused:\n%s", out)
	}
}

func TestStaleExemptionFails(t *testing.T) {
	root := tree(t, `<a class="text-black/70">Pricing</a>`)
	write(t, root, "tools/docs/classcheck-exemptions.tsv", "text-black\ttext-black/70\tnothing matches this any more\n")
	code, out := run(root)
	if code == 0 || !strings.Contains(out, "stale") {
		t.Fatalf("an exemption that matches nothing must fail:\n%s", out)
	}
}

func TestNoBuildSaysSo(t *testing.T) {
	root := t.TempDir()
	code, out := run(root)
	if code == 0 || !strings.Contains(out, "npm run build") {
		t.Fatalf("a missing build should say how to make one:\n%s", out)
	}
}

// An application that exists and is NOT built must fail, naming itself.
//
// The regression guard for the reason this gate reads both applications. It
// read www alone for its first life, and the console looked exempt from the
// whole class of defect because it never imports cn. It composes in template
// literals instead, and `${inputClass} mt-0` carries the same hazard for the
// same reason: the interpolated string can already set a margin, and the
// cascade rather than the order of the literal decides which one lands.
//
// Refusing is the point. Reading one application and reporting success reads
// exactly like reading both.
func TestAnApplicationThatIsNotBuiltFailsAndNamesItself(t *testing.T) {
	root := tree(t, `<div class="text-black text-black/70"></div>`)
	if err := os.MkdirAll(filepath.Join(root, "console"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "console", "next.config.ts"), []byte("export default {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := run(root)
	if code == 0 {
		t.Fatalf("an unbuilt application was skipped rather than refused:\n%s", out)
	}
	if !strings.Contains(out, "console") {
		t.Errorf("the report does not name the application that is missing:\n%s", out)
	}
}
