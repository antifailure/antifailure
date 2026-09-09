package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 8, 15, 0, 0, 0, time.UTC)

// fresh is a commit inside the grace, stale is one well past it.
func commitAt(sha string, ago time.Duration) commit {
	var c commit
	c.SHA = sha
	c.Commit.Committer.Date = now.Add(-ago)
	return c
}

func runFor(sha, status, conclusion string) run {
	return run{
		ID:         1,
		Path:       ciWorkflow,
		HeadSHA:    sha,
		Status:     status,
		Conclusion: conclusion,
		CreatedAt:  now.Add(-3 * time.Hour),
		Event:      "push",
		HeadBranch: "main",
	}
}

// THE STATE TABLE THIS GATE EXISTS FOR. Every conclusion GitHub documents for a
// workflow run appears here, plus the two ways of having no conclusion, and the
// split is the whole design: `success` and `failure` are verdicts and pass,
// because a red main is already the loudest thing in the repository. Everything
// else is the absence of a verdict and refuses, because an absence renders in a
// list exactly as a pass does.
//
// The positive controls are the first two rows and they are load bearing: a
// command that refused everything would satisfy every other row in this file
// and would be a watchdog nobody could ever leave switched on.
func TestEveryStateGitHubCanReport(t *testing.T) {
	cases := []struct {
		status     string
		conclusion string
		want       answer
	}{
		{"completed", "success", pass},
		{"completed", "failure", pass},

		{"completed", "cancelled", refuse},
		{"completed", "timed_out", refuse},
		{"completed", "startup_failure", refuse},
		{"completed", "stale", refuse},
		{"completed", "action_required", refuse},
		{"completed", "neutral", refuse},
		{"completed", "skipped", refuse},
		{"completed", "", refuse},
		{"completed", "something_github_has_not_invented_yet", refuse},
	}
	for _, c := range cases {
		name := c.status + "/" + c.conclusion
		t.Run(name, func(t *testing.T) {
			commits := []commit{commitAt("aaaaaaaa", 4*time.Hour)}
			got, findings, why := decide(commits, []run{runFor("aaaaaaaa", c.status, c.conclusion)},
				ciWorkflow, defaultGrace, now, false)
			if got != c.want {
				t.Fatalf("conclusion %q: got %s, want %s (%s)", c.conclusion, got, c.want, why)
			}
			if findings[0].Why == "" {
				t.Error("the answer came with no reason, so nobody reading the log learns anything")
			}
		})
	}
}

// THE CASE THAT PRODUCED THIS COMMAND. Two of four commits merged inside four
// minutes on 2026-09-08 were cancelled before a job started. This is that
// branch, in the API's own words, and the command must refuse it and must name
// the two commits rather than only the count.
func TestTheNightOfTheFourMerges(t *testing.T) {
	commits := []commit{
		commitAt("822584fa", 100*time.Minute),
		commitAt("f5e2ef68", 101*time.Minute),
		commitAt("39a771ff", 102*time.Minute),
		commitAt("09078be0", 105*time.Minute),
	}
	runs := []run{
		runFor("822584fa", "completed", "success"),
		runFor("f5e2ef68", "completed", "cancelled"),
		runFor("39a771ff", "completed", "cancelled"),
		runFor("09078be0", "completed", "success"),
	}
	got, findings, why := decide(commits, runs, ciWorkflow, defaultGrace, now, false)
	if got != refuse {
		t.Fatalf("the night two commits lost their verdict answered %s, not refuse: %s", got, why)
	}
	var out bytes.Buffer
	report(&out, "antifailure/antifailure", "main", got, findings, why)
	for _, sha := range []string{"f5e2ef68", "39a771ff"} {
		if !strings.Contains(out.String(), sha) {
			t.Errorf("the report refuses without naming %s, so nobody knows which commit to re-run", sha)
		}
	}
	if !strings.Contains(out.String(), "2 of 4") {
		t.Errorf("the summary does not count the commits it refused:\n%s", out.String())
	}
}

// THE NEGATIVE CONTROL, and it is the half that makes the other half mean
// something. The same four commits with the verdicts they should have had must
// pass, so a refusal is evidence about the branch rather than about the command.
func TestTheSameFourCommitsWithVerdictsPass(t *testing.T) {
	commits := []commit{
		commitAt("822584fa", 100*time.Minute),
		commitAt("f5e2ef68", 101*time.Minute),
		commitAt("39a771ff", 102*time.Minute),
		commitAt("09078be0", 105*time.Minute),
	}
	runs := []run{
		runFor("822584fa", "completed", "success"),
		runFor("f5e2ef68", "completed", "success"),
		runFor("39a771ff", "completed", "failure"),
		runFor("09078be0", "completed", "success"),
	}
	got, _, why := decide(commits, runs, ciWorkflow, defaultGrace, now, false)
	if got != pass {
		t.Fatalf("a branch where every commit was judged answered %s: %s", got, why)
	}
}

// A commit still running inside the grace is not a hole, and the exit code must
// not move for it. A watchdog that reds because CI is merely slow is one people
// switch off, and this repository's main run takes about an hour under load.
func TestARunningCommitInsideTheGraceIsNotAHole(t *testing.T) {
	commits := []commit{commitAt("aaaaaaaa", 10*time.Minute)}
	got, findings, why := decide(commits, []run{runFor("aaaaaaaa", "in_progress", "")},
		ciWorkflow, defaultGrace, now, false)
	if got != pass {
		t.Fatalf("a ten minute old commit still running answered %s: %s", got, why)
	}
	if findings[0].Answer != waiting {
		t.Fatalf("the commit itself should be waiting, got %s", findings[0].Answer)
	}
}

// Past the grace it IS a hole. A run that has been queued for three hours has
// reached no verdict and is not going to.
func TestARunningCommitPastTheGraceIsAHole(t *testing.T) {
	commits := []commit{commitAt("aaaaaaaa", 4*time.Hour)}
	got, _, why := decide(commits, []run{runFor("aaaaaaaa", "queued", "")},
		ciWorkflow, defaultGrace, now, false)
	if got != refuse {
		t.Fatalf("a four hour old commit still queued answered %s: %s", got, why)
	}
}

// A commit with no run at all, past the grace, is the loudest version of the
// same hole: nothing ever checked it.
func TestACommitWithNoRunAtAllPastTheGraceIsAHole(t *testing.T) {
	commits := []commit{commitAt("aaaaaaaa", 4*time.Hour)}
	got, _, why := decide(commits, nil, ciWorkflow, defaultGrace, now, false)
	if got != refuse {
		t.Fatalf("a four hour old commit with no run answered %s: %s", got, why)
	}
}

// COULD NOT LOOK IS A DISTINCT ANSWER AND NOT A PASS. When the run page is
// truncated and does not reach back as far as the commit, the run may simply be
// on the next page. Reporting a hole would be inventing one; reporting a pass
// would be inventing a verdict.
func TestACommitPastTheEdgeOfThePageIsUnreadRatherThanClean(t *testing.T) {
	commits := []commit{
		commitAt("aaaaaaaa", 1*time.Hour),
		commitAt("bbbbbbbb", 9*time.Hour),
	}
	runs := []run{runFor("aaaaaaaa", "completed", "success")}
	got, findings, why := decide(commits, runs, ciWorkflow, defaultGrace, now, true)
	if got != couldNotLook {
		t.Fatalf("a commit past the edge of the page answered %s, which claims to know: %s", got, why)
	}
	if findings[1].Answer != couldNotLook {
		t.Fatalf("the unreachable commit answered %s", findings[1].Answer)
	}
	if strings.Contains(why, "carry no CI verdict") {
		t.Error("could not look is being reported as a refusal, which sends somebody to re-run a run that may be fine")
	}
}

// The same branch with an UNtruncated page is a real refusal, not an unread
// one: the page reached the end of the history, so a missing run is missing.
func TestAnUntruncatedPageTurnsTheSameGapIntoARefusal(t *testing.T) {
	commits := []commit{
		commitAt("aaaaaaaa", 1*time.Hour),
		commitAt("bbbbbbbb", 9*time.Hour),
	}
	runs := []run{runFor("aaaaaaaa", "completed", "success")}
	got, _, why := decide(commits, runs, ciWorkflow, defaultGrace, now, false)
	if got != refuse {
		t.Fatalf("a complete page with a commit missing its run answered %s: %s", got, why)
	}
}

// An empty commit list is the shape that would otherwise let this pass on
// having read nothing, which is how a check starts reporting ok about a
// question it never asked.
func TestNoCommitsIsNotACleanBranch(t *testing.T) {
	got, _, why := decide(nil, nil, ciWorkflow, defaultGrace, now, false)
	if got != couldNotLook {
		t.Fatalf("an empty branch answered %s, which is a pass on having read nothing: %s", got, why)
	}
}

// A run for another workflow on the same commit is not this workflow's verdict.
// Without the path filter, security.yml passing would read as CI passing.
func TestAnotherWorkflowsRunIsNotCIsVerdict(t *testing.T) {
	commits := []commit{commitAt("aaaaaaaa", 4*time.Hour)}
	other := runFor("aaaaaaaa", "completed", "success")
	other.Path = ".github/workflows/security.yml"
	got, _, why := decide(commits, []run{other}, ciWorkflow, defaultGrace, now, false)
	if got != refuse {
		t.Fatalf("another workflow's success answered %s for CI: %s", got, why)
	}
}

// A re-run is the current answer about a commit. The newest run wins, and the
// order is imposed here rather than trusted from the response.
func TestTheNewestRunOnACommitIsTheAnswer(t *testing.T) {
	commits := []commit{commitAt("aaaaaaaa", 4*time.Hour)}
	old := runFor("aaaaaaaa", "completed", "cancelled")
	old.ID, old.CreatedAt = 1, now.Add(-5*time.Hour)
	newer := runFor("aaaaaaaa", "completed", "success")
	newer.ID, newer.CreatedAt = 2, now.Add(-1*time.Hour)
	// Given oldest first on purpose, so a command that trusted the response's
	// order would read the cancelled one.
	got, _, why := decide(commits, []run{old, newer}, ciWorkflow, defaultGrace, now, false)
	if got != pass {
		t.Fatalf("a commit re-run to green answered %s: %s", got, why)
	}
}

// Every conclusion this command knows must carry a sentence. A refusal with no
// reason sends somebody to the wrong file at three in the morning.
func TestEveryKnownConclusionCarriesAReason(t *testing.T) {
	for conclusion, why := range noVerdict {
		if strings.TrimSpace(why) == "" {
			t.Errorf("conclusion %q refuses with no reason", conclusion)
		}
	}
	for conclusion, why := range verdicts {
		if strings.TrimSpace(why) == "" {
			t.Errorf("conclusion %q passes with no reason", conclusion)
		}
	}
	for conclusion := range verdicts {
		if _, both := noVerdict[conclusion]; both {
			t.Errorf("conclusion %q is both a verdict and not one", conclusion)
		}
	}
}

// THE REQUEST, NOT ONLY THE DECISION. Every test above answers from a slice
// this file wrote, so all of them stayed green while the command asked the
// wrong endpoint: the repository wide `/actions/runs` interleaves nine
// workflows on a push to main, so one page of a hundred carried thirteen CI
// runs and reached back seven hours, and the default window of twenty commits
// fell off the edge of it and answered `could not look` every time. The
// decision was right and the question was wrong, which is the shape
// `tools/prmerge` was caught in with twenty five green tests behind it.
func TestTheRunsRequestAsksTheWorkflowsOwnEndpoint(t *testing.T) {
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.RequestURI())
		fmt.Fprint(w, `{"total_count":0,"workflow_runs":[]}`)
	}))
	defer server.Close()

	c := &api{base: server.URL, repo: "antifailure/antifailure", token: "t", http: server.Client()}
	if _, _, err := c.runs("main", ciWorkflow); err != nil {
		t.Fatalf("reading runs: %v", err)
	}
	got := asked[0]
	for _, want := range []string{
		"/actions/workflows/ci.yml/runs",
		"branch=main",
		"event=push",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the runs request does not carry %q, so it is not asking for this\n"+
				"workflow's runs on this branch: %s", want, got)
		}
	}
	if strings.Contains(got, "/actions/runs?") {
		t.Errorf("the runs request asks the repository wide endpoint, which interleaves every "+
			"workflow and reaches back a fraction as far: %s", got)
	}
}

// The commit window starts where it was told to. Without this, `-from` could be
// accepted, ignored, and the positive control would silently be the same
// negative one.
func TestTheCommitsRequestStartsAtTheCommitItWasGiven(t *testing.T) {
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.RequestURI())
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()

	c := &api{base: server.URL, repo: "antifailure/antifailure", token: "t", http: server.Client()}
	if _, err := c.commits("09078be0", 4); err != nil {
		t.Fatalf("reading commits: %v", err)
	}
	// per_page is the window widened for the first parent walk, not the window.
	for _, want := range []string{"sha=09078be0", "per_page=16"} {
		if !strings.Contains(asked[0], want) {
			t.Errorf("the commits request does not carry %q: %s", want, asked[0])
		}
	}
}

// A refusal must say what a cancelled run on main actually means, because the
// sentence is the whole of what somebody gets at three in the morning. GitHub
// spells several unrelated things `cancelled`, and the one this repository has
// met is a run cancelled while merely PENDING.
func TestTheCancelledRefusalNamesThePendingCause(t *testing.T) {
	commits := []commit{commitAt("39a771ff", 4*time.Hour)}
	_, findings, _ := decide(commits, []run{runFor("39a771ff", "completed", "cancelled")},
		ciWorkflow, defaultGrace, now, false)
	if !strings.Contains(findings[0].Why, "PENDING") {
		t.Errorf("a cancelled run refuses without naming the cause this repository has actually "+
			"met, so the reader goes looking for a person who pressed a button: %q", findings[0].Why)
	}
}

// A MERGE COMMIT ON MAIN MUST NOT DRAG THE MERGED BRANCH INTO THE WINDOW.
// `/commits?sha=main` is `git log`, not `git log --first-parent`, so it returns
// every commit reachable from the branch in date order. The branch's own
// commits were never pushed to main, so no push run on main ever judged them,
// and without this walk each one is reported as a commit nothing ever checked.
// Every one of those refusals is false, and a watchdog that cries wolf gets
// muted, which puts the real hole back in the dark.
//
// This is not hypothetical here: main's first 200 reachable commits already
// differ from its first 200 first parents.
func TestAMergedBranchesCommitsAreNotOnMainsLine(t *testing.T) {
	merge := commitAt("mmmmmmmm", 1*time.Hour)
	merge.Parents = []struct {
		SHA string `json:"sha"`
	}{{SHA: "pppppppp"}, {SHA: "bbbbbbbb"}}

	onMain := commitAt("pppppppp", 3*time.Hour)
	onMain.Parents = []struct {
		SHA string `json:"sha"`
	}{{SHA: "oooooooo"}}

	// The merged branch's own commit. Newer than `pppppppp`, so a date ordered
	// list puts it between the two and a walk that trusted the order would take
	// it. Nothing on main ever ran CI on it.
	onBranch := commitAt("bbbbbbbb", 2*time.Hour)

	older := commitAt("oooooooo", 4*time.Hour)

	fetched := []commit{merge, onBranch, onMain, older}
	got := firstParents(fetched, 3)

	var shas []string
	for _, c := range got {
		shas = append(shas, c.SHA)
	}
	want := []string{"mmmmmmmm", "pppppppp", "oooooooo"}
	if len(shas) != len(want) {
		t.Fatalf("the first parent walk returned %v, want %v", shas, want)
	}
	for i := range want {
		if shas[i] != want[i] {
			t.Fatalf("the first parent walk returned %v, want %v", shas, want)
		}
	}
	for _, s := range shas {
		if s == "bbbbbbbb" {
			t.Error("a merged branch's own commit is in the window, so this would refuse main " +
				"over a commit that was never pushed to it")
		}
	}
}

// The walk stops at the edge of what was fetched rather than guessing. Judging
// fewer commits than asked for is a smaller lie than judging a commit that is
// not on this branch's line.
func TestTheWalkStopsAtTheEdgeOfWhatWasFetched(t *testing.T) {
	head := commitAt("aaaaaaaa", 1*time.Hour)
	head.Parents = []struct {
		SHA string `json:"sha"`
	}{{SHA: "not_in_this_page"}}

	got := firstParents([]commit{head}, 20)
	if len(got) != 1 {
		t.Fatalf("the walk returned %d commits from a page holding one reachable commit", len(got))
	}
}

// The whole window is judged when the line is linear, which is what main looks
// like today. Without this the test above is satisfied by a walk that returns
// one commit and stops.
func TestALinearBranchYieldsTheWholeWindow(t *testing.T) {
	link := func(sha, parent string, ago time.Duration) commit {
		c := commitAt(sha, ago)
		c.Parents = []struct {
			SHA string `json:"sha"`
		}{{SHA: parent}}
		return c
	}
	fetched := []commit{
		link("aaaaaaaa", "bbbbbbbb", 1*time.Hour),
		link("bbbbbbbb", "cccccccc", 2*time.Hour),
		link("cccccccc", "dddddddd", 3*time.Hour),
		link("dddddddd", "eeeeeeee", 4*time.Hour),
	}
	if got := firstParents(fetched, 4); len(got) != 4 {
		t.Fatalf("a linear branch of four yielded %d commits", len(got))
	}
}

// The commits request asks for more than the window, because the list is date
// ordered and the walk then reduces it. Asking for exactly the window would
// leave the walk short by however many off line commits the response carried.
func TestTheCommitsRequestFetchesWiderThanTheWindow(t *testing.T) {
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.RequestURI())
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()

	c := &api{base: server.URL, repo: "antifailure/antifailure", token: "t", http: server.Client()}
	if _, err := c.commits("main", 20); err != nil {
		t.Fatalf("reading commits: %v", err)
	}
	if strings.Contains(asked[0], "per_page=20") {
		t.Errorf("the commits request asks for exactly the window, so a merge commit in the "+
			"response leaves the first parent walk short: %s", asked[0])
	}
	if !strings.Contains(asked[0], "per_page=80") {
		t.Errorf("the commits request does not fetch wider than the window: %s", asked[0])
	}
}
