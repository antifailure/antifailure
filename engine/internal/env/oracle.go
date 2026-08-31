package env

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/oracle"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The differential oracle brings a SECOND environment up and compares the two.
//
// Everything hard about it already existed. An environment comes up from a
// commit, a masked golden branches so both sides hold the same rows, the
// sidecar records what either one reached, and the runner drives real
// workflows. What was missing was the comparison and the second environment,
// and this file is the second environment.
//
// Two environments rather than one, and one golden rather than two. Both are
// forced choices. Two versions of an application cannot run in one environment
// because they want the same ports, the same service names and the same
// database; and two goldens would mean the two sides started from different
// rows, which turns every row in the report into noise. The candidate comes up
// first and the baseline is pinned to whatever golden the candidate used, so a
// scheduled refresh landing between the two cannot separate them.
//
// The baseline's images are built from a checkout of the baseline revision and
// everything else comes from the candidate's manifest: the same egress policy,
// the same personas, the same ports, the same secrets. If the baseline's own
// manifest were used, a manifest change in the pull request would move both the
// application and the harness, and no difference in the report could be
// attributed to either.

// baselineSuffix distinguishes the baseline environment's identifier.
//
// Part of the branch name rather than of the project name, because EnvID hashes
// both and a suffix on the branch is what makes `af down --branch` able to
// address the baseline by hand when a teardown was interrupted.
const baselineSuffix = " (oracle baseline)"

// OracleOptions are the choices a caller makes.
type OracleOptions struct {
	// BaseRef overrides the manifest's oracle.base_ref.
	BaseRef string
	// Keep leaves the baseline environment running, for looking at a
	// difference by hand.
	Keep bool
	// Progress receives a line per probe.
	Progress func(name string, index, total int)
}

// OracleResult is the comparison and what it cost.
type OracleResult struct {
	*oracle.Result
	// BaselineTornDown reports whether the second environment was removed. A
	// false here with Keep unset is a leak somebody has to finish by hand, and
	// the command says the exact line to run.
	BaselineTornDown bool
	// BaselineBranch is the branch name the baseline environment was created
	// under, which is what `af down --branch` takes.
	BaselineBranch string
}

// Oracle runs the whole comparison.
//
// It never tears the CANDIDATE environment down, whether or not this call
// brought it up. Somebody who had an environment open and ran this would
// otherwise lose it, and the environment is the expensive thing. The baseline
// is always removed unless Keep says otherwise, because nothing else will ever
// look at it.
func (o *Orchestrator) Oracle(ctx context.Context, opts OracleOptions) (*OracleResult, error) {
	cfg := o.opts.Manifest.Oracle
	if cfg == nil {
		return nil, aferrors.Coded(aferrors.AFORC001)
	}
	if len(cfg.Probes) == 0 {
		return nil, aferrors.Coded(aferrors.AFORC002)
	}

	started := o.opts.Clock.Now()
	baseRef := opts.BaseRef
	if baseRef == "" {
		baseRef = cfg.BaseRef
	}
	rev, how, err := resolveBaseline(o.opts.Root, cfg.Baseline, baseRef)
	if err != nil {
		return nil, err
	}
	head := gitOutput(o.opts.Root, "rev-parse", "HEAD")
	if head != "" && head == rev {
		return nil, aferrors.Coded(aferrors.AFORC004, "commit", short(rev))
	}

	// The candidate first, so the golden it uses is the one to pin.
	o.progress("bringing the candidate up")
	candidate, err := o.Up(ctx)
	if err != nil {
		return nil, err
	}
	if candidate.URL == "" {
		return nil, aferrors.Coded(aferrors.AFORC006, "side", "candidate")
	}

	tree, cleanTree, err := o.baselineTree(ctx, rev)
	if err != nil {
		return nil, err
	}
	defer cleanTree()

	baseline, err := o.baselineOrchestrator(tree, candidate.Golden)
	if err != nil {
		return nil, err
	}

	result := &OracleResult{BaselineBranch: o.opts.Branch + baselineSuffix}
	o.progress("bringing " + short(rev) + " up beside it as the baseline")
	baseEnv, upErr := baseline.Up(ctx)
	if !opts.Keep {
		// Deferred rather than a later step, for the reason af ci gives: a
		// step that runs after a failing step does not run, and an environment
		// that outlives its comparison is the leak this product exists to
		// prevent. Registered before the error is checked, because a failed Up
		// leaves resources behind and those are exactly the ones to remove.
		defer func() {
			c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
			defer cancel()
			if td, downErr := baseline.Down(c); downErr == nil {
				result.BaselineTornDown = true
				o.progress("the baseline is torn down, " +
					plural(td.Removed, "resource", "resources") + " removed")
			}
		}()
	}
	if upErr != nil {
		return result, aferrors.Wrap(upErr, aferrors.AFORC007, "detail", upErr.Error())
	}
	if baseEnv.URL == "" {
		return result, aferrors.Coded(aferrors.AFORC006, "side", "baseline")
	}

	in := oracle.Input{
		Config:   comparisonConfig(cfg),
		Database: databaseOptions(cfg),
	}

	// Before any request, so a row that already differs is attributed to the
	// two sets of migrations rather than to the two versions of the code.
	compareDB := cfg.Database == nil || cfg.Database.Enabled == nil || *cfg.Database.Enabled
	if compareDB {
		o.progress("reading both branches before any request")
		if in.BaselineBefore, in.CandidateBefore, err = snapshotBoth(ctx, baseline, o, in.Database); err != nil {
			return result, err
		}
	}

	o.progress("sending " + plural(len(cfg.Probes), "request", "requests") + " to both versions")
	driver := &oracle.Driver{Clock: o.opts.Clock, Client: &http.Client{Timeout: 60 * time.Second}}
	probes := oracle.Drive(ctx, driver, baseEnv.URL, candidate.URL, toProbes(cfg.Probes), opts.Progress)
	in.Probes = probes

	if compareDB {
		o.progress("reading both branches again")
		if in.BaselineAfter, in.CandidateAfter, err = snapshotBoth(ctx, baseline, o, in.Database); err != nil {
			return result, err
		}
	}

	res := oracle.Compare(in)
	res.BaselineRef, res.CandidateRef, res.BaselineHow = rev, head, how
	res.Golden = candidate.Golden
	res.BaselineEnv, res.CandidateEnv = baseline.EnvID(), o.envID
	res.DurationMs = o.opts.Clock.Since(started).Milliseconds()
	if !compareDB {
		res.Notes = append(res.Notes,
			"The contents of the two branches were not compared, because "+
				"oracle.database.enabled is false.")
	}
	result.Result = res
	return result, nil
}

// snapshotBoth reads the two branches, naming which one failed.
//
// Both or neither. A comparison against one snapshot is not a comparison, and
// returning the one that worked would produce a report where every table on the
// missing side reads as dropped.
func snapshotBoth(
	ctx context.Context, baseline, candidate *Orchestrator, opts oracle.DatabaseOptions,
) (base, cand *oracle.Snapshot, err error) {
	if base, err = baseline.snapshotBranch(ctx, opts); err != nil {
		return nil, nil, aferrors.Wrap(err, aferrors.AFORC008,
			"side", "baseline", "detail", err.Error())
	}
	if cand, err = candidate.snapshotBranch(ctx, opts); err != nil {
		return nil, nil, aferrors.Wrap(err, aferrors.AFORC008,
			"side", "candidate", "detail", err.Error())
	}
	return base, cand, nil
}

// snapshotBranch reads this environment's own database.
//
// A session per call, like RunInvariants, rather than one held across the whole
// comparison. Holding the environment lock for the length of a run would stop
// anybody looking at the environment while it happened, and the four snapshots
// this takes are seconds apart rather than minutes.
func (o *Orchestrator) snapshotBranch(
	ctx context.Context, opts oracle.DatabaseOptions,
) (*oracle.Snapshot, error) {
	s, err := o.open(ctx, "af oracle")
	if err != nil {
		return nil, err
	}
	defer s.close()

	conn, err := connectSession(ctx, o, s)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	return oracle.Capture(ctx, conn, opts)
}

// baselineOrchestrator builds the second environment's lifecycle.
//
// The candidate's manifest, the candidate's root, the candidate's secrets, and
// only the build context and the branch name differ. Anything else taken from
// the baseline checkout would let a manifest change move the harness and the
// application at once, and then no difference in the report could be attributed
// to either.
func (o *Orchestrator) baselineOrchestrator(tree, golden string) (*Orchestrator, error) {
	opts := o.opts
	opts.Branch = o.opts.Branch + baselineSuffix
	opts.BuildRoot = tree
	opts.PinGolden = golden
	opts.Progress = func(line string) {
		// Prefixed, because two environments coming up produce two identical
		// streams of "web is starting" and a reader cannot tell which is which.
		o.progress("baseline: " + line)
	}
	return New(opts)
}

// comparisonConfig turns the manifest block into what the comparison reads.
func comparisonConfig(cfg *schema.Oracle) oracle.Config {
	out := oracle.Config{
		KeepTimestamps: cfg.CompareTimestamps,
		KeepUUIDs:      cfg.CompareUUIDs,
	}
	if cfg.Ignore != nil {
		out.IgnoreHeaders = cfg.Ignore.Headers
		out.IgnoreFields = cfg.Ignore.Fields
	}
	return out
}

func databaseOptions(cfg *schema.Oracle) oracle.DatabaseOptions {
	if cfg.Database == nil {
		return oracle.DatabaseOptions{}
	}
	return oracle.DatabaseOptions{
		Include: cfg.Database.Tables,
		Exclude: cfg.Database.Exclude,
		MaxRows: cfg.Database.MaxRows,
	}
}

func toProbes(in []schema.Probe) []oracle.Probe {
	out := make([]oracle.Probe, 0, len(in))
	for _, p := range in {
		out = append(out, oracle.Probe{
			Name: p.Name, Method: p.Method, Path: p.Path,
			Headers: p.Headers, Body: p.Body,
		})
	}
	return out
}

// resolveBaseline decides which revision the comparison is against, and says
// how it decided.
//
// The how matters as much as the revision. "The merge base with origin/main"
// and "the tag v2.4.0" answer different questions, and a report that named a
// commit without saying which question it answered would leave the reader to
// guess.
func resolveBaseline(root string, source schema.BaselineSource, ref string) (rev, how string, err error) {
	if ref == "" {
		// In this order because it is the order of confidence. origin/HEAD is
		// what the remote itself says its default branch is; the rest are the
		// two names almost everybody uses, remote before local because a local
		// main can be behind.
		for _, candidate := range []string{"origin/HEAD", "origin/main", "origin/master", "main", "master"} {
			if gitOutput(root, "rev-parse", "--verify", "--quiet", candidate+"^{commit}") != "" {
				ref = candidate
				break
			}
		}
	}
	if ref == "" {
		return "", "", aferrors.Coded(aferrors.AFORC003, "detail",
			"no oracle.base_ref is set and none of origin/HEAD, origin/main, "+
				"origin/master, main or master exists in this checkout")
	}

	if source == schema.BaselineRef {
		rev = gitOutput(root, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
		if rev == "" {
			return "", "", aferrors.Coded(aferrors.AFORC003, "detail",
				ref+" is not a revision this checkout can resolve")
		}
		return rev, "the ref " + ref, nil
	}

	rev = gitOutput(root, "merge-base", "HEAD", ref)
	if rev == "" {
		return "", "", aferrors.Coded(aferrors.AFORC003, "detail",
			"HEAD and "+ref+" have no merge base in this checkout, which usually "+
				"means the clone is shallow")
	}
	return rev, "the merge base with " + ref, nil
}

// repoLayout reports the repository's top level and where the manifest's
// directory sits inside it, in git's own path syntax.
//
// Asked of git rather than computed from the two paths. The top level and the
// root can differ in symlinks, in case, and in trailing separators on the three
// platforms this runs on, and rev-parse already knows the answer.
func repoLayout(root string) (top, prefix string) {
	return gitOutput(root, "rev-parse", "--show-toplevel"),
		strings.TrimSuffix(gitOutput(root, "rev-parse", "--show-prefix"), "/")
}

// gitOutput runs a git command and returns its trimmed output, or empty.
//
// Empty rather than an error for every failure, because every caller here is
// asking "does this resolve" and an exit status is the answer. The one caller
// that needs a reason builds it from what it asked for.
func gitOutput(root string, args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// baselineTree writes the baseline revision into a temporary directory and
// returns a function that removes it.
//
// git archive rather than git worktree. A worktree writes into .git/worktrees
// and takes a lock there, so an interrupted run leaves an entry somebody has to
// find and prune, and two comparisons in one repository would contend. An
// archive is a tar of one commit and the cleanup is a directory removal.
//
// OUTSIDE the repository, which is not a detail. A build context is the whole
// root minus what .dockerignore excludes, and nothing excludes .antifailure, so
// a checkout under the state directory would land in the candidate's own build
// context: a second copy of the source inside the image, and a context digest
// that changes on every run, which is a cache miss on every build forever. The
// checkout holds no state teardown needs, so it does not have to be findable
// after a crash the way the journal does.
//
// Untarred in Go rather than by shelling out to tar, so that the path
// confinement is this package's rather than the local tar's: an archive entry
// naming ../../etc would otherwise write outside the directory this function
// promises to remove.
func (o *Orchestrator) baselineTree(ctx context.Context, rev string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "af-oracle-"+o.envID+"-")
	if err != nil {
		return "", nil, aferrors.Wrap(err, aferrors.AFORC005, "commit", short(rev), "detail", err.Error())
	}
	clean := func() { _ = os.RemoveAll(dir) }

	// The tree at that revision, and only the part of it this manifest is
	// about. A manifest in a subdirectory of a monorepo asks git for
	// "<rev>:<prefix>", which archives that subtree with paths relative to it;
	// plain "<rev>" would archive the whole repository and put every file one
	// directory deeper than the build context expects.
	//
	// Run from the repository's top level, not from the manifest's directory.
	// git archive restricts its output to the working directory's path INSIDE
	// the tree it was given, so asking for the subtree from inside the
	// subdirectory looks for services/api within services/api and produces an
	// empty archive. That is what the first version of this did, and the
	// symptom was a baseline build failing with no Dockerfile in a tree that
	// plainly had one.
	from, spec := o.opts.Root, rev
	if top, prefix := repoLayout(o.opts.Root); prefix != "" {
		from, spec = top, rev+":"+prefix
	}
	cmd := exec.CommandContext(ctx, "git", "-C", from, "archive", "--format=tar", spec)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		clean()
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", nil, aferrors.Coded(aferrors.AFORC005, "commit", short(rev), "detail", detail)
	}

	if err := untar(dir, &stdout); err != nil {
		clean()
		return "", nil, aferrors.Wrap(err, aferrors.AFORC005, "commit", short(rev), "detail", err.Error())
	}
	o.progress("checked " + short(rev) + " out to " + dir + " to compare against")
	return dir, clean, nil
}

// untar writes an archive into a directory, refusing anything that would land
// outside it.
func untar(dir string, r io.Reader) error {
	tr := tar.NewReader(r)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, ok := confined(dir, header.Name)
		if !ok {
			return errors.New("the archive holds a path that resolves out of the checkout: " + header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode)&0o777)
			if err != nil {
				return err
			}
			// Bounded, so a crafted archive cannot fill the disk claiming to
			// hold one file. A repository this size is not a repository the
			// build was going to succeed on anyway.
			_, err = io.Copy(f, io.LimitReader(tr, 1<<30))
			if closeErr := f.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				return err
			}
		case tar.TypeSymlink:
			// A symlink out of the tree is the same escape as a path out of
			// it, so the target is confined too. Inside the tree it is kept,
			// because a repository that uses one to share a file between
			// services builds differently without it.
			if _, ok := confined(dir, filepath.Join(filepath.Dir(header.Name), header.Linkname)); !ok {
				return errors.New("the archive holds a symlink out of the checkout: " + header.Name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(header.Linkname, target); err != nil {
				return err
			}
		default:
			// Everything else a git archive can hold is metadata this build
			// does not need. Skipped rather than refused: a commit with a
			// submodule entry is not a reason to abandon the comparison.
		}
	}
}

// confined resolves a path inside a directory, or reports that it escapes.
func confined(dir, name string) (string, bool) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	target := filepath.Join(dir, clean)
	if target != dir && !strings.HasPrefix(target, dir+string(filepath.Separator)) {
		return "", false
	}
	return target, true
}

func short(rev string) string {
	if len(rev) > 8 {
		return rev[:8]
	}
	return rev
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
