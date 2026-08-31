package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The defect this tool exists for, in the shape it actually shipped in: a
// client-rendered label, invisible to curl and to any gate reading built HTML.
func TestTheFidelityScoreIsFound(t *testing.T) {
	got := Check("scene.tsx", `<MonoLabel className="text-[9px]">fid 87%</MonoLabel>`+"\n")
	if len(got) != 1 {
		t.Fatalf("want one finding, got %+v", got)
	}
	if got[0].fig != "87%" {
		t.Errorf("fig = %q, want 87%%", got[0].fig)
	}
	// And in the other place it lived, a string typed out a character at a time.
	if n := len(Check("scene.tsx", `typeLine("rpt_08f2  BLOCK  fid 87%", s)`+"\n")); n != 1 {
		t.Errorf("want the figure found inside the string literal, got %d", n)
	}
}

func TestACountAgainstADenominatorIsFound(t *testing.T) {
	for _, s := range []string{
		`<span>17 of 21 measured</span>` + "\n",
		`<span>3 out of 5 passed</span>` + "\n",
		`title="81 percent of components"` + "\n",
	} {
		if n := len(Check("x.tsx", s)); n != 1 {
			t.Errorf("%q should be one finding, got %d", s, n)
		}
	}
}

// CSS is the entire difficulty. The site has nearly three hundred percentages
// and almost none of them is a claim.
func TestCSSIsNotAClaim(t *testing.T) {
	for _, s := range []string{
		`<div className="mx-[10%] max-w-[46%]" />` + "\n",
		`<div style={{ left: "50%", width: "38%" }} />` + "\n",
		`  bottom: "100%",` + "\n",
		`  0%, 100% { opacity: 1; }` + "\n",
		`  40% { transform: scaleX(0.8); }` + "\n",
		`const BAR = "linear-gradient(180deg, #33bf00 0%, #4CB782 42%)";` + "\n",
		`const end = right ? "#000 calc(100% - 28px), transparent 100%" : "#000 100%";` + "\n",
		`gl.canvas.style.width = "100%";` + "\n",
		`<stop offset="58%" />` + "\n",
		`<line x1="6%" y1="58%" y2="42%" />` + "\n",
		`useInView(ref, { amount, margin: "0px 0px -8% 0px" });` + "\n",
	} {
		if got := Check("x.tsx", s); len(got) != 0 {
			t.Errorf("%q is styling, not a claim, got %+v", s, got)
		}
	}
}

// The rule that got this wrong first time round. Skipping any line with a brace
// and a colon treats every object literal as CSS, which silently swallowed five
// real data rows. A gate that hides a true finding is worse than one that
// reports a false one.
func TestAnObjectLiteralIsNotCSS(t *testing.T) {
	line := `  { route: "GET /api/subscriptions", share: "18%", p95: "412ms", delta: 1.29 },` + "\n"
	got := Check("x.tsx", line)
	if len(got) != 1 {
		t.Fatalf("want the route share found in a data row, got %+v", got)
	}
	if got[0].fig != "18%" {
		t.Errorf("fig = %q, want 18%%", got[0].fig)
	}
}

// A figure beside a style on one line is still found, which is why CSS is
// masked rather than used to skip the line.
func TestAFigureBesideAStyleIsStillFound(t *testing.T) {
	line := `<Pill className="bg-[#D94841]/12">129% slower</Pill>` + "\n"
	if got := Check("x.tsx", line); len(got) != 1 {
		t.Fatalf("want the copy found beside the class list, got %+v", got)
	}
}

// Comments render nowhere, so they cannot mislead a reader. This is the
// opposite of prosecheck's rule, deliberately: there a comment is prose a
// person wrote, here the only question is what reaches the page.
func TestACommentIsNotAClaim(t *testing.T) {
	for _, s := range []string{
		"// the invented 87% came out of here\n",
		"/* a card pinned to 78% of the height */\n",
		" * ReportScene dropped the same invented 87%\n",
		"/*\n * 87% lived here once\n */\n",
	} {
		if got := Check("x.tsx", s); len(got) != 0 {
			t.Errorf("%q is a comment, got %+v", s, got)
		}
	}
}

// A duration is not a ratio and is deliberately out of scope: including it
// would have put forty rows in the allowlist for no extra safety.
func TestADurationIsNotInScope(t *testing.T) {
	if got := Check("x.tsx", `<span>p95 412ms · 2,140 requests at 18/s</span>`+"\n"); len(got) != 0 {
		t.Errorf("durations are out of scope, got %+v", got)
	}
}

func TestTheLineNumberIsReported(t *testing.T) {
	got := Check("x.tsx", "one\ntwo\n<b>12% kept</b>\n")
	if len(got) != 1 || got[0].num != 3 {
		t.Fatalf("want line 3, got %+v", got)
	}
}

// An exemption needs three fields and the third has to say something.
func TestAnExemptionWithoutAReasonIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "e.tsv")
	if err := writeFile(path, "a.tsx\t87%\t\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := readExemptions(path); err == nil {
		t.Error("an exemption with an empty reason should be refused")
	}
	if err := writeFile(path, "a.tsx\t87%\tit comes from here\n"); err != nil {
		t.Fatal(err)
	}
	got, err := readExemptions(path)
	if err != nil {
		t.Fatal(err)
	}
	if got[key{"a.tsx", "87%"}] != "it comes from here" {
		t.Errorf("got %+v", got)
	}
}

// The real site, which is the check that matters: every figure it renders is
// either accounted for or absent, and the walk actually reaches www.
func TestTheRealSiteIsAccountedFor(t *testing.T) {
	root := filepath.Join("..", "..")
	files, err := collect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 100 {
		t.Fatalf("found %d files, which suggests this is looking in the wrong place", len(files))
	}
	for _, f := range files {
		if strings.Contains(f, "node_modules/") || strings.Contains(f, "/out/") {
			t.Errorf("%s is not ours to check", f)
		}
	}
	var out strings.Builder
	if err := run(root, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
}

func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o600)
}
