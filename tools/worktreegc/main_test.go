package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Every test in this file runs the real decision path against a REAL git
// repository built in a temporary directory. Only `gh` is replaced, because
// GitHub's merged pull request list is the one signal that cannot be
// constructed locally. Ancestry, the commit counts, the worktree listing, the
// status of each working tree, the branch deletion and the backup ref are all
// done by git, so a test that says a branch survived means git still holds it.
//
// Nothing here asserts that the source contains a word. The structural shape of
// this program is not what went wrong: the previous predicate read correctly and
// was missing a signal, and only a run against a branch in that exact state can
// tell the difference.

// fakeGH answers `gh` and hands everything else to the real git.
type fakeGH struct {
	inner      local
	pulls      string
	ghCode     int
	ghErr      error
	fetchFails bool
	// lsof is the process listing to hand back. Empty means a listing that
	// holds only this test process, working in the package directory, which
	// is what a machine with no lane working in any fixture looks like.
	lsof    string
	lsofErr error
	calls   []string
}

func (f *fakeGH) run(dir, name string, args ...string) result {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if name == "gh" {
		return result{stdout: f.pulls, code: f.ghCode, err: f.ghErr, stderr: "gh was told to fail"}
	}
	if name == "lsof" {
		if f.lsofErr != nil {
			return result{code: -1, err: f.lsofErr}
		}
		if f.lsof == "" {
			wd, _ := os.Getwd()
			return result{stdout: "p" + strconv.Itoa(os.Getpid()) + "\nfcwd\nn" + wd + "\n"}
		}
		return result{stdout: f.lsof}
	}
	if f.fetchFails && len(args) > 0 && args[0] == "fetch" {
		return result{code: 128, stderr: "could not read from remote repository"}
	}
	return f.inner.run(dir, name, args...)
}

// did answers whether any command matching every one of these fragments was run.
func (f *fakeGH) did(fragments ...string) bool {
	for _, c := range f.calls {
		all := true
		for _, frag := range fragments {
			if !strings.Contains(c, frag) {
				all = false
			}
		}
		if all {
			return true
		}
	}
	return false
}

type fixture struct {
	t      *testing.T
	dir    string
	origin string
	gh     *fakeGH
	repo   repo
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH, so nothing here could be established")
	}
	root := t.TempDir()
	f := &fixture{
		t:      t,
		dir:    filepath.Join(root, "work"),
		origin: filepath.Join(root, "origin.git"),
		gh:     &fakeGH{pulls: "[]"},
	}
	f.repo = repo{runner: f.gh, dir: f.dir}

	f.run(root, "git", "init", "--quiet", "--bare", "-b", "main", f.origin)
	f.run(root, "git", "init", "--quiet", "-b", "main", f.dir)
	f.git("config", "user.email", "lane@example.invalid")
	f.git("config", "user.name", "Lane")
	f.git("config", "commit.gpgsign", "false")
	// The repository's own hooks add a sign-off and check the author address.
	// A fixture has neither, and inheriting a hooks path from an outer clone
	// would make these tests depend on it.
	f.git("config", "core.hooksPath", filepath.Join(root, "no-hooks"))
	f.git("remote", "add", "origin", f.origin)
	f.write("base.txt", "base\n")
	f.git("add", "-A")
	f.git("commit", "--quiet", "-m", "base")
	f.git("push", "--quiet", "origin", "main")
	f.git("fetch", "--quiet", "origin")
	return f
}

func (f *fixture) run(dir, name string, args ...string) string {
	f.t.Helper()
	r := local{}.run(dir, name, args...)
	if r.err != nil || r.code != 0 {
		f.t.Fatalf("%s %s in %s: code %d err %v: %s", name, strings.Join(args, " "), dir, r.code, r.err, r.stderr)
	}
	return strings.TrimSpace(r.stdout)
}

func (f *fixture) git(args ...string) string {
	f.t.Helper()
	return f.run(f.dir, "git", args...)
}

func (f *fixture) write(rel, body string) {
	f.t.Helper()
	p := filepath.Join(f.dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// commitOn adds one commit to a branch, creating it from main if needed, and
// returns the new tip.
func (f *fixture) commitOn(branch, rel, body, subject string) string {
	f.t.Helper()
	if !f.hasBranch(branch) {
		f.git("branch", branch, "main")
	}
	// Written through a temporary worktree so the main working tree stays on
	// main and stays clean, which is what the real repository looks like.
	tmp := filepath.Join(f.t.TempDir(), "edit-"+strings.ReplaceAll(branch, "/", "-"))
	f.git("worktree", "add", "--quiet", tmp, branch)
	p := filepath.Join(tmp, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.run(tmp, "git", "add", "-A")
	f.run(tmp, "git", "commit", "--quiet", "-m", subject)
	tip := f.run(tmp, "git", "rev-parse", "HEAD")
	f.git("worktree", "remove", "--force", tmp)
	return tip
}

func (f *fixture) hasBranch(name string) bool {
	f.t.Helper()
	return local{}.run(f.dir, "git", "rev-parse", "--verify", "refs/heads/"+name).code == 0
}

func (f *fixture) sha(rev string) string {
	f.t.Helper()
	return f.git("rev-parse", rev)
}

// squashOntoMain writes one commit on main carrying the branch's content, which
// is what `just merge` leaves behind, and pushes it. It returns the squash
// commit, which is the pull request's merge commit.
func (f *fixture) squashOntoMain(branch, subject string) string {
	f.t.Helper()
	f.git("merge", "--quiet", "--squash", branch)
	f.git("commit", "--quiet", "-m", subject)
	f.git("push", "--quiet", "origin", "main")
	f.git("fetch", "--quiet", "origin")
	return f.sha("main")
}

// addWorktree checks a branch out into its own directory and returns the path.
func (f *fixture) addWorktree(name, branch string) string {
	f.t.Helper()
	p := filepath.Join(f.t.TempDir(), name)
	f.git("worktree", "add", "--quiet", p, branch)
	return p
}

// setPulls hands the fake the JSON `gh` really returns, so the decoder under
// test is the production one.
func (f *fixture) setPulls(ps ...pull) {
	f.t.Helper()
	body, err := json.Marshal(ps)
	if err != nil {
		f.t.Fatal(err)
	}
	f.gh.pulls = string(body)
}

func mergedPull(number int, branch, headOid, mergeOid string) pull {
	p := pull{Number: number, State: "MERGED", HeadRefName: branch, HeadRefOid: headOid}
	p.MergeCommit = &struct {
		Oid string `json:"oid"`
	}{Oid: mergeOid}
	return p
}

// act runs the command and returns its exit code and everything it printed. The
// recency signal is off here, because every fixture file was written a second
// ago and the signal would keep everything; the two tests that are about it
// switch it on.
func (f *fixture) act(apply bool) (int, string) {
	f.t.Helper()
	return f.actWith(options{mainRef: "origin/main", act: apply})
}

func (f *fixture) actWith(o options) (int, string) {
	f.t.Helper()
	var out bytes.Buffer
	code := run(f.repo, o, &out)
	return code, out.String()
}

// exclude adds a pattern to the repository's own exclude file, which is where
// this repository's lanes keep their handover and scratch out of git.
func (f *fixture) exclude(pattern string) {
	f.t.Helper()
	p := filepath.Join(f.dir, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	fh, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		f.t.Fatal(err)
	}
	defer fh.Close()
	if _, err := fh.WriteString(pattern + "\n"); err != nil {
		f.t.Fatal(err)
	}
}

// section returns one labelled block of the report, so a test can say a path was
// named as NOT CHECKED rather than merely mentioned somewhere.
//
// Headers are matched at the start of a line and followed by a comma, because
// the summary line above them carries the same words ("1 NOT CHECKED") and the
// first version of this helper read the summary as the section.
func section(report, label string) string {
	i := strings.Index("\n"+report, "\n"+label+",")
	if i < 0 {
		return ""
	}
	rest := report[i+len(label):]
	for _, next := range []string{"\nREMOVABLE,", "\nKEPT,", "\nNOT CHECKED,", "\nOUT OF SCOPE,", "\nDry run", "\nDone."} {
		if j := strings.Index(rest, next); j >= 0 {
			rest = rest[:j]
		}
	}
	return rest
}

func (f *fixture) assertBranch(branch string, want bool) {
	f.t.Helper()
	if got := f.hasBranch(branch); got != want {
		if want {
			f.t.Fatalf("the branch %s was deleted and it holds work that is not in main", branch)
		}
		f.t.Fatalf("the branch %s still exists and it was proven disposable", branch)
	}
}

func (f *fixture) assertDir(path string, want bool) {
	f.t.Helper()
	_, err := os.Stat(path)
	if (err == nil) != want {
		if want {
			f.t.Fatalf("the worktree %s was removed", path)
		}
		f.t.Fatalf("the worktree %s still exists", path)
	}
}

// landedBranch is the ordinary case: a branch whose pull request squashed and
// which carries nothing after the merge. It returns the branch name, its
// worktree path and the pull request GitHub would report.
func (f *fixture) landedBranch(branch, dirName string) (string, pull) {
	f.t.Helper()
	f.commitOn(branch, branch+"/one.txt", "one\n", "first")
	tip := f.commitOn(branch, branch+"/two.txt", "two\n", "second")
	squash := f.squashOntoMain(branch, "landed "+branch)
	f.addWorktree(dirName, branch)
	return tip, mergedPull(300, branch, tip, squash)
}

// ---------------------------------------------------------------------------
// The defect this program exists for.
// ---------------------------------------------------------------------------

// TestABranchWithCommitsAfterItsMergedPullRequestIsRefused is the exact case
// that produced this file. On 2026-09-11 w-runtime-aca, w-detect-clouds-datastores
// and w-engine-red were each in this state with a clean worktree, so a predicate
// made of a merged pull request plus a clean tree called them disposable.
func TestABranchWithCommitsAfterItsMergedPullRequestIsRefused(t *testing.T) {
	f := newFixture(t)
	f.commitOn("w-runtime-aca", "aca/one.txt", "one\n", "first")
	merged := f.commitOn("w-runtime-aca", "aca/two.txt", "two\n", "second")
	squash := f.squashOntoMain("w-runtime-aca", "runtime aca (#316)")
	// The nine commits w-runtime-aca really carried, one is enough to prove it.
	after := f.commitOn("w-runtime-aca", "aca/three.txt", "three\n", "work after the merge")
	if after == merged {
		t.Fatal("the fixture did not advance the branch, so it proves nothing")
	}
	wt := f.addWorktree("aca", "w-runtime-aca")
	f.setPulls(mergedPull(316, "w-runtime-aca", merged, squash))

	code, report := f.act(true)

	f.assertBranch("w-runtime-aca", true)
	f.assertDir(wt, true)
	if code != 0 {
		t.Logf("exit %d, which is fine here: the refusal is the point", code)
	}
	if !strings.Contains(section(report, "KEPT"), wt) {
		t.Fatalf("the worktree was not reported as kept:\n%s", report)
	}
	if !strings.Contains(report, "written after the merge") {
		t.Fatalf("the refusal does not say why:\n%s", report)
	}
	if strings.Contains(section(report, "REMOVABLE"), wt) {
		t.Fatalf("the worktree was called removable:\n%s", report)
	}
}

// TestABranchIdenticalToTheCommitItsPullRequestSquashedIsRemoved is the other
// half. The signal has to be able to say yes, or the tool is useless and the
// next person reaches for `git branch -D` by hand, which is what this replaces.
func TestABranchIdenticalToTheCommitItsPullRequestSquashedIsRemoved(t *testing.T) {
	f := newFixture(t)
	f.commitOn("w-landed", "landed/one.txt", "one\n", "first")
	tip := f.commitOn("w-landed", "landed/two.txt", "two\n", "second")
	squash := f.squashOntoMain("w-landed", "landed (#300)")
	wt := f.addWorktree("landed", "w-landed")
	f.setPulls(mergedPull(300, "w-landed", tip, squash))

	// The branch really does hold commits main cannot reach, which is what
	// makes this route distinct from the ancestry one.
	if n, err := f.repo.countUnreachable(tip, "origin/main"); err != nil || n == 0 {
		t.Fatalf("the fixture is not a squash merge: %d unreachable, %v", n, err)
	}

	code, report := f.act(true)

	f.assertBranch("w-landed", false)
	f.assertDir(wt, false)
	if code != 0 {
		t.Fatalf("exit %d on a clean removal:\n%s", code, report)
	}
	if !strings.Contains(report, "the exact commit it squashed") {
		t.Fatalf("the proof does not name the third signal:\n%s", report)
	}
}

// TestABranchWithNoCommitsMainCannotReachIsRemoved is the second shape of the
// third signal, and the only one where deleting the ref orphans no object at all.
func TestABranchWithNoCommitsMainCannotReachIsRemoved(t *testing.T) {
	f := newFixture(t)
	f.git("branch", "w-rebased", "origin/main")
	wt := f.addWorktree("rebased", "w-rebased")
	// No pull request at all. The ancestry route does not need one.
	f.setPulls()

	code, report := f.act(true)

	f.assertBranch("w-rebased", false)
	f.assertDir(wt, false)
	if code != 0 {
		t.Fatalf("exit %d on a clean removal:\n%s", code, report)
	}
	if !strings.Contains(report, "cannot orphan a commit") {
		t.Fatalf("the proof does not name the ancestry route:\n%s", report)
	}
}

// ---------------------------------------------------------------------------
// The signals that say no.
// ---------------------------------------------------------------------------

func TestADirtyWorktreeIsKept(t *testing.T) {
	f := newFixture(t)
	_, p := f.landedBranch("w-dirty", "dirty")
	f.setPulls(p)
	path := f.worktreeOf("w-dirty")
	if err := os.WriteFile(filepath.Join(path, "w-dirty", "one.txt"), []byte("an edit nobody committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, report := f.act(true)

	f.assertBranch("w-dirty", true)
	f.assertDir(path, true)
	if !strings.Contains(section(report, "KEPT"), "UNCOMMITTED CHANGES") {
		t.Fatalf("the uncommitted edit was not the reason given:\n%s", report)
	}
}

func TestAnAbsentWorktreeDirectoryIsNotCheckedRatherThanSkipped(t *testing.T) {
	f := newFixture(t)
	_, p := f.landedBranch("w-gone", "gone")
	f.setPulls(p)
	path := f.worktreeOf("w-gone")
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}

	code, report := f.act(false)

	if !strings.Contains(section(report, "NOT CHECKED"), path) {
		t.Fatalf("a worktree whose directory is gone was not reported as unchecked:\n%s", report)
	}
	if code == 0 {
		t.Fatalf("the run passed with something unchecked:\n%s", report)
	}
	f.assertBranch("w-gone", true)
}

func TestABranchWithNoMergedPullRequestIsKept(t *testing.T) {
	f := newFixture(t)
	f.commitOn("w-in-progress", "wip/one.txt", "one\n", "work nobody merged")
	path := f.addWorktree("wip", "w-in-progress")
	f.setPulls()

	_, report := f.act(true)

	f.assertBranch("w-in-progress", true)
	f.assertDir(path, true)
	if !strings.Contains(section(report, "KEPT"), "no merged pull request") {
		t.Fatalf("the absent pull request was not the reason given:\n%s", report)
	}
}

func TestAProtectedBranchIsNeverRemoved(t *testing.T) {
	f := newFixture(t)
	// status-data is ahead of main by design and a merged pull request from a
	// branch of the same name would be the worst possible coincidence.
	f.commitOn("status-data", "status/one.txt", "one\n", "five minutes of history")
	path := f.addWorktree("statusdata", "status-data")
	f.setPulls(mergedPull(1, "status-data", f.sha("status-data"), f.sha("origin/main")))

	_, report := f.act(true)

	f.assertBranch("status-data", true)
	f.assertDir(path, true)
	if !strings.Contains(section(report, "KEPT"), "protected") {
		t.Fatalf("the protection was not the reason given:\n%s", report)
	}
}

// TestADetachedWorktreeIsKeptWithWhatItsHeadHolds. A detached worktree has no
// branch to delete, so this program keeps it, and it can still establish what
// removing it would cost: here, one commit that nothing else points at.
//
// The first version filed every detached worktree as NOT CHECKED. On this
// machine that was thirteen of them, so every run exited nonzero for a reason
// that never changed, which teaches the operator that the exit code means
// nothing. That is the habit the exit code exists to prevent.
func TestADetachedWorktreeIsKeptWithWhatItsHeadHolds(t *testing.T) {
	f := newFixture(t)
	tip := f.commitOn("w-temp", "temp/one.txt", "one\n", "a commit reachable from nothing else")
	path := filepath.Join(t.TempDir(), "detached")
	f.git("worktree", "add", "--quiet", "--detach", path, tip)
	f.git("branch", "-D", "w-temp")
	path = f.worktreeAt(path)

	code, report := f.act(true)

	kept := section(report, "KEPT")
	if !strings.Contains(kept, path) || !strings.Contains(kept, "reachable from nothing else") {
		t.Fatalf("a detached worktree holding an unreachable commit was not kept with that reason:\n%s", report)
	}
	f.assertDir(path, true)
	if code != 0 {
		t.Fatalf("exit %d for a machine where every worktree was judged:\n%s", code, report)
	}
}

func TestAMergeCommitThatIsNotAnAncestorOfMainIsRefused(t *testing.T) {
	f := newFixture(t)
	f.commitOn("w-elsewhere", "elsewhere/one.txt", "one\n", "first")
	tip := f.commitOn("w-elsewhere", "elsewhere/two.txt", "two\n", "second")
	// A merge commit on a branch that is not main, which is what a pull
	// request merged into a release line or a stale origin/main looks like.
	other := f.commitOn("w-release", "release/one.txt", "one\n", "merged somewhere else")
	path := f.addWorktree("elsewhere", "w-elsewhere")
	f.setPulls(mergedPull(400, "w-elsewhere", tip, other))

	_, report := f.act(true)

	f.assertBranch("w-elsewhere", true)
	f.assertDir(path, true)
	if !strings.Contains(report, "did not land here") {
		t.Fatalf("the refusal does not say the merge commit is not in main:\n%s", report)
	}
}

// ---------------------------------------------------------------------------
// The instrument's own failures, which must read as refusals rather than as
// clean runs.
// ---------------------------------------------------------------------------

func TestAFailingGhRefusesEverything(t *testing.T) {
	f := newFixture(t)
	_, p := f.landedBranch("w-landed", "landed")
	f.setPulls(p)
	path := f.worktreeOf("w-landed")
	f.gh.ghCode = 1

	code, report := f.act(true)

	if code == 0 {
		t.Fatalf("a failed gh passed:\n%s", report)
	}
	f.assertBranch("w-landed", true)
	f.assertDir(path, true)
	if f.gh.did("git", "branch", "-D") {
		t.Fatalf("a branch was deleted after gh failed: %v", f.gh.calls)
	}
}

func TestAMissingGhBinaryRefusesRatherThanCrashing(t *testing.T) {
	f := newFixture(t)
	_, p := f.landedBranch("w-landed", "landed")
	f.setPulls(p)
	f.gh.ghErr = fmt.Errorf("gh: executable file not found in $PATH")

	code, report := f.act(true)

	if code == 0 {
		t.Fatalf("an absent gh passed:\n%s", report)
	}
	f.assertBranch("w-landed", true)
}

func TestAFailedFetchBlocksApply(t *testing.T) {
	f := newFixture(t)
	_, p := f.landedBranch("w-landed", "landed")
	f.setPulls(p)
	path := f.worktreeOf("w-landed")
	f.gh.fetchFails = true

	var out bytes.Buffer
	code := run(f.repo, options{mainRef: "origin/main", fetch: true, act: true}, &out)

	if code == 0 {
		t.Fatalf("a failed fetch let an apply through:\n%s", out.String())
	}
	f.assertBranch("w-landed", true)
	f.assertDir(path, true)
	if f.gh.did("git", "worktree", "remove") {
		t.Fatalf("a worktree was removed after the fetch failed: %v", f.gh.calls)
	}
}

func TestAnUnresolvableMainRefRefusesEverything(t *testing.T) {
	f := newFixture(t)
	_, p := f.landedBranch("w-landed", "landed")
	f.setPulls(p)

	var out bytes.Buffer
	code := run(f.repo, options{mainRef: "origin/a-branch-that-does-not-exist", act: true}, &out)

	if code == 0 {
		t.Fatalf("an unresolvable main passed:\n%s", out.String())
	}
	f.assertBranch("w-landed", true)
	// Every later git call would also fail against a ref that does not exist,
	// so the two assertions above hold even without the early refusal. What
	// the early refusal owns is that nothing else is asked at all, and the
	// operator is told the one fact that matters rather than a wall of
	// per-worktree errors.
	if f.gh.did("gh", "pr", "list") {
		t.Fatalf("GitHub was asked about pull requests after main failed to resolve: %v", f.gh.calls)
	}
	if !strings.Contains(out.String(), "could not be resolved") {
		t.Fatalf("the refusal does not say main could not be resolved:\n%s", out.String())
	}
}

// TestAMalformedPorcelainRecordIsNotCheckedRatherThanSkipped is the second thing
// the brief asked for: a worktree the tool could not parse must be named, not
// passed over. The listing is constructed here because git will not emit a
// broken one on demand, and the run above proves the parse feeds the report.
func TestAMalformedPorcelainRecordIsNotCheckedRatherThanSkipped(t *testing.T) {
	// The /b record names a branch, so the only rule that can refuse it is the
	// one about the line nobody recognised. Its first version named no branch
	// either, and a second rule caught it, which hid whether the unknown line
	// was noticed at all: the mutation that stopped reading unknown lines left
	// this test passing.
	listing := "worktree /a\nHEAD " + strings.Repeat("a", 40) + "\nbranch refs/heads/main\n\n" +
		"worktree /b\nHEAD " + strings.Repeat("b", 40) + "\nbranch refs/heads/w-future\nsomething-new-in-git\n\n" +
		"worktree /c\nbranch refs/heads/w-no-head\n\n"
	got := parseWorktrees(listing)
	if len(got) != 3 {
		t.Fatalf("parsed %d records, want 3: %+v", len(got), got)
	}
	if got[0].malformed != "" {
		t.Errorf("a good record was called malformed: %q", got[0].malformed)
	}
	if got[1].malformed == "" {
		t.Error("a record carrying an unknown line was accepted, so a future git could be silently misread")
	}
	if got[2].malformed == "" {
		t.Error("a record carrying no HEAD was accepted")
	}
}

// ---------------------------------------------------------------------------
// The orderings.
// ---------------------------------------------------------------------------

// TestABranchTipThatMovedBetweenTheScanAndTheDeletionIsRefused is the ordering
// the previous script could not see. Five other lanes work on this machine at
// once, so a commit landing on a branch between the moment it was judged and the
// moment it is deleted is an ordinary Tuesday, not a theoretical race.
func TestABranchTipThatMovedBetweenTheScanAndTheDeletionIsRefused(t *testing.T) {
	f := newFixture(t)
	f.commitOn("w-moving", "moving/one.txt", "one\n", "first")
	tip := f.commitOn("w-moving", "moving/two.txt", "two\n", "second")
	squash := f.squashOntoMain("w-moving", "moving (#401)")
	path := f.addWorktree("moving", "w-moving")
	f.setPulls(mergedPull(401, "w-moving", tip, squash))

	merged, _, err := f.repo.mergedPulls()
	if err != nil {
		t.Fatal(err)
	}
	e := env{mainRef: "origin/main", merged: merged, now: time.Now()}
	js, err := scan(f.repo, e)
	if err != nil {
		t.Fatal(err)
	}
	var judged bool
	for _, j := range js {
		if j.wt.branch == "w-moving" && j.removable {
			judged = true
		}
	}
	if !judged {
		t.Fatal("the fixture was not judged removable, so the ordering is not the thing under test")
	}

	// Another lane commits into the worktree that is about to be removed.
	if err := os.WriteFile(filepath.Join(path, "moving", "three.txt"), []byte("three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.run(path, "git", "add", "-A")
	f.run(path, "git", "commit", "--quiet", "-m", "a lane commits between the scan and the apply")

	var out bytes.Buffer
	removed, refused := apply(f.repo, js, e, &out)

	if removed != 0 {
		t.Fatalf("a branch that moved after being judged was still deleted:\n%s", out.String())
	}
	if refused != 1 {
		t.Fatalf("refused %d, want 1:\n%s", refused, out.String())
	}
	f.assertBranch("w-moving", true)
	f.assertDir(path, true)
	if !strings.Contains(out.String(), "stopped being disposable") {
		t.Fatalf("the refusal does not say the branch changed:\n%s", out.String())
	}
}

// TestABackupRefHoldsEveryDeletedTip is the net under the whole program. The
// third signal can only be as good as the reasoning behind it, and a ref costs
// nothing and makes every deletion reversible.
func TestABackupRefHoldsEveryDeletedTip(t *testing.T) {
	f := newFixture(t)
	f.commitOn("w-landed", "landed/one.txt", "one\n", "first")
	tip := f.commitOn("w-landed", "landed/two.txt", "two\n", "second")
	squash := f.squashOntoMain("w-landed", "landed (#300)")
	f.addWorktree("landed", "w-landed")
	f.setPulls(mergedPull(300, "w-landed", tip, squash))

	code, report := f.act(true)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, report)
	}
	f.assertBranch("w-landed", false)

	got := f.git("rev-parse", "--verify", backupRefPrefix+"w-landed")
	if got != tip {
		t.Fatalf("the backup ref is %s, want the deleted tip %s", got, tip)
	}
	// And the commits really are restorable, which is the claim the report makes.
	f.git("branch", "w-landed", backupRefPrefix+"w-landed")
	if f.sha("w-landed") != tip {
		t.Fatal("the restore named in the report does not bring the tip back")
	}
}

// TestTheReportStatesWhatIsOutOfScope is the third thing the brief asked for.
// A branch with no worktree is never judged here, and a reader who is not told
// that will read the removable list as the whole answer.
func TestTheReportStatesWhatIsOutOfScope(t *testing.T) {
	f := newFixture(t)
	f.commitOn("w-no-worktree", "nowt/one.txt", "one\n", "landed long ago")
	f.squashOntoMain("w-no-worktree", "landed (#402)")

	_, report := f.act(false)

	out := section(report, "OUT OF SCOPE")
	if !strings.Contains(out, "w-no-worktree") {
		t.Fatalf("a branch with no worktree was not named as out of scope:\n%s", report)
	}
	if !strings.Contains(out, "never judged") {
		t.Fatalf("the report does not say a branch with no worktree is not judged:\n%s", report)
	}
}

// TestTheMainWorkingTreeIsNeverRemoved because git refuses it anyway and a tool
// that lets the refusal happen at the end has already told the operator it
// planned to.
//
// The primary checkout here sits on an ordinary lane branch rather than on main,
// and on 2026-09-11 that branch was one commit ahead and otherwise landed. So the
// fixture puts the main working tree on a branch the ancestry route would call
// disposable, because a main working tree on `main` is kept by the protected
// branch rule and would hide whether this guard exists at all.
func TestTheMainWorkingTreeIsNeverRemoved(t *testing.T) {
	f := newFixture(t)
	f.git("checkout", "--quiet", "-b", "w-primary", "origin/main")
	f.setPulls()

	_, report := f.act(true)

	if !strings.Contains(section(report, "KEPT"), f.dir) {
		t.Fatalf("the main working tree was not reported as kept:\n%s", report)
	}
	if strings.Contains(section(report, "REMOVABLE"), f.dir) {
		t.Fatalf("the main working tree was called removable:\n%s", report)
	}
	f.assertDir(f.dir, true)
	f.assertBranch("w-primary", true)
}

// worktreeOf finds where a branch is checked out, which the fixture helpers
// create under directories the test does not otherwise hold.
func (f *fixture) worktreeOf(branch string) string {
	f.t.Helper()
	for _, wt := range parseWorktrees(f.git("worktree", "list", "--porcelain")) {
		if wt.branch == branch {
			return wt.path
		}
	}
	f.t.Fatalf("no worktree holds %s", branch)
	return ""
}

// worktreeAt returns the spelling git uses for a worktree it holds, which on
// macOS resolves the /var symlink that t.TempDir hands out.
func (f *fixture) worktreeAt(given string) string {
	f.t.Helper()
	want, err := filepath.EvalSymlinks(given)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, wt := range parseWorktrees(f.git("worktree", "list", "--porcelain")) {
		if got, err := filepath.EvalSymlinks(wt.path); err == nil && got == want {
			return wt.path
		}
	}
	f.t.Fatalf("git holds no worktree at %s", given)
	return ""
}

// ---------------------------------------------------------------------------
// The signals that say somebody is working here, which the first run against
// the real machine proved were missing: it called seven live lanes removable.
// ---------------------------------------------------------------------------

// TestIgnoredScratchThatNoBuildWritesKeepsTheWorktree is the case that makes a
// clean tree a lie. A lane's handover lives in PROGRESS.md and its scratch under
// .lane/, both excluded through .git/info/exclude, so `git status` reports the
// tree clean and `git worktree remove` deletes both without a prompt.
func TestIgnoredScratchThatNoBuildWritesKeepsTheWorktree(t *testing.T) {
	f := newFixture(t)
	_, p := f.landedBranch("w-lane", "lane")
	f.setPulls(p)
	path := f.worktreeOf("w-lane")
	f.exclude("PROGRESS.md")
	if err := os.WriteFile(filepath.Join(path, "PROGRESS.md"), []byte("the only copy of a handover\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st := f.run(path, "git", "status", "--porcelain"); st != "" {
		t.Fatalf("the fixture is not clean to git, so it is not the case under test: %q", st)
	}

	_, report := f.act(true)

	f.assertBranch("w-lane", true)
	if _, err := os.Stat(filepath.Join(path, "PROGRESS.md")); err != nil {
		t.Fatalf("the ignored handover was deleted: %v\n%s", err, report)
	}
	if !strings.Contains(section(report, "KEPT"), "PROGRESS.md") {
		t.Fatalf("the ignored scratch was not named as the reason:\n%s", report)
	}
}

// TestBuildOutputAloneDoesNotKeepAWorktree is the other half. node_modules is
// most of the disk this program exists to free, and a signal that kept every
// worktree with dependencies installed would keep all of them.
func TestBuildOutputAloneDoesNotKeepAWorktree(t *testing.T) {
	f := newFixture(t)
	_, p := f.landedBranch("w-built", "built")
	f.setPulls(p)
	path := f.worktreeOf("w-built")
	f.exclude("node_modules/")
	f.exclude(".next/")
	for _, rel := range []string{"node_modules/pkg/index.js", "www/.next/cache/x"} {
		full := filepath.Join(path, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("built\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	code, report := f.act(true)

	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, report)
	}
	f.assertBranch("w-built", false)
	f.assertDir(path, false)
}

func TestAProcessWorkingInsideTheWorktreeKeepsIt(t *testing.T) {
	f := newFixture(t)
	_, p := f.landedBranch("w-busy", "busy")
	f.setPulls(p)
	path := f.worktreeOf("w-busy")
	wd, _ := os.Getwd()
	f.gh.lsof = "p" + strconv.Itoa(os.Getpid()) + "\nfcwd\nn" + wd + "\n" +
		"p424242\nfcwd\nn" + filepath.Join(path, "engine") + "\n"

	_, report := f.act(true)

	f.assertBranch("w-busy", true)
	f.assertDir(path, true)
	if !strings.Contains(section(report, "KEPT"), "process 424242 is working in it") {
		t.Fatalf("the process was not named as the reason:\n%s", report)
	}
}

func TestALsofThatCannotRunIsNotCheckedRatherThanSkipped(t *testing.T) {
	f := newFixture(t)
	_, p := f.landedBranch("w-landed", "landed")
	f.setPulls(p)
	path := f.worktreeOf("w-landed")
	f.gh.lsofErr = fmt.Errorf("lsof: executable file not found in $PATH")

	code, report := f.act(true)

	f.assertBranch("w-landed", true)
	f.assertDir(path, true)
	if !strings.Contains(section(report, "NOT CHECKED"), path) {
		t.Fatalf("a worktree nobody could check for processes was not reported as unchecked:\n%s", report)
	}
	if code == 0 {
		t.Fatalf("the run passed with something unchecked:\n%s", report)
	}
}

// TestALsofListingWithoutThisProcessIsNotTrusted because lsof exits zero on a
// listing it could only partly read, and a listing that cannot see the one
// process known to exist has not shown that no lane is working anywhere.
func TestALsofListingWithoutThisProcessIsNotTrusted(t *testing.T) {
	f := newFixture(t)
	_, p := f.landedBranch("w-landed", "landed")
	f.setPulls(p)
	f.gh.lsof = "p1\nfcwd\nn/\n"

	code, report := f.act(true)

	f.assertBranch("w-landed", true)
	if !strings.Contains(section(report, "NOT CHECKED"), "cannot be trusted") {
		t.Fatalf("a listing without this process was trusted:\n%s", report)
	}
	if code == 0 {
		t.Fatalf("the run passed on a listing it could not trust:\n%s", report)
	}
}

// TestARecentlyWrittenWorktreeIsKept is the signal that caught the live lanes.
// An agent's shell leaves the worktree between commands, so most of the time no
// process is working in it, and a lane that has not yet written scratch has
// nothing ignored. What it always has is a checkout from tonight.
func TestARecentlyWrittenWorktreeIsKept(t *testing.T) {
	f := newFixture(t)
	_, p := f.landedBranch("w-fresh", "fresh")
	f.setPulls(p)
	path := f.worktreeOf("w-fresh")

	_, report := f.actWith(options{mainRef: "origin/main", act: true, idle: time.Hour})

	f.assertBranch("w-fresh", true)
	f.assertDir(path, true)
	if !strings.Contains(section(report, "KEPT"), "idle window") {
		t.Fatalf("the recent write was not named as the reason:\n%s", report)
	}
}

func TestAWorktreeIdleLongerThanTheWindowIsRemoved(t *testing.T) {
	f := newFixture(t)
	_, p := f.landedBranch("w-stale", "stale")
	f.setPulls(p)
	path := f.worktreeOf("w-stale")
	old := time.Now().Add(-48 * time.Hour)
	filepath.WalkDir(path, func(q string, d os.DirEntry, err error) error {
		if err == nil {
			os.Chtimes(q, old, old)
		}
		return nil
	})

	code, report := f.actWith(options{mainRef: "origin/main", act: true, idle: 24 * time.Hour})

	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, report)
	}
	f.assertBranch("w-stale", false)
	f.assertDir(path, false)
}

// TestTheRealLsofListsThisProcess reads the real command's real output through
// the production parser, because every other test here hands the parser a
// listing written by hand, and a parser only ever fed its author's idea of the
// format is the shape of decoder this repository keeps finding wrong.
func TestTheRealLsofListsThisProcess(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof is not on PATH here, so there is no real listing to read")
	}
	procs, err := repo{runner: local{}, dir: "."}.processCwds()
	if err != nil {
		t.Fatalf("the real listing was refused: %v", err)
	}
	wd, _ := os.Getwd()
	self := strconv.Itoa(os.Getpid())
	for _, p := range procs {
		if p.pid == self {
			a, _ := filepath.EvalSymlinks(p.cwd)
			b, _ := filepath.EvalSymlinks(wd)
			if a != b {
				t.Fatalf("this process is listed at %s and is working in %s", p.cwd, wd)
			}
			return
		}
	}
	t.Fatalf("this process is not in its own listing of %d", len(procs))
}
