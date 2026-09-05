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
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
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

// attributionRules are the shapes this repository refuses in a commit message,
// a pull request description and a merge body. CLAUDE.md states the rule as
// "No attribution trailers. No Generated with Claude, no Co-Authored-By,
// nothing of that shape, anywhere", and this list is the instrument for it.
//
// THE RULE SAID "NOTHING OF THAT SHAPE" AND THIS MATCHED THREE KEYS. That gap
// is why the list exists as a list. A harness instructed this session to end
// every commit message with `Claude-Session: <url>` and every pull request
// description with the bare url, and the old expression, three literal keys
// anchored to a line, saw neither. The rule was right and the gate was narrower
// than the rule, which is the exact shape of defect this repository keeps
// finding in its own instruments.
//
// THE BARE URL IS THE ONE THAT MATTERS. A pull request description was to carry
// the session link with NO KEY IN FRONT OF IT, so every key anchored rule in
// this file, present and future, is blind to it by construction. It is matched
// as a url anywhere in the text instead, which is the only way to see a trailer
// that is not shaped like a trailer.
//
// BROAD ON PURPOSE, AND THE ASYMMETRY IS THE ARGUMENT. A false positive costs
// somebody one rephrased line before a merge. A miss puts a permanent
// attribution trailer in the history of a public repository, where the remedy
// is rewriting main and breaking every clone. The error names which rule fired,
// so a wrong refusal is legible rather than mysterious.
var attributionRules = []struct {
	name    string
	pattern *regexp.Regexp
	why     string
}{
	{
		"a co-author trailer",
		regexp.MustCompile(`(?im)^[ \t]*Co-authored-by[ \t]*:`),
		"a commit here is authored by the person who wrote it and signed off by the person relaying it, and nothing else names a second author",
	},
	{
		"a generator trailer",
		regexp.MustCompile(`(?im)^[ \t]*Generated-(?:with|by)[ \t]*:`),
		"what generated a commit is not a fact about the change, and the message is for the failure the change fixes",
	},
	{
		"an assistant session trailer",
		regexp.MustCompile(`(?im)^[ \t]*[A-Za-z][A-Za-z0-9]*-Session[ \t]*:`),
		"a session identifier attributes the commit to a tool run rather than describing the change, and it outlives the session it names",
	},
	{
		"a link to an assistant session",
		regexp.MustCompile(`(?i)\bclaude\.ai/code/(?:session|artifact)`),
		"the same attribution with the key taken off, which is how it arrives in a pull request description, where no key anchored rule can see it",
	},
	{
		"the generated-with footer",
		regexp.MustCompile(`(?im)^.*\bGenerated with[ \t]+\[?(?:Claude|Cursor|Copilot|Codex)\b`),
		"the prose half of the same footer, which is a sentence rather than a trailer and which a key anchored rule therefore misses",
	},
}

// attributionIn reports every rule a text breaks, as sentences a person can act
// on. Every rule is reported rather than only the first, because a footer
// carries the prose line and the trailer together and fixing one at a time is
// two refused merges instead of one.
func attributionIn(what, text string) []string {
	var problems []string
	for _, r := range attributionRules {
		if found := r.pattern.FindString(text); found != "" {
			problems = append(problems, fmt.Sprintf(
				"the %s carries %s, %q. This repository puts no attribution trailer in a commit: %s",
				what, r.name, strings.TrimSpace(found), r.why))
		}
	}
	return problems
}

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
			return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, said)
		}
		return out, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out, nil
}

// pull is the part of a pull request this command reasons about.
type pull struct {
	Number           int        `json:"number"`
	Title            string     `json:"title"`
	State            string     `json:"state"`
	IsDraft          bool       `json:"isDraft"`
	Closed           bool       `json:"closed"`
	MergedAt         *time.Time `json:"mergedAt"`
	MergeStateStatus string     `json:"mergeStateStatus"`
	HeadRefOid       string     `json:"headRefOid"`
	HeadRefName      string     `json:"headRefName"`
	BaseRefName      string     `json:"baseRefName"`
	MergeCommit      struct {
		Oid string `json:"oid"`
	} `json:"mergeCommit"`
}

// merged reports whether the pull request has been merged.
//
// `mergedAt` rather than `merged`, and that is not a preference. This asked gh
// for a field called `merged` first, every test passed against a fixture that
// invented it, and the first run against the real API failed with "Unknown
// JSON field: merged". gh exposes `mergedAt`, which is null until the merge
// happens and a timestamp afterwards, and `state`, which becomes MERGED. The
// timestamp is the fact and the state corroborates it.
//
// The fixtures in the tests are now the bytes gh really returned for an open
// pull request and for a merged one, because a fixture nobody compared against
// the real thing is a test of this file's imagination.
func (p pull) merged() bool { return p.MergedAt != nil || p.State == "MERGED" }

// pullFields is the exact `--json` list, kept in one place so the struct above
// and the request cannot drift apart. Every name in it is one gh accepts; an
// invented one is not a silent nil, it is an error that names the field.
const pullFields = "number,title,state,isDraft,closed,mergedAt,mergeStateStatus," +
	"headRefOid,headRefName,baseRefName,mergeCommit"

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

// The four reads this command makes, as the argument lists that make them.
//
// Named here rather than written inline so that the field check below runs the
// SAME calls the merge path runs. A field check against a copy of the call is
// a check of the copy, and the copy is the thing that stays right while the
// original drifts.
func pullArgs(repo string, number int) []string {
	return []string{"pr", "view", fmt.Sprint(number), "--repo", repo, "--json", pullFields}
}

func checkRunArgs(repo, sha string) []string {
	return []string{"api", fmt.Sprintf("repos/%s/commits/%s/check-runs?per_page=100", repo, sha)}
}

func statusArgs(repo, sha string) []string {
	return []string{"api", fmt.Sprintf("repos/%s/commits/%s/status?per_page=100", repo, sha)}
}

func protectionArgs(repo, branch string) []string {
	return []string{"api", fmt.Sprintf("repos/%s/branches/%s/protection", repo, branch)}
}

func commitArgs(repo, sha string) []string {
	return []string{"api", fmt.Sprintf("repos/%s/commits/%s", repo, sha)}
}

// pull reads one pull request.
func (h hub) pull(number int) (pull, error) {
	out, err := h.gh(pullArgs(h.repo, number)...)
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
	out, err := h.gh(protectionArgs(h.repo, branch)...)
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
	out, err := h.gh(checkRunArgs(h.repo, sha)...)
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

	out, err = h.gh(statusArgs(h.repo, sha)...)
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
	out, err := h.gh(commitArgs(h.repo, sha)...)
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
	if problems := attributionIn("body", given); len(problems) > 0 {
		return "", errors.New(strings.Join(problems, "\n  "))
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
			_, _ = fmt.Fprintf(out, "ok  %-40s success\n", name)
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
		} else if p.merged() && p.MergeCommit.Oid != "" {
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
			_, _ = fmt.Fprintf(out, "ok  %-40s %s\n", "the commit carries the sign-off", p.MergeCommit.Oid)
			return nil
		} else if !p.merged() && p.Closed {
			return fmt.Errorf("pull request %d is closed and not merged. Nothing landed", number)
		} else {
			last = fmt.Errorf("pull request %d reports state %s with merge commit %q",
				number, p.State, p.MergeCommit.Oid)
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

// pullFieldNames is the request's field list, split.
func pullFieldNames() []string { return strings.Split(pullFields, ",") }

// structFieldNames is every json tag on the pull struct.
//
// Read by reflection rather than listed, because a list would be the third
// place the same names live and the one nobody updates.
func structFieldNames() []string {
	var out []string
	t := reflect.TypeOf(pull{})
	for i := 0; i < t.NumField(); i++ {
		if tag := t.Field(i).Tag.Get("json"); tag != "" && tag != "-" {
			out = append(out, strings.Split(tag, ",")[0])
		}
	}
	return out
}

// fieldDrift compares what the struct reads against what the request asks for.
//
// This is the half of the contract that needs no network, and it is the half
// that goes wrong silently. A field asked for and never read is dead weight; a
// field READ and never asked for decodes as a zero value with no error at all,
// which is a merge command quietly believing a pull request is not a draft, or
// not merged, because nobody requested the field that says so.
//
// The other half, whether the API has these fields at all, cannot be answered
// from here: gh answers it, by refusing an unknown name. That is what
// -check-fields is for, and it is why the two exist separately.
func fieldDrift(asked, read []string) []string {
	inAsked, inRead := map[string]bool{}, map[string]bool{}
	for _, f := range asked {
		inAsked[f] = true
	}
	for _, f := range read {
		inRead[f] = true
	}
	var problems []string
	for _, f := range read {
		if !inAsked[f] {
			problems = append(problems, fmt.Sprintf(
				"the pull struct reads %q and pullFields never asks for it, so it decodes as a "+
					"zero value with no error. Add it to pullFields", f))
		}
	}
	for _, f := range asked {
		if !inRead[f] {
			problems = append(problems, fmt.Sprintf(
				"pullFields asks for %q and nothing reads it. Remove it, or add the field that "+
					"needs it", f))
		}
	}
	sort.Strings(problems)
	return problems
}

// keysOf returns the top level keys of a JSON object, and says so when the
// bytes were not an object at all.
func keysOf(raw []byte) (map[string]bool, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("the response was not a JSON object: %w", err)
	}
	keys := map[string]bool{}
	for k := range object {
		keys[k] = true
	}
	return keys, nil
}

// missing names the fields a response does not carry.
func missing(keys map[string]bool, want []string) []string {
	var absent []string
	for _, f := range want {
		if !keys[f] {
			absent = append(absent, f)
		}
	}
	return absent
}

// checkFields asks the real API whether this command's field names exist.
//
// WHY IT EXISTS. This command asked `gh pr view` for a field called `merged`.
// There is no such field. All 25 of its tests passed, because every one
// answered from a fixture this file had written and the fixture invented the
// field the code was reading. The first real invocation died on the first call
// with `Unknown JSON field: "merged"`. A suite that builds its own input can be
// complete and still describe a system that does not exist.
//
// So this is the check that reads the other side of the boundary. It runs the
// same argument lists the merge path runs, against a real pull request, and
// reports a name the API does not have or a key a real response does not carry.
//
// WHAT IT DOES NOT CLAIM. Branch protection needs administration rights, so a
// token without them cannot read it. That is reported as NOT CHECKED by name
// rather than passed over, and the same for a commit that carries no check run
// or no commit status: an element shape cannot be examined in a list with no
// elements. The rule this repository holds to is to say what was not checked,
// and the claim this makes is exactly the reads it managed.
//
// It is worth knowing that all four reads already fail CLOSED on the merge
// path. A wrong name in pullFields makes gh refuse; a wrong key under
// check-runs or status leaves an empty list, which reads as a required context
// that never reported; a wrong key under protection leaves an empty required
// list, which reads as nine contexts protection no longer requires. Every one
// of those refuses the merge. This exists so that the refusal arrives before
// somebody is standing at a merge button, and so that it names the field.
// checkAttribution refuses an attribution trailer in every commit a range adds
// and, when a pull request is named, in its description as well.
//
// THREE SURFACES, AND ONLY ONE OF THEM WAS GUARDED. bodyFor guards the body
// this command writes into a squash commit. It cannot see the messages of the
// commits a branch already carries, and it cannot see the pull request's own
// description, which is public the moment it is opened and stays public whether
// or not anything is merged. A rule that holds only where this command happens
// to be looking is a rule somebody satisfies by not running this command.
//
// So this runs in CI, on the range the pull request adds, beside the sign-off
// check that already computes that range and already knows how to compute it
// correctly: HEAD^1..HEAD^2 on the merge commit a pull request checkout is,
// which is the commits the branch ADDS rather than everything since its base
// moved.
//
// MERGE COMMITS ARE SKIPPED for the same reason the sign-off step skips them.
// A merge is not a contribution and its message is written by git.
//
// It reports every offending commit rather than stopping at the first, because
// a branch instructed to add a trailer added it to all of them, and fixing them
// one refused run at a time is the slowest possible way to learn that.
func checkAttribution(r runner, h hub, rang string, number int, out io.Writer) error {
	var problems []string
	// examined counts SURFACES asked for, not commits found. A range that
	// holds no commits has still been examined, and on `main` that is the
	// normal answer rather than a check that ran over nothing: the count is
	// printed either way so the difference is visible instead of implied.
	examined, commits := 0, 0

	if rang != "" {
		examined++
		listed, err := r.run("git", "rev-list", "--no-merges", rang)
		if err != nil {
			return fmt.Errorf("listing the commits in %s: %w", rang, err)
		}
		for _, sha := range strings.Fields(string(listed)) {
			message, err := r.run("git", "log", "-1", "--format=%B", sha)
			if err != nil {
				return fmt.Errorf("reading %s: %w", sha, err)
			}
			commits++
			problems = append(problems, attributionIn("message of "+short(sha), string(message))...)
		}
		// The ok line comes AFTER the verdict for this surface, not before it.
		// It printed first and said "no commit attributes itself" three lines
		// above the list of commits that did, which is a check reporting a
		// pass it had not earned in the same breath as the failure.
		if len(problems) == 0 {
			_, _ = fmt.Fprintf(out, "ok  %-44s %d commit(s)\n", "no commit in "+rang+" attributes itself", commits)
		}
	}

	if number > 0 {
		raw, err := h.gh("pr", "view", fmt.Sprint(number), "--repo", h.repo, "--json", "body", "--jq", ".body")
		if err != nil {
			return fmt.Errorf("reading the description of pull request %d: %w", number, err)
		}
		before := len(problems)
		examined++
		problems = append(problems, attributionIn(fmt.Sprintf("description of pull request %d", number), string(raw))...)
		if len(problems) == before {
			_, _ = fmt.Fprintf(out, "ok  %-44s\n", fmt.Sprintf("description of pull request %d attributes nothing", number))
		}
	}

	if examined == 0 {
		return errors.New("no surface was named, so nothing was checked. Pass -check-attribution <range>, a -pr, or both.\n" +
			"  A check that examined nothing and printed ok is worse than no check")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n  "))
	}
	return nil
}

// short is a sha as a person reads one.
func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func checkFields(h hub, number int, out io.Writer) error {
	var problems, unchecked []string

	if drift := fieldDrift(pullFieldNames(), structFieldNames()); len(drift) > 0 {
		problems = append(problems, drift...)
	} else {
		_, _ = fmt.Fprintf(out, "ok  %-44s %d fields\n", "the request and the struct ask the same", len(pullFieldNames()))
	}

	raw, err := h.gh(pullArgs(h.repo, number)...)
	if err != nil {
		// gh names the field it does not recognise, which is the whole point.
		return fmt.Errorf("the pull request field list was refused: %w", err)
	}
	keys, err := keysOf(raw)
	if err != nil {
		return fmt.Errorf("reading pull request %d: %w", number, err)
	}
	if absent := missing(keys, pullFieldNames()); len(absent) > 0 {
		problems = append(problems, fmt.Sprintf(
			"gh accepted %v and the response carries no such key", absent))
	} else {
		_, _ = fmt.Fprintf(out, "ok  %-44s #%d\n", "gh has every field pullFields asks for", number)
	}

	var p pull
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("reading pull request %d: %w", number, err)
	}

	raw, err = h.gh(checkRunArgs(h.repo, p.HeadRefOid)...)
	if err != nil {
		return fmt.Errorf("reading the check runs on %s: %w", p.HeadRefOid, err)
	}
	var runs struct {
		CheckRuns []map[string]json.RawMessage `json:"check_runs"`
	}
	if err := json.Unmarshal(raw, &runs); err != nil {
		return fmt.Errorf("reading the check runs on %s: %w", p.HeadRefOid, err)
	}
	switch {
	case len(runs.CheckRuns) == 0:
		unchecked = append(unchecked, fmt.Sprintf(
			"the shape of a check run, because %s carries none", p.HeadRefOid))
	default:
		absent := map[string]bool{}
		for _, r := range runs.CheckRuns {
			for _, f := range []string{"id", "name", "status", "conclusion", "started_at"} {
				if _, ok := r[f]; !ok {
					absent[f] = true
				}
			}
		}
		if len(absent) > 0 {
			problems = append(problems, fmt.Sprintf(
				"a check run carries none of %v, and this command reads them", sortedKeys(absent)))
		} else {
			_, _ = fmt.Fprintf(out, "ok  %-44s %d runs\n", "a check run carries the five fields read", len(runs.CheckRuns))
		}
	}

	raw, err = h.gh(statusArgs(h.repo, p.HeadRefOid)...)
	if err != nil {
		return fmt.Errorf("reading the commit statuses on %s: %w", p.HeadRefOid, err)
	}
	var statuses struct {
		Statuses []map[string]json.RawMessage `json:"statuses"`
	}
	if err := json.Unmarshal(raw, &statuses); err != nil {
		return fmt.Errorf("reading the commit statuses on %s: %w", p.HeadRefOid, err)
	}
	if keys, err := keysOf(raw); err == nil && !keys["statuses"] {
		problems = append(problems, "the commit status response carries no `statuses` key, and "+
			"this command reads one. A required context reported as a status would be invisible")
	} else if len(statuses.Statuses) == 0 {
		unchecked = append(unchecked, fmt.Sprintf(
			"the shape of a commit status, because %s carries none. Every required context "+
				"on this repository is a check run", p.HeadRefOid))
		_, _ = fmt.Fprintf(out, "ok  %-44s\n", "the commit status response has its list")
	} else {
		absent := map[string]bool{}
		for _, st := range statuses.Statuses {
			for _, f := range []string{"id", "context", "state", "created_at"} {
				if _, ok := st[f]; !ok {
					absent[f] = true
				}
			}
		}
		if len(absent) > 0 {
			problems = append(problems, fmt.Sprintf(
				"a commit status carries none of %v, and this command reads them", sortedKeys(absent)))
		} else {
			_, _ = fmt.Fprintf(out, "ok  %-44s %d statuses\n", "a commit status carries the four fields read", len(statuses.Statuses))
		}
	}

	raw, err = h.gh(protectionArgs(h.repo, p.BaseRefName)...)
	switch {
	case err != nil:
		unchecked = append(unchecked, fmt.Sprintf(
			"the shape of branch protection on %s, because this token cannot read it. That "+
				"read needs administration rights, and it is proved on the real path instead: "+
				"a wrong key there leaves an empty required list, which refuses every merge",
			p.BaseRefName))
	default:
		var body struct {
			RequiredStatusChecks struct {
				Contexts []string `json:"contexts"`
			} `json:"required_status_checks"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			problems = append(problems, fmt.Sprintf("reading branch protection: %v", err))
		} else if len(body.RequiredStatusChecks.Contexts) == 0 {
			problems = append(problems, "branch protection carries no "+
				"`required_status_checks.contexts`, and that is the list this command refuses on. "+
				"An empty one would let every merge past the context check")
		} else {
			_, _ = fmt.Fprintf(out, "ok  %-44s %d contexts\n",
				"protection carries the required list", len(body.RequiredStatusChecks.Contexts))
		}
	}

	for _, u := range unchecked {
		_, _ = fmt.Fprintf(out, "not checked  %s\n", u)
	}
	if len(problems) > 0 {
		return fmt.Errorf("the field names this command uses do not match the API.\n  %s",
			strings.Join(problems, "\n  "))
	}
	return nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func main() {
	repo := flag.String("repo", "", "owner/name, defaulting to the repository gh resolves here")
	number := flag.Int("pr", 0, "the pull request to merge")
	bodyText := flag.String("body", "", "the squash body, which this command appends the sign-off to")
	bodyFile := flag.String("body-file", "", "read the squash body from a file")
	dry := flag.Bool("dry-run", false, "check everything and print the merge command without running it")
	fields := flag.Bool("check-fields", false, "ask the real API whether this command's field names exist, and merge nothing")
	attrib := flag.String("check-attribution", "", "a git range whose commit messages must carry no attribution trailer, and merge nothing")
	only := flag.Bool("confirm-only", false, "read an already merged pull request and prove its commit carries the sign-off")
	attempts := flag.Int("confirm-attempts", 10, "how many times to ask whether the merge landed")
	interval := flag.Duration("confirm-interval", 3*time.Second, "how long to wait between asks")
	flag.Parse()

	if *number == 0 && len(flag.Args()) > 0 {
		if _, err := fmt.Sscanf(flag.Args()[0], "%d", number); err != nil {
			fail("%q is not a pull request number", flag.Args()[0])
		}
	}

	// -check-attribution runs on a RANGE and needs no pull request, which is
	// why it is handled before the number is demanded. A push to main has a
	// range and no pull request, and that is exactly the case where an
	// attribution trailer would otherwise land unread.
	if *attrib != "" {
		r := local{}
		repoName := *repo
		if repoName == "" && *number > 0 {
			out, err := r.run("gh", "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
			if err != nil {
				fail("working out which repository this is: %v\n  Pass -repo owner/name.", err)
			}
			repoName = strings.TrimSpace(string(out))
		}
		if err := checkAttribution(r, hub{runner: r, repo: repoName}, *attrib, *number, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "\nprmerge: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("prmerge: nothing here attributes itself to whatever wrote it\n")
		return
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

	h := hub{runner: r, repo: *repo}

	// Two read only modes, both of which merge nothing.
	//
	// -check-fields exists because this command once asked gh for a field that
	// does not exist and every test passed anyway. -confirm-only exists
	// because the readback that proves a merge carried its sign-off used to
	// run only after a merge, which meant it only ever ran under a fixture. It
	// runs against any merged pull request now, so the code path that decides
	// whether a commit is signed can be exercised on a real commit without
	// merging anything to exercise it.
	switch {
	case *fields:
		if err := checkFields(h, *number, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "\nprmerge: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("prmerge: every field name this command uses is one the API has\n")
		return
	case *only:
		name, email, err := identity(r)
		if err != nil {
			fail("%v", err)
		}
		own, err := trailer(name, email)
		if err != nil {
			fail("%v", err)
		}
		if err := confirm(h, *number, own, *attempts, *interval, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "\nprmerge: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := merge(h, r, *number, *bodyText, *dry, *attempts, *interval, os.Stdout); err != nil {
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
	_, _ = fmt.Fprintf(out, "prmerge: #%d %q\n  head %s on %s, base %s, mergeStateStatus %s\n",
		p.Number, p.Title, p.HeadRefOid, p.HeadRefName, p.BaseRefName, p.MergeStateStatus)

	var problems []string
	switch {
	case p.merged():
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
		_, _ = fmt.Fprintf(out, "ok  %-40s %d contexts on %s\n", "the required list is the live one", len(required), p.BaseRefName)
	}

	all, err := h.checks(p.HeadRefOid)
	if err != nil {
		return err
	}
	problems = append(problems, contexts(p.HeadRefOid, all, out)...)

	if why, ok := willMerge[p.MergeStateStatus]; ok {
		_, _ = fmt.Fprintf(out, "ok  %-40s %s, %s\n", "mergeStateStatus", p.MergeStateStatus, why)
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
		_, _ = fmt.Fprintf(out, "    not required by protection, and not blocking: %s\n", strings.Join(noted, ", "))
	}

	if len(problems) > 0 {
		return fmt.Errorf("refusing to merge #%d.\n  %s", number, strings.Join(problems, "\n  "))
	}

	if dry {
		_, _ = fmt.Fprintf(out, "\nwould run: gh %s\n", strings.Join(args, " "))
		_, _ = fmt.Fprintf(out, "dry run, so nothing was merged\n")
		return nil
	}

	if _, err := h.gh(args...); err != nil {
		return fmt.Errorf("the merge was refused: %w", err)
	}
	if err := confirm(h, number, own, attempts, interval, out); err != nil {
		return err
	}

	// The branch is left alone, deliberately. `gh pr merge --delete-branch`
	// deletes it even when the merge is REFUSED, and deleting the branch closes
	// the pull request, so one refusal discards the work. The merge is confirmed
	// by the time this prints, which is the only point at which deleting is
	// safe, and a person does it.
	_, _ = fmt.Fprintf(out, "\nmerged #%d. The branch is still there, on purpose.\n", number)
	_, _ = fmt.Fprintf(out, "  Delete it when you are satisfied: git push origin --delete %s\n", p.HeadRefName)
	return nil
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "prmerge: "+format+"\n", args...)
	os.Exit(1)
}
