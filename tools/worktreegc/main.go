// Command worktreegc removes a git worktree, and deletes the branch that
// worktree held, only when it has PROVEN that doing so orphans nothing and that
// nobody is working there.
//
// Why this exists as a program rather than as advice.
//
// `git branch --merged` is the instrument everyone reaches for and it is wrong
// in this repository. `just merge` squashes, so a squash merged branch is never
// an ancestor of main and ancestry answers false for every branch that actually
// landed. Measured on 2026-09-06: it reported 0 removable worktrees when the
// true answer was 77 holding 28.5 GB.
//
// What replaced it was a NAME match against GitHub's merged pull request list,
// crossed with a clean working tree. That is two signals and it needs three,
// because a squash merge leaves the branch behind. A branch can have a merged
// pull request AND carry commits written after the merge, and nothing in those
// two signals can see them. On 2026-09-11 six branches in this repository were
// in exactly that state, and three of them had clean worktrees, so the two
// signal predicate called them disposable and would have run `git branch -D`
// over work that is not in main:
//
//	w-runtime-aca                9 commits after pull request 316
//	w-detect-clouds-datastores   7 commits after pull request 308
//	w-engine-red                 1 commit after pull request 333
//
// The third signal is the one this program refuses to act without, and it comes
// in two shapes. Either the branch has zero commits that main cannot already
// reach, so deleting the ref cannot orphan a commit at all. Or the branch tip
// is byte identical to the exact commit its merged pull request squashed, so
// the branch holds nothing written after the merge and every commit under it is
// in main as that squash.
//
// Content diffing is NOT a substitute for either shape and is deliberately not
// used. A squash, followed by further evolution of the same files on main,
// makes even a branch merged hours ago report conflicts against main. Diffing
// would have refused most of the 81 branches that really were landed.
//
// The first run of this program against the real machine found the next hole.
// A lane that has just branched from main has no commits of its own, so the
// ancestry shape calls it disposable, and it called seven live lanes removable,
// one of them a session that was writing files at that minute. A clean tree does
// not mean an idle one: the scratch a lane keeps is ignored by design, through
// .git/info/exclude, so `git status` cannot see it and `git worktree remove`
// deletes it without asking. So three more signals stand between a proof and a
// removal. No ignored file may be anything but build output, no process may be
// working inside the worktree, and nothing in it may have been written inside
// the idle window.
//
// Two other habits this program is built to break. It reports what it could NOT
// establish, by name and by missing signal, and exits nonzero when that list is
// not empty, because a tool that silently passes over what it could not parse
// looks exactly like a tool that found nothing wrong. And before it deletes a
// branch it writes the tip to a backup ref under refs/worktree-gc/, so the
// objects stay reachable and every deletion this program makes is reversible
// with one command.
//
// Dry run by default. Pass -apply to act.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// protectedBranches are never removed whatever the signals say. `status-data`
// is an orphan data branch that a workflow writes every five minutes and it
// holds the status page's history, so it reads as many commits ahead of main and
// that is correct. `hosted-loop` and `hl` belong to another session.
var protectedBranches = map[string]bool{
	"main":        true,
	"status-data": true,
	"hosted-loop": true,
	"hl":          true,
}

// protectedPaths are worktrees belonging to another session on this machine.
var protectedPaths = []string{
	"/Users/vir/af-hosted-loop",
	"/Users/vir/af-work/infra",
}

// regenerableDirs are directory names a build writes and a build can write
// again. An ignored path under one of them is not work, and it is the bulk of
// the disk this program exists to free. Anything else that git ignores is kept,
// because the ignored files in a lane's worktree are exactly its handover, its
// evidence and its scratch.
var regenerableDirs = map[string]bool{
	"node_modules":  true,
	".next":         true,
	"out":           true,
	"dist":          true,
	"build":         true,
	".gate-reports": true,
	"coverage":      true,
	".turbo":        true,
	"__pycache__":   true,
}

// regenerableFiles are single files a build writes at a fixed name.
var regenerableFiles = map[string]bool{
	"next-env.d.ts": true,
}

// backupRefPrefix holds the tip of every branch this program deletes. A ref
// keeps the objects reachable, so `git gc` cannot collect them and any deletion
// can be undone. It does not make the third signal optional: these refs are
// local to one machine and somebody will eventually delete them, and a deleted
// backup of unmerged work is the same loss one step later.
const backupRefPrefix = "refs/worktree-gc/"

// result is one external command's outcome. The exit code is carried separately
// from err because `git merge-base --is-ancestor` answers no with exit code 1,
// and a program that read that as a failed command would treat "this commit is
// not an ancestor" and "git could not run" as the same fact. They are the two
// halves of the distinction this whole program is about.
type result struct {
	stdout string
	stderr string
	code   int
	// err is set only when the command could not be started or its exit
	// status could not be read. A nonzero exit is not an err.
	err error
}

type runner interface {
	run(dir, name string, args ...string) result
}

// local runs commands for real.
type local struct{}

func (local) run(dir, name string, args ...string) result {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var out, errBuf strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	err := cmd.Run()
	r := result{stdout: out.String(), stderr: errBuf.String()}
	if err == nil {
		return r
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		r.code = ee.ExitCode()
		return r
	}
	// A missing binary lands here. Without this branch, no `gh` on PATH
	// produced a crash instead of the refusal below, which is the
	// difference between a tool that says no and a tool that falls over.
	r.code = -1
	r.err = fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	return r
}

// repo is a git repository reached through whatever runner it was given.
type repo struct {
	runner runner
	dir    string
}

func (g repo) git(args ...string) result { return g.runner.run(g.dir, "git", args...) }

func (g repo) gh(args ...string) result { return g.runner.run(g.dir, "gh", args...) }

// rev resolves one revision to a full object name.
func (g repo) rev(spec string) (string, error) {
	r := g.git("rev-parse", "--verify", spec+"^{commit}")
	if r.err != nil {
		return "", r.err
	}
	if r.code != 0 {
		return "", fmt.Errorf("git rev-parse %s exited %d: %s", spec, r.code, oneLine(r.stderr))
	}
	sha := strings.TrimSpace(r.stdout)
	if len(sha) != 40 {
		return "", fmt.Errorf("git rev-parse %s returned %q, which is not an object name", spec, sha)
	}
	return sha, nil
}

// isAncestor answers whether a is reachable from b. The three way return is the
// point: exit 0 is yes, exit 1 is no, and anything else is a question that was
// never answered, which this program must never read as either answer.
func (g repo) isAncestor(a, b string) (bool, error) {
	r := g.git("merge-base", "--is-ancestor", a, b)
	if r.err != nil {
		return false, r.err
	}
	switch r.code {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("git merge-base --is-ancestor %s %s exited %d: %s",
			a, b, r.code, oneLine(r.stderr))
	}
}

// countUnreachable is how many commits are reachable from tip and not from
// mainRef. Zero means deleting the ref cannot orphan a commit.
func (g repo) countUnreachable(tip, mainRef string) (int, error) {
	r := g.git("rev-list", "--count", tip, "--not", mainRef)
	if r.err != nil {
		return 0, r.err
	}
	if r.code != 0 {
		return 0, fmt.Errorf("git rev-list --count %s --not %s exited %d: %s",
			tip, mainRef, r.code, oneLine(r.stderr))
	}
	n, err := strconv.Atoi(strings.TrimSpace(r.stdout))
	if err != nil {
		return 0, fmt.Errorf("git rev-list --count returned %q, which is not a number",
			strings.TrimSpace(r.stdout))
	}
	return n, nil
}

// pull is the part of a pull request this program reasons about. headRefOid is
// the field that carries the whole fix: it is the exact commit GitHub squashed,
// and comparing the branch tip against it is the third signal.
type pull struct {
	Number      int    `json:"number"`
	State       string `json:"state"`
	HeadRefName string `json:"headRefName"`
	HeadRefOid  string `json:"headRefOid"`
	MergeCommit *struct {
		Oid string `json:"oid"`
	} `json:"mergeCommit"`
}

const pullLimit = 500

// mergedPulls is GitHub's own record of what landed, indexed by head ref name.
// The second return says the list may have been truncated, which can only make
// this program keep more than it had to, never delete more.
func (g repo) mergedPulls() (map[string][]pull, bool, error) {
	r := g.gh("pr", "list", "--state", "merged", "--limit", strconv.Itoa(pullLimit),
		"--json", "number,state,headRefName,headRefOid,mergeCommit")
	if r.err != nil {
		return nil, false, r.err
	}
	if r.code != 0 {
		return nil, false, fmt.Errorf("gh pr list exited %d: %s", r.code, oneLine(r.stderr))
	}
	var pulls []pull
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.stdout)), &pulls); err != nil {
		return nil, false, fmt.Errorf("gh pr list returned something this cannot read: %w", err)
	}
	by := map[string][]pull{}
	for _, p := range pulls {
		if p.State != "MERGED" {
			continue
		}
		by[p.HeadRefName] = append(by[p.HeadRefName], p)
	}
	for name := range by {
		sort.Slice(by[name], func(i, j int) bool { return by[name][i].Number > by[name][j].Number })
	}
	return by, len(pulls) >= pullLimit, nil
}

// proc is one process and the directory it is working in.
type proc struct {
	pid string
	cwd string
}

// processCwds lists every process this user can see, with its working
// directory. A listing that does not include this program's own process is
// refused, because lsof exits zero on a listing it could only partly read, and
// a listing that cannot see the one process known to exist cannot be trusted to
// see a lane's.
func (g repo) processCwds() ([]proc, error) {
	r := g.runner.run(g.dir, "lsof", "-a", "-d", "cwd", "-F", "pn")
	if r.err != nil {
		return nil, r.err
	}
	var procs []proc
	var pid string
	sawSelf := false
	self := strconv.Itoa(os.Getpid())
	for _, line := range strings.Split(r.stdout, "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			pid = line[1:]
			if pid == self {
				sawSelf = true
			}
		case strings.HasPrefix(line, "n") && pid != "":
			procs = append(procs, proc{pid: pid, cwd: line[1:]})
		}
	}
	if !sawSelf {
		return nil, fmt.Errorf("lsof exited %d and listed %d process(es) without this one, so its listing cannot be trusted to show a lane's: %s",
			r.code, len(procs), oneLine(r.stderr))
	}
	return procs, nil
}

// worktree is one record from `git worktree list --porcelain`.
type worktree struct {
	path     string
	head     string
	branch   string // empty when detached or bare
	detached bool
	bare     bool
	locked   bool
	prunable bool
	// malformed says the record could not be read, so this worktree is
	// unjudged rather than judged safe.
	malformed string
}

// parseWorktrees reads the porcelain listing strictly. Anything it cannot
// account for becomes a malformed record rather than a silently skipped line,
// because a worktree this program never saw and a worktree it decided to keep
// look identical in a summary.
func parseWorktrees(out string) []worktree {
	var all []worktree
	var cur *worktree
	flush := func() {
		if cur == nil {
			return
		}
		if cur.head == "" && !cur.bare {
			cur.malformed = "the porcelain record carried no HEAD line"
		}
		if !cur.bare && !cur.detached && cur.branch == "" && cur.malformed == "" {
			cur.malformed = "the porcelain record named neither a branch nor a detached HEAD"
		}
		all = append(all, *cur)
		cur = nil
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur = &worktree{path: strings.TrimPrefix(line, "worktree ")}
		case cur == nil:
			// A line before any worktree line, or trailing blank lines.
			if strings.TrimSpace(line) != "" {
				all = append(all, worktree{malformed: "a porcelain line arrived before any worktree line: " + line})
			}
		case strings.HasPrefix(line, "HEAD "):
			cur.head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			cur.branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "detached":
			cur.detached = true
		case line == "bare":
			cur.bare = true
		case line == "locked" || strings.HasPrefix(line, "locked "):
			cur.locked = true
		case line == "prunable" || strings.HasPrefix(line, "prunable "):
			cur.prunable = true
		case line == "":
			flush()
		default:
			cur.malformed = "the porcelain record carried a line this does not understand: " + line
		}
	}
	flush()
	return all
}

// judgment is what this program concluded about one worktree, and it has three
// shapes rather than two. removable and kept are both decisions. unproven is
// the absence of one, and it is reported separately and fails the run.
type judgment struct {
	wt        worktree
	removable bool
	// reason is the proof when removable, and why not when kept.
	reason string
	// unproven names the signal that could not be established. When it is
	// set, removable is false and reason is empty.
	unproven string
	// route says which shape of the third signal carried a removal, so the
	// report can distinguish a deletion that orphans no commit object at
	// all from one that orphans pre squash commits whose content is in main.
	route string
	// tip is the branch tip the proof was taken against.
	tip string
}

// env is everything a judgment is measured against, taken once per pass so every
// worktree in a pass is judged against the same main and the same process list.
type env struct {
	mainRef string
	merged  map[string][]pull
	// idle is how long nothing in a worktree may have been written before it
	// can be removed. Zero switches the recency signal off.
	idle    time.Duration
	now     time.Time
	procs   []proc
	procErr error
	self    string
}

// scan judges every worktree.
func scan(g repo, e env) ([]judgment, error) {
	r := g.git("worktree", "list", "--porcelain")
	if r.err != nil {
		return nil, r.err
	}
	if r.code != 0 {
		return nil, fmt.Errorf("git worktree list exited %d: %s", r.code, oneLine(r.stderr))
	}
	all := parseWorktrees(r.stdout)
	var out []judgment
	for i, wt := range all {
		j := judgment{wt: wt}
		switch {
		case wt.malformed != "":
			j.unproven = wt.malformed
		case i == 0:
			// `git worktree list` always reports the main working tree
			// first, and git refuses to remove it. Saying so beats
			// letting the removal fail later.
			j.reason = "this is the repository's main working tree and is never removed"
		case wt.bare:
			j.reason = "a bare repository holds no working tree to remove"
		case wt.locked:
			j.reason = "the worktree is locked, so somebody marked it as not disposable"
		case wt.prunable:
			j.unproven = "git reports the worktree as prunable, so its directory is gone and no working tree could be read"
		case e.self != "" && within(wt.path, e.self):
			j.reason = "this program is running inside that worktree"
		case protectedPath(wt.path):
			j.reason = "the path belongs to another session"
		case wt.detached:
			j = judgeDetached(g, wt, e.mainRef)
		case protectedBranches[wt.branch]:
			j.reason = wt.branch + " is protected and is never removed"
		default:
			j = judgeBranch(g, wt, e)
		}
		out = append(out, j)
	}
	return out, nil
}

// judgeDetached keeps a detached worktree and says what its HEAD holds. This
// program only removes worktrees that hold a branch, so the ancestry is here to
// make the reason specific rather than to license anything.
func judgeDetached(g repo, wt worktree, mainRef string) judgment {
	j := judgment{wt: wt}
	n, err := g.countUnreachable(wt.head, mainRef)
	if err != nil {
		j.unproven = "the worktree has a detached HEAD and what it holds could not be counted: " + err.Error()
		return j
	}
	if n > 0 {
		j.reason = fmt.Sprintf("detached at %s, which holds %d commit(s) %s cannot reach and no branch names, so removing it would leave them reachable from nothing else",
			short(wt.head), n, mainRef)
		return j
	}
	j.reason = fmt.Sprintf("detached at %s, which %s already holds. This removes only worktrees that hold a branch, so it is kept",
		short(wt.head), mainRef)
	return j
}

// judgeBranch applies the signals in order: the directory must be readable, the
// working tree clean, nothing ignored may be anything but build output, nobody
// may be working there, and nothing may be orphaned.
func judgeBranch(g repo, wt worktree, e env) judgment {
	j := judgment{wt: wt}

	if _, err := os.Stat(wt.path); err != nil {
		j.unproven = "the worktree directory could not be read, so nothing about its working tree was established: " + err.Error()
		return j
	}

	// No optional locks, so the scan never rewrites a lane's index. That
	// matters twice: a live lane's index is its to write, and an index this
	// program touched would make every worktree look recently used.
	st := g.runner.run(wt.path, "git", "--no-optional-locks", "status", "--porcelain", "--ignored=matching")
	if st.err != nil {
		j.unproven = "git status could not run in the worktree, so whether it holds uncommitted work is unknown: " + st.err.Error()
		return j
	}
	if st.code != 0 {
		j.unproven = fmt.Sprintf("git status exited %d in the worktree, so whether it holds uncommitted work is unknown: %s",
			st.code, oneLine(st.stderr))
		return j
	}
	var dirty, ignored []string
	for _, line := range strings.Split(strings.TrimRight(st.stdout, "\n"), "\n") {
		switch {
		case line == "":
		case strings.HasPrefix(line, "!! "):
			ignored = append(ignored, strings.TrimPrefix(line, "!! "))
		default:
			dirty = append(dirty, line)
		}
	}
	if len(dirty) > 0 {
		j.reason = fmt.Sprintf("UNCOMMITTED CHANGES in %d path(s), somebody's work in progress", len(dirty))
		return j
	}
	if work := notBuildOutput(ignored); len(work) > 0 {
		j.reason = fmt.Sprintf("%d ignored path(s) that no build writes would be deleted with the worktree, and a lane's scratch and handover are ignored by design: %s",
			len(work), strings.Join(firstN(work, 4), ", "))
		return j
	}

	if e.procErr != nil {
		j.unproven = "whether a process is working in it could not be established: " + e.procErr.Error()
		return j
	}
	for _, p := range e.procs {
		if within(wt.path, p.cwd) {
			j.reason = fmt.Sprintf("process %s is working in it, at %s", p.pid, p.cwd)
			return j
		}
	}

	if e.idle > 0 {
		newest, name, err := newestWrite(wt.path)
		if err != nil {
			j.unproven = "when anything in it was last written could not be read: " + err.Error()
			return j
		}
		if age := e.now.Sub(newest); age < e.idle {
			j.reason = fmt.Sprintf("%s was written %s ago, inside the %s idle window, so a lane may be working here",
				name, age.Round(time.Minute), e.idle)
			return j
		}
	}

	tip, err := g.rev(wt.branch)
	if err != nil {
		j.unproven = "the branch tip could not be resolved, so nothing about what it holds was established: " + err.Error()
		return j
	}
	j.tip = tip

	proof, route, refusal, err := proveNothingOrphaned(g, wt.branch, tip, e.mainRef, e.merged)
	if err != nil {
		j.unproven = err.Error()
		return j
	}
	if refusal != "" {
		j.reason = refusal
		return j
	}
	j.removable, j.reason, j.route = true, proof, route
	return j
}

// notBuildOutput filters an ignored listing down to what a build could not
// write again.
func notBuildOutput(ignored []string) []string {
	var work []string
	for _, p := range ignored {
		clean := strings.TrimSuffix(p, "/")
		regen := regenerableFiles[filepath.Base(clean)]
		for _, seg := range strings.Split(clean, "/") {
			if regenerableDirs[seg] {
				regen = true
			}
		}
		if !regen {
			work = append(work, p)
		}
	}
	return work
}

// newestWrite finds the most recently modified file in a worktree, skipping the
// .git link and dependency trees, whose times say when an install ran rather
// than when somebody worked.
func newestWrite(root string) (time.Time, string, error) {
	var newest time.Time
	var name string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil
		}
		if d.Name() == ".git" || d.Name() == "node_modules" {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
			name = p
		}
		return nil
	})
	if err != nil {
		return time.Time{}, "", err
	}
	if rel, rerr := filepath.Rel(root, name); rerr == nil {
		name = rel
	}
	return newest, name, nil
}

// proveNothingOrphaned is the third signal, and the reason this file exists.
//
// It returns a proof and a route when it holds, a refusal when it provably does
// not, and an error when it could not be established either way. Those are
// three different facts and collapsing any two of them is how the previous
// predicate came to delete unmerged work.
func proveNothingOrphaned(g repo, branch, tip, mainRef string, merged map[string][]pull) (proof, route, refusal string, err error) {
	unreachable, err := g.countUnreachable(tip, mainRef)
	if err != nil {
		return "", "", "", fmt.Errorf("the count of commits this branch holds and %s does not could not be taken, so nothing was established: %w", mainRef, err)
	}
	if unreachable == 0 {
		// Redundant with the count, and kept because the two answers
		// disagreeing would mean one of them is wrong and this program
		// must not pick a side silently.
		anc, aerr := g.isAncestor(tip, mainRef)
		if aerr != nil {
			return "", "", "", fmt.Errorf("ancestry against %s could not be read, so nothing was established: %w", mainRef, aerr)
		}
		if !anc {
			return "", "", "", fmt.Errorf("git reports zero commits on this branch that %s cannot reach, and also reports the tip is not an ancestor of %s. Those cannot both be true, so nothing is established", mainRef, mainRef)
		}
		return fmt.Sprintf("zero commits are reachable from this branch and not from %s, and the tip %s is an ancestor of it, so deleting the ref cannot orphan a commit",
			mainRef, short(tip)), "no distinct commits", "", nil
	}

	candidates := merged[branch]
	if len(candidates) == 0 {
		return "", "", fmt.Sprintf("%d commit(s) here are not reachable from %s and GitHub reports no merged pull request from this branch, so they are in no other place",
			unreachable, mainRef), nil
	}

	var tried []string
	for _, p := range candidates {
		if p.MergeCommit == nil || p.MergeCommit.Oid == "" {
			tried = append(tried, fmt.Sprintf("pull request %d is merged but GitHub named no merge commit for it", p.Number))
			continue
		}
		anc, aerr := g.isAncestor(p.MergeCommit.Oid, mainRef)
		if aerr != nil {
			return "", "", "", fmt.Errorf("whether pull request %d's merge commit %s is in %s could not be read, so nothing was established: %w",
				p.Number, short(p.MergeCommit.Oid), mainRef, aerr)
		}
		if !anc {
			tried = append(tried, fmt.Sprintf("pull request %d's merge commit %s is not an ancestor of %s, so it did not land here",
				p.Number, short(p.MergeCommit.Oid), mainRef))
			continue
		}
		if len(p.HeadRefOid) != 40 {
			return "", "", "", fmt.Errorf("pull request %d's head object name is %q, which is not an object name, so the tip could not be compared against it",
				p.Number, p.HeadRefOid)
		}
		if p.HeadRefOid != tip {
			// The measured defect. The pull request merged, its squash
			// is in main, and the branch carries commits written after
			// it. Count them so the refusal says how much would have
			// been lost.
			after, cerr := g.countUnreachable(tip, p.HeadRefOid)
			if cerr != nil {
				return "", "", "", fmt.Errorf("pull request %d merged %s and this branch is at %s, and how far ahead it is could not be counted, so nothing was established: %w",
					p.Number, short(p.HeadRefOid), short(tip), cerr)
			}
			tried = append(tried, fmt.Sprintf("pull request %d squashed %s but the branch is at %s, which is %d commit(s) later, so the branch holds work written after the merge",
				p.Number, short(p.HeadRefOid), short(tip), after))
			continue
		}
		return fmt.Sprintf("pull request %d is merged, its merge commit %s is an ancestor of %s, and the branch tip %s is the exact commit it squashed, so the branch holds nothing written after the merge",
			p.Number, short(p.MergeCommit.Oid), mainRef, short(tip)), "squashed by a merged pull request", "", nil
	}
	return "", "", fmt.Sprintf("%d commit(s) here are not reachable from %s and no merged pull request accounts for them: %s",
		unreachable, mainRef, strings.Join(tried, "; ")), nil
}

// apply removes the worktrees and deletes the branches that were proven
// disposable. The scan and the removal are two events, and five lanes work on
// this machine at once, so every worktree is judged AGAIN immediately before it
// is touched, against a fresh process list and a fresh clock. A worktree that
// stopped being disposable in between is a refusal, not a race this program
// wins. The branch is then deleted by compare and swap against the tip that was
// proven, so a commit landing in the last millisecond fails the deletion rather
// than being deleted with it.
func apply(g repo, js []judgment, e env, out io.Writer) (removed int, refused int) {
	for _, j := range js {
		if !j.removable {
			continue
		}
		again := judgeBranch(g, j.wt, e)
		if again.unproven != "" {
			_, _ = fmt.Fprintf(out, "  kept %s: a signal could not be re-established just before removing it: %s\n", j.wt.path, again.unproven)
			refused++
			continue
		}
		if !again.removable {
			_, _ = fmt.Fprintf(out, "  kept %s: it stopped being disposable between the scan and now: %s\n", j.wt.path, again.reason)
			refused++
			continue
		}
		tip := again.tip

		// The backup ref first, so nothing is unreachable at any moment.
		ref := backupRefPrefix + j.wt.branch
		if r := g.git("update-ref", ref, tip); r.err != nil || r.code != 0 {
			_, _ = fmt.Fprintf(out, "  kept %s: the backup ref %s could not be written, so the deletion would not be reversible: %s\n",
				j.wt.path, ref, firstNonEmpty(errText(r.err), oneLine(r.stderr), "git update-ref exited "+strconv.Itoa(r.code)))
			refused++
			continue
		}
		if got, err := g.rev(ref); err != nil || got != tip {
			_, _ = fmt.Fprintf(out, "  kept %s: the backup ref %s does not read back as %s, so the deletion would not be reversible\n",
				j.wt.path, ref, short(tip))
			refused++
			continue
		}

		if r := g.git("worktree", "remove", j.wt.path); r.err != nil || r.code != 0 {
			_, _ = fmt.Fprintf(out, "  kept %s: %s\n", j.wt.path,
				firstNonEmpty(errText(r.err), oneLine(r.stderr), "git worktree remove exited "+strconv.Itoa(r.code)))
			refused++
			continue
		}
		if r := g.git("update-ref", "-d", "refs/heads/"+j.wt.branch, tip); r.err != nil || r.code != 0 {
			_, _ = fmt.Fprintf(out, "  removed %s but kept the branch %s, which is no longer at the proven tip %s: %s\n",
				j.wt.path, j.wt.branch, short(tip),
				firstNonEmpty(errText(r.err), oneLine(r.stderr), "git update-ref -d exited "+strconv.Itoa(r.code)))
			refused++
			continue
		}
		// What `git branch -D` would also have removed. Absent for most
		// branches, and its absence is not a failure.
		g.git("config", "--remove-section", "branch."+j.wt.branch)
		_, _ = fmt.Fprintf(out, "  removed %s and deleted %s (was %s, kept at %s)\n",
			j.wt.path, j.wt.branch, short(tip), ref)
		removed++
	}
	g.git("worktree", "prune")
	return removed, refused
}

func report(js []judgment, truncated bool, e env, mainSha string, branchesWithoutWorktree []string, out io.Writer) {
	var removable, kept, unproven []judgment
	for _, j := range js {
		switch {
		case j.unproven != "":
			unproven = append(unproven, j)
		case j.removable:
			removable = append(removable, j)
		default:
			kept = append(kept, j)
		}
	}

	_, _ = fmt.Fprintf(out, "%s is %s\n", e.mainRef, mainSha)
	_, _ = fmt.Fprintf(out, "%d removable, %d kept, %d NOT CHECKED\n\n", len(removable), len(kept), len(unproven))

	var freed float64
	if len(removable) > 0 {
		_, _ = fmt.Fprintln(out, "REMOVABLE, proven to orphan nothing and to be idle")
		for _, j := range removable {
			gb := sizeGB(j.wt.path)
			freed += gb
			_, _ = fmt.Fprintf(out, "  %6.2f GB  %s  [%s]\n", gb, j.wt.path, j.wt.branch)
			_, _ = fmt.Fprintf(out, "            %s: %s\n", j.route, j.reason)
		}
		_, _ = fmt.Fprintf(out, "\n  %.1f GB total\n\n", freed)
	}

	if len(kept) > 0 {
		_, _ = fmt.Fprintln(out, "KEPT, a signal says no")
		for _, j := range kept {
			_, _ = fmt.Fprintf(out, "  %s  [%s]\n            %s\n", j.wt.path, nameOr(j.wt.branch, "detached"), j.reason)
		}
		_, _ = fmt.Fprintln(out)
	}

	// The section that exists because a tool which passes over what it could
	// not read looks exactly like a tool that found nothing wrong.
	_, _ = fmt.Fprintln(out, "NOT CHECKED, a signal could not be established")
	if len(unproven) == 0 {
		_, _ = fmt.Fprintln(out, "  nothing. Every worktree above was judged on established signals.")
	}
	for _, j := range unproven {
		_, _ = fmt.Fprintf(out, "  %s  [%s]\n            %s\n", j.wt.path, nameOr(j.wt.branch, "detached"), j.unproven)
	}
	_, _ = fmt.Fprintln(out)

	_, _ = fmt.Fprintln(out, "OUT OF SCOPE, stated rather than left to be assumed")
	_, _ = fmt.Fprintf(out, "  %d local branch(es) have no worktree. This command removes worktrees and the\n", len(branchesWithoutWorktree))
	_, _ = fmt.Fprintln(out, "  branches they held, so a branch with no worktree is never judged and never")
	_, _ = fmt.Fprintln(out, "  deleted here, however landed it is.")
	if len(branchesWithoutWorktree) > 0 {
		_, _ = fmt.Fprintf(out, "  %s\n", strings.Join(branchesWithoutWorktree, " "))
	}
	names := make([]string, 0, len(regenerableDirs))
	for n := range regenerableDirs {
		names = append(names, n)
	}
	sort.Strings(names)
	_, _ = fmt.Fprintf(out, "  Ignored files under %s are treated as build output and are\n", strings.Join(names, ", "))
	_, _ = fmt.Fprintln(out, "  deleted with a worktree without being counted as work.")
	_, _ = fmt.Fprintln(out, "  Processes are read with lsof as this user, so another user's process working in")
	_, _ = fmt.Fprintln(out, "  a worktree is not seen.")
	if e.idle > 0 {
		_, _ = fmt.Fprintf(out, "  A worktree with any file written in the last %s is kept as possibly in use.\n", e.idle)
	} else {
		_, _ = fmt.Fprintln(out, "  The recency signal was switched off with -idle 0, so a lane that is working without")
		_, _ = fmt.Fprintln(out, "  a process inside its worktree and without ignored scratch would not be seen.")
	}
	if truncated {
		_, _ = fmt.Fprintf(out, "  gh returned the full %d pull requests it was asked for, so the merged list may\n", pullLimit)
		_, _ = fmt.Fprintln(out, "  be truncated. That can only make this keep a branch it could have removed.")
	}
	_, _ = fmt.Fprintln(out)
}

// branchesWithoutWorktrees is the out of scope list the report prints.
func branchesWithoutWorktrees(g repo, js []judgment) ([]string, error) {
	r := g.git("for-each-ref", "--format=%(refname:short)", "refs/heads")
	if r.err != nil {
		return nil, r.err
	}
	if r.code != 0 {
		return nil, fmt.Errorf("git for-each-ref exited %d: %s", r.code, oneLine(r.stderr))
	}
	held := map[string]bool{}
	for _, j := range js {
		if j.wt.branch != "" {
			held[j.wt.branch] = true
		}
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(r.stdout), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || held[name] {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// options are what the command line decides.
type options struct {
	mainRef string
	fetch   bool
	act     bool
	idle    time.Duration
}

func run(g repo, o options, out io.Writer) int {
	if o.fetch {
		// A stale main can only make this program keep a branch it could
		// have removed, so the fetch is not what makes it safe. It blocks
		// -apply anyway, because the report names one exact object name as
		// the main every proof was measured against, and a failed fetch
		// means that name may not be main.
		r := g.git("fetch", "origin", "--quiet")
		if r.err != nil || r.code != 0 {
			_, _ = fmt.Fprintf(out, "git fetch origin failed: %s\n",
				firstNonEmpty(errText(r.err), oneLine(r.stderr), "exit "+strconv.Itoa(r.code)))
			_, _ = fmt.Fprintf(out, "So %s may not be main, and every proof below would name an object that is not it.\n", o.mainRef)
			if o.act {
				_, _ = fmt.Fprintln(out, "Refusing to remove anything. Pass -no-fetch to measure against the "+o.mainRef+" already on disk.")
				return 1
			}
		}
	}

	mainSha, err := g.rev(o.mainRef)
	if err != nil {
		_, _ = fmt.Fprintf(out, "%s could not be resolved, so nothing can be judged safe. Refusing rather than guessing.\n%v\n", o.mainRef, err)
		return 1
	}

	merged, truncated, err := g.mergedPulls()
	if err != nil {
		_, _ = fmt.Fprintf(out, "gh could not list merged pull requests, so nothing can be judged safe. Refusing rather than guessing.\n%v\n", err)
		return 1
	}

	e := env{mainRef: o.mainRef, merged: merged, idle: o.idle, now: time.Now()}
	e.self, _ = os.Getwd()
	e.procs, e.procErr = g.processCwds()

	js, err := scan(g, e)
	if err != nil {
		_, _ = fmt.Fprintf(out, "the worktree list could not be read, so nothing can be judged safe. Refusing rather than guessing.\n%v\n", err)
		return 1
	}

	loose, err := branchesWithoutWorktrees(g, js)
	if err != nil {
		_, _ = fmt.Fprintf(out, "the branch list could not be read, so what is out of scope could not be stated.\n%v\n", err)
		return 1
	}

	report(js, truncated, e, mainSha, loose, out)

	unproven := 0
	for _, j := range js {
		if j.unproven != "" {
			unproven++
		}
	}

	if !o.act {
		_, _ = fmt.Fprintln(out, "Dry run. Nothing was removed. Pass -apply to act.")
		if unproven > 0 {
			return 1
		}
		return 0
	}

	// Fresh for the second judgment, which is the point of taking it.
	e.now = time.Now()
	e.procs, e.procErr = g.processCwds()
	removed, refused := apply(g, js, e, out)
	_, _ = fmt.Fprintf(out, "\nDone. %d removed, %d refused at the last moment.\n", removed, refused)
	if removed > 0 {
		_, _ = fmt.Fprintf(out, "Every deleted tip is at %s<branch>. Restore one with\n", backupRefPrefix)
		_, _ = fmt.Fprintf(out, "  git branch <name> %s<name>\n", backupRefPrefix)
		_, _ = fmt.Fprintf(out, "and list them with `git for-each-ref %s`.\n", backupRefPrefix)
	}
	if unproven > 0 || refused > 0 {
		return 1
	}
	return 0
}

func main() {
	dir := flag.String("C", ".", "the repository to work in")
	mainRef := flag.String("main", "origin/main", "the revision landed work must be reachable from")
	act := flag.Bool("apply", false, "actually remove. Without it this only reports.")
	noFetch := flag.Bool("no-fetch", false, "do not fetch origin first")
	idle := flag.Duration("idle", 24*time.Hour, "keep a worktree with any file written this recently. 0 switches the signal off.")
	flag.Parse()

	abs, err := filepath.Abs(*dir)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(run(repo{runner: local{}, dir: abs},
		options{mainRef: *mainRef, fetch: !*noFetch, act: *act, idle: *idle}, os.Stdout))
}

func short(sha string) string {
	if len(sha) > 9 {
		return sha[:9]
	}
	return sha
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return append(append([]string{}, s[:n]...), fmt.Sprintf("and %d more", len(s)-n))
	}
	return s
}

func nameOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func protectedPath(path string) bool {
	for _, p := range protectedPaths {
		if within(p, path) {
			return true
		}
	}
	return false
}

// within answers whether path is prefix or lies under it, on path boundaries, so
// /Users/vir/af-work/infra does not also protect /Users/vir/af-work/infrastructure.
func within(prefix, path string) bool {
	prefix = filepath.Clean(prefix)
	path = filepath.Clean(path)
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, prefix+string(filepath.Separator))
}

func sizeGB(path string) float64 {
	var total int64
	// The error is dropped on purpose: a size this could not finish measuring is
	// a smaller number in a report, and never a reason to keep or remove anything.
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return float64(total) / 1e9
}
