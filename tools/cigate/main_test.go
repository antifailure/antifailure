package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The state table this gate exists for. Every conclusion GitHub documents for a
// workflow run appears here, plus the two ways of having no conclusion, and
// exactly one row passes.
//
// It is written as a table so that a conclusion added to `refusals` without a
// row here, or a row here with no handling, is visible as a hole rather than as
// an absence. The positive control is the first row, and it is load bearing: a
// command that refused everything would satisfy every other row in this file
// and would be a gate nobody could ever release through.
func TestEveryStateGitHubCanReport(t *testing.T) {
	cases := []struct {
		status     string
		conclusion string
		want       verdict
	}{
		{"completed", "success", pass},

		{"completed", "failure", refuse},
		{"completed", "cancelled", refuse},
		{"completed", "timed_out", refuse},
		{"completed", "startup_failure", refuse},
		{"completed", "stale", refuse},
		{"completed", "action_required", refuse},
		{"completed", "neutral", refuse},
		{"completed", "skipped", refuse},
		{"completed", "", refuse},
		{"completed", "something_github_has_not_invented_yet", refuse},

		{"queued", "", wait},
		{"pending", "", wait},
		{"waiting", "", wait},
		{"requested", "", wait},
		{"in_progress", "", wait},
	}
	for _, c := range cases {
		name := c.status
		if c.conclusion != "" {
			name += "/" + c.conclusion
		}
		t.Run(name, func(t *testing.T) {
			got, why, _ := decide([]run{{
				Path:       ciWorkflow,
				Status:     c.status,
				Conclusion: c.conclusion,
				CreatedAt:  time.Unix(1, 0),
			}}, ciWorkflow)
			if got != c.want {
				t.Fatalf("status %q conclusion %q: got %s, want %s (%s)",
					c.status, c.conclusion, got, c.want, why)
			}
			if why == "" {
				t.Error("the verdict came with no reason, so nobody reading the log learns anything")
			}
		})
	}
}

// A cancelled run is the trap this repository has walked into before. GitHub
// spells a job that hit its own timeout-minutes, a run somebody stopped by hand
// and a run superseded by a newer push with the same word, and not one of them
// is a verdict about the commit. The message has to name more than one cause,
// because "cancelled" on its own sends somebody looking for a person who
// pressed a button and that is the least likely of the three.
func TestCancelledRefusesAndNamesMoreThanOneCause(t *testing.T) {
	got, why, _ := decide([]run{{
		Path: ciWorkflow, Status: "completed", Conclusion: "cancelled", CreatedAt: time.Unix(1, 0),
	}}, ciWorkflow)
	if got != refuse {
		t.Fatalf("a cancelled CI run was treated as %s", got)
	}
	for _, want := range []string{"never reached a verdict", "timeout-minutes", "by hand", "superseded"} {
		if !strings.Contains(why, want) {
			t.Errorf("the refusal never mentions %q, so it does not point at the real cause: %s", want, why)
		}
	}
}

// A skipped run reads as an absence of red and means nothing ran. It must never
// be counted as evidence.
func TestSkippedIsNotEvidenceOfAnything(t *testing.T) {
	got, why, _ := decide([]run{{
		Path: ciWorkflow, Status: "completed", Conclusion: "skipped", CreatedAt: time.Unix(1, 0),
	}}, ciWorkflow)
	if got != refuse {
		t.Fatalf("a skipped CI run was treated as %s", got)
	}
	if !strings.Contains(why, "nothing was checked") {
		t.Errorf("the refusal does not say the run checked nothing: %s", why)
	}
}

// No run at all is a wait rather than a refusal, because the tag can be pushed
// while the commit's CI is still being created. It is also never a pass: the
// caller turns an exhausted budget into a refusal, which the poll tests cover.
func TestNoRunAtAllWaits(t *testing.T) {
	got, why, r := decide(nil, ciWorkflow)
	if got != wait {
		t.Fatalf("an empty run list was treated as %s", got)
	}
	if r != nil {
		t.Error("it returned a run it did not have")
	}
	if !strings.Contains(why, ciWorkflow) {
		t.Errorf("the reason does not name the workflow it is waiting for: %s", why)
	}
}

// Runs exist for the commit and none of them is CI. This is what a rename of
// ci.yml looks like from here, and it must not read as green because some other
// workflow went green on the same commit.
func TestAnotherWorkflowsGreenRunIsNotCIsVerdict(t *testing.T) {
	got, _, _ := decide([]run{
		{Path: ".github/workflows/links.yml", Name: "CI", Status: "completed", Conclusion: "success"},
		{Path: ".github/workflows/cd.yml", Status: "completed", Conclusion: "success"},
	}, ciWorkflow)
	if got != wait {
		t.Fatalf("a green run from a different workflow was treated as %s", got)
	}
}

// A commit can carry more than one run: a pull request run on the branch head
// and a push run on main, or a run somebody re-ran. The newest is the current
// answer, and the ordering is computed here rather than taken from the
// response, because the endpoint's ordering is a convention and not a promise.
func TestTheNewestRunIsTheAnswerWhicheverOrderTheyArriveIn(t *testing.T) {
	older := run{Path: ciWorkflow, ID: 1, Status: "completed", Conclusion: "success", CreatedAt: time.Unix(100, 0)}
	newer := run{Path: ciWorkflow, ID: 2, Status: "completed", Conclusion: "cancelled", CreatedAt: time.Unix(200, 0)}

	for _, order := range [][]run{{newer, older}, {older, newer}} {
		got, _, r := decide(order, ciWorkflow)
		if got != refuse {
			t.Fatalf("an older green run overrode a newer cancelled one: got %s", got)
		}
		if r == nil || r.ID != 2 {
			t.Fatalf("it judged the wrong run: %+v", r)
		}
	}

	// And the other direction, so this is not a rule that only ever refuses.
	older.Conclusion, newer.Conclusion = "cancelled", "success"
	got, _, r := decide([]run{older, newer}, ciWorkflow)
	if got != pass {
		t.Fatalf("a re-run that went green did not pass: got %s", got)
	}
	if r == nil || r.ID != 2 {
		t.Fatalf("it judged the wrong run: %+v", r)
	}
}

// Two runs created in the same second, which the API reports at second
// granularity, so this is reachable rather than theoretical.
func TestRunsCreatedInTheSameSecondAreOrderedByID(t *testing.T) {
	same := time.Unix(300, 0)
	got, _, r := decide([]run{
		{Path: ciWorkflow, ID: 7, Status: "completed", Conclusion: "success", CreatedAt: same},
		{Path: ciWorkflow, ID: 8, Status: "completed", Conclusion: "failure", CreatedAt: same},
	}, ciWorkflow)
	if got != refuse || r.ID != 8 {
		t.Fatalf("got %s on run %+v, want a refusal on run 8", got, r)
	}
}

// ---------------------------------------------------------------------------
// The polling loop.
// ---------------------------------------------------------------------------

type scripted struct {
	answers [][]run
	errs    []error
	calls   int
}

func (s *scripted) runs(string) ([]run, error) {
	i := s.calls
	s.calls++
	if i < len(s.errs) && s.errs[i] != nil {
		return nil, s.errs[i]
	}
	if i < len(s.answers) {
		return s.answers[i], nil
	}
	return nil, nil
}

func completed(conclusion string) []run {
	return []run{{Path: ciWorkflow, ID: 1, Status: "completed", Conclusion: conclusion, CreatedAt: time.Unix(1, 0)}}
}

func running() []run {
	return []run{{Path: ciWorkflow, ID: 1, Status: "in_progress", CreatedAt: time.Unix(1, 0)}}
}

func TestItWaitsThroughAQueueAndThenPasses(t *testing.T) {
	c := &scripted{answers: [][]run{nil, running(), running(), completed("success")}}
	var log bytes.Buffer
	got, _, _ := poll(c, "abc", ciWorkflow, 10, 0, &log)
	if got != pass {
		t.Fatalf("got %s, want pass. log:\n%s", got, log.String())
	}
	if c.calls != 4 {
		t.Errorf("it asked %d times, want 4", c.calls)
	}
}

func TestItWaitsThroughAQueueAndThenRefuses(t *testing.T) {
	c := &scripted{answers: [][]run{running(), running(), completed("failure")}}
	var log bytes.Buffer
	got, why, _ := poll(c, "abc", ciWorkflow, 10, 0, &log)
	if got != refuse {
		t.Fatalf("got %s, want refuse. log:\n%s", got, log.String())
	}
	if !strings.Contains(why, "CI failed") {
		t.Errorf("the refusal does not say CI failed: %s", why)
	}
}

// The budget running out is a refusal, and a different one from a red CI. A
// release does not publish on nobody knowing.
func TestRunningOutOfBudgetRefusesAndSaysNobodyKnows(t *testing.T) {
	c := &scripted{answers: [][]run{running(), running(), running()}}
	var log bytes.Buffer
	got, why, _ := poll(c, "abc", ciWorkflow, 3, 0, &log)
	if got != refuse {
		t.Fatalf("an unfinished CI ran out the budget and returned %s", got)
	}
	if strings.Contains(why, "CI failed") {
		t.Errorf("it reported a timeout as a failed commit, which sends somebody to the wrong file: %s", why)
	}
	if !strings.Contains(why, "no conclusion") {
		t.Errorf("the refusal does not say why it gave up: %s", why)
	}
	if c.calls != 3 {
		t.Errorf("it asked %d times against a budget of 3", c.calls)
	}
}

// A commit that never had a CI run burns the whole budget and then refuses. It
// is the same shape as the case above and worth its own test, because this is
// what a tag on a commit that never reached main looks like.
func TestACommitWithNoRunEverRefusesAtTheEnd(t *testing.T) {
	c := &scripted{}
	var log bytes.Buffer
	got, _, _ := poll(c, "abc", ciWorkflow, 2, 0, &log)
	if got != refuse {
		t.Fatalf("a commit CI never ran on returned %s", got)
	}
}

// A transient error is retried and never read as a pass or as a refusal on its
// own. The commit is green underneath it and the gate has to find that out.
func TestATransientErrorIsRetriedRatherThanBelieved(t *testing.T) {
	c := &scripted{
		errs:    []error{fmt.Errorf("the API answered 502, which may pass"), nil},
		answers: [][]run{nil, completed("success")},
	}
	var log bytes.Buffer
	got, _, _ := poll(c, "abc", ciWorkflow, 5, 0, &log)
	if got != pass {
		t.Fatalf("got %s, want pass. log:\n%s", got, log.String())
	}
	if !strings.Contains(log.String(), "could not read the run list") {
		t.Error("the retry is invisible in the log")
	}
}

// Errors all the way to the end of the budget refuse, and say the API never
// answered rather than blaming the commit.
func TestErrorsAllTheWayThroughRefuse(t *testing.T) {
	boom := errors.New("dial tcp: no route to host")
	c := &scripted{errs: []error{boom, boom}}
	var log bytes.Buffer
	got, why, _ := poll(c, "abc", ciWorkflow, 2, 0, &log)
	if got != refuse {
		t.Fatalf("got %s, want refuse", got)
	}
	if !strings.Contains(why, "never answered") {
		t.Errorf("the refusal blames the commit rather than the API: %s", why)
	}
}

// A bad token never becomes a good one. Spending 38 minutes discovering that
// hides the real cause behind a timeout message.
func TestAFatalErrorRefusesImmediatelyInsteadOfBurningTheBudget(t *testing.T) {
	c := &scripted{errs: []error{fatal{errors.New("the API answered 401")}}}
	var log bytes.Buffer
	got, why, _ := poll(c, "abc", ciWorkflow, 115, 0, &log)
	if got != refuse {
		t.Fatalf("got %s, want refuse", got)
	}
	if c.calls != 1 {
		t.Errorf("it asked %d times about a credential that will never work", c.calls)
	}
	if !strings.Contains(why, "401") {
		t.Errorf("the refusal does not carry the status: %s", why)
	}
}

// ---------------------------------------------------------------------------
// The reader.
// ---------------------------------------------------------------------------

func TestItReadsARealRunListAndAnAuthFailure(t *testing.T) {
	var code int
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer t" {
			t.Errorf("Authorization header was %q", got)
		}
		if !strings.Contains(r.URL.RawQuery, "head_sha=abc") {
			t.Errorf("it did not ask about the commit: %s", r.URL.RawQuery)
		}
		w.WriteHeader(code)
		fmt.Fprint(w, body)
	}))
	defer server.Close()
	a := &api{base: server.URL, repo: "antifailure/antifailure", token: "t", http: server.Client()}

	code, body = 200, `{"workflow_runs":[{"id":9,"name":"CI","path":".github/workflows/ci.yml",
		"status":"completed","conclusion":"failure","html_url":"https://example.invalid/9",
		"created_at":"2026-09-02T13:16:00Z","run_attempt":1,"event":"push"}]}`
	got, err := a.runs("abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Conclusion != "failure" || got[0].Path != ciWorkflow {
		t.Fatalf("it read %+v", got)
	}
	if v, _, _ := decide(got, ciWorkflow); v != refuse {
		t.Fatalf("a real red run list produced %s", v)
	}

	code, body = 401, `{"message":"Bad credentials"}`
	var stop fatal
	if _, err := a.runs("abc"); !errors.As(err, &stop) {
		t.Fatalf("a 401 was retryable: %v", err)
	}

	code, body = 500, `{"message":"oops"}`
	if _, err := a.runs("abc"); err == nil || errors.As(err, &stop) {
		t.Fatalf("a 500 was fatal or ignored: %v", err)
	}
}

// Every conclusion the code refuses has a sentence, and every sentence belongs
// to a conclusion the code refuses. A message added to the map and never
// reachable is dead, and a conclusion handled with no message is a refusal
// nobody can act on.
func TestEveryRefusalCarriesItsOwnSentence(t *testing.T) {
	for conclusion, why := range refusals {
		if why == "" {
			t.Errorf("%q refuses with no explanation", conclusion)
		}
		v, got, _ := decide([]run{{
			Path: ciWorkflow, Status: "completed", Conclusion: conclusion, CreatedAt: time.Unix(1, 0),
		}}, ciWorkflow)
		if v != refuse {
			t.Errorf("%q is in the refusal table and returned %s", conclusion, v)
		}
		if got != why {
			t.Errorf("%q reported %q rather than its own sentence", conclusion, got)
		}
	}
	if _, ok := refusals["success"]; ok {
		t.Error("success is in the refusal table, so nothing could ever be released")
	}
}
