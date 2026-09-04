package main

import (
	"strings"
	"testing"
)

// The shape production.md actually has, so a green test means the real page's
// shape passes rather than a shape invented to pass. Fifteen steps, a forward
// reference in the preamble and two backward ones in the body, which is the
// arrangement every cross reference rule here is about.
func wholePage() string {
	titles := []string{
		"Give production its own Terraform state", "Check the region", "Grant the identity DNS",
		"Decide who gets paged", "Plan, price it, apply", "Bind the certificate",
		"Confirm the assumptions the alerts are built on", "Create the production OAuth App",
		"Create the production GitHub App", "Put the four values in Key Vault",
		"Tell Terraform the App exists", "Install the App on the organization",
		"Let continuous deployment reach production", "Set the approval rule", "Release",
	}
	var b strings.Builder
	b.WriteString("Step 6 below is that command.\n\n")
	for i, title := range titles {
		b.WriteString("### " + itoa(i+1) + ". " + title + "\n\n")
	}
	b.WriteString("Set the numeric id from step 9, then plan and apply.\n")
	b.WriteString("The same key you put in the vault in step 10.\n")
	return b.String()
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func TestAcceptsTheRunbookThisRepositoryActuallyHas(t *testing.T) {
	findings, isRunbook := checkPage("production.md", wholePage())
	if !isRunbook {
		t.Fatal("a fifteen step page was not recognised as a numbered runbook")
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings, got %v", findings)
	}
}

// The failure this tool exists for: nine headings renumbered by hand and one
// cross reference four hundred lines away left pointing at the old number.
func TestRefusesAReferenceToAStepThatIsNotThere(t *testing.T) {
	findings, isRunbook := checkPage("production.md", strings.Replace(wholePage(), "from step 9", "from step 16", 1))
	if !isRunbook {
		t.Fatal("page was not recognised as a numbered runbook")
	}
	if len(findings) != 1 {
		t.Fatalf("expected exactly one finding, got %v", findings)
	}
	if !strings.Contains(findings[0].text, "refers to step 16") {
		t.Fatalf("finding does not name the dangling reference: %q", findings[0].text)
	}
}

// A step deleted and the hole left open. Every later heading is now off by one
// against its position, and the preamble's reference to the deleted step
// dangles, so this is reported twice over and both are true.
func TestRefusesAHoleInTheNumbering(t *testing.T) {
	page := wholePage()
	i := strings.Index(page, "### 6. Bind the certificate")
	j := strings.Index(page, "### 7. Confirm")
	findings, _ := checkPage("production.md", page[:i]+page[j:])
	if len(findings) == 0 {
		t.Fatal("a deleted step with the numbering left open was not reported")
	}
	var sawDangling, sawGap bool
	for _, f := range findings {
		if strings.Contains(f.text, "refers to step 6") {
			sawDangling = true
		}
		if strings.Contains(f.text, "step 7 is the 6th heading") {
			sawGap = true
		}
	}
	if !sawDangling {
		t.Errorf("did not report the preamble reference to the deleted step: %v", findings)
	}
	if !sawGap {
		t.Errorf("did not report the hole the deletion left: %v", findings)
	}
}

// The positive control this tool needs and the reason it is not oversold. A
// step deleted and every later step renumbered to close the hole is internally
// consistent, so this tool passes it. ff893073 did precisely that to
// production.md and nothing here would have said so. A gate that cannot be
// shown to miss something is a gate nobody knows the edge of.
func TestPassesTheRevertItCannotSee(t *testing.T) {
	page := wholePage()
	i := strings.Index(page, "### 6. Bind the certificate")
	j := strings.Index(page, "### 7. Confirm")
	closed := page[:i] + page[j:]
	for n := 7; n <= 15; n++ {
		closed = strings.Replace(closed, "### "+itoa(n)+". ", "### "+itoa(n-1)+". ", 1)
	}
	closed = strings.Replace(closed, "Step 6 below is that command.\n\n", "", 1)
	closed = strings.Replace(closed, "from step 9", "from step 8", 1)
	closed = strings.Replace(closed, "vault in step 10", "vault in step 9", 1)
	findings, isRunbook := checkPage("production.md", closed)
	if !isRunbook {
		t.Fatal("page was not recognised as a numbered runbook")
	}
	if len(findings) != 0 {
		t.Fatalf("this tool is documented as blind to a clean renumber; it reported %v", findings)
	}
}

// A page that is not a numbered runbook is out of scope rather than passing,
// and the difference matters to the count this tool prints.
func TestAPageWithOneNumberedHeadingIsNotARunbook(t *testing.T) {
	_, isRunbook := checkPage("concepts.md", "### 1. The only numbered heading here\n\nProse.\n")
	if isRunbook {
		t.Fatal("a page with one numbered heading was treated as a runbook")
	}
}

// A shell an operator pastes carries its own numbers and is not this page's
// numbering. Blanking fences is what stops a code block failing the page.
func TestIgnoresStepReferencesInsideACodeBlock(t *testing.T) {
	page := wholePage() + "\n```sh\n# see step 99 of the vendor's guide\n```\n"
	findings, _ := checkPage("production.md", page)
	if len(findings) != 0 {
		t.Fatalf("a reference inside a fenced block was read as this page's numbering: %v", findings)
	}
}
