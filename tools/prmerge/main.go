// Command prmerge squash merges a pull request and then proves the commit it
// created carries a Developer Certificate of Origin sign-off.
//
// WHY THIS EXISTS. Six pull requests were merged on 2026-09-05 with
// `gh pr merge --squash --body ""`. A squash commit made that way carries no
// `Signed-off-by` trailer, and the trailer is a required context: the
// `commits are attributed to their author` job failed on main in run
// 33934603009 with "1 commits have no Developer Certificate of Origin
// sign-off". cd.yml then did what it is built to do. Its gate job waits for CI
// on the same commit, polled 42 times, read the failure and refused with "CI
// concluded 'failure' on e836979577732c8d879cd96ce57ac1a447e12b91. Nothing is
// deployed", so its build, staging and production jobs were all skipped.
//
// Six merged pull requests did not reach staging because a merge commit was
// missing one line. That is the whole failure, and it is worth stating in that
// order: the missing trailer was not a cosmetic lapse, it was a deployment
// outage with a one line cause.
//
// WHY NOTHING CAUGHT IT. `.githooks/prepare-commit-msg` writes the trailer
// onto LOCAL commits, and a squash merge is made on GitHub's side, so no hook
// ran. `just authorship` skips merge commits and in any case cannot read a
// commit that does not exist until somebody presses the button. The CI gate
// does catch it, and only after the commit is on main, at which point the
// remedies are rewriting main or pushing another commit. So the convention
// lived in one person's memory and in the bodies of the two merges before
// those six, `cb3f30f1` and `b23e796b`, which both carry the trailer. A
// convention nothing can execute is a convention that holds until the day
// somebody is in a hurry.
//
// WHY IT SIGNS IN THE MERGER'S OWN NAME. This is clause (c) of the Developer
// Certificate of Origin: somebody relaying a contribution certifies that it
// came to them from a person who certified it, and signs as themselves. So the
// trailer is built from the merging clone's own `git config user.name` and
// `user.email` and from nothing else. A trailer naming anybody else would be a
// certification made in another person's name, which is worse than no trailer
// at all, so a body arriving here with somebody else's sign-off in it is
// refused rather than merged alongside one.
//
// WHAT IT REFUSES, each because it has happened here rather than as a
// precaution:
//
//   - a required context that is not the literal word `success` on the pull
//     request's exact head sha, including one that never reported;
//   - a required list that has drifted from the nine this file names;
//   - an unmergeable `mergeStateStatus`, read instead of `mergeable`, which
//     only answers whether the branch conflicts;
//   - an empty check list, which usually means the pull request conflicts and
//     not that the queue is busy;
//   - an identity that cannot produce a trailer, because a merge whose body
//     would carry no sign-off is the defect this command exists for;
//   - a subject or body carrying an em dash, an en dash or a double hyphen,
//     which this repository bans in commit messages and which prosecheck
//     cannot see, because a commit message is not a tracked file.
//
// WHAT IT NEVER DOES. It never passes `--delete-branch`. That flag deletes the
// branch even when the merge is REFUSED, and deleting the branch closes the
// pull request, so one refused merge silently discards the work. It prints the
// deletion command instead, for a human to run once the merge is confirmed.
//
// WHAT IT DELIBERATELY DOES NOT DO. It does not sign anybody else's commits.
// Relaying a contribution from a fork is the other operation in this area and
// it is a different act: the contributor stays the author of their commits,
// the maintainer adds their OWN trailer across the range, and the safe way to
// do that is a message filter rather than a rebase, because a filter cannot
// change content and a rebase re-applies patches and can resolve one wrongly.
// It is proved by the tree hash being identical before and after. That is a
// hand operation with a person's judgement in it, and automating it is not
// what this command is for. What this command owns is the squash commit that
// lands on main, and the only name it will ever write into a trailer is the
// name of the person running it.
//
// AND IT CHECKS ITS OWN WORK. After merging it reads the commit that landed and
// requires the trailer to be there. A tool that intends to write a trailer and
// cannot prove it did is the same class of defect as the one it fixes.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// required is the nine contexts main's protection blocks a merge on.
//
// Named here AND compared against the live list on every run. Named so a
// refusal can say which context is missing rather than print whatever the API
// happened to return, and compared so that this list going stale fails loudly:
// a tenth context added to protection and not added here would otherwise be a
// gate this command merges straight past, which is a check that cannot say no.
//
// `Antifailure` and `dogfood, against the control plane` are deliberately NOT
// here. Neither is required by protection, and three agents in one session read
// a red mark on one of them as a block and lost hours to it. A red
// non required context is reported by this command and does not stop it.
var required = []string{
	"engine",
	"control plane",
	"edition boundary",
	"enterprise",
	"runner",
	"www",
	"known vulnerabilities",
	"no credentials in the tree",
	"commits are attributed to their author",
}

// willMerge are the `mergeStateStatus` values this command will merge on, with
// why each is safe.
//
// `mergeable` is read nowhere in this file, on purpose. It answers a narrower
// question, whether the branch conflicts, and a pull request with no conflicts
// and a required check still running reports MERGEABLE. Reading it as
// permission to merge is how a merge gets attempted against a tree nothing has
// finished checking.
var willMerge = map[string]string{
	"CLEAN":     "every protection is satisfied",
	"HAS_HOOKS": "satisfied, with a repository pre-receive hook still to run",
	// UNSTABLE means a check protection does not require is red or still
	// running. That is the ordinary state of a pull request here, because
	// `Antifailure` and Dogfood are not required contexts, and refusing it
	// would refuse nearly every pull request this repository opens. The nine
	// required contexts are checked one by one above, so this state is not
	// being trusted for anything protection would have blocked.
	"UNSTABLE": "a check protection does not require is red or still running, which does not block a merge",
}

// willNotMerge are the states that stop this command, each with the sentence
// somebody reads when their merge did not happen.
var willNotMerge = map[string]string{
	"DIRTY": "the branch conflicts with its base, so there is nothing to squash yet. " +
		"Rebase or merge the base in, let the checks run again, and merge after that.",
	"BLOCKED": "GitHub is blocking this merge. A required review, a required context that " +
		"has not passed, or a protection this command cannot read. When it is a context, " +
		"the required list above says which one.",
	"BEHIND": "protection requires the branch to be up to date with its base and it is not. " +
		"Update it, wait for its checks to run against the new head, and merge then.",
	"DRAFT":   "the pull request is a draft. Mark it ready for review first.",
	"UNKNOWN": "GitHub has not finished working out whether this can merge. That is not a no and it is certainly not a yes. Ask again in a moment.",
}

// banned are the characters this repository does not allow in prose, and a
// commit message is prose.
//
// tools/prosecheck enforces this over tracked files, and a commit message is
// not a tracked file, so the subject taken from a pull request title and any
// body handed to this command are the one prose surface with no gate on it.
// The subject in particular is written by whoever opened the pull request and
// read again by nobody.
var banned = []struct {
	pattern *regexp.Regexp
	what    string
}{
	{regexp.MustCompile(`\x{2014}`), "an em dash"},
	{regexp.MustCompile(`\x{2013}`), "an en dash"},
	{regexp.MustCompile(`\s--\s`), "a double hyphen as punctuation"},
}

// attribution are the trailers this repository does not put in a commit, in
// the shape a key is matched: at the start of a line, case insensitively.
var attribution = regexp.MustCompile(`(?im)^(Co-authored-by|Generated-with|Generated-by):`)

// signOffLine finds any sign-off trailer in a body, so one naming somebody
// else can be refused rather than merged alongside the merger's own.
var signOffLine = regexp.MustCompile(`(?im)^Signed-off-by:.*$`)

// runner runs one external command and returns its standard output.
//
// One interface for both `gh` and `git` rather than two, because the assertion
// that matters most about this command is about the whole set of commands it
// issues: `--delete-branch` must never appear in any of them. A test can only
// make that assertion against a recording of every invocation, and two
// interfaces would give it two recordings and one blind spot.
type runner interface {
	run(name string, args ...string) ([]byte, error)
}

// local runs commands for real.
type local struct{}

func (local) run(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// The message matters more than the exit code here: `gh` puts the
		// reason a merge was refused on stderr and nothing else says it.
		if said := strings.TrimSpace(stderr.String()); said != "" {
			return out, fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, said)
		}
		return out, fmt.Errorf("%s %s: %v", name, strings.Join(args, " "), err)
	}
	return out, nil
}

// pull is the part of a pull request this command reasons about.
type pull struct {
	Number           int    `json:"number"`
	Title            string `json:"title"`
	State            string `json:"state"`
	IsDraft          bool   `json:"isDraft"`
	Merged           bool   `json:"merged"`
	MergeStateStatus string `json:"mergeStateStatus"`
	HeadRefOid       string `json:"headRefOid"`
	HeadRefName      string `json:"headRefName"`
	BaseRefName      string `json:"baseRefName"`
	MergeCommit      struct {
		Oid string `json:"oid"`
	} `json:"mergeCommit"`
}

// pullFields is the exact `--json` list, kept in one place so the struct above
// and the request cannot drift apart.
const pullFields = "number,title,state,isDraft,merged,mergeStateStatus,headRefOid,headRefName,baseRefName,mergeCommit"

// check is one reported context on a commit, whether it arrived as a check run
// or as a commit status.
//
// Both shapes are read. The nine required contexts are all check runs today,
// and a required context that was a commit status would be invisible to a
// command that read only check runs: it would report as never having run, and
// this command would refuse a pull request that was in fact green. Refusing
// wrongly is the safe direction and it is still wrong, and the cost of being
// right is one more request.
type check struct {
	Name       string
	Status     string
	Conclusion string
	At         time.Time
	ID         int64
}

// hub is GitHub, reached through whatever runner it was given.
type hub struct {
	runner runner
	repo   string
}

func (h hub) gh(args ...string) ([]byte, error) { return h.runner.run("gh", args...) }

// pull reads one pull request.
func (h hub) pull(number int) (pull, error) {
	out, err := h.gh("pr", "view", fmt.Sprint(number), "--repo", h.repo, "--json", pullFields)
	if err != nil {
		return pull{}, err
	}
	var p pull
	if err := json.Unmarshal(out, &p); err != nil {
		return pull{}, fmt.Errorf("reading pull request %d: %w", number, err)
	}
	return p, nil
}

// protection is the live required context list for a branch.
//
// A failure here is fatal rather than skipped. Reading branch protection needs
// administration rights, so a 403 means the person running this cannot confirm
// what blocks a merge, and merging on a list nobody confirmed is exactly the
// gate this command is supposed to be.
func (h hub) protection(branch string) ([]string, error) {
	out, err := h.gh("api", fmt.Sprintf("repos/%s/branches/%s/protection", h.repo, branch))
	if err != nil {
		return nil, fmt.Errorf("reading the required contexts on %s: %w\n"+
			"  This command will not merge on a required list it could not confirm. "+
			"Reading branch protection needs administration rights on the repository", branch, err)
	}
	var body struct {
		RequiredStatusChecks struct {
			Contexts []string `json:"contexts"`
		} `json:"required_status_checks"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		return nil, fmt.Errorf("reading the required contexts on %s: %w", branch, err)
	}
	return body.RequiredStatusChecks.Contexts, nil
}

// checks is everything that reported on a commit, check runs and statuses both.
func (h hub) checks(sha string) ([]check, error) {
	out, err := h.gh("api", fmt.Sprintf("repos/%s/commits/%s/check-runs?per_page=100", h.repo, sha))
	if err != nil {
		return nil, fmt.Errorf("reading the check runs on %s: %w", sha, err)
	}
	var runs struct {
		CheckRuns []struct {
			ID         int64     `json:"id"`
			Name       string    `json:"name"`
			Status     string    `json:"status"`
			Conclusion string    `json:"conclusion"`
			StartedAt  time.Time `json:"started_at"`
		} `json:"check_runs"`
	}
	if err := json.Unmarshal(out, &runs); err != nil {
		return nil, fmt.Errorf("reading the check runs on %s: %w", sha, err)
	}
	var all []check
	for _, r := range runs.CheckRuns {
		all = append(all, check{Name: r.Name, Status: r.Status, Conclusion: r.Conclusion, At: r.StartedAt, ID: r.ID})
	}

	out, err = h.gh("api", fmt.Sprintf("repos/%s/commits/%s/status?per_page=100", h.repo, sha))
	if err != nil {
		return nil, fmt.Errorf("reading the commit statuses on %s: %w", sha, err)
	}
	var statuses struct {
		Statuses []struct {
			ID        int64     `json:"id"`
			Context   string    `json:"context"`
			State     string    `json:"state"`
			CreatedAt time.Time `json:"created_at"`
		} `json:"statuses"`
	}
	if err := json.Unmarshal(out, &statuses); err != nil {
		return nil, fmt.Errorf("reading the commit statuses on %s: %w", sha, err)
	}
	for _, s := range statuses.Statuses {
		// A status has one field where a check run has two. `pending` is the
		// only one of its states that means no verdict yet, so it becomes a
		// status rather than a conclusion, and every other value becomes a
		// conclusion that is compared against `success` like any other.
		c := check{Name: s.Context, Status: "completed", Conclusion: s.State, At: s.CreatedAt, ID: s.ID}
		if s.State == "pending" {
			c.Status, c.Conclusion = "in_progress", ""
		}
		all = append(all, c)
	}
	return all, nil
}

// commitMessage is the full message of a commit on the remote.
func (h hub) commitMessage(sha string) (string, error) {
	out, err := h.gh("api", fmt.Sprintf("repos/%s/commits/%s", h.repo, sha))
	if err != nil {
		return "", fmt.Errorf("reading the commit %s: %w", sha, err)
	}
	var body struct {
		Commit struct {
			Message string `json:"message"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		return "", fmt.Errorf("reading the commit %s: %w", sha, err)
	}
	return body.Commit.Message, nil
}

// willCarry reports whether the command about to be issued really puts the
// trailer in the squash body.
//
// It reads the ARGUMENTS rather than the variable they were built from,
// because the arguments are what GitHub sees and the variable is only what
// this file believes. The defect that produced this command was
// `gh pr merge --squash --body ""`, which is an argument list, and this is the
// one line that looks at the argument list before it goes out. A command with
// no --body at all fails it too: an absent body is not a body that carries a
// sign-off, and gh would fill it in with something nobody chose.
func willCarry(args []string, own string) bool {
	for i, a := range args {
		if a == "--body" && i+1 < len(args) {
			return carries(args[i+1], own)
		}
	}
	return false
}

// mergeArgs is the whole command, built in one place so a test can read it.
//
// There is no `--delete-branch` here and there must never be one. It deletes
// the branch even when the merge is REFUSED, and deleting the branch closes the
// pull request, so a single refusal discards the work.
func mergeArgs(repo string, number int, subject, body string) []string {
	return []string{"pr", "merge", fmt.Sprint(number), "--repo", repo, "--squash",
		"--subject", subject, "--body", body}
}

// identity is the merging clone's own commit identity.
func identity(r runner) (string, string, error) {
	name, err := configValue(r, "user.name")
	if err != nil {
		return "", "", err
	}
	email, err := configValue(r, "user.email")
	if err != nil {
		return "", "", err
	}
	return name, email, nil
}

func configValue(r runner, key string) (string, error) {
	out, err := r.run("git", "config", "--get", key)
	if err != nil {
		return "", fmt.Errorf("reading %s from git config: %v\n"+
			"  The sign-off is built from it, so a merge made without one would carry no "+
			"trailer, which is the failure this command exists to prevent.\n"+
			"  Set it: git config %s \"...\"", key, err, key)
	}
	return strings.TrimSpace(string(out)), nil
}

// trailer builds the sign-off, and refuses to build a broken one.
//
// Every refusal here is the same refusal in different clothes: a merge whose
// body would carry no usable trailer must not happen. An empty name or address
// is the case that produced this command. A newline is the case that would
// produce a body with a trailer in it that is not a trailer, because git reads
// trailers by line.
func trailer(name, email string) (string, error) {
	name, email = strings.TrimSpace(name), strings.TrimSpace(email)
	switch {
	case name == "":
		return "", fmt.Errorf("git config user.name is empty, so there is no name to sign off in.\n" +
			"  Set it: git config user.name \"Your Name\"")
	case email == "":
		return "", fmt.Errorf("git config user.email is empty, so there is no address to sign off with.\n" +
			"  Set it: git config user.email you@example.com")
	case !strings.Contains(email, "@"):
		return "", fmt.Errorf("git config user.email is %q, which is not an address. A sign-off "+
			"has to name somebody who can be reached", email)
	case strings.ContainsAny(name, "\r\n") || strings.ContainsAny(email, "\r\n"):
		return "", fmt.Errorf("the commit identity contains a line break. git reads trailers by " +
			"line, so this would put something in the body that only looks like a sign-off")
	}
	return fmt.Sprintf("Signed-off-by: %s <%s>", name, email), nil
}

// subjectFor is the squash subject: the pull request's title with its number,
// which is the shape every merge on main already has.
func subjectFor(title string, number int) string {
	title = strings.TrimSpace(title)
	suffix := fmt.Sprintf("(#%d)", number)
	if strings.HasSuffix(title, suffix) {
		return title
	}
	return title + " " + suffix
}

// bodyFor puts the trailer at the end of the body, and refuses a body it must
// not sign.
//
// The trailer goes last and after a blank line, because git only reads a
// trailer out of the final paragraph. A sign-off in the middle of prose is not
// a trailer, it is a sentence that looks like one, and `git interpret-trailers`
// and the CI gate disagree about it.
func bodyFor(given, own string) (string, error) {
	given = strings.TrimRight(strings.ReplaceAll(given, "\r\n", "\n"), "\n \t")
	if found := attribution.FindString(given); found != "" {
		return "", fmt.Errorf("the body carries %q. This repository puts no attribution trailer "+
			"in a commit", strings.TrimSuffix(strings.TrimSpace(found), ":"))
	}
	for _, line := range signOffLine.FindAllString(given, -1) {
		if strings.TrimSpace(line) != own {
			return "", fmt.Errorf("the body already carries %q, and this command signs off as "+
				"%q.\n  Relaying somebody else's contribution is signed in your own name, "+
				"which is clause (c) of the Developer Certificate of Origin. A trailer naming "+
				"another person is a certification made in their name", strings.TrimSpace(line), own)
		}
	}
	if given == "" {
		return own, nil
	}
	if carries(given, own) {
		return given, nil
	}
	return given + "\n\n" + own, nil
}

// prose refuses the characters this repository bans in a commit message.
func prose(what, text string) []string {
	var problems []string
	for _, b := range banned {
		if b.pattern.MatchString(text) {
			problems = append(problems, fmt.Sprintf(
				"the %s carries %s. This repository writes a comma, a colon or a new sentence "+
					"instead, in commit messages as much as anywhere, and prosecheck cannot see "+
					"a commit message because it is not a tracked file", what, b.what))
		}
	}
	return problems
}

// latest keeps the newest report per context name.
//
// A commit carries more than one run under the same name: a re-run of a
// workflow adds a check run rather than replacing one, and this repository has
// a commit where `build` appears twice, once skipped and once successful.
// Protection reads the newest, so this reads the newest. Taking the oldest, or
// requiring every report under a name to be green, would refuse a pull request
// whose re-run fixed it, and this command must not be stricter than the thing
// it is standing in for.
func latest(all []check) map[string]check {
	sorted := make([]check, len(all))
	copy(sorted, all)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].At.Equal(sorted[j].At) {
			return sorted[i].At.Before(sorted[j].At)
		}
		return sorted[i].ID < sorted[j].ID
	})
	newest := map[string]check{}
	for _, c := range sorted {
		newest[c.Name] = c
	}
	return newest
}

// listDrift compares the required list this file names against the live one.
func listDrift(live []string) []string {
	want, have := map[string]bool{}, map[string]bool{}
	for _, c := range required {
		want[c] = true
	}
	for _, c := range live {
		have[c] = true
	}
	var problems []string
	for _, c := range live {
		if !want[c] {
			problems = append(problems, fmt.Sprintf(
				"protection requires %q and this command does not know about it, so it would "+
					"have merged without reading it. Add it to `required` in tools/prmerge", c))
		}
	}
	for _, c := range required {
		if !have[c] {
			problems = append(problems, fmt.Sprintf(
				"this command requires %q and protection does not, so it would refuse a merge "+
					"nothing is blocking. Remove it from `required` in tools/prmerge", c))
		}
	}
	sort.Strings(problems)
	return problems
}

// contexts is the verdict on the nine, and the whole reason for reading a
// commit's checks one by one rather than trusting a rollup.
//
// Only the literal word `success` passes. `skipped` and `cancelled` are the two
// that have to be said out loud: both render as an absence of red in a list,
// `skipped` means nothing ran, and `cancelled` is spelled the same way for a
// job that hit its own timeout, a run somebody stopped and a run superseded by
// a newer push. None of those is a verdict, and a thing that is not a verdict
// is not a pass.
func contexts(sha string, all []check, out io.Writer) []string {
	newest := latest(all)
	if len(all) == 0 {
		return []string{fmt.Sprintf(
			"nothing at all has reported on %s. An empty check list usually means the pull "+
				"request CONFLICTS rather than that the queue is busy, so read "+
				"mergeStateStatus before waiting for anything", sha)}
	}
	var problems []string
	for _, name := range required {
		c, reported := newest[name]
		switch {
		case !reported:
			problems = append(problems, fmt.Sprintf(
				"%q never reported on %s. A required context that is absent is not a context "+
					"that passed", name, sha))
		case c.Status != "completed":
			problems = append(problems, fmt.Sprintf(
				"%q is %s on %s, so it has reached no verdict yet", name, c.Status, sha))
		case c.Conclusion != "success":
			problems = append(problems, fmt.Sprintf(
				"%q concluded %q on %s. Only the literal word success is a pass: skipped means "+
					"nothing ran, and cancelled is how GitHub spells a timeout, a stopped run "+
					"and a superseded run alike", name, c.Conclusion, sha))
		default:
			fmt.Fprintf(out, "ok  %-40s success\n", name)
		}
	}
	return problems
}

// unrequired reports the contexts that are red and do not block anything.
//
// Printed rather than counted, because the opposite mistake is expensive here:
// `Antifailure` and `dogfood, against the control plane` are not required, and
// three agents in one session read a red mark on one of them as a block and
// lost hours to it.
func unrequired(all []check) []string {
	want := map[string]bool{}
	for _, c := range required {
		want[c] = true
	}
	var noted []string
	for name, c := range latest(all) {
		if want[name] || c.Conclusion == "success" {
			continue
		}
		state := c.Conclusion
		if c.Status != "completed" {
			state = c.Status
		}
		noted = append(noted, fmt.Sprintf("%s (%s)", name, state))
	}
	sort.Strings(noted)
	return noted
}

// carries reports whether a commit message ends a line with exactly this
// trailer.
//
// Line exact rather than a substring search: a message that mentions a
// sign-off in a sentence is not a commit that carries one, and the CI gate
// greps for `^Signed-off-by: `, so anything looser here would pass a commit the
// gate then fails, which is this command lying about the one thing it is for.
func carries(message, own string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(message, "\r\n", "\n"), "\n") {
		if strings.TrimRight(line, " \t") == own {
			return true
		}
	}
	return false
}

// confirm reads back what the merge actually produced.
//
// It polls because the merge commit is not always on the pull request the
// instant the merge returns, and an unconfirmed merge must not be reported as
// either done or broken. Running out of attempts is its own refusal, worded so
// that nobody reads "could not confirm" as "did not happen".
func confirm(h hub, number int, own string, attempts int, interval time.Duration, out io.Writer) error {
	var last error
	for attempt := 1; attempt <= attempts; attempt++ {
		p, err := h.pull(number)
		if err != nil {
			last = err
		} else if p.Merged && p.MergeCommit.Oid != "" {
			message, err := h.commitMessage(p.MergeCommit.Oid)
			if err != nil {
				return err
			}
			if !carries(message, own) {
				return fmt.Errorf("the merge LANDED as %s and the commit does not carry %q.\n"+
					"  main's `commits are attributed to their author` context will fail on it, "+
					"and cd.yml's gate refuses to deploy a commit whose CI failed, so staging "+
					"will not move.\n"+
					"  Do not rewrite main to fix this: it would break every existing clone and "+
					"the merge base of every open pull request. Push a signed commit on top "+
					"instead", p.MergeCommit.Oid, own)
			}
			fmt.Fprintf(out, "ok  %-40s %s\n", "the commit carries the sign-off", p.MergeCommit.Oid)
			return nil
		} else if !p.Merged && p.State == "CLOSED" {
			return fmt.Errorf("pull request %d is closed and not merged. Nothing landed", number)
		} else {
			last = fmt.Errorf("pull request %d reports merged=%v with merge commit %q",
				number, p.Merged, p.MergeCommit.Oid)
		}
		if attempt < attempts {
			time.Sleep(interval)
		}
	}
	return fmt.Errorf("the merge was accepted and could not be confirmed within the budget. "+
		"The last thing GitHub said was: %v.\n"+
		"  That is not a statement that the merge failed, it is a statement that nobody knows "+
		"whether the commit on main carries a sign-off. Read it: "+
		"git fetch origin && git log -1 --format='%%B' origin/main", last)
}

func main() {
	repo := flag.String("repo", "", "owner/name, defaulting to the repository gh resolves here")
	number := flag.Int("pr", 0, "the pull request to merge")
	bodyText := flag.String("body", "", "the squash body, which this command appends the sign-off to")
	bodyFile := flag.String("body-file", "", "read the squash body from a file")
	dry := flag.Bool("dry-run", false, "check everything and print the merge command without running it")
	attempts := flag.Int("confirm-attempts", 10, "how many times to ask whether the merge landed")
	interval := flag.Duration("confirm-interval", 3*time.Second, "how long to wait between asks")
	flag.Parse()

	if *number == 0 && len(flag.Args()) > 0 {
		if _, err := fmt.Sscanf(flag.Args()[0], "%d", number); err != nil {
			fail("%q is not a pull request number", flag.Args()[0])
		}
	}
	if *number <= 0 {
		fail("prmerge needs a pull request. Pass its number: just merge 231")
	}
	if *bodyText != "" && *bodyFile != "" {
		fail("pass -body or -body-file, not both")
	}
	if *bodyFile != "" {
		read, err := os.ReadFile(*bodyFile)
		if err != nil {
			fail("reading %s: %v", *bodyFile, err)
		}
		*bodyText = string(read)
	}

	r := local{}
	if *repo == "" {
		out, err := r.run("gh", "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
		if err != nil {
			fail("working out which repository this is: %v\n  Pass -repo owner/name.", err)
		}
		*repo = strings.TrimSpace(string(out))
	}

	if err := merge(hub{runner: r, repo: *repo}, r, *number, *bodyText, *dry, *attempts, *interval, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "\nprmerge: %v\n", err)
		os.Exit(1)
	}
}

// merge is the whole decision, kept out of main so that a test can run it end
// to end against recorded answers rather than against GitHub.
func merge(h hub, r runner, number int, given string, dry bool, attempts int, interval time.Duration, out io.Writer) error {
	name, email, err := identity(r)
	if err != nil {
		return err
	}
	own, err := trailer(name, email)
	if err != nil {
		return err
	}

	p, err := h.pull(number)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "prmerge: #%d %q\n  head %s on %s, base %s, mergeStateStatus %s\n",
		p.Number, p.Title, p.HeadRefOid, p.HeadRefName, p.BaseRefName, p.MergeStateStatus)

	var problems []string
	switch {
	case p.Merged:
		return fmt.Errorf("pull request %d is already merged", number)
	case p.State != "OPEN":
		return fmt.Errorf("pull request %d is %s, not OPEN", number, p.State)
	case p.IsDraft:
		return fmt.Errorf("pull request %d is a draft, so nobody has said it is finished. "+
			"Mark it ready for review first", number)
	case p.HeadRefOid == "":
		return fmt.Errorf("pull request %d reports no head sha, so there is no commit to check", number)
	}

	live, err := h.protection(p.BaseRefName)
	if err != nil {
		return err
	}
	if drift := listDrift(live); len(drift) > 0 {
		problems = append(problems, drift...)
	} else {
		fmt.Fprintf(out, "ok  %-40s %d contexts on %s\n", "the required list is the live one", len(required), p.BaseRefName)
	}

	all, err := h.checks(p.HeadRefOid)
	if err != nil {
		return err
	}
	problems = append(problems, contexts(p.HeadRefOid, all, out)...)

	if why, ok := willMerge[p.MergeStateStatus]; ok {
		fmt.Fprintf(out, "ok  %-40s %s, %s\n", "mergeStateStatus", p.MergeStateStatus, why)
	} else if why, known := willNotMerge[p.MergeStateStatus]; known {
		problems = append(problems, fmt.Sprintf("mergeStateStatus is %s: %s", p.MergeStateStatus, why))
	} else {
		problems = append(problems, fmt.Sprintf(
			"mergeStateStatus is %q, which this command does not recognise. An unfamiliar "+
				"answer is not a yes", p.MergeStateStatus))
	}

	subject := subjectFor(p.Title, number)
	body, err := bodyFor(given, own)
	if err != nil {
		problems = append(problems, err.Error())
	}
	problems = append(problems, prose("subject", subject)...)
	problems = append(problems, prose("body", body)...)

	args := mergeArgs(h.repo, number, subject, body)

	// The last line of defence, and it is not redundant with bodyFor. That one
	// builds a body; this one reads the command that is about to be issued and
	// refuses to run it if the body in it does not carry the trailer, whatever
	// built that body. The whole failure was a command that went out with
	// `--body ""`.
	if !willCarry(args, own) {
		problems = append(problems, fmt.Sprintf(
			"the squash body would not carry %q, so the commit on %s would have no Developer "+
				"Certificate of Origin sign-off. That is the exact defect this command exists "+
				"to prevent: six merges made that way failed main's authorship context and "+
				"cd.yml refused to deploy any of them", own, p.BaseRefName))
	}

	if noted := unrequired(all); len(noted) > 0 {
		fmt.Fprintf(out, "    not required by protection, and not blocking: %s\n", strings.Join(noted, ", "))
	}

	if len(problems) > 0 {
		return fmt.Errorf("refusing to merge #%d.\n  %s", number, strings.Join(problems, "\n  "))
	}

	if dry {
		fmt.Fprintf(out, "\nwould run: gh %s\n", strings.Join(args, " "))
		fmt.Fprintf(out, "dry run, so nothing was merged\n")
		return nil
	}

	if _, err := h.gh(args...); err != nil {
		return fmt.Errorf("the merge was refused: %v", err)
	}
	if err := confirm(h, number, own, attempts, interval, out); err != nil {
		return err
	}

	// The branch is left alone, deliberately. `gh pr merge --delete-branch`
	// deletes it even when the merge is REFUSED, and deleting the branch closes
	// the pull request, so one refusal discards the work. The merge is confirmed
	// by the time this prints, which is the only point at which deleting is
	// safe, and a person does it.
	fmt.Fprintf(out, "\nmerged #%d. The branch is still there, on purpose.\n", number)
	fmt.Fprintf(out, "  Delete it when you are satisfied: git push origin --delete %s\n", p.HeadRefName)
	return nil
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "prmerge: "+format+"\n", args...)
	os.Exit(1)
}
