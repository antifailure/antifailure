package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// site writes a fake build, since the gate reads a render and a render is just
// files on disk. Using a fixture rather than the real build keeps these tests
// able to fail on purpose, which is the only way to know the gate works.
func site(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	// A fixture has to look like a repository, not just like a build output.
	// The gate finds its applications by looking for the config file that makes
	// one, so a tree with a .next and no next.config is not an application it
	// has any reason to read. Writing the config here is what makes these
	// fixtures faithful; without it they were testing a shape the repository
	// never has.
	for name := range files {
		if i := strings.Index(name, "/"); i > 0 {
			cfg := filepath.Join(root, name[:i], "next.config.ts")
			if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cfg, []byte("export default {};\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name, body := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const cssPath = "www/.next/static/chunks/app.css"
const htmlPath = "www/.next/server/app/index.html"

func TestAStylesheetAnimationThatNeverStopsIsAFinding(t *testing.T) {
	root := site(t, map[string]string{
		cssPath:  `.hero-scan{background:red;animation:film-scan 7s linear infinite}`,
		htmlPath: `<div class="hero-scan"></div>`,
	})
	code, out := run(root)
	if code == 0 {
		t.Fatalf("an infinite animation passed:\n%s", out)
	}
	if !strings.Contains(out, ".hero-scan") {
		t.Errorf("the report does not name the selector:\n%s", out)
	}
}

// The defect that earned the tool. hero-scan was read in the source twice and
// called harmless both times, because the rule and the element that uses it
// live in different files. Here they do too, and the gate still catches it.
func TestTheRuleAndItsElementNeedNotBeInOneFile(t *testing.T) {
	root := site(t, map[string]string{
		cssPath:  `.hero-scan{animation:film-scan 7s linear infinite}`,
		htmlPath: `<div class="hero-scan absolute inset-0"></div>`,
	})
	if code, out := run(root); code == 0 {
		t.Fatalf("expected a finding:\n%s", out)
	}
}

func TestAnInlineStyleAnimationIsAFinding(t *testing.T) {
	root := site(t, map[string]string{
		cssPath:  `.a{color:red}`,
		htmlPath: `<div style="animation:af-verdict-stream 54s linear infinite"></div>`,
	})
	code, out := run(root)
	if code == 0 {
		t.Fatalf("an inline infinite animation passed:\n%s", out)
	}
	if !strings.Contains(out, "af-verdict-stream") {
		t.Errorf("the report does not name the animation:\n%s", out)
	}
}

func TestAnimatePulseAndAnimatePingAreFindings(t *testing.T) {
	for _, class := range []string{"animate-pulse", "animate-ping"} {
		root := site(t, map[string]string{
			cssPath:  `.a{color:red}`,
			htmlPath: `<span class="ml-2 ` + class + ` rounded-full"></span>`,
		})
		code, out := run(root)
		if code == 0 {
			t.Fatalf("%s passed:\n%s", class, out)
		}
		if !strings.Contains(out, class) {
			t.Errorf("the report does not name %s:\n%s", class, out)
		}
	}
}

// Tailwind defines --animate-pulse in its theme block of every build, used or
// not. Reporting that is a false positive on every page in the repository, and
// it is what a scan for the word "infinite" would do.
func TestTailwindsThemeVariableAloneIsNotAFinding(t *testing.T) {
	root := site(t, map[string]string{
		cssPath: `@layer theme{:root,:host{--animate-pulse:pulse 2s cubic-bezier(.4,0,.6,1) infinite;--spacing:.25rem}}` +
			`.text-black{color:#000}`,
	})
	if code, out := run(root); code != 0 {
		t.Fatalf("the unused theme variable was reported:\n%s", out)
	}
}

// The other half of the same trap. Tailwind reaches infinite through a var, so
// the rule that actually animates contains no "infinite" of its own.
func TestAnAnimationReachingInfiniteThroughAVarIsAFinding(t *testing.T) {
	root := site(t, map[string]string{
		cssPath: `@layer theme{:root{--animate-pulse:pulse 2s cubic-bezier(.4,0,.6,1) infinite}}` +
			`.animate-pulse{animation:var(--animate-pulse)}`,
		htmlPath: `<span class="animate-pulse"></span>`,
	})
	code, out := run(root)
	if code == 0 {
		t.Fatalf("the var() hop hid an infinite animation:\n%s", out)
	}
	if !strings.Contains(out, ".animate-pulse") {
		t.Errorf("the report does not name the rule:\n%s", out)
	}
}

func TestAnAnimationThatStopsPasses(t *testing.T) {
	root := site(t, map[string]string{
		cssPath: `.film-play{animation:film-clone 1.4s cubic-bezier(.16,1,.3,1) both}` +
			`.wt-sheen{animation-iteration-count:1}`,
		htmlPath: `<div style="animation:wt-sheen .9s ease 1"></div>`,
	})
	if code, out := run(root); code != 0 {
		t.Fatalf("a finite animation was reported:\n%s", out)
	}
}

// A reduced-motion block collapses duration rather than removing the animation,
// which is correct and must not read as a finding.
func TestTheReducedMotionBlockIsNotAFinding(t *testing.T) {
	root := site(t, map[string]string{
		cssPath: `@media (prefers-reduced-motion:reduce){*,:before,:after{animation-duration:.001ms!important;` +
			`animation-iteration-count:1!important;transition-duration:.001ms!important}}`,
	})
	if code, out := run(root); code != 0 {
		t.Fatalf("the reduced-motion guard was reported:\n%s", out)
	}
}

func TestAnExemptionNeedsToBeNamedToSilenceAFinding(t *testing.T) {
	root := site(t, map[string]string{
		cssPath:  `.earns-it{animation:spin 1s linear infinite}`,
		htmlPath: `<div class="earns-it"></div>`,
	})
	if code, _ := run(root); code == 0 {
		t.Fatal("expected a finding before the exemption")
	}

	exempt[".earns-it"] = "a test, restored immediately below"
	defer delete(exempt, ".earns-it")

	if code, out := run(root); code != 0 {
		t.Fatalf("the exemption did not silence it:\n%s", out)
	}
}

// An unbuilt tree must fail rather than pass. A gate that reads a render and
// finds no render has checked nothing, and reporting that as success is how a
// check that never ran becomes a check that always passes.
func TestAnUnbuiltTreeFailsRatherThanPassing(t *testing.T) {
	code, out := run(t.TempDir())
	if code == 0 {
		t.Fatalf("an unbuilt tree passed:\n%s", out)
	}
	if !strings.Contains(out, "npm run build") {
		t.Errorf("the report does not say how to fix it:\n%s", out)
	}
}

// The finding this tool produced on its very first run against the real build,
// and it was wrong. Tailwind v4 scans source files as text, so the word
// `animate-pulse` inside the comment in console/components/ui.tsx explaining
// why that screen stopped using it was enough to emit the rule. No element
// carried the class. A gate that reports a rule nothing uses is making the
// same mistake it exists to catch, one level along.
func TestARuleNoElementCarriesIsNotAFinding(t *testing.T) {
	root := site(t, map[string]string{
		cssPath: `@layer theme{:root{--animate-pulse:pulse 2s cubic-bezier(.4,0,.6,1) infinite}}` +
			`.animate-pulse{animation:var(--animate-pulse)}`,
		htmlPath: `<div class="rounded-sm bg-white"></div>`,
	})
	if code, out := run(root); code != 0 {
		t.Fatalf("a rule no element carries was reported:\n%s", out)
	}
}

// An application that exists and is NOT built must fail, naming itself.
//
// This is the regression guard for the defect that motivated reading both
// applications. discover used to append a site only when something was found
// on disk, so an unbuilt application was dropped from the run in silence. In
// CI the console was installed and built after this gate ran, so for the whole
// life of the gate it read www alone and reported success. It said 42 built
// files where the built tree has 64, and exited zero.
//
// Skipping is the failure, not the message. A gate that is green about a
// surface it never opened is worse than no gate, because it is quoted.
func TestAnApplicationThatIsNotBuiltFailsAndNamesItself(t *testing.T) {
	root := site(t, map[string]string{
		cssPath:  ".x{color:red}",
		htmlPath: `<div class="x"></div>`,
	})
	// A second application, with a config and no build. It must not be skipped.
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
