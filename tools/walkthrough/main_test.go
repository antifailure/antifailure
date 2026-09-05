package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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

// runnerStarts is the assertion af runner check deliberately does not make.
//
// The check reads a directory and reports what is in it. On 2026-09-05 it
// reported ready while the run resolved a DIFFERENT directory, so this
// walkthrough learned about a runner with no dependencies three steps later,
// from inside af test, as ERR_MODULE_NOT_FOUND on playwright. Loading the
// runner the check NAMED puts that failure at the step whose job is to answer
// the question.
//
// Written against a real runner source and a real node, because a test that
// only proved the function contains the right words would prove nothing about
// whether node agrees.
func runnerTree(t *testing.T, withDependency bool) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	main := "import './dep.ts';\nprocess.stderr.write('af-runner: expected a job document on standard input\\n');\nprocess.exit(2);\n"
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	// The import that fails, standing in for runner/src/browser.ts importing
	// playwright, which is the line the real failure came from.
	if err := os.WriteFile(filepath.Join(dir, "src", "dep.ts"), []byte("import 'af-walkthrough-fixture';\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if withDependency {
		pkg := filepath.Join(dir, "node_modules", "af-walkthrough-fixture")
		if err := os.MkdirAll(pkg, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkg, "package.json"),
			[]byte(`{"name":"af-walkthrough-fixture","version":"1.0.0","type":"module","main":"index.js"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkg, "index.js"), []byte("export default 1;\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func needsNode(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		// Said rather than passed. A skip that reads as a pass is the thing
		// this repository keeps finding in its own instruments.
		t.Skip("node is not on this machine, so the runner cannot be started and this proves nothing")
	}
}

func TestRunnerStartsRefusesARunnerNodeCannotLoad(t *testing.T) {
	needsNode(t)
	dir := runnerTree(t, false)

	err := runnerStarts(t.Context(), dir)
	if err == nil {
		t.Fatal("a runner whose import cannot be resolved was reported as starting, " +
			"which is exactly the tree af runner check called ready")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("the refusal does not name the runner it tried: %v", err)
	}
	// The specific message, not just some error. A generic "did not start"
	// would also fire here, and it does not tell the reader that the check
	// called this tree ready, which is the whole finding.
	if !strings.Contains(err.Error(), "node cannot load it") {
		t.Errorf("the refusal reads %q, which does not say the check called an unloadable "+
			"runner ready", err)
	}
}

func TestRunnerStartsAcceptsARunnerThatRefusesForWantOfAJobDocument(t *testing.T) {
	needsNode(t)

	// The real runner exits non zero here, because there is no job document on
	// standard input. That is the runner working, and treating a non zero exit
	// as failure would fail every walkthrough.
	if err := runnerStarts(t.Context(), runnerTree(t, true)); err != nil {
		t.Errorf("a runner that started and asked for a job document was refused: %v", err)
	}
}

func TestRunnerStartsRefusesWhenTheCheckNamedNoPath(t *testing.T) {
	err := runnerStarts(t.Context(), "")
	if err == nil {
		t.Fatal("an empty path was treated as a runner that starts")
	}
	// Named, rather than reaching node with a relative path and coming back
	// with whatever node says about a file called src/main.ts.
	if !strings.Contains(err.Error(), "named no path") {
		t.Errorf("the refusal reads %q, which does not say the check named nothing", err)
	}
}
