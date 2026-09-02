package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestJSONFieldReadsWhatUpPrints(t *testing.T) {
	doc := `{"env_id":"demo-main-abc","url":"http://127.0.0.1:46000","proxied":true}`
	if got := jsonField(doc, "url"); got != "http://127.0.0.1:46000" {
		t.Errorf("url = %q", got)
	}
	if got := jsonField(doc, "env_id"); got != "demo-main-abc" {
		t.Errorf("env_id = %q", got)
	}
	// A field that is not there is empty rather than a panic or a wrong
	// answer, because the caller checks for empty and says something useful.
	if got := jsonField(doc, "golden"); got != "" {
		t.Errorf("a missing field returned %q", got)
	}
	if got := jsonField("not json at all", "url"); got != "" {
		t.Errorf("nonsense returned %q", got)
	}
}

// A service can report ready a moment before its port is accepting, so the
// check retries. A walkthrough that failed on that would be measuring the
// harness rather than the product.
func TestReachableRetriesUntilTheServiceAnswers(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := reachable(ctx, srv.URL); err != nil {
		t.Fatalf("reachable: %v", err)
	}
	if got := calls.Load(); got < 3 {
		t.Errorf("answered after %d calls, so it did not retry", got)
	}
}

// A 404 is an answer. The step is "the address it printed serves something",
// not "the root path is implemented", and an example whose root is not a route
// is still a working environment.
func TestReachableAcceptsAnyAnswerBelowFiveHundred(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no such route", http.StatusNotFound)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := reachable(ctx, srv.URL); err != nil {
		t.Errorf("a 404 should count as reachable: %v", err)
	}
}

// A cancelled context stops the retry loop rather than running to the
// deadline, so an interrupted walkthrough tears down promptly.
func TestReachableStopsWhenTheContextIsCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := reachable(ctx, srv.URL)
	if err == nil {
		t.Fatal("a service answering 503 forever should not count as reachable")
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("took %s, so cancellation did not stop the loop", took.Round(time.Second))
	}
}

// Pointing it at a directory with no manifest is an error that names the
// directory, rather than a run that creates nothing and reports success.
func TestWalkingSomethingThatIsNotAnExampleIsAnError(t *testing.T) {
	err := walk(t.TempDir(), "nope", 0)
	if err == nil {
		t.Fatal("a directory with no manifest was accepted")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("the error does not name what was missing: %v", err)
	}
}

// af test can answer with a report or with an error document, and telling the
// walkthrough which it got is the difference between "the product refused" and
// "this tool cannot read the answer". The first version reported the engine
// refusing to provision a persona as "af test returned no flaky count", which
// described the instrument and buried the engine's own message.
func TestAnErrorDocumentIsReportedAsARefusalRatherThanAMissingField(t *testing.T) {
	_, err := verdictCounts(`{"code":"AF-GEN-000","message":"no users table could be found",` +
		`"exit_code":1}`)
	if err == nil {
		t.Fatal("an error document was read as a report")
	}
	if !strings.Contains(err.Error(), "refused before it reached a workflow") {
		t.Errorf("the error is %q, which does not say af test refused", err)
	}
	if !strings.Contains(err.Error(), "no users table could be found") {
		t.Errorf("the error is %q, which drops the engine's own message", err)
	}
}

// And a report that really is missing a count still says so, because a zero
// read out of an absent field is indistinguishable from a run that examined
// nothing.
func TestAReportMissingACountIsStillRefused(t *testing.T) {
	_, err := verdictCounts(`{"passed":1,"failed":0,"blocked":0,"unverified":0}`)
	if err == nil {
		t.Fatal("a report with no flaky count was accepted")
	}
	if !strings.Contains(err.Error(), `no "flaky" count`) {
		t.Errorf("the error is %q, which does not name the missing count", err)
	}
}

func TestAWholeReportReadsBack(t *testing.T) {
	counts, err := verdictCounts(`{"passed":6,"failed":0,"flaky":0,"blocked":0,"unverified":0}`)
	if err != nil {
		t.Fatal(err)
	}
	if counts["passed"] != 6 {
		t.Errorf("passed read back as %d, want 6", counts["passed"])
	}
}
