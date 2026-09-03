package main

import (
	"strings"
	"testing"
)

// rowsFor builds the table a test runs against. Written out rather than read
// from tools/docs/contact-routes.tsv, so a test says what it is testing and
// does not go red when somebody adds a placeholder to the console.
func rowsFor(t *testing.T, in ...*row) []*row {
	t.Helper()
	for _, r := range in {
		if r.why == "" {
			r.why = "a reason, because loadRows refuses a row without one"
		}
	}
	return in
}

func problems(t *testing.T, rel, body string, rows []*row) []finding {
	t.Helper()
	return Check(rel, body, rows)
}

// The defect this whole tool exists for, in the words it was written in.
func TestTheSentenceThatStartedThisIsRefused(t *testing.T) {
	body := "## Enforcement\n\nInstances of abusive behavior may be\n" +
		"reported to the community leaders responsible for enforcement at\n" +
		"conduct@antifailure.dev. All complaints will be reviewed.\n"

	got := problems(t, "CODE_OF_CONDUCT.md", body, nil)
	if len(got) != 1 {
		t.Fatalf("want one finding, got %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].problem, "no row") {
		t.Errorf("problem = %q, want the missing row", got[0].problem)
	}
	// A finding that only forbids teaches somebody to work around it.
	if got[0].fix == "" {
		t.Error("a finding must say what to do instead")
	}
}

// The instruction wraps: the verb is on one line and the address is on the
// next. A rule scoped to a single line would have passed the original defect,
// which is the reason the window spans line breaks.
func TestTheInvitationIsFoundAcrossALineBreak(t *testing.T) {
	body := "reported to the community leaders responsible for enforcement at\nconduct@antifailure.dev.\n"
	rows := rowsFor(t, &row{path: "CODE_OF_CONDUCT.md", address: "conduct@antifailure.dev", verdict: verdictNotRoute})

	got := problems(t, "CODE_OF_CONDUCT.md", body, rows)
	if len(got) != 1 {
		t.Fatalf("want one finding, got %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].problem, "instruction") {
		t.Errorf("problem = %q, want the invitation rule", got[0].problem)
	}
}

// The laundering path, and the reason the verdicts are a closed set. A row
// cannot argue a domain with no mail exchanger into receiving mail.
func TestAReceivesRowCannotRescueADeadDomain(t *testing.T) {
	rows := rowsFor(t, &row{
		path: "SECURITY.md", address: "security@antifailure.dev", verdict: verdictReceives,
		why: "the maintainers read this",
	})
	got := problems(t, "SECURITY.md", "Send findings here: security@antifailure.dev\n", rows)
	if len(got) != 1 {
		t.Fatalf("want one finding, got %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].problem, "cannot receive mail") {
		t.Errorf("problem = %q, want the dead domain rule", got[0].problem)
	}
}

// The other laundering path: relabel the instruction as furniture.
func TestFurnitureCannotBeAnInstruction(t *testing.T) {
	rows := rowsFor(t, &row{path: "SECURITY.md", address: "security@antifailure.dev", verdict: verdictNotRoute})
	if got := problems(t, "SECURITY.md", "Please write to security@antifailure.dev.\n", rows); len(got) != 1 {
		t.Fatalf("want one finding, got %d: %+v", len(got), got)
	}
}

// One row covers a whole file, so a file that legitimately quotes a dead
// address once must not thereby license an instruction further down. This is
// why the invitation rule sits above the verdicts rather than inside one.
func TestOneQuotationDoesNotLicenseAnInstructionLater(t *testing.T) {
	body := "The address named here, conduct@antifailure.dev, cannot receive mail.\n\n" +
		strings.Repeat("Filler that is not about mail at all.\n", 6) +
		"Report abuse to conduct@antifailure.dev.\n"
	rows := rowsFor(t, &row{path: "CODE_OF_CONDUCT.md", address: "conduct@antifailure.dev", verdict: verdictDefect})

	got := problems(t, "CODE_OF_CONDUCT.md", body, rows)
	if len(got) != 1 {
		t.Fatalf("want exactly the second occurrence, got %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].problem, "instruction") {
		t.Errorf("problem = %q, want the invitation rule", got[0].problem)
	}
}

// A quotation of a defect has to be accompanied by the correction, or the
// reader takes the address away and nothing else.
func TestQuotingADefectNeedsTheFileToSayItIsOne(t *testing.T) {
	rows := rowsFor(t, &row{path: "notes.md", address: "conduct@antifailure.dev", verdict: verdictDefect})

	if got := problems(t, "notes.md", "The old address was conduct@antifailure.dev.\n", rows); len(got) != 1 {
		t.Fatalf("a bare quotation should be refused, got %d: %+v", len(got), got)
	}
	with := "The old address was conduct@antifailure.dev, at a domain with no mail exchanger, " +
		"so it could not receive anything.\n"
	if got := problems(t, "notes.md", with, rows); len(got) != 0 {
		t.Errorf("a quotation beside the correction is fine, got %+v", got)
	}
}

// The sentence in the changelog fragment that records this defect. It opens
// "an email address that cannot receive mail", which is the opposite of an
// invitation, and a rule matching the bare word `email` anywhere in the window
// convicted it. That is why `email` is anchored to the end of the window.
func TestASentenceAboutAMailboxIsNotAnInvitationToUseIt(t *testing.T) {
	body := "The legal pages no longer publish an email address that cannot receive mail.\n\n" +
		"The addendum and the retention page both named\n`security@antifailure.dev` as the destination.\n"
	rows := rowsFor(t, &row{path: "f.md", address: "security@antifailure.dev", verdict: verdictDefect})

	if got := problems(t, "f.md", body, rows); len(got) != 0 {
		t.Errorf("want clean, got %+v", got)
	}
}

// `email` immediately in front of the address is the shape that does mean an
// invitation, in each of the ways people write it.
func TestEmailInFrontOfTheAddressIsAnInvitation(t *testing.T) {
	rows := rowsFor(t, &row{path: "f.md", address: "security@antifailure.dev", verdict: verdictNotRoute})
	for _, body := range []string{
		"Just email security@antifailure.dev.\n",
		"Email: security@antifailure.dev\n",
		"You can email us at security@antifailure.dev.\n",
	} {
		if got := problems(t, "f.md", body, rows); len(got) != 1 {
			t.Errorf("%q should be refused, got %+v", body, got)
		}
	}
}

// The documentation domains exist so an illustration cannot name a real
// mailbox. They need no row, because there is nothing on the other end and
// there never can be. Without this the fixtures in this repository would need
// two hundred rows and the twenty real ones would be unreadable.
func TestReservedDomainsNeedNoRow(t *testing.T) {
	body := "af inbox wait --to owner@example.test\nWrite to ada@example.com or grace@example.org.\n" +
		"Signed by 67278851+VirSanghavi@users.noreply.github.com.\n"
	if got := problems(t, "docs/x.md", body, nil); len(got) != 0 {
		t.Errorf("want clean, got %+v", got)
	}
}

// A placeholder in a sign-in field is not a promise, and the invitation rule
// must not reach it. This is the case that made the rule dead-domain only:
// the label above the input is the word Email.
func TestAPlaceholderAtALiveDomainIsNotConvictedByItsLabel(t *testing.T) {
	rows := rowsFor(t, &row{path: "www/AuthScreen.tsx", address: "you@company.com", verdict: verdictNotRoute})
	body := "<label>Email</label>\n<input placeholder=\"you@company.com\" />\n"
	if got := problems(t, "www/AuthScreen.tsx", body, rows); len(got) != 0 {
		t.Errorf("want clean, got %+v", got)
	}
}

// The probe row. It is deliberately an address nothing can deliver to, it is
// posted to an API rather than offered to a reader, and the paragraph that
// documents it ends by saying not to email it.
func TestASyntheticValueAtTheDeadDomainIsAllowed(t *testing.T) {
	rows := rowsFor(t, &row{path: "api/README.md", address: "waitlist-probe@antifailure.dev", verdict: verdictNotRoute})
	body := "One row in that table is not a person. `waitlist-probe@antifailure.dev` is\n" +
		"written by the workflow every morning. Do not count it, and do not email it.\n"
	if got := problems(t, "api/README.md", body, rows); len(got) != 0 {
		t.Errorf("want clean, got %+v", got)
	}
}

func TestExemptKnowsTheReservedNames(t *testing.T) {
	for _, d := range []string{
		"example.com", "example.net", "example.org",
		"antifailure.test", "corp.example", "host.invalid", "db.localhost",
		"users.noreply.github.com",
	} {
		if !exempt(d) {
			t.Errorf("%s should need no row", d)
		}
	}
	for _, d := range []string{"antifailure.dev", "gmail.com", "acme.com", "example.io", "notexample.com"} {
		if exempt(d) {
			t.Errorf("%s is a real domain and should need a row", d)
		}
	}
}

func TestDomainOfIgnoresATrailingFullStop(t *testing.T) {
	if got := domainOf("conduct@antifailure.dev."); got != "antifailure.dev" {
		t.Errorf("domainOf = %q", got)
	}
}

// A fixture is not published. The tests in this repository carry hundreds of
// addresses and none of them is a route.
func TestTestsAndLockfilesAreNotScanned(t *testing.T) {
	for _, p := range []string{
		"engine/internal/env/oracle_test.go",
		"web/apps/api/test/legal-facts.test.ts",
		"ee/web/sso/test/idp.ts",
		"engine/go.sum",
	} {
		if !testFile(p) {
			t.Errorf("%s should not be scanned", p)
		}
	}
	for _, p := range []string{"SECURITY.md", "tools/contactcheck/main.go", "www/components/AuthScreen.tsx"} {
		if testFile(p) {
			t.Errorf("%s is published and must be scanned", p)
		}
	}
}

// The one blind spot worth restating as a test, so nobody reads the gate as
// promising more than it does: a live domain with a `receives` row passes, and
// whether anybody actually reads that mailbox is not a question this tool can
// answer.
func TestAReceivesRowAtAnUncheckedDomainPasses(t *testing.T) {
	rows := rowsFor(t, &row{
		path: "SECURITY.md", address: "security@somewhere-else.dev", verdict: verdictReceives,
		why: "a person reads this",
	})
	if got := problems(t, "SECURITY.md", "Email security@somewhere-else.dev.\n", rows); len(got) != 0 {
		t.Errorf("this gate cannot check a domain it has not been told about, got %+v", got)
	}
}

// Prose wraps, and a phrase splits across the break with the comment marker of
// the next line in the middle of it. `no mail exchanger` is three words, and
// in ci.yml it sat as "no mail" then a newline then "# exchanger", which is
// the exact sentence the evidence rule looks for and could not see.
func TestEvidenceIsFoundAcrossAWrappedLine(t *testing.T) {
	rows := rowsFor(t, &row{path: ".github/workflows/ci.yml", address: "conduct@antifailure.dev", verdict: verdictDefect})
	body := "        # The file named conduct@antifailure.dev. The domain has no mail\n" +
		"        # exchanger, so nothing sent there was delivered.\n"
	if got := problems(t, ".github/workflows/ci.yml", body, rows); len(got) != 0 {
		t.Errorf("want clean, got %+v", got)
	}
}

// The review finding that tightened the evidence rule from the file to the
// paragraph. CODE_OF_CONDUCT.md legitimately quotes the dead address once,
// with the correction beside it. Under the file wide rule that one correction
// licensed every later occurrence in the same file, including a genuine
// instruction forty lines down, in the file a person opens after being
// harassed. File membership is not proximity.
func TestEvidenceFarFromTheAddressDoesNotCountAsBesideIt(t *testing.T) {
	rows := rowsFor(t, &row{path: "CODE_OF_CONDUCT.md", address: "conduct@antifailure.dev", verdict: verdictDefect})
	body := "The old address cannot receive mail, and here is why.\n" +
		strings.Repeat("A paragraph about something else entirely.\n", 40) +
		"The address is conduct@antifailure.dev.\n"

	got := problems(t, "CODE_OF_CONDUCT.md", body, rows)
	if len(got) != 1 {
		t.Fatalf("want the far quotation refused, got %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].problem, "lines of it") {
		t.Errorf("problem = %q, want the proximity rule", got[0].problem)
	}
}

// The same quotation with the correction in its own paragraph is fine, which
// is what every real one in this repository looks like.
func TestEvidenceBesideTheAddressCounts(t *testing.T) {
	rows := rowsFor(t, &row{path: "CODE_OF_CONDUCT.md", address: "conduct@antifailure.dev", verdict: verdictDefect})
	body := strings.Repeat("A paragraph about something else entirely.\n", 40) +
		"It named conduct@antifailure.dev, and that address\ncannot receive mail.\n"
	if got := problems(t, "CODE_OF_CONDUCT.md", body, rows); len(got) != 0 {
		t.Errorf("want clean, got %+v", got)
	}
}
