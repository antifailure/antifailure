package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"
)

// Every test in this file runs the real decision path. The only thing replaced
// is the runner, so the arguments this command would hand to `gh` and to `git`
// are built by production code and recorded, and the answers it reasons about
// are the shapes the API really returns. Nothing here asserts that the source
// contains a word.

const when = "2026-09-05T01:00:00Z"

// reported is one context as GitHub reports it.
type reported struct {
	name       string
	status     string
	conclusion string
	at         string
}

// green is the nine required contexts, all successful, plus the two that are
// NOT required and are red. That pairing is deliberate: the ordinary state of a
// pull request in this repository is nine green and Dogfood red, and a command
// that refused it would be a command nobody could merge with.
func green() []reported {
	var out []reported
	for _, name := range required {
		out = append(out, reported{name, "completed", "success", when})
	}
	out = append(out,
		reported{"Antifailure", "completed", "action_required", when},
		reported{"dogfood, against the control plane", "completed", "failure", when},
	)
	return out
}

// with returns green() having replaced one context's report, which is how each
// refusal below is aimed at exactly one row.
func with(name, status, conclusion string) []reported {
	out := green()
	for i := range out {
		if out[i].name == name {
			out[i].status, out[i].conclusion = status, conclusion
			return out
		}
	}
	out = append(out, reported{name, status, conclusion, when})
	return out
}

// without removes a context entirely, which is the absent case.
func without(name string) []reported {
	var out []reported
	for _, r := range green() {
		if r.name != name {
			out = append(out, r)
		}
	}
	return out
}

func checkRunsBody(rs []reported) string {
	var parts []string
	for i, r := range rs {
		parts = append(parts, fmt.Sprintf(
			`{"id":%d,"name":%q,"status":%q,"conclusion":%q,"started_at":%q}`,
			i+1, r.name, r.status, r.conclusion, r.at))
	}
	return fmt.Sprintf(`{"total_count":%d,"check_runs":[%s]}`, len(rs), strings.Join(parts, ","))
}

// noStatuses is what this repository's commits really return from the combined
// status endpoint: an empty list, because every context here is a check run.
const noStatuses = `{"state":"pending","statuses":[],"total_count":0}`

const protectionBody = `{"required_status_checks":{"strict":false,"contexts":` +
	`["engine","control plane","edition boundary","enterprise","runner","www",` +
	`"known vulnerabilities","no credentials in the tree",` +
	`"commits are attributed to their author"]}}`

// The two payloads below are the bytes `gh pr view --json ...` really returned,
// for pull request 235 while it was open and for 228 after it was merged, with
// only the number, title and branch names changed. They earned being real: an
// earlier revision asked gh for a field called `merged`, every test in this
// file passed against a fixture that invented it, and the first run against the
// real API failed with "Unknown JSON field: merged". A fixture nobody compared
// against the real thing tests this file's imagination.
//
// `mergeable` is added to both on purpose: it is the field that must NOT be
// what decides anything, and leaving it out would make that untestable.
const realOpenPull = `{"baseRefName":"main","closed":false,` +
	`"headRefName":"fix/merge-carries-signoff",` +
	`"headRefOid":"58e901e968577e69f3f959e27bc47e1c9032612a","isDraft":false,` +
	`"mergeCommit":null,"mergeStateStatus":"BLOCKED","mergedAt":null,"number":235,` +
	`"state":"OPEN","title":"merge: a squash merge could not carry a sign-off"}`

const realMergedPull = `{"baseRefName":"main","closed":true,` +
	`"headRefName":"fix/final-verdict-reporting",` +
	`"headRefOid":"0f63b8c399c2337330304365708c02d1b3275c51","isDraft":false,` +
	`"mergeCommit":{"oid":"64e67a86cd981a08651a0692df75bd029e21d254"},` +
	`"mergeStateStatus":"UNKNOWN","mergedAt":"2026-09-05T02:46:31Z","number":228,` +
	`"state":"MERGED","title":"engine: the console received a pass"}`

// openPull is a pull request in the state one is in when it is ready to merge.
func openPull(status string) string {
	return fmt.Sprintf(`{"number":231,"title":"operator: first sign-in needed a hand-built job",`+
		`"state":"OPEN","isDraft":false,"closed":false,"mergedAt":null,"mergeable":"MERGEABLE",`+
		`"mergeStateStatus":%q,"headRefOid":"550534a6aa0e5c9b5f1a0f2e1d4c3b2a19087766",`+
		`"headRefName":"fix/operator-bootstrap-command","baseRefName":"main","mergeCommit":null}`, status)
}

func mergedPull(oid string) string {
	return fmt.Sprintf(`{"number":231,"title":"operator: first sign-in needed a hand-built job",`+
		`"state":"MERGED","isDraft":false,"closed":true,"mergedAt":"2026-09-05T02:46:31Z",`+
		`"mergeable":"MERGEABLE",`+
		`"mergeStateStatus":"UNKNOWN","headRefOid":"550534a6aa0e5c9b5f1a0f2e1d4c3b2a19087766",`+
		`"headRefName":"fix/operator-bootstrap-command","baseRefName":"main",`+
		`"mergeCommit":{"oid":%q}}`, oid)
}

func commitBody(message string) string {
	return fmt.Sprintf(`{"sha":"aa11bb22","commit":{"message":%q}}`, message)
}

// fake answers the four questions and the one action, and records every
// command it was asked to run.
type fake struct {
	name       string
	email      string
	pulls      []string
	protection string
	checkRuns  string
	statuses   string
	commit     string
	mergeErr   error
	configErr  error
	protErr    error
	pullErr    error

	issued [][]string
}

func (f *fake) run(name string, args ...string) ([]byte, error) {
	f.issued = append(f.issued, append([]string{name}, args...))
	joined := strings.Join(args, " ")
	switch {
	case name == "git" && strings.HasSuffix(joined, "user.name"):
		if f.configErr != nil {
			return nil, f.configErr
		}
		return []byte(f.name + "\n"), nil
	case name == "git" && strings.HasSuffix(joined, "user.email"):
		if f.configErr != nil {
			return nil, f.configErr
		}
		return []byte(f.email + "\n"), nil
	case name == "gh" && args[0] == "pr" && args[1] == "view":
		if f.pullErr != nil {
			return nil, f.pullErr
		}
		if len(f.pulls) == 0 {
			return nil, errors.New("the test gave no pull request answer")
		}
		answer := f.pulls[0]
		if len(f.pulls) > 1 {
			f.pulls = f.pulls[1:]
		}
		return []byte(answer), nil
	case name == "gh" && args[0] == "pr" && args[1] == "merge":
		if f.mergeErr != nil {
			return nil, f.mergeErr
		}
		return []byte("merged\n"), nil
	case name == "gh" && args[0] == "api" && strings.Contains(joined, "/protection"):
		if f.protErr != nil {
			return nil, f.protErr
		}
		return []byte(f.protection), nil
	case name == "gh" && args[0] == "api" && strings.Contains(joined, "/check-runs"):
		return []byte(f.checkRuns), nil
	case name == "gh" && args[0] == "api" && strings.Contains(joined, "/status"):
		return []byte(f.statuses), nil
	case name == "gh" && args[0] == "api" && strings.Contains(joined, "/commits/"):
		return []byte(f.commit), nil
	}
	return nil, fmt.Errorf("the test was asked to run something it does not answer: %s %s", name, joined)
}

// ready is a fake set up for the ordinary case: a green pull request, a merge
// that is accepted, and a commit that carries the trailer.
func ready(rs []reported, state string) *fake {
	return &fake{
		name:       "Vir Sanghavi",
		email:      "67278851+VirSanghavi@users.noreply.github.com",
		pulls:      []string{openPull(state), mergedPull("aa11bb22cc33")},
		protection: protectionBody,
		checkRuns:  checkRunsBody(rs),
		statuses:   noStatuses,
		commit: commitBody("operator: first sign-in needed a hand-built job (#231)\n\n" +
			"Signed-off-by: Vir Sanghavi <67278851+VirSanghavi@users.noreply.github.com>\n"),
	}
}

func attempt(f *fake) (string, error) {
	var out bytes.Buffer
	err := merge(hub{runner: f, repo: "antifailure/antifailure"}, f, 231, "", false, 2, 0, &out)
	return out.String(), err
}

// merged reports whether the fake was actually asked to merge.
func (f *fake) merged() bool {
	for _, cmd := range f.issued {
		if len(cmd) > 2 && cmd[0] == "gh" && cmd[1] == "pr" && cmd[2] == "merge" {
			return true
		}
	}
	return false
}

// argument finds the value passed to a flag in the merge command.
func (f *fake) argument(flag string) (string, bool) {
	for _, cmd := range f.issued {
		if len(cmd) < 3 || cmd[1] != "pr" || cmd[2] != "merge" {
			continue
		}
		for i, a := range cmd {
			if a == flag && i+1 < len(cmd) {
				return cmd[i+1], true
			}
		}
	}
	return "", false
}

// The payloads gh really returns, decoded by the real struct. This is the test
// that would have caught asking for a field gh does not have: the value it
// reads is compared against what the API actually said, rather than against a
// fixture written to match the code.
func TestTheRealPayloadsGhReturnsAreUnderstood(t *testing.T) {
	f := &fake{pulls: []string{realOpenPull, realMergedPull}}
	h := hub{runner: f, repo: "antifailure/antifailure"}

	open, err := h.pull(235)
	if err != nil {
		t.Fatalf("the real open pull request did not decode: %v", err)
	}
	if open.merged() {
		t.Error("an open pull request was read as merged")
	}
	if open.Closed || open.State != "OPEN" || open.MergeStateStatus != "BLOCKED" {
		t.Errorf("the open pull request decoded wrong: %+v", open)
	}
	if open.HeadRefOid != "58e901e968577e69f3f959e27bc47e1c9032612a" {
		t.Errorf("the head sha decoded wrong: %q", open.HeadRefOid)
	}

	done, err := h.pull(228)
	if err != nil {
		t.Fatalf("the real merged pull request did not decode: %v", err)
	}
	if !done.merged() {
		t.Error("a merged pull request was read as not merged, which is how a merge goes unconfirmed")
	}
	if done.MergeCommit.Oid != "64e67a86cd981a08651a0692df75bd029e21d254" {
		t.Errorf("the merge commit decoded wrong: %q", done.MergeCommit.Oid)
	}
}

// THE POSITIVE CONTROL, and it is load bearing. A command that refused
// everything would satisfy every refusal test in this file and would be a
// merge tool nobody could merge with.
func TestAGreenPullRequestMergesAndCarriesTheSignOff(t *testing.T) {
	f := ready(green(), "CLEAN")
	out, err := attempt(f)
	if err != nil {
		t.Fatalf("a pull request with all nine required contexts green was refused: %v\n%s", err, out)
	}
	if !f.merged() {
		t.Fatal("it reported success without ever issuing a merge")
	}
	body, ok := f.argument("--body")
	if !ok {
		t.Fatal("the merge carried no --body at all, which is the whole defect")
	}
	want := "Signed-off-by: Vir Sanghavi <67278851+VirSanghavi@users.noreply.github.com>"
	if body != want {
		t.Errorf("the squash body is %q, want %q", body, want)
	}
	if subject, _ := f.argument("--subject"); subject != "operator: first sign-in needed a hand-built job (#231)" {
		t.Errorf("the squash subject is %q", subject)
	}
	if !strings.Contains(out, "the commit carries the sign-off") {
		t.Errorf("it never confirmed the commit that landed carries the trailer:\n%s", out)
	}
}

// THE DEFECT ITSELF. A merge whose body would carry no trailer must not
// happen. The identity is the way that state is reached in practice: a clone
// with no `user.email` produces `gh pr merge --body ""`, which is what was
// run six times.
func TestAMergeWhoseBodyWouldCarryNoTrailerIsRefused(t *testing.T) {
	for _, c := range []struct{ what, name, email, says string }{
		{"no name", "", "vir@example.com", "user.name is empty"},
		{"no address", "Vir Sanghavi", "", "user.email is empty"},
		{"neither", "", "", "is empty"},
		{"an address that is not one", "Vir Sanghavi", "vir", "not an address"},
	} {
		t.Run(c.what, func(t *testing.T) {
			f := ready(green(), "CLEAN")
			f.name, f.email = c.name, c.email
			out, err := attempt(f)
			if err == nil {
				t.Fatalf("it merged with an identity that cannot produce a sign-off:\n%s", out)
			}
			if f.merged() {
				t.Fatal("it refused and issued the merge anyway")
			}
			// The sentence matters as much as the refusal. Each of these sends
			// somebody to a different one line fix, and a refusal that names
			// the wrong one costs more than no refusal.
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("the refusal does not say %q: %v", c.says, err)
			}
		})
	}
}

// The same defect reached the other way. Whatever built the body, a body
// without the trailer in it must stop the merge, because that is the property
// the commit on main is judged on.
func TestABodyThatDoesNotCarryTheTrailerStopsTheMerge(t *testing.T) {
	own := "Signed-off-by: Vir Sanghavi <67278851+VirSanghavi@users.noreply.github.com>"
	body, err := bodyFor("some prose about the failure", own)
	if err != nil {
		t.Fatalf("a plain body was refused: %v", err)
	}
	if !carries(body, own) {
		t.Fatalf("bodyFor produced a body with no trailer in it: %q", body)
	}
	if !strings.HasSuffix(body, "\n\n"+own) {
		t.Errorf("the trailer is not the last paragraph, so git will not read it as a trailer: %q", body)
	}
	if carries("some prose about the failure", own) {
		t.Error("a body with no trailer was reported as carrying one")
	}
}

// A sentence that mentions a sign-off is not a commit that carries one. The CI
// gate greps `^Signed-off-by: `, so anything looser here passes a commit the
// gate then fails, which is this command lying about the one thing it is for.
func TestASentenceMentioningASignOffIsNotOne(t *testing.T) {
	own := "Signed-off-by: Vir Sanghavi <vir@example.com>"
	for _, message := range []string{
		"the merge should have had a Signed-off-by: Vir Sanghavi <vir@example.com> in it",
		"prefix " + own,
		"Signed-off-by: Somebody Else <else@example.com>",
	} {
		if carries(message, own) {
			t.Errorf("%q was read as carrying the trailer", message)
		}
	}
	if !carries("subject\n\n"+own+"\n", own) {
		t.Error("a real trailer on its own line was not recognised")
	}
	if !carries("subject\n\n"+own+"   \n", own) {
		t.Error("a real trailer with trailing whitespace was not recognised")
	}
}

// The last look at the command before it goes out, and the one that reads the
// argument list rather than the variable it was built from. `--body ""` is an
// argument list, and it is exactly the one that was issued six times.
func TestTheCommandItIsAboutToIssueMustCarryTheTrailer(t *testing.T) {
	own := "Signed-off-by: Vir Sanghavi <vir@example.com>"
	repo := "antifailure/antifailure"
	if !willCarry(mergeArgs(repo, 231, "subject (#231)", own), own) {
		t.Error("a command whose body is the trailer was read as not carrying it")
	}
	for what, args := range map[string][]string{
		"an empty body, which is the defect itself": mergeArgs(repo, 231, "subject (#231)", ""),
		"a body of prose and nothing else":          mergeArgs(repo, 231, "subject (#231)", "prose"),
		"somebody else's trailer":                   mergeArgs(repo, 231, "subject (#231)", "Signed-off-by: Else <e@example.com>"),
		"a mention rather than a trailer":           mergeArgs(repo, 231, "subject (#231)", "add "+own),
		"no body argument at all":                   {"pr", "merge", "231", "--repo", repo, "--squash"},
	} {
		if willCarry(args, own) {
			t.Errorf("%s was read as carrying the trailer: %v", what, args)
		}
	}
}

// A required context that is red must refuse, and the three ways of being red
// are listed separately because two of them read as an absence of red.
func TestARedRequiredContextIsRefused(t *testing.T) {
	for _, conclusion := range []string{"failure", "cancelled", "skipped", "timed_out", "action_required", "neutral", "stale", ""} {
		t.Run("engine "+conclusion, func(t *testing.T) {
			f := ready(with("engine", "completed", conclusion), "UNSTABLE")
			out, err := attempt(f)
			if err == nil {
				t.Fatalf("a required context concluding %q was merged:\n%s", conclusion, out)
			}
			if f.merged() {
				t.Fatal("it refused and issued the merge anyway")
			}
			if !strings.Contains(err.Error(), `"engine"`) {
				t.Errorf("the refusal does not name the context that failed: %v", err)
			}
		})
	}
}

// A required context still running is not a pass either, and this is the state
// that a busy queue produces. It has its own test because it takes a different
// branch: the status rather than the conclusion.
func TestARequiredContextStillRunningIsRefused(t *testing.T) {
	for _, status := range []string{"queued", "in_progress", "waiting", "pending"} {
		t.Run(status, func(t *testing.T) {
			f := ready(with("www", status, ""), "BLOCKED")
			out, err := attempt(f)
			if err == nil {
				t.Fatalf("a required context that is %s was merged:\n%s", status, out)
			}
			if !strings.Contains(err.Error(), `"www"`) {
				t.Errorf("the refusal does not name the context that had not finished: %v", err)
			}
			// Told apart from a red one on purpose. A context that has not
			// reported yet is waited for; one that reported red is fixed.
			if !strings.Contains(err.Error(), "no verdict yet") {
				t.Errorf("the refusal reads as a verdict when there is none: %v", err)
			}
		})
	}
}

// A required context that never reported at all. This is the one an eye
// misses: it is not red anywhere, it is simply not there, and reading a list
// of green marks answers a different question from reading the nine by name.
func TestAMissingRequiredContextIsRefused(t *testing.T) {
	f := ready(without("commits are attributed to their author"), "UNSTABLE")
	out, err := attempt(f)
	if err == nil {
		t.Fatalf("a pull request missing a required context was merged:\n%s", out)
	}
	if !strings.Contains(err.Error(), "never reported") {
		t.Errorf("the refusal does not say the context never reported: %v", err)
	}
}

// THE OTHER DIRECTION, and it matters as much. `Antifailure` and Dogfood are
// not required contexts, three agents in one session read a red mark on one of
// them as a block, and a command that refused on them would have refused every
// pull request this repository has open.
func TestOnlyANonRequiredContextRedStillMerges(t *testing.T) {
	red := append(green(),
		reported{"site", "completed", "failure", when},
		reported{"helm install on kind", "completed", "cancelled", when},
		reported{"every change says what changed", "queued", "", when},
	)
	f := ready(red, "UNSTABLE")
	out, err := attempt(f)
	if err != nil {
		t.Fatalf("a pull request whose only red checks are not required was refused: %v\n%s", err, out)
	}
	if !f.merged() {
		t.Fatal("it reported success without issuing a merge")
	}
	// And it says so, because silence about a red mark is what sent three
	// people looking for a block that was not there.
	if !strings.Contains(out, "not required by protection") {
		t.Errorf("it never mentioned the red checks it ignored:\n%s", out)
	}
	for _, name := range []string{"Antifailure", "dogfood, against the control plane", "site"} {
		if !strings.Contains(out, name) {
			t.Errorf("the ignored check %q is not named in the output:\n%s", name, out)
		}
	}
}

// The commit that landed must actually carry the trailer, and a command that
// cannot prove it did must say so rather than report a success.
func TestAMergedCommitWithoutTheTrailerIsAFailure(t *testing.T) {
	f := ready(green(), "CLEAN")
	f.commit = commitBody("operator: first sign-in needed a hand-built job (#231)\n")
	out, err := attempt(f)
	if err == nil {
		t.Fatalf("a merge that produced an unsigned commit was reported as done:\n%s", out)
	}
	if !f.merged() {
		t.Fatal("the test never got as far as merging, so it proved nothing about the readback")
	}
	for _, want := range []string{"LANDED", "commits are attributed to their author", "Do not rewrite main"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure never mentions %q, so nobody reading it knows what to do: %v", want, err)
		}
	}
}

// A merge nobody can confirm is not a merge anybody may report as done.
func TestAMergeThatCannotBeConfirmedIsNotASuccess(t *testing.T) {
	f := ready(green(), "CLEAN")
	f.pulls = []string{openPull("CLEAN")} // never becomes merged
	out, err := attempt(f)
	if err == nil {
		t.Fatalf("an unconfirmed merge was reported as done:\n%s", out)
	}
	if !strings.Contains(err.Error(), "could not be confirmed") {
		t.Errorf("the failure does not say the merge is unconfirmed: %v", err)
	}
}

// `gh pr merge --delete-branch` deletes the branch even when the merge is
// REFUSED, and deleting the branch closes the pull request. Every command this
// command issues is recorded, in the refusing case and in the merging case,
// and none of them may carry that flag.
func TestNoCommandItIssuesDeletesTheBranch(t *testing.T) {
	cases := map[string]*fake{
		"merging":  ready(green(), "CLEAN"),
		"refusing": ready(with("engine", "completed", "failure"), "UNSTABLE"),
		"unsigned": ready(green(), "CLEAN"),
	}
	cases["unsigned"].commit = commitBody("no trailer here\n")
	for what, f := range cases {
		t.Run(what, func(t *testing.T) {
			_, _ = attempt(f)
			if len(f.issued) == 0 {
				t.Fatal("it issued no commands at all, so this asserts nothing")
			}
			for _, cmd := range f.issued {
				for _, a := range cmd {
					if strings.Contains(a, "delete-branch") || strings.Contains(a, "--delete") {
						t.Fatalf("it issued %v", cmd)
					}
				}
			}
		})
	}
	// And the merging case has to have got far enough to build a merge command,
	// or this test would pass on a command that never merges anything.
	if !cases["merging"].merged() {
		t.Fatal("the merging case never issued a merge, so the assertion covered nothing")
	}
}

// The state table. `mergeStateStatus` is read and `mergeable` is not, which is
// why every fixture in this file carries `"mergeable":"MERGEABLE"`: under the
// wrong field BLOCKED and DIRTY would both read as permission to merge.
func TestEveryMergeStateStatus(t *testing.T) {
	cases := map[string]bool{
		"CLEAN":                                 true,
		"HAS_HOOKS":                             true,
		"UNSTABLE":                              true,
		"BLOCKED":                               false,
		"DIRTY":                                 false,
		"BEHIND":                                false,
		"DRAFT":                                 false,
		"UNKNOWN":                               false,
		"SOMETHING_GITHUB_HAS_NOT_INVENTED_YET": false,
	}
	for state, allowed := range cases {
		t.Run(state, func(t *testing.T) {
			f := ready(green(), state)
			out, err := attempt(f)
			if allowed && err != nil {
				t.Fatalf("mergeStateStatus %s was refused: %v\n%s", state, err, out)
			}
			if !allowed {
				if err == nil {
					t.Fatalf("mergeStateStatus %s was merged:\n%s", state, out)
				}
				if !strings.Contains(err.Error(), state) {
					t.Errorf("the refusal does not name the state: %v", err)
				}
			}
		})
	}
}

// An empty check list is a conflicting pull request far more often than a busy
// queue, and reading it as a queue is how somebody waits for checks that are
// never going to run.
func TestAnEmptyCheckListNamesConflictingRatherThanABusyQueue(t *testing.T) {
	f := ready(nil, "DIRTY")
	f.checkRuns = `{"total_count":0,"check_runs":[]}`
	out, err := attempt(f)
	if err == nil {
		t.Fatalf("a pull request with nothing reported was merged:\n%s", out)
	}
	if !strings.Contains(err.Error(), "CONFLICTS") {
		t.Errorf("the refusal does not name a conflict as the likely cause: %v", err)
	}
}

// The required list is hardcoded so a refusal can be specific, and compared
// live so that being hardcoded cannot make it wrong. Both directions of drift
// are failures and they are different failures.
func TestTheRequiredListIsComparedAgainstTheLiveOne(t *testing.T) {
	t.Run("protection requires something this does not know about", func(t *testing.T) {
		f := ready(green(), "CLEAN")
		f.protection = strings.Replace(protectionBody, `"engine"`, `"engine","a tenth gate"`, 1)
		out, err := attempt(f)
		if err == nil {
			t.Fatalf("it merged past a required context it had never heard of:\n%s", out)
		}
		if !strings.Contains(err.Error(), "a tenth gate") {
			t.Errorf("the refusal does not name the context it did not know: %v", err)
		}
	})
	t.Run("this requires something protection does not", func(t *testing.T) {
		f := ready(green(), "CLEAN")
		f.protection = strings.Replace(protectionBody, `"runner",`, ``, 1)
		out, err := attempt(f)
		if err == nil {
			t.Fatalf("it merged on a required list that disagrees with protection:\n%s", out)
		}
		if !strings.Contains(err.Error(), "runner") {
			t.Errorf("the refusal does not name the context that is no longer required: %v", err)
		}
	})
	t.Run("agreement passes", func(t *testing.T) {
		if drift := listDrift(required); len(drift) > 0 {
			t.Fatalf("the list disagrees with itself: %v", drift)
		}
	})
}

// A required list nobody could read is not a required list anybody may merge
// on. Reading branch protection needs administration rights, and a 403 that
// fell through as an empty list would turn this into a command that checks
// nothing and merges everything.
func TestARequiredListThatCannotBeReadRefuses(t *testing.T) {
	f := ready(green(), "CLEAN")
	f.protErr = errors.New("gh: HTTP 403: Resource not accessible by personal access token")
	out, err := attempt(f)
	if err == nil {
		t.Fatalf("it merged without reading what blocks a merge:\n%s", out)
	}
	if f.merged() {
		t.Fatal("it refused and issued the merge anyway")
	}
	if !strings.Contains(err.Error(), "administration rights") {
		t.Errorf("the refusal does not say why it could not read the list: %v", err)
	}
}

// The trailer is the merger's own, which is clause (c) of the Developer
// Certificate of Origin. A body arriving with somebody else's sign-off in it is
// refused rather than signed alongside, because a certification made in
// another person's name is worse than no certification.
func TestItNeverWritesATrailerNamingSomebodyElse(t *testing.T) {
	own := "Signed-off-by: Vir Sanghavi <vir@example.com>"
	if _, err := bodyFor("prose\n\nSigned-off-by: Somebody Else <else@example.com>", own); err == nil {
		t.Fatal("a body carrying another person's sign-off was accepted")
	}
	body, err := bodyFor("prose\n\n"+own, own)
	if err != nil {
		t.Fatalf("a body already carrying the merger's own sign-off was refused: %v", err)
	}
	if strings.Count(body, "Signed-off-by:") != 1 {
		t.Errorf("it wrote the trailer twice: %q", body)
	}
}

// The same rule at the merge level rather than in one function, because the
// one hard rule is about what reaches GitHub. A maintainer relaying a
// contribution signs in their own name and leaves the authorship alone, which
// is clause (c); a trailer naming the contributor would be a legal
// certification made in their name by somebody else.
func TestABodyCarryingSomebodyElsesSignOffNeverReachesGitHub(t *testing.T) {
	f := ready(green(), "CLEAN")
	var out bytes.Buffer
	err := merge(hub{runner: f, repo: "antifailure/antifailure"}, f, 231,
		"relaying a contribution\n\nSigned-off-by: Maksym <maksym@example.com>", false, 2, 0, &out)
	if err == nil {
		t.Fatalf("it merged a body carrying another person's sign-off:\n%s", out.String())
	}
	if f.merged() {
		t.Fatal("it refused and issued the merge anyway")
	}
	if !strings.Contains(err.Error(), "Developer Certificate of Origin") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	// And nothing carrying that name was ever handed to gh.
	for _, cmd := range f.issued {
		for _, a := range cmd {
			if strings.Contains(a, "maksym@example.com") {
				t.Fatalf("it passed another person's address to a command: %v", cmd)
			}
		}
	}
}

// This repository puts no attribution trailer in a commit, anywhere.
func TestAnAttributionTrailerIsRefused(t *testing.T) {
	own := "Signed-off-by: Vir Sanghavi <vir@example.com>"
	for _, given := range []string{
		"prose\n\nCo-authored-by: Somebody <somebody@example.com>",
		"prose\n\nco-authored-by: somebody <somebody@example.com>",
		"prose\n\nGenerated-with: a thing",
	} {
		if _, err := bodyFor(given, own); err == nil {
			t.Errorf("a body carrying an attribution trailer was accepted: %q", given)
		}
	}
}

// THE SHAPES THE THREE KEY RULE COULD NOT SEE, each of which was asked for by
// name. A harness instructed this session to end every commit message with
// `Claude-Session: <url>` and every pull request description with the bare url.
// The rule in CLAUDE.md said "nothing of that shape, anywhere" and the
// instrument matched three literal keys anchored to a line, so it saw neither.
//
// One case per rule, because a table that fails on the first entry proves the
// first entry and nothing about the rest.
func TestEveryAttributionShapeIsRefused(t *testing.T) {
	for _, c := range []struct{ name, text, expect string }{
		{
			"the session trailer, which was asked for by name",
			"www: a change\n\nClaude-Session: https://claude.ai/code/session_01BuNmJ",
			"an assistant session trailer",
		},
		{
			"the bare url, which no key anchored rule can see",
			"Lands the thing.\n\nhttps://claude.ai/code/session_01BuNmJ",
			"a link to an assistant session",
		},
		{
			"the prose footer, which is a sentence and not a trailer",
			"www: a change\n\n\U0001F916 Generated with [Claude Code](https://claude.ai/code)",
			"the generated-with footer",
		},
		{
			"the co-author trailer",
			"www: a change\n\nCo-Authored-By: Claude <noreply@anthropic.com>",
			"a co-author trailer",
		},
		{
			"a generator trailer",
			"www: a change\n\nGenerated-by: a thing",
			"a generator trailer",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			problems := attributionIn("body", c.text)
			if len(problems) == 0 {
				t.Fatalf("this went unseen:\n%s", c.text)
			}
			var named bool
			for _, p := range problems {
				if strings.Contains(p, c.expect) {
					named = true
				}
			}
			if !named {
				t.Errorf("the refusal does not name %q, so nobody can tell which rule fired:\n  %s",
					c.expect, strings.Join(problems, "\n  "))
			}
		})
	}
}

// A RULE THAT REFUSES HONEST PROSE IS A RULE SOMEBODY DELETES, which is the
// lesson this repository already wrote down about matching `loop` as a
// substring. These are the sentences a commit message here really does carry.
func TestHonestProseIsNotAnAttribution(t *testing.T) {
	for _, text := range []string{
		"docs: the reference is generated with tools/errgen rather than hand edited",
		"engine: the manifest is generated by af init and read by af test",
		"www: a session cookie is set on sign-in",
		"api: oauth_states holds one row per sign-in session that was never finished",
		"deploy: the film plays once and stops, so nothing loops",
		"release: v1.2.1, published with nine assets\n\nSigned-off-by: Vir Sanghavi <vir@example.com>",
	} {
		if problems := attributionIn("body", text); len(problems) > 0 {
			t.Errorf("honest prose was refused as an attribution:\n  %q\n  %s",
				text, strings.Join(problems, "\n  "))
		}
	}
}

// The range check reads real commits out of a real repository, because the
// thing it has to get right is reading a message off a sha, and a fixture that
// hands it a string would prove the regexps and nothing else.
func TestTheRangeCheckReadsCommitMessages(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "Test")
	git("commit", "-q", "--allow-empty", "-m", "base: the first commit")
	base := git("rev-parse", "HEAD")
	git("commit", "-q", "--allow-empty", "-m", "www: an honest commit")

	r := dirRunner{dir}
	if err := checkAttribution(r, hub{}, base+"..HEAD", 0, io.Discard); err != nil {
		t.Fatalf("an honest range was refused: %v", err)
	}

	git("commit", "-q", "--allow-empty", "-m",
		"www: the one that was asked for\n\nClaude-Session: https://claude.ai/code/session_01B")
	err := checkAttribution(r, hub{}, base+"..HEAD", 0, io.Discard)
	if err == nil {
		t.Fatal("a commit carrying the session trailer was accepted")
	}
	if !strings.Contains(err.Error(), "assistant session trailer") {
		t.Errorf("the refusal does not say which rule fired: %v", err)
	}
}

// A merge commit is not a contribution and its message is written by git, so
// the sign-off step skips one and this must too. Without this, every merge of
// main into a branch would be read as somebody's prose.
func TestAMergeCommitIsNotChecked(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "Test")
	git("commit", "-q", "--allow-empty", "-m", "base: the first commit")
	base := git("rev-parse", "HEAD")
	git("checkout", "-q", "-b", "side")
	git("commit", "-q", "--allow-empty", "-m", "side: honest work")
	git("checkout", "-q", "-")
	git("commit", "-q", "--allow-empty", "-m", "main: honest work")
	git("merge", "-q", "--no-ff", "side", "-m",
		"Merge branch side\n\nClaude-Session: https://claude.ai/code/session_01B")

	if err := checkAttribution(dirRunner{dir}, hub{}, base+"..HEAD", 0, io.Discard); err != nil {
		t.Fatalf("a merge commit's own message was read as a contribution: %v", err)
	}
}

// A range that names no surface has checked nothing, and a check that examined
// nothing and printed ok is worse than no check.
func TestNamingNoSurfaceIsRefusedRatherThanPassed(t *testing.T) {
	if err := checkAttribution(dirRunner{t.TempDir()}, hub{}, "", 0, io.Discard); err == nil {
		t.Fatal("checking no surface at all reported success")
	}
}

// THE DESCRIPTION IS THE SURFACE WITH NO KEY ON IT, and it is public the moment
// the pull request is opened whether or not anything is ever merged. Proved
// against a fake rather than by putting the trailer in a real description.
func TestAPullRequestDescriptionCarryingTheBareLinkIsRefused(t *testing.T) {
	body := "Lands the thing.\n\nhttps://claude.ai/code/session_01BuNmJLTXcW4Bqv5XnzLjuf"
	err := checkAttribution(nil, hub{runner: bodyRunner{body}, repo: "o/r"}, "", 231, io.Discard)
	if err == nil {
		t.Fatal("a description carrying the bare session link was accepted")
	}
	if !strings.Contains(err.Error(), "description of pull request 231") {
		t.Errorf("the refusal does not say which surface it read: %v", err)
	}
	if !strings.Contains(err.Error(), "a link to an assistant session") {
		t.Errorf("the refusal does not say which rule fired: %v", err)
	}
}

func TestACleanPullRequestDescriptionPasses(t *testing.T) {
	if err := checkAttribution(nil, hub{runner: bodyRunner{"Lands the thing. Nothing attached."}, repo: "o/r"},
		"", 231, io.Discard); err != nil {
		t.Fatalf("an honest description was refused: %v", err)
	}
}

// bodyRunner answers the one read the description check makes.
type bodyRunner struct{ body string }

func (b bodyRunner) run(name string, args ...string) ([]byte, error) {
	return []byte(b.body), nil
}

// dirRunner runs a command in one directory, so the range check can be pointed
// at a fixture repository rather than at whichever tree the test binary is in.
type dirRunner struct{ dir string }

func (d dirRunner) run(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = d.dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, stderr.String())
	}
	return out, nil
}

// A commit message is prose and this repository bans two characters in prose.
// prosecheck cannot see one, because a commit message is not a tracked file,
// and the subject comes from a pull request title that nobody reads twice.
func TestTheBannedCharactersAreRefusedInACommitMessage(t *testing.T) {
	for _, title := range []string{
		"operator: the job \u2014 which nobody ran \u2014 needed a database url",
		"operator: pages 3\u20134 of the runbook were wrong",
		"operator: the job -- which nobody ran -- needed a database url",
	} {
		t.Run(title, func(t *testing.T) {
			f := ready(green(), "CLEAN")
			f.pulls = []string{
				strings.Replace(openPull("CLEAN"),
					"operator: first sign-in needed a hand-built job", title, 1),
				mergedPull("aa11bb22cc33"),
			}
			out, err := attempt(f)
			if err == nil {
				t.Fatalf("a subject carrying a banned character was merged:\n%s", out)
			}
			if f.merged() {
				t.Fatal("it refused and issued the merge anyway")
			}
		})
	}
}

// A re-run adds a check run rather than replacing one, so a commit carries two
// reports under one name. Protection reads the newest, so this reads the
// newest: being stricter than the thing it stands in for would refuse a pull
// request whose re-run fixed it, and being looser would merge one whose re-run
// broke it. Both directions are here.
func TestTheNewestReportUnderANameIsTheOneThatCounts(t *testing.T) {
	t.Run("a re-run that fixed it is a pass", func(t *testing.T) {
		rs := with("engine", "completed", "failure")
		rs = append(rs, reported{"engine", "completed", "success", "2026-09-05T02:00:00Z"})
		f := ready(rs, "UNSTABLE")
		out, err := attempt(f)
		if err != nil {
			t.Fatalf("a context whose re-run is green was refused: %v\n%s", err, out)
		}
	})
	t.Run("a stale success does not outrank a newer failure", func(t *testing.T) {
		rs := green()
		rs = append(rs, reported{"engine", "completed", "failure", "2026-09-05T02:00:00Z"})
		f := ready(rs, "UNSTABLE")
		out, err := attempt(f)
		if err == nil {
			t.Fatalf("an older green report was read over a newer red one:\n%s", out)
		}
	})
}

// A required context can arrive as a commit status rather than as a check run.
// None of the nine does today, and a command that read only check runs would
// report such a context as never having run and refuse a pull request that was
// green, so both endpoints are read.
func TestARequiredContextArrivingAsACommitStatusIsRead(t *testing.T) {
	f := ready(without("runner"), "CLEAN")
	f.statuses = `{"state":"success","statuses":[` +
		`{"id":9,"context":"runner","state":"success","created_at":"` + when + `"}],"total_count":1}`
	out, err := attempt(f)
	if err != nil {
		t.Fatalf("a required context reported as a commit status was not counted: %v\n%s", err, out)
	}
	t.Run("and a pending one is still not a pass", func(t *testing.T) {
		g := ready(without("runner"), "CLEAN")
		g.statuses = `{"state":"pending","statuses":[` +
			`{"id":9,"context":"runner","state":"pending","created_at":"` + when + `"}],"total_count":1}`
		_, err := attempt(g)
		if err == nil {
			t.Fatal("a pending commit status was read as a pass")
		}
		// And it is read as having reached no verdict rather than as having
		// concluded the word "pending", which is the same distinction a check
		// run gets: one is waited for, the other is fixed.
		if !strings.Contains(err.Error(), "no verdict yet") {
			t.Errorf("a pending status was reported as a verdict: %v", err)
		}
	})
}

// The subject is the shape every merge on main already has, and it must not
// double the number when the title already carries it.
func TestTheSubjectNamesThePullRequest(t *testing.T) {
	if got := subjectFor("console: align the operator overview", 225); got != "console: align the operator overview (#225)" {
		t.Errorf("got %q", got)
	}
	if got := subjectFor("console: align the operator overview (#225)", 225); got != "console: align the operator overview (#225)" {
		t.Errorf("it doubled the number: %q", got)
	}
}

// A pull request nobody can merge is refused before anything else happens, and
// each of these is a different sentence because each sends somebody somewhere
// different.
func TestAPullRequestThatIsNotOpenIsRefused(t *testing.T) {
	cases := []struct{ what, answer, says string }{
		{"already merged", mergedPull("aa11bb22cc33"), "already merged"},
		{"closed", strings.Replace(openPull("DIRTY"), `"state":"OPEN"`, `"state":"CLOSED"`, 1), "not OPEN"},
		{"a draft", strings.Replace(openPull("DRAFT"), `"isDraft":false`, `"isDraft":true`, 1), "nobody has said it is finished"},
	}
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			f := ready(green(), "CLEAN")
			f.pulls = []string{c.answer}
			out, err := attempt(f)
			if err == nil {
				t.Fatalf("it merged a pull request that is %s:\n%s", c.what, out)
			}
			if f.merged() {
				t.Fatal("it refused and issued the merge anyway")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("the refusal does not say %q: %v", c.says, err)
			}
		})
	}
}

// A dry run checks everything and merges nothing, which is what makes this
// command safe to point at a pull request before deciding.
func TestADryRunIssuesNoMerge(t *testing.T) {
	f := ready(green(), "CLEAN")
	var out bytes.Buffer
	if err := merge(hub{runner: f, repo: "antifailure/antifailure"}, f, 231, "", true, 2, 0, &out); err != nil {
		t.Fatalf("a dry run over a green pull request failed: %v\n%s", err, out.String())
	}
	if f.merged() {
		t.Fatal("a dry run merged the pull request")
	}
	if !strings.Contains(out.String(), "nothing was merged") {
		t.Errorf("a dry run did not say it merged nothing:\n%s", out.String())
	}
}

// The refusal has to list every reason at once. A command that reports the
// first problem and stops sends somebody round the loop once per problem, and
// each loop is a push and twenty minutes of CI.
func TestARefusalNamesEveryReasonAtOnce(t *testing.T) {
	rs := without("www")
	for i := range rs {
		if rs[i].name == "engine" {
			rs[i].conclusion = "failure"
		}
	}
	f := ready(rs, "BLOCKED")
	_, err := attempt(f)
	if err == nil {
		t.Fatal("a pull request with three separate problems was merged")
	}
	for _, want := range []string{"engine", "www", "BLOCKED"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

// THE CONTRACT WITH THE OTHER SIDE OF THE BOUNDARY.
//
// Everything above this line answers from a fixture this file wrote. That is
// how a version of this command shipped asking `gh pr view` for a field called
// `merged`, which does not exist, with 25 green tests behind it. The tests
// below are about the shapes the API really produces.

// The half that needs no network, and the half that fails silently. A field
// the struct reads and the request never asks for decodes as a zero value with
// no error at all: `isDraft` unasked means every pull request looks ready,
// `mergedAt` unasked means every merged one looks open.
func TestTheRequestAndTheStructAskForTheSameFields(t *testing.T) {
	if drift := fieldDrift(pullFieldNames(), structFieldNames()); len(drift) > 0 {
		for _, d := range drift {
			t.Error(d)
		}
	}
	// A positive control on the reflection itself. If structFieldNames stopped
	// finding tags it would return nothing, agree with everything, and this
	// file would have a check that cannot say no.
	if got := len(structFieldNames()); got != len(pullFieldNames()) || got == 0 {
		t.Fatalf("the struct has %d json tags against %d requested fields",
			got, len(pullFieldNames()))
	}
}

// Both directions of that drift, on synthetic lists, because each one is a
// different bug and each gets a different sentence.
func TestFieldDriftNamesBothDirections(t *testing.T) {
	t.Run("read but never asked for", func(t *testing.T) {
		drift := fieldDrift([]string{"number"}, []string{"number", "mergedAt"})
		if len(drift) != 1 || !strings.Contains(drift[0], "zero value") {
			t.Fatalf("got %v", drift)
		}
	})
	t.Run("asked for but never read", func(t *testing.T) {
		drift := fieldDrift([]string{"number", "title"}, []string{"number"})
		if len(drift) != 1 || !strings.Contains(drift[0], "nothing reads it") {
			t.Fatalf("got %v", drift)
		}
	})
	t.Run("agreement", func(t *testing.T) {
		if drift := fieldDrift([]string{"a", "b"}, []string{"b", "a"}); len(drift) != 0 {
			t.Fatalf("agreement was reported as drift: %v", drift)
		}
	})
}

// fieldsFake answers the four reads that -check-fields makes.
func fieldsFake() *fake {
	return &fake{
		pulls:      []string{realOpenPull},
		protection: protectionBody,
		checkRuns:  checkRunsBody(green()),
		statuses:   noStatuses,
	}
}

func fields(f *fake) (string, error) {
	var out bytes.Buffer
	err := checkFields(hub{runner: f, repo: "antifailure/antifailure"}, 235, &out)
	return out.String(), err
}

// The positive control for the contract check, against the payloads gh really
// returns.
func TestCheckFieldsPassesOnTheRealShapes(t *testing.T) {
	out, err := fields(fieldsFake())
	if err != nil {
		t.Fatalf("the real shapes were reported as wrong: %v\n%s", err, out)
	}
	for _, want := range []string{
		"the request and the struct ask the same",
		"gh has every field pullFields asks for",
		"a check run carries the five fields read",
		"protection carries the required list",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("it never reported %q, so it did not check it:\n%s", want, out)
		}
	}
}

// THE EXACT CASE THAT PRODUCED THIS. gh refuses a field it does not have, and
// the refusal names the field.
func TestCheckFieldsRefusesAFieldTheAPIDoesNotHave(t *testing.T) {
	f := fieldsFake()
	// The words gh really answered with, on the first real run of this command.
	f.pullErr = errors.New(`gh pr view 235 --repo antifailure/antifailure --json ` +
		pullFields + `: exit status 1: Unknown JSON field: "merged"`)
	out, err := fields(f)
	if err == nil {
		t.Fatalf("a refused field list was reported as fine:\n%s", out)
	}
	if !strings.Contains(err.Error(), "refused") {
		t.Errorf("the failure does not say the field list was refused: %v", err)
	}
}

// gh accepted the names and the response does not carry one. Different bug,
// same consequence: a field read as a zero value.
func TestCheckFieldsRefusesAResponseMissingAKeyItAskedFor(t *testing.T) {
	f := fieldsFake()
	f.pulls = []string{strings.Replace(realOpenPull, `"mergedAt":null,`, ``, 1)}
	out, err := fields(f)
	if err == nil {
		t.Fatalf("a response missing a requested key passed:\n%s", out)
	}
	if !strings.Contains(err.Error(), "mergedAt") {
		t.Errorf("the failure does not name the missing key: %v", err)
	}
}

// The check run shape, which is where the nine required contexts are read from.
func TestCheckFieldsRefusesACheckRunMissingAFieldItReads(t *testing.T) {
	f := fieldsFake()
	f.checkRuns = `{"total_count":1,"check_runs":[{"id":1,"name":"engine","status":"completed"}]}`
	out, err := fields(f)
	if err == nil {
		t.Fatalf("a check run with no conclusion field passed:\n%s", out)
	}
	for _, want := range []string{"conclusion", "started_at"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not name %q: %v", want, err)
		}
	}
}

// The required list is the thing every refusal rests on. An empty one would
// let every merge past the context check.
func TestCheckFieldsRefusesProtectionWithNoRequiredList(t *testing.T) {
	f := fieldsFake()
	f.protection = `{"required_status_checks":{"strict":false,"checks":[]}}`
	out, err := fields(f)
	if err == nil {
		t.Fatalf("protection carrying no required list passed:\n%s", out)
	}
	if !strings.Contains(err.Error(), "required_status_checks") {
		t.Errorf("the failure does not name the key it could not find: %v", err)
	}
}

// What it could NOT check is named rather than passed over. Reading branch
// protection needs administration rights, so a token without them reaches this
// every time, and a check that quietly counted that as a pass would be the
// defect this repository keeps finding in its own instruments.
func TestCheckFieldsNamesWhatItCouldNotCheck(t *testing.T) {
	f := fieldsFake()
	f.protErr = errors.New("gh: HTTP 403: Resource not accessible by personal access token")
	out, err := fields(f)
	if err != nil {
		t.Fatalf("it failed over a read it never claimed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "not checked") || !strings.Contains(out, "administration rights") {
		t.Errorf("it did not say which read it could not make:\n%s", out)
	}
	// The commit here carries no status, so that gap is named too rather than
	// counted as a check of the status shape.
	if !strings.Contains(out, "carries none") {
		t.Errorf("it did not say the commit status shape went unexamined:\n%s", out)
	}
}

// The readback that proves a merge carried its sign-off used to run only after
// a merge, which meant it only ever ran under a fixture. It runs against any
// merged pull request now, and both directions are proved: a merge commit that
// carries the trailer and one that does not. The unsigned case is real, and it
// is what `337f4b70` and its five siblings look like.
func TestTheReadbackRunsAgainstAMergedPullRequest(t *testing.T) {
	own := "Signed-off-by: Vir Sanghavi <67278851+VirSanghavi@users.noreply.github.com>"
	t.Run("a merge that carried it", func(t *testing.T) {
		f := ready(green(), "CLEAN")
		f.pulls = []string{mergedPull("3692ab054444d634939b5df4bac89eb95465acd2")}
		var out bytes.Buffer
		if err := confirm(hub{runner: f, repo: "antifailure/antifailure"}, 235, own, 2, 0, &out); err != nil {
			t.Fatalf("a signed merge commit was reported as unsigned: %v\n%s", err, out.String())
		}
		if f.merged() {
			t.Fatal("the readback merged something")
		}
		// It has to SAY it read the commit. A readback that returns quietly is
		// indistinguishable from one that did not run, which is the whole
		// reason this path is being exercised on its own.
		if !strings.Contains(out.String(), "the commit carries the sign-off") {
			t.Errorf("it never said it had read the commit back:\n%s", out.String())
		}
	})
	t.Run("a merge that did not", func(t *testing.T) {
		f := ready(green(), "CLEAN")
		f.pulls = []string{mergedPull("337f4b7000000000000000000000000000000000")}
		f.commit = commitBody("billing: delivery order could remove paid access (#232)\n")
		var out bytes.Buffer
		err := confirm(hub{runner: f, repo: "antifailure/antifailure"}, 232, own, 2, 0, &out)
		if err == nil {
			t.Fatalf("an unsigned merge commit was reported as fine:\n%s", out.String())
		}
		if !strings.Contains(err.Error(), "LANDED") {
			t.Errorf("the failure does not say the merge landed unsigned: %v", err)
		}
	})
}
