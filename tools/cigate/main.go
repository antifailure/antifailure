// Command cigate refuses to publish a release from a commit CI has not passed.
//
// WHY THIS EXISTS. release.yml triggers on a `v*` tag and on nothing else, and
// it had no gate of its own. A tag pushed onto a commit whose CI was red built
// four binaries, wrote a checksum file, generated a bill of materials, signed
// both with cosign and published the lot. Every one of those artifacts carries
// the authority of a signature, and the signature says the release workflow in
// this repository produced them, which was true. It says nothing about whether
// the commit worked, and nothing else did either.
//
// WHY IT WAITS FOR CI RATHER THAN CHECKING ANYTHING ITSELF. The same reasoning
// cd.yml's gate is built on, and deliberately not a second opinion. CI already
// ran on this commit and is the promise the repository makes about itself.
// Running the checks again here would double the work and could disagree with
// the first run, and then nobody knows which verdict was real.
//
// WHY A COMMAND RATHER THAN FIFTEEN LINES OF SHELL IN THE WORKFLOW. A gate is
// only worth what its refusal is worth, and shell inside a tag triggered
// workflow can be watched refusing exactly once, on a real red tag, which is
// the rehearsal nobody gets to do twice. Every state below has a test that
// feeds this the API's own words, and the negative controls are the point.
//
// THE THREE ANSWERS, AND THE TWO STATES THAT LOOK LIKE A THIRD.
//
//	completed and success        pass
//	completed and anything else  refuse
//	not completed, or no run     wait, and refuse when the budget runs out
//
// `cancelled` refuses, and that is the part worth writing down, because GitHub
// spells several unrelated things with that one word. A job that hits its own
// `timeout-minutes` is reported cancelled. So is a run somebody stopped by
// hand, and so is a run superseded by a newer push on the same branch. Not one
// of them is a verdict about the commit, and a thing that is not a verdict is
// not a pass.
//
// ci.yml no longer cancels a superseded run on main or on a tag, and the reason
// it was changed is this one: six merges landed inside one run's length on
// 2026-09-02 and each cancelled the one before it, so main went hours with no
// completed run and no commit a release could have been cut from. Superseding
// is still why the word appears on a branch. On main it now means something
// else, and something worth reading before re-running anything.
//
// `skipped` refuses for the same reason and is easier to get wrong, because a
// skipped run and a passing run render as the same absence of red in a list. A
// step behind an earlier failure is skipped rather than failed, so skipped is
// evidence of nothing at all.
//
// Only the literal word `success` passes. Everything else refuses, including a
// conclusion GitHub has not invented yet, which arrives here as a word this
// command does not recognise and is refused for being unrecognised rather than
// waved through for being unfamiliar.
//
// WHY IT DOES NOT WAIT FOR A RUN THE TAG ITSELF TRIGGERS. ci.yml runs on a push
// to main and on a pull request. It does not run on tags. So the run this waits
// for is the one the commit already had when it landed on main, found by
// `head_sha`, and it is normally complete before the tag is pushed at all. This
// command deliberately does not filter by event or by branch, because doing so
// would discard the only run that exists.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// ciWorkflow is the workflow whose conclusion decides this, named by the file
// that produced the run rather than by its `name:`.
//
// Both identify it and only one of them is stable. `name: CI` is a string
// anybody can change in the same pull request that changes what CI does, and
// the run object records the path regardless. If either is renamed no run
// matches, which is a wait and then a refusal, so the direction of that failure
// is the safe one whichever field is read. The path is read because it is the
// one that does not move.
const ciWorkflow = ".github/workflows/ci.yml"

// The polling budget, sized against the gate job's own timeout-minutes: 45,
// exactly as cd.yml sizes its own and for the reason written there. 115
// attempts at 20 seconds is about 38 minutes. The earlier 60 attempts in cd.yml
// was 20 minutes, less than half that timeout, and CI on this repository does
// not finish in 20 minutes when the queue is busy: the engine job alone runs
// about 14 minutes and control plane is similar, and both queue behind every
// other branch in flight. Giving up while CI is still running and calling that
// a failure is a verdict about the queue, not about the commit.
const (
	defaultAttempts = 115
	defaultInterval = 20 * time.Second
)

// run is the part of a workflow run this reasons about. The API returns far
// more and none of the rest changes the answer.
type run struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	HTMLURL    string    `json:"html_url"`
	CreatedAt  time.Time `json:"created_at"`
	RunAttempt int       `json:"run_attempt"`
	Event      string    `json:"event"`
}

type runList struct {
	Runs []run `json:"workflow_runs"`
}

// verdict is what this command knows about a commit. Three values rather than
// two, because "not yet" is a real answer and collapsing it into either of the
// other two is the bug: into pass, and a release publishes ahead of its own
// tests; into refuse, and a slow queue looks like a broken commit.
type verdict int

const (
	wait verdict = iota
	pass
	refuse
)

func (v verdict) String() string {
	switch v {
	case pass:
		return "pass"
	case refuse:
		return "refuse"
	default:
		return "wait"
	}
}

// refusals maps each conclusion GitHub can report to the sentence somebody
// reads at three in the morning when their release did not publish.
//
// Every one of these is a refusal. The map exists to say WHY in words that
// point at the actual cause, not to sort them into two piles: "cancelled" sends
// people looking for a person who pressed a button, and on this repository it
// is almost always the concurrency group.
var refusals = map[string]string{
	"failure": "CI failed on this commit. The release publishes nothing.",
	"cancelled": "CI on this commit was cancelled, so it never reached a verdict. Several " +
		"unrelated things are spelled that way: a job that hit its own timeout-minutes, a run " +
		"somebody stopped by hand, and a run superseded by a newer push on the same branch. " +
		"None of them says the commit is good. ci.yml no longer cancels a superseded run on " +
		"main or on a tag, so a cancelled run here is worth reading rather than assuming. " +
		"Re-run CI on this commit, and re-run this release once it is green.",
	"timed_out":       "CI on this commit ran out of time and never reached a verdict.",
	"startup_failure": "CI on this commit never started, so nothing was checked.",
	"stale":           "CI on this commit is stale: GitHub discarded the result without a verdict.",
	"action_required": "CI on this commit is waiting on a human. It has not passed.",
	"neutral":         "CI on this commit concluded neutral, which is not a pass.",
	"skipped": "CI on this commit was skipped. A skipped run and a passing run look " +
		"the same in a list and mean opposite things: nothing ran, so nothing was checked.",
}

// decide is the whole of the judgement, kept away from the network so that a
// test can hand it the six states without arranging six repositories.
//
// It returns the run it judged so the caller can print a link, and a nil run
// when there was nothing to judge.
func decide(runs []run, workflow string) (verdict, string, *run) {
	matching := make([]run, 0, len(runs))
	for _, r := range runs {
		if r.Path == workflow {
			matching = append(matching, r)
		}
	}
	if len(matching) == 0 {
		return wait, fmt.Sprintf("no %s run for this commit yet", workflow), nil
	}

	// Newest first, and sorted here rather than trusted from the response. The
	// endpoint does return runs newest first today, and a check whose
	// correctness rests on an ordering nobody promised in writing is a check
	// that breaks on a day the answer matters most. A commit can carry more
	// than one run: a pull request run on the branch head and a push run on
	// main are two, and a re-run keeps the same object rather than adding one.
	// The newest is the current answer about the commit.
	sort.SliceStable(matching, func(i, j int) bool {
		if !matching[i].CreatedAt.Equal(matching[j].CreatedAt) {
			return matching[i].CreatedAt.After(matching[j].CreatedAt)
		}
		return matching[i].ID > matching[j].ID
	})
	latest := matching[0]

	if latest.Status != "completed" {
		return wait, fmt.Sprintf("CI is %s", statusWords(latest.Status)), &latest
	}
	if latest.Conclusion == "success" {
		return pass, "CI is green on this commit", &latest
	}
	if why, known := refusals[latest.Conclusion]; known {
		return refuse, why, &latest
	}
	// An unrecognised conclusion. Refusing is the only safe reading: the set of
	// conclusions is GitHub's to extend, and a word this command has never seen
	// is a word it cannot argue is a pass.
	if latest.Conclusion == "" {
		return refuse, "CI reports this commit as completed with no conclusion at all, " +
			"which is not a result anybody can act on and is certainly not a pass.", &latest
	}
	return refuse, fmt.Sprintf("CI concluded %q on this commit. That is not a conclusion this "+
		"command recognises, and an unfamiliar answer is not a pass.", latest.Conclusion), &latest
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

// client reads workflow runs. An interface so the polling loop can be tested
// against a sequence of answers rather than against a server.
type client interface {
	runs(sha string) ([]run, error)
}

// fatal is an error the loop must not retry past. A bad token or a repository
// this job cannot read never becomes readable by asking again for 38 minutes,
// and burning the budget on it hides the real cause behind a timeout message.
type fatal struct{ err error }

func (f fatal) Error() string { return f.err.Error() }
func (f fatal) Unwrap() error { return f.err }

type api struct {
	base  string
	repo  string
	token string
	http  *http.Client
}

func (a *api) runs(sha string) ([]run, error) {
	url := fmt.Sprintf("%s/repos/%s/actions/runs?head_sha=%s&per_page=100", a.base, a.repo, sha)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fatal{err}
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		// A network error is worth retrying and is never worth reading as a
		// pass. It falls through to the loop as an ordinary error.
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return nil, fmt.Errorf("the API answered %d, which may pass", resp.StatusCode)
	default:
		return nil, fatal{fmt.Errorf("the API answered %d for %s, which asking again will not fix: %s",
			resp.StatusCode, a.repo, strings.TrimSpace(string(body)))}
	}
	var list runList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("reading the run list: %w", err)
	}
	return list.Runs, nil
}

// poll asks until it has a verdict or runs out of budget. Running out is a
// refusal, and it is a different refusal from a red CI: one says the commit
// failed, the other says nobody found out in time, and telling a person the
// wrong one of those sends them to the wrong file.
func poll(c client, sha, workflow string, attempts int, interval time.Duration, out io.Writer) (verdict, string, *run) {
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		found, err := c.runs(sha)
		if err != nil {
			var stop fatal
			if errors.As(err, &stop) {
				return refuse, stop.Error(), nil
			}
			lastErr = err
			_, _ = fmt.Fprintf(out, "attempt %d: could not read the run list: %v\n", attempt, err)
			sleep(interval, attempt, attempts)
			continue
		}
		lastErr = nil
		v, why, r := decide(found, workflow)
		_, _ = fmt.Fprintf(out, "attempt %d: %s (%s)%s\n", attempt, why, v, link(r))
		if v != wait {
			return v, why, r
		}
		sleep(interval, attempt, attempts)
	}
	if lastErr != nil {
		return refuse, fmt.Sprintf("the API never answered within the budget. The last thing it said "+
			"was: %v. Nothing is published, because nothing was checked.", lastErr), nil
	}
	return refuse, "CI reached no conclusion on this commit within the budget. That is not a " +
		"statement that the commit is bad, it is a statement that nobody knows, and a release " +
		"does not publish on nobody knowing.", nil
}

// sleep skips the wait after the final attempt, because a command that has
// already decided to give up should not spend another 20 seconds doing it.
func sleep(d time.Duration, attempt, attempts int) {
	if attempt < attempts {
		time.Sleep(d)
	}
}

func link(r *run) string {
	if r == nil || r.HTMLURL == "" {
		return ""
	}
	return " " + r.HTMLURL
}

func main() {
	repo := flag.String("repo", os.Getenv("GITHUB_REPOSITORY"), "owner/name")
	sha := flag.String("sha", os.Getenv("GITHUB_SHA"), "the commit this release is being cut from")
	workflow := flag.String("workflow", ciWorkflow, "the workflow whose conclusion decides this")
	attempts := flag.Int("attempts", defaultAttempts, "how many times to ask before giving up")
	interval := flag.Duration("interval", defaultInterval, "how long to wait between asks")
	base := flag.String("api", "https://api.github.com", "the API root")
	flag.Parse()

	if *repo == "" || *sha == "" {
		fail("cigate needs a repository and a commit. Pass -repo and -sha, or set " +
			"GITHUB_REPOSITORY and GITHUB_SHA.")
	}

	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		// Refused rather than attempted. Unauthenticated the API answers 60
		// requests an hour from a shared address and hides private runs, so the
		// likely result is an empty list, which reads as "no run yet" and then
		// as a timeout. That is a gate that fails for the wrong reason, and
		// this repository has shipped enough of those.
		fail("cigate has no token. Set GH_TOKEN or GITHUB_TOKEN to a token that can read " +
			"actions on this repository, which in a workflow means permissions: actions: read.")
	}

	c := &api{base: *base, repo: *repo, token: token, http: &http.Client{Timeout: 30 * time.Second}}
	fmt.Printf("cigate: waiting for %s on %s in %s\n", *workflow, *sha, *repo)

	v, why, r := poll(c, *sha, *workflow, *attempts, *interval, os.Stdout)
	if v == pass {
		summarise(fmt.Sprintf("CI is green on `%s`.%s", *sha, link(r)))
		fmt.Printf("cigate: %s%s\n", why, link(r))
		return
	}
	summarise(fmt.Sprintf("Refused to publish `%s`. %s%s", *sha, why, link(r)))
	fmt.Fprintf(os.Stderr, "::error title=CI is not green on this commit::%s%s\n", why, link(r))
	os.Exit(1)
}

// summarise writes to the job summary when there is one. Best effort on
// purpose: the verdict is the exit code, and a summary that could not be
// written must never change it.
func summarise(line string) {
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintln(f, line)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "cigate: "+format+"\n", args...)
	os.Exit(1)
}
