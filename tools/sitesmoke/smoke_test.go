package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The one thing this whole file is really about.
//
// PR 243 in this repository fixed a report that published a failure as a pass.
// The shape recurs because "pass" is the answer with no work attached to it:
// every branch that forgets to decide falls into it. So every verdict this
// tool can be handed, including the ones it has never seen, is checked here
// against the rule that only the literal word "pass" is allowed to be a pass.
func TestOnlyAPassIsAPass(t *testing.T) {
	for _, tc := range []struct {
		verdict string
		want    Answer
	}{
		{"pass", Allowed},
		{"fail", Refused},
		{"flaky", Undecided},
		{"blocked", Undecided},
		{"unverified", Undecided},
		{"", Undecided},
		{"succeeded", Undecided},
		{"ok", Undecided},
		{"PASS", Undecided},
	} {
		got := decide("https://antifailure.dev", workflowResult{
			Workflow: "w", Outcome: outcome{Verdict: tc.verdict, Detail: "d"},
		})
		if got.Answer != tc.want {
			t.Errorf("verdict %q became %v, want %v", tc.verdict, got.Answer, tc.want)
		}
	}
}

// A failure has to arrive carrying the sentence the page showed.
//
// "exploration failed" is a red mark somebody has to reproduce. The page's own
// words are a red mark somebody can take straight to the cause, and they are
// the only thing that tells a control plane with no such route apart from one
// refusing this hostname.
func TestAFailureQuotesWhatThePageSaid(t *testing.T) {
	said := `The page shows an error rather than what was expected. It says: "Could not reach the server."`
	got := decide("https://www.antifailure.dev", workflowResult{
		Workflow: "the-careers-form-reaches-the-control-plane",
		Outcome:  outcome{Verdict: "fail", Cause: "expectation-not-met", Detail: said},
	})
	if got.Answer != Refused {
		t.Fatalf("answer is %v, want refused", got.Answer)
	}
	if !strings.Contains(got.Said, "Could not reach the server.") {
		t.Errorf("the finding does not quote the page: %q", got.Said)
	}
}

// Unreachable and reached-but-broken must not read the same.
//
// They have different first steps: one is "is the site up", the other is "what
// did the control plane answer". A tool that says "failed" for both sends
// whoever reads it to the wrong place half the time.
func TestUnreachableReadsDifferentlyFromABrokenPage(t *testing.T) {
	unreachable := decide("https://antifailure.dev", workflowResult{
		Outcome: outcome{
			Verdict: "blocked", Cause: "runner-failure",
			Detail: "page.goto: net::ERR_NAME_NOT_RESOLVED at https://antifailure.dev/careers",
		},
	})
	broken := decide("https://antifailure.dev", workflowResult{
		Outcome: outcome{
			Verdict: "fail", Cause: "expectation-not-met",
			Detail: `It says: "Could not reach the server."`,
		},
	})
	if unreachable.Answer == broken.Answer {
		t.Fatalf("both answered %v", unreachable.Answer)
	}
	if unreachable.Answer != Undecided || broken.Answer != Refused {
		t.Fatalf("unreachable=%v broken=%v", unreachable.Answer, broken.Answer)
	}
	if !strings.Contains(unreachable.Said, "could not drive") {
		t.Errorf("the unreachable message does not say the browser never got there: %q", unreachable.Said)
	}
	if strings.Contains(unreachable.Said, "Could not reach the server.") {
		t.Errorf("the unreachable message quotes a page it never read: %q", unreachable.Said)
	}
}

// A run that checked nothing has proved nothing.
func TestNothingCheckedIsNotAPass(t *testing.T) {
	if got := verdict(nil); got != Undecided {
		t.Errorf("an empty run answered %v, want could not tell", got)
	}
	if got := verdict([]Finding{}); got != Undecided {
		t.Errorf("an empty run answered %v, want could not tell", got)
	}
}

// One bad origin decides the whole run, and the worst answer wins.
func TestTheWorstAnswerWins(t *testing.T) {
	pass := Finding{Answer: Allowed}
	for _, tc := range []struct {
		name     string
		findings []Finding
		want     Answer
	}{
		{"all pass", []Finding{pass, pass}, Allowed},
		{"one refused", []Finding{pass, {Answer: Refused}}, Refused},
		{"one undecided", []Finding{pass, {Answer: Undecided}}, Undecided},
		{"refused outranks undecided", []Finding{{Answer: Undecided}, {Answer: Refused}}, Refused},
	} {
		if got := verdict(tc.findings); got != tc.want {
			t.Errorf("%s answered %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestExitCodesAreTheDocumentedOnes(t *testing.T) {
	for answer, want := range map[Answer]int{Allowed: 0, Refused: 1, Undecided: 2} {
		if got := answer.exitCode(); got != want {
			t.Errorf("%v exits %d, want %d", answer, got, want)
		}
	}
}

// The offline half against the real tree, and against a tree missing a sentence.
func TestTheContractIsCheckedAgainstTheRealTree(t *testing.T) {
	var out strings.Builder
	if err := contractHolds(filepath.Join("..", ".."), &out); err != nil {
		t.Fatalf("the sentences this check waits for are not in the tree: %v", err)
	}
	for _, c := range contracts() {
		if !strings.Contains(out.String(), c.sentence) {
			t.Errorf("the report does not name %q, so a reader cannot see what was checked", c.sentence)
		}
	}
}

func TestAMissingSentenceIsRefusedRatherThanIgnored(t *testing.T) {
	root := t.TempDir()
	for _, c := range contracts() {
		path := filepath.Join(root, c.file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		body := c.sentence
		if c.sentence == recordedSentence {
			// The one sentence this tree does not carry, which is the case:
			// an expectation waiting for words the site no longer renders can
			// never be satisfied by any deployment.
			body = "nothing like it"
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out strings.Builder
	err := contractHolds(root, &out)
	if err == nil {
		t.Fatal("a tree missing the confirmation sentence passed the contract")
	}
	if !strings.Contains(err.Error(), recordedSentence) {
		t.Errorf("the refusal does not name the missing sentence: %v", err)
	}
}

// A file that is not there at all is refused, not skipped.
func TestAnUnreadableFileFailsRatherThanSkips(t *testing.T) {
	var out strings.Builder
	if err := contractHolds(t.TempDir(), &out); err == nil {
		t.Fatal("a tree with none of the files passed the contract")
	}
}

// The scheduled workflow must not be able to become one that writes.
func TestTheScheduledWorkflowWritesNothing(t *testing.T) {
	for _, w := range workflowsFor(false) {
		if w.writes {
			t.Fatalf("%s writes and runs without -allow-writes", w.Name)
		}
		if strings.TrimSpace(w.why) == "" {
			t.Errorf("%s claims to write nothing and gives no reason, which is an assertion "+
				"nobody can check", w.Name)
		}
	}
	names := map[string]bool{}
	for _, w := range workflowsFor(true) {
		names[w.Name] = true
	}
	if !names[applyForAFoundingRole().Name] {
		t.Error("-allow-writes does not add the workflow that files an application")
	}
}

// Every expectation is quoted, so every one of them can say no.
//
// An unquoted expectation is judged by how many of its words are somewhere on
// the page, and the control plane's own refusal scores six of its seven words
// against the careers page before the form is touched. One unquoted line here
// would be an assertion that passes a broken deployment.
func TestEveryExpectationIsVerbatim(t *testing.T) {
	for _, w := range workflowsFor(true) {
		if len(w.Expect) == 0 {
			t.Errorf("%s expects nothing, so it cannot fail", w.Name)
		}
		for _, e := range w.Expect {
			if !strings.HasPrefix(e, `"`) || !strings.HasSuffix(e, `"`) || len(e) < 3 {
				t.Errorf("%s expects %q, which is judged by word overlap and not by whether "+
					"the page says it", w.Name, e)
			}
		}
	}
}

// The work link that keeps the scheduled check inert has to stay inert.
func TestTheInertWorkLinkCarriesCredentials(t *testing.T) {
	if !strings.Contains(inertWorkLink, "@") || !strings.Contains(inertWorkLink, ":") {
		t.Fatalf("%q has no credentials in it, so the control plane would accept it and the "+
			"scheduled check would file a job application every time it ran", inertWorkLink)
	}
	if !strings.Contains(inertWorkLink, ".test") {
		t.Errorf("%q is not in a reserved domain", inertWorkLink)
	}
	w := theCareersFormReachesTheControlPlane()
	if w.Answers["Link to your work"] != inertWorkLink {
		t.Fatalf("the scheduled workflow answers the work link with %q, not the inert one",
			w.Answers["Link to your work"])
	}
}

// Driving the runner: everything that can go wrong is Undecided, never Allowed.
func fakeRunner(t *testing.T, script string) runner {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-node")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(entry, []byte("// not read by the fake\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return runner{
		Entry: entry, Node: path, Artifacts: filepath.Join(dir, "artifacts"),
		Attempts: 1, Timeout: 30 * time.Second,
	}
}

func TestARunnerThatSaysNothingIsNotAPass(t *testing.T) {
	r := fakeRunner(t, `exit 0`)
	got := r.drive(context.Background(), "https://antifailure.dev", theCareersFormReachesTheControlPlane())
	if got.Answer != Undecided {
		t.Fatalf("a runner that printed nothing answered %v", got.Answer)
	}
	// The message as well as the answer. Both guards below this one also
	// return Undecided, so the answer alone cannot say which of them caught
	// it, and a mutation that removes this check would be covered by the next
	// one and look tested.
	if !strings.Contains(got.Said, "did not return a result document") {
		t.Errorf("the message does not say the runner returned nothing readable: %q", got.Said)
	}
}

func TestARunnerThatCrashesNamesWhatItSaid(t *testing.T) {
	// Two lines, because node prints its experimental type stripping warning
	// before anything the runner says and the useful line is the LAST one.
	r := fakeRunner(t, `echo "ExperimentalWarning: Type Stripping is experimental" >&2
echo "af-runner: Cannot find package 'playwright'" >&2
exit 1`)
	got := r.drive(context.Background(), "https://antifailure.dev", theCareersFormReachesTheControlPlane())
	if got.Answer != Undecided {
		t.Fatalf("a runner that crashed answered %v", got.Answer)
	}
	if !strings.Contains(got.Said, "playwright") {
		t.Errorf("the message does not carry what the runner said: %q", got.Said)
	}
}

func TestAnEmptyResultListIsNotAPass(t *testing.T) {
	r := fakeRunner(t, `echo '{"results":[],"passed":0,"failed":0}'`)
	got := r.drive(context.Background(), "https://antifailure.dev", theCareersFormReachesTheControlPlane())
	if got.Answer != Undecided {
		t.Fatalf("a document with no results answered %v", got.Answer)
	}
	if !strings.Contains(got.Said, "nothing was checked") {
		t.Errorf("the message does not say nothing was checked: %q", got.Said)
	}
}

func TestAPassingDocumentIsRead(t *testing.T) {
	r := fakeRunner(t, `echo '{"results":[{"workflow":"w","outcome":{"verdict":"pass","cause":"succeeded","detail":"ok"},"steps":["Open /careers"],"evidence":{"screenshot":"/tmp/a.png"}}]}'`)
	got := r.drive(context.Background(), "https://antifailure.dev", theCareersFormReachesTheControlPlane())
	if got.Answer != Allowed {
		t.Fatalf("a passing document answered %v: %s", got.Answer, got.Said)
	}
	if len(got.Evidence) != 1 || got.Evidence[0] != "/tmp/a.png" {
		t.Errorf("the evidence was lost: %v", got.Evidence)
	}
}

// The runner exits 8 on a failing workflow and 0 on a blocked one. Neither
// exit code is the answer here, and reading them instead of the document would
// turn "could not reach the origin" into "the site is fine".
func TestTheDocumentDecidesAndNotTheExitCode(t *testing.T) {
	r := fakeRunner(t, `echo '{"results":[{"workflow":"w","outcome":{"verdict":"blocked","cause":"runner-failure","detail":"page.goto timed out"}}]}'; exit 0`)
	got := r.drive(context.Background(), "https://antifailure.dev", theCareersFormReachesTheControlPlane())
	if got.Answer != Undecided {
		t.Fatalf("a blocked run that exited zero answered %v", got.Answer)
	}
}
