// Command mainverdict refuses a main branch carrying commits CI never judged.
//
// WHY THIS EXISTS. On 2026-09-08 four pull requests merged inside four minutes
// and two of the four commits ended with no CI verdict at all. `822584fa` and
// `09078be0` ran. `39a771ff` and `f5e2ef68` were cancelled before a single job
// started, and the API confirms it: both runs carry an empty job list and both
// were cancelled within one second of the NEXT run being created.
//
//	09078be0  created 13:09:53  jobs started 13:12:57  in_progress
//	39a771ff  created 13:13:15  no jobs             cancelled 13:13:51
//	f5e2ef68  created 13:13:50  no jobs             cancelled 13:14:23
//	822584fa  created 13:14:22  no jobs yet         pending
//
// `cancel-in-progress: false` protects a run that has STARTED and nothing else.
// GitHub holds exactly one pending run per concurrency group, so each merge
// cancelled the one queued behind the in progress run. ci.yml now keys its
// group on the commit for a push to main, so no main run can supersede another
// and this shape cannot recur from the concurrency group. It can still happen
// three other ways: somebody cancels a run by hand, a job hits its own
// timeout-minutes, and the concurrency expression gets edited back.
//
// WHAT IT REFUSES, AND WHAT IT DELIBERATELY DOES NOT. This asks one question:
// does every recent commit on main carry a COMPLETED CI verdict? A commit whose
// run concluded `failure` HAS one, and this says so and passes it, because a red
// main is already the loudest thing in the repository and a second instrument
// shouting about it teaches people to silence both. The gap this closes is the
// silent one: `cancelled` and `skipped` render in a list as an absence of red,
// which is what a pass renders as too.
//
// THE THREE ANSWERS, and the exit code each leaves.
//
//	0  pass            every commit in the window carries a verdict
//	1  refuse          at least one does not, and it is named
//	2  could not look  the run list does not reach as far back as the window
//
// The third is not a pass wearing a different word. This reads the branch's
// runs in one page and joins them to the commit list; when the page does not
// reach the oldest commit asked about, the commits past its edge are unjudged
// by this command rather than clean, and reporting them clean would be the
// exact defect this repository keeps finding in its own instruments. Widen the
// page or narrow the window, and it will answer.
//
// WHY IT READS PUSH RUNS ON THE BRANCH. A squash merge creates a commit that
// existed nowhere before, so the pull request's own runs are about a different
// sha and cannot stand in for it. The run that judges a main commit is the push
// run on main, and this filters to exactly that: event `push`, branch `main`,
// workflow path `.github/workflows/ci.yml`. Filtering by the workflow's `name:`
// instead would follow a string anybody can change in the pull request that
// changes what CI does; the path is the field that does not move.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

// The workflow whose conclusion decides this, named by the file that produced
// the run. Same constant and same reasoning as tools/cigate.
const ciWorkflow = ".github/workflows/ci.yml"

const (
	defaultWindow = 20
	// Sized against how long CI actually takes on this repository rather than
	// against how long it ought to. The engine job alone runs about fourteen
	// minutes, twelve jobs queue behind every other branch in flight, and a
	// main run measured on 2026-09-08 took fifty five minutes end to end. A
	// grace shorter than the slowest real run turns a busy night into a false
	// alarm, and a watchdog that cries on a busy night is one people learn to
	// ignore.
	defaultGrace = 90 * time.Minute
	// One page. Twenty commits of window against a hundred runs of history is
	// four runs per commit before the page can fail to reach, and a commit
	// carries one push run plus its re-runs, which keep the same object rather
	// than adding one. When it does not reach, the answer is `could not look`
	// rather than a quiet pass.
	runsPerPage = 100
	// The commit list is fetched wider than the window and then walked down the
	// first parents, so this is the ceiling on that widening rather than the
	// number of commits judged.
	commitsPerPage = 100
)

// commit is the part of a commit this reasons about.
//
// Parents is here for one reason, and it is a correctness one rather than a
// completeness one. See firstParents.
type commit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Committer struct {
			Date time.Time `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
	Parents []struct {
		SHA string `json:"sha"`
	} `json:"parents"`
}

// firstParents reduces a commit list to the branch's own line of descent,
// newest first, and stops at `window` or at the edge of what was fetched.
//
// WHY THIS IS NOT TIDYING. `/repos/{repo}/commits?sha=main` is `git log`, not
// `git log --first-parent`: it returns every commit REACHABLE from the branch,
// in date order, so a real merge commit on main drags the whole merged
// branch's history into the window. Those commits were never pushed to main,
// so no push run on main ever judged them, and this command would report each
// one as a commit nothing ever checked. Every one of those refusals would be
// false, and a watchdog that cries wolf is the one thing worse here than no
// watchdog: it gets muted, and then the real hole is silent again.
//
// This repository squash merges through `just merge`, so main's top is linear
// today and this changes nothing about it. It is not linear further down: the
// first 200 commits reachable from main differ from its first 200 first
// parents, so the shape this guards against is already in this history rather
// than hypothetical.
func firstParents(commits []commit, window int) []commit {
	if len(commits) == 0 {
		return nil
	}
	bySHA := make(map[string]commit, len(commits))
	for _, c := range commits {
		bySHA[c.SHA] = c
	}
	out := make([]commit, 0, window)
	// The API returns the branch head first, and that is the only commit this
	// takes on trust from the ordering. Everything after it is reached by
	// following the first parent, which is a link in the data rather than a
	// position in a list.
	cur, ok := bySHA[commits[0].SHA]
	for ok && len(out) < window {
		out = append(out, cur)
		if len(cur.Parents) == 0 {
			break
		}
		// Past the edge of what was fetched, this stops rather than guessing.
		// Judging fewer commits than asked for is a smaller lie than judging a
		// commit that is not on this branch's line at all.
		cur, ok = bySHA[cur.Parents[0].SHA]
	}
	return out
}

// run is the part of a workflow run this reasons about.
type run struct {
	ID         int64     `json:"id"`
	Path       string    `json:"path"`
	HeadSHA    string    `json:"head_sha"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	HTMLURL    string    `json:"html_url"`
	CreatedAt  time.Time `json:"created_at"`
	Event      string    `json:"event"`
	HeadBranch string    `json:"head_branch"`
}

type runList struct {
	// TotalCount is what says whether the page reached the end of the history
	// or stopped at the edge of one page.
	TotalCount int   `json:"total_count"`
	Runs       []run `json:"workflow_runs"`
}

// answer is what this command knows about one commit, and about the branch.
type answer int

const (
	pass answer = iota
	waiting
	refuse
	couldNotLook
)

func (a answer) String() string {
	switch a {
	case pass:
		return "pass"
	case waiting:
		return "waiting"
	case refuse:
		return "refuse"
	default:
		return "could not look"
	}
}

// finding is one commit and what CI said about it.
type finding struct {
	SHA    string
	Answer answer
	Why    string
	URL    string
}

// verdicts are the conclusions that ARE an answer about the commit. Both of
// them: a red commit was judged, and the judgement was no.
//
// Every other conclusion GitHub can report is the absence of a judgement, and
// listing the two rather than the eight is deliberate. A conclusion GitHub has
// not invented yet arrives here as a word this map does not carry and is
// treated as no verdict, which is the safe direction: the alternative is a new
// word being read as a pass by a command that has never seen it.
var verdicts = map[string]string{
	"success": "CI passed",
	"failure": "CI failed, which is a verdict and is not this command's business",
}

// noVerdict maps each conclusion that leaves a commit unjudged to the sentence
// that points at its actual cause.
var noVerdict = map[string]string{
	"cancelled": "CI was cancelled, so this commit was never judged. On this repository " +
		"that has meant a run cancelled while merely PENDING, because GitHub holds one " +
		"pending run per concurrency group and the next merge takes its place. It also " +
		"means a run somebody stopped by hand.",
	"timed_out":       "CI ran out of time on this commit and reached no conclusion about it.",
	"startup_failure": "CI never started on this commit, so nothing was checked.",
	"stale":           "CI on this commit is stale: GitHub discarded the result without a verdict.",
	"action_required": "CI on this commit is waiting on a human and has not passed.",
	"neutral":         "CI on this commit concluded neutral, which is not a verdict.",
	"skipped": "CI on this commit was skipped. A skipped run and a passing run look the same " +
		"in a list and mean opposite things: nothing ran, so nothing was checked.",
	"": "CI reports this commit as completed with no conclusion at all.",
}

// decide is the whole of the judgement, kept away from the network so a test
// can hand it a branch in any state without arranging a repository.
//
// It returns the overall answer and a finding per commit, newest first, in the
// order the commits were given.
func decide(commits []commit, runs []run, workflow string, grace time.Duration, now time.Time, truncated bool) (answer, []finding, string) {
	if len(commits) == 0 {
		return couldNotLook, nil, "no commits came back for this branch, so there was nothing to judge. " +
			"That is not a clean branch, it is an unread one."
	}

	// Newest first per commit, and sorted here rather than trusted from the
	// response. The endpoint does return runs newest first today, and a check
	// whose correctness rests on an ordering nobody promised in writing is a
	// check that breaks on the day the answer matters. Same reasoning as
	// tools/cigate, and the same shape.
	byCommit := map[string][]run{}
	oldestRun := time.Time{}
	for _, r := range runs {
		if r.Path != workflow {
			continue
		}
		byCommit[r.HeadSHA] = append(byCommit[r.HeadSHA], r)
		if oldestRun.IsZero() || r.CreatedAt.Before(oldestRun) {
			oldestRun = r.CreatedAt
		}
	}
	for sha := range byCommit {
		rs := byCommit[sha]
		sort.SliceStable(rs, func(i, j int) bool {
			if !rs[i].CreatedAt.Equal(rs[j].CreatedAt) {
				return rs[i].CreatedAt.After(rs[j].CreatedAt)
			}
			return rs[i].ID > rs[j].ID
		})
	}

	// THE EDGE OF THE PAGE, and the reason this command has a third answer.
	// A commit older than the oldest run on the page is a commit whose run may
	// simply be on the next page. Reporting it as having no run would be this
	// command inventing a hole; reporting it as fine would be it inventing a
	// verdict. Neither is available, so it says it could not look.
	unreachable := func(c commit) bool {
		if !truncated || oldestRun.IsZero() {
			return false
		}
		return c.Commit.Committer.Date.Before(oldestRun)
	}

	var findings []finding
	worst := pass
	for _, c := range commits {
		f := finding{SHA: c.SHA}
		rs := byCommit[c.SHA]
		age := now.Sub(c.Commit.Committer.Date)
		switch {
		case len(rs) == 0 && unreachable(c):
			f.Answer, f.Why = couldNotLook, fmt.Sprintf(
				"no run for this commit on the page that was read, and the page does not "+
					"reach back this far. Ask for more runs or a shorter window.")
		case len(rs) == 0 && age < grace:
			f.Answer, f.Why = waiting, fmt.Sprintf(
				"no CI run yet, and this commit is only %s old, which is inside the %s grace.",
				short(age), short(grace))
		case len(rs) == 0:
			f.Answer, f.Why = refuse, fmt.Sprintf(
				"no CI run on this commit at all, %s after it landed. A commit on this branch "+
					"with no run is a commit nothing ever checked.", short(age))
		default:
			latest := rs[0]
			f.URL = latest.HTMLURL
			switch {
			case latest.Status != "completed" && age < grace:
				f.Answer, f.Why = waiting, fmt.Sprintf("CI is %s, %s after the commit landed.",
					statusWords(latest.Status), short(age))
			case latest.Status != "completed":
				f.Answer, f.Why = refuse, fmt.Sprintf(
					"CI is still %s %s after the commit landed, past the %s grace. It has "+
						"reached no verdict and at this age it is not going to.",
					statusWords(latest.Status), short(age), short(grace))
			default:
				if why, isVerdict := verdicts[latest.Conclusion]; isVerdict {
					f.Answer, f.Why = pass, why+"."
				} else if why, known := noVerdict[latest.Conclusion]; known {
					f.Answer, f.Why = refuse, why
				} else {
					f.Answer, f.Why = refuse, fmt.Sprintf(
						"CI concluded %q on this commit, which is not a word this command "+
							"recognises, and an unfamiliar answer is not a verdict.", latest.Conclusion)
				}
			}
		}
		findings = append(findings, f)
		if rank(f.Answer) > rank(worst) {
			worst = f.Answer
		}
	}

	return worst, findings, summary(worst, findings)
}

// rank orders the answers by how badly this command wants to stop. `waiting` is
// below `pass` on purpose: a branch whose newest commit is still running is not
// worse than a clean one, it is a clean one with the newest answer outstanding,
// and the overall exit code must not move for it.
func rank(a answer) int {
	switch a {
	case refuse:
		return 3
	case couldNotLook:
		return 2
	case pass:
		return 1
	default:
		return 0
	}
}

func summary(worst answer, findings []finding) string {
	var refused, unread, held int
	for _, f := range findings {
		switch f.Answer {
		case refuse:
			refused++
		case couldNotLook:
			unread++
		case waiting:
			held++
		}
	}
	switch worst {
	case refuse:
		return fmt.Sprintf("%d of %d commits on this branch carry no CI verdict. A commit with "+
			"no verdict cannot be released from, was never deployed, and makes a red on a later "+
			"commit ambiguous across every commit behind it.", refused, len(findings))
	case couldNotLook:
		return fmt.Sprintf("%d of %d commits could not be judged, because the run history read "+
			"does not reach them. This is not a pass.", unread, len(findings))
	default:
		if held > 0 {
			return fmt.Sprintf("every commit on this branch carries a CI verdict, or is new "+
				"enough to still be earning one (%d of %d).", held, len(findings))
		}
		return fmt.Sprintf("every one of the %d commits read on this branch carries a CI verdict.", len(findings))
	}
}

// statusWords turns the API's own status into something readable without
// pretending to know statuses it does not.
func statusWords(status string) string {
	switch status {
	case "queued", "pending", "waiting", "requested":
		return status + " and has not started"
	case "in_progress":
		return "still running"
	default:
		return status
	}
}

// short prints a duration in the units a person reads at three in the morning.
func short(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

type api struct {
	base  string
	repo  string
	token string
	http  *http.Client
}

func (a *api) get(path string, into any) error {
	req, err := http.NewRequest(http.MethodGet, a.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the API answered %d for %s: %s", resp.StatusCode, path, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, into)
}

// commits reads the window, starting at `from`. That is the branch in every
// unattended run and a commit when somebody is pointing this at a stretch of
// history on purpose, which is the only way to express a POSITIVE control
// against a branch that currently carries a hole: a window from the head always
// includes the hole, so without this the command could only ever be watched
// refusing, and a gate that has never been seen to pass is not a gate anybody
// should trust.
func (a *api) commits(from string, window int) ([]commit, error) {
	// More than the window, because the list is date ordered and firstParents
	// then walks it. A merge commit on the line puts commits in this response
	// that are not on it, so asking for exactly `window` would leave the walk
	// short by however many of them there were.
	fetch := window * 4
	if fetch > commitsPerPage {
		fetch = commitsPerPage
	}
	var out []commit
	err := a.get(fmt.Sprintf("/repos/%s/commits?sha=%s&per_page=%d", a.repo, from, fetch), &out)
	return out, err
}

// runs reads one page of this workflow's push runs on the branch, newest first,
// and reports whether the history is longer than the page.
//
// ASKING THE WORKFLOW'S OWN ENDPOINT IS NOT A TIDYING UP, it is the difference
// between reading twenty commits of history and reading eight. The repository
// wide `/actions/runs` returns every workflow's runs interleaved, and this
// repository runs nine of them on a push to main: one page of a hundred carried
// thirteen CI runs and reached back seven hours. The same page from
// `/actions/workflows/ci.yml/runs` carries a hundred CI runs and reaches back
// three days. Against the wide endpoint the default window of twenty commits
// would have fallen off the edge of the page and answered `could not look`
// every time, which is honest and useless.
//
// The endpoint takes the workflow's FILE NAME, and the path is filtered again
// in decide. Two checks of the same thing on purpose: the file name is what the
// route accepts, the path is the field on the run, and if either ever names
// something else the mismatch is an empty result rather than another
// workflow's verdict standing in for CI's.
func (a *api) runs(branch, workflow string) ([]run, bool, error) {
	var list runList
	err := a.get(fmt.Sprintf("/repos/%s/actions/workflows/%s/runs?branch=%s&event=push&per_page=%d",
		a.repo, path.Base(workflow), branch, runsPerPage), &list)
	if err != nil {
		return nil, false, err
	}
	return list.Runs, list.TotalCount > len(list.Runs), nil
}

func main() {
	repo := flag.String("repo", os.Getenv("GITHUB_REPOSITORY"), "owner/name")
	branch := flag.String("branch", "main", "the branch whose commits must each carry a verdict")
	from := flag.String("from", "", "the commit to start the window at, defaulting to the branch head")
	workflow := flag.String("workflow", ciWorkflow, "the workflow whose conclusion decides this")
	window := flag.Int("window", defaultWindow, "how many commits back to read")
	grace := flag.Duration("grace", defaultGrace, "how long a commit may go without a verdict before that is a refusal")
	base := flag.String("api", "https://api.github.com", "the API root")
	flag.Parse()

	if *repo == "" {
		fail("mainverdict needs a repository. Pass -repo or set GITHUB_REPOSITORY.")
	}
	if *window < 1 {
		fail("a window of %d commits asks about nothing at all.", *window)
	}

	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		// Refused rather than attempted, for the reason tools/cigate refuses:
		// unauthenticated the API hides runs and rate limits hard, so the likely
		// result is an empty list, which would read here as a branch of commits
		// nothing ever ran. That is a false alarm, and a watchdog that cries
		// wolf about its own token is one people turn off.
		fail("mainverdict has no token. Set GH_TOKEN or GITHUB_TOKEN to a token that can " +
			"read actions on this repository, which in a workflow means permissions: actions: read.")
	}

	c := &api{base: *base, repo: *repo, token: token, http: &http.Client{Timeout: 30 * time.Second}}

	start := *from
	if start == "" {
		start = *branch
	}
	fetched, err := c.commits(start, *window)
	if err != nil {
		fail("reading the commits at %s: %v", start, err)
	}
	commits := firstParents(fetched, *window)
	runs, truncated, err := c.runs(*branch, *workflow)
	if err != nil {
		fail("reading the runs on %s: %v", *branch, err)
	}

	worst, findings, why := decide(commits, runs, *workflow, *grace, time.Now().UTC(), truncated)
	report(os.Stdout, *repo, *branch, worst, findings, why)

	switch worst {
	case refuse:
		fmt.Fprintf(os.Stderr, "::error title=A commit on %s carries no CI verdict::%s\n", *branch, why)
		summarise(*branch, worst, findings, why)
		os.Exit(1)
	case couldNotLook:
		fmt.Fprintf(os.Stderr, "::error title=mainverdict could not read far enough to answer::%s\n", why)
		summarise(*branch, worst, findings, why)
		os.Exit(2)
	}
	summarise(*branch, worst, findings, why)
}

func report(w io.Writer, repo, branch string, worst answer, findings []finding, why string) {
	_, _ = fmt.Fprintf(w, "mainverdict: %s on %s, %d commits read\n\n", repo, branch, len(findings))
	for _, f := range findings {
		mark := map[answer]string{pass: "ok    ", waiting: "wait  ", refuse: "NO    ", couldNotLook: "UNREAD"}[f.Answer]
		_, _ = fmt.Fprintf(w, "  %s %s  %s", mark, f.SHA[:min(8, len(f.SHA))], f.Why)
		if f.URL != "" {
			_, _ = fmt.Fprintf(w, " %s", f.URL)
		}
		_, _ = fmt.Fprintln(w)
	}
	_, _ = fmt.Fprintf(w, "\nmainverdict: %s. %s\n", worst, why)
}

// summarise writes to the job summary when there is one. Best effort on
// purpose: the verdict is the exit code, and a summary that could not be
// written must never change it.
func summarise(branch string, worst answer, findings []finding, why string) {
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "### `%s`: %s\n\n%s\n\n", branch, worst, why)
	for _, x := range findings {
		if x.Answer == pass {
			continue
		}
		_, _ = fmt.Fprintf(f, "- `%s` %s %s\n", x.SHA[:min(8, len(x.SHA))], x.Why, x.URL)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "mainverdict: "+format+"\n", args...)
	os.Exit(2)
}
