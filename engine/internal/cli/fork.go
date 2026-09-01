package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/ghevent"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The fork gate: where `github.fork_policy` stops being a printed word.
//
// The claim in the schema is "a fork's code would otherwise run with the
// environment's credentials", and the claim in the pull request guide is
// "nothing runs until a maintainer adds the label, which is a person
// deciding". Neither was true. ForkPolicy was normalised, validated and
// printed by `af explain`, and no code path anywhere read it, so
// `fork_policy: never` refused nothing and the label was a string in two
// descriptions.
//
// What customers actually had was GitHub's own default, which withholds
// secrets from a fork's `pull_request` job on a GitHub-hosted runner. That is
// real and it is not this. It does nothing on a self-hosted runner, where the
// Docker daemon, the registry login and the network are ambient on the
// machine, and it does nothing under `pull_request_target`, which hands the
// base repository's secrets to a job checking out a stranger's code on
// purpose. Self-hosted is the ordinary shape for this product, because an
// environment needs a daemon and a golden.
//
// THE TRAP THIS AVOIDS, and it is the whole reason the gate is not three
// lines: the manifest is IN the repository, so on a fork pull request the
// checked out `antifailure.yaml` is the FORK'S manifest. Reading the policy
// out of the tree under test would let anyone defeat the control by adding
// `fork_policy: always` to their own pull request, which is a security control
// that is not one. The policy is therefore read from the BASE branch, which is
// the only copy the fork cannot edit, and the base commit is taken from a ref
// GitHub wrote rather than from anything in the checkout.
//
// It fails closed. Every state where fork-ness or the policy cannot be
// established lands on `label`, which needs a person, rather than on "carry
// on". The one exception is a run that is not a pull request at all: a push, a
// dispatch and a workstation have no fork in the question and the policy is
// silent for them.

// forkDecision is what the gate concluded, plus how it got there.
type forkDecision struct {
	ghevent.Decision
	// Policy is the policy that was applied.
	Policy schema.ForkPolicy
	// Source says where the policy was read from, for the report. A gate that
	// silently used a different file than the reader is looking at is a gate
	// nobody can debug.
	Source string
	// Notes are what could not be established, said out loud. A fallback that
	// happens quietly is a fallback nobody knows they are relying on.
	Notes []string
}

// forkGate decides whether this process may bring an environment up.
//
// It reads the manifest itself rather than taking one, because it has to run
// before the orchestrator is built. Building an orchestrator resolves
// providers, reaches the Docker daemon and names an environment, and a
// refusal that happens after all of that is a refusal that already did some of
// what it was refusing.
func forkGate(e *Env) forkDecision {
	pr, err := ghevent.Read(e.Getenv)
	if err != nil {
		// A pull request event whose payload could not be read. This is the
		// state where an early version of this function would have been
		// worthless: folding it into "not a pull request" makes every
		// unreadable payload an allow, and the payload is the only thing that
		// says whether the code is a stranger's.
		return forkDecision{
			Decision: ghevent.Decision{Refused: true, Reason: fmt.Sprintf(
				"This is a %s event and the payload could not be read (%v), so whether the code "+
					"comes from a fork could not be established. Nothing runs on an answer nobody has.",
				e.Getenv("GITHUB_EVENT_NAME"), err)},
			Policy: schema.ForkLabel,
			Source: "not read",
		}
	}
	if pr == nil || !pr.FromFork() {
		return forkDecision{}
	}

	policy, source, notes := baseForkPolicy(e)
	d := forkDecision{
		Decision: ghevent.Decide(policy, pr),
		Policy:   policy, Source: source, Notes: notes,
	}
	return d
}

// baseForkPolicy reads github.fork_policy from the base branch.
//
// The returned policy is never empty: every failure lands on label, because
// label is the answer that asks a person and every other answer decides on
// their behalf with less information than they have.
func baseForkPolicy(e *Env) (schema.ForkPolicy, string, []string) {
	path, err := manifest.Find(e.WorkDir)
	if err != nil {
		return schema.ForkLabel, "the safe default",
			[]string{"there is no manifest here (" + err.Error() + "), so the fork policy is the default, `label`"}
	}
	root := repoRoot(path)
	rel := strings.TrimPrefix(strings.TrimPrefix(path, root), "/")
	if rel == "" {
		rel = "antifailure.yaml"
	}

	for _, ref := range baseRefs(e, root) {
		body, gitErr := exec.Command("git", "-C", root, "show", ref.rev+":"+rel).Output()
		if gitErr != nil {
			continue
		}
		m, parseErr := manifest.Parse(body, ref.rev+":"+rel, root)
		if parseErr != nil {
			// A base manifest this build cannot parse is not a licence to use
			// the fork's. Said, and then the default.
			return schema.ForkLabel, "the safe default", []string{
				"the manifest on " + ref.name + " could not be read (" + parseErr.Error() +
					"), so the fork policy is the default, `label`"}
		}
		if m.GitHub == nil {
			return schema.ForkLabel, ref.name, nil
		}
		return m.GitHub.ForkPolicy, ref.name, nil
	}

	// Nothing in this checkout carries the base branch. The usual cause is a
	// shallow clone, which is why the workflow template sets fetch-depth: 0,
	// and the usual symptom until now was nothing at all.
	return schema.ForkLabel, "the safe default", []string{
		"the base branch is not in this checkout, so the fork policy could not be read from it " +
			"and is the default, `label`. Check out with `fetch-depth: 0`"}
}

// baseRef is one place the base branch's manifest might be.
type baseRef struct {
	// rev is what git is asked for.
	rev string
	// name is how it is described to a person.
	name string
}

// baseRefs are the revisions that hold the base branch, best first.
//
// Every one of them is written by GitHub or by the checkout action, and none
// of them is reachable from the pull request's own content. That property is
// the point: a ref a fork could move is a ref that defeats the gate.
func baseRefs(e *Env, root string) []baseRef {
	var refs []baseRef
	if base := strings.TrimSpace(e.Getenv("GITHUB_BASE_REF")); base != "" {
		refs = append(refs,
			baseRef{"refs/remotes/origin/" + base, "the base branch " + base},
			baseRef{"origin/" + base, "the base branch " + base},
			baseRef{base, "the base branch " + base},
		)
	}
	// The first parent of the merge commit, which is the base tip GitHub
	// merged into. Only when HEAD really has two parents: on a checkout of the
	// head commit rather than the merge ref, HEAD^1 is the previous commit on
	// the CONTRIBUTOR'S branch, which is exactly the content this gate refuses
	// to trust. The parent count is what tells the two apart.
	out, err := exec.Command("git", "-C", root, "rev-list", "--parents", "-n", "1", "HEAD").Output()
	if err == nil && len(strings.Fields(string(out))) == 3 {
		refs = append(refs, baseRef{"HEAD^1", "the commit this pull request merges into"})
	}
	return refs
}

// refuseFork turns a refusal into the error a command that has no report exits
// with.
func refuseFork(d forkDecision) error {
	detail := d.Reason
	for _, n := range d.Notes {
		detail += " (" + n + ")"
	}
	return aferrors.Coded(aferrors.AFGH003, "detail", detail)
}

// forkRun is the report for a pull request the fork policy refused.
//
// A whole Run rather than a line on stderr, because the report is how this
// product speaks on a pull request and a refusal that only reaches the job log
// is a refusal a maintainer never sees. It carries the branch, the commit and
// the documentation base like any other run, so the comment that replaces the
// last one is the same shape as the one it replaces.
func forkRun(e *Env, branch, docsBase string, d forkDecision) report.Run {
	skipped := d.Reason
	if d.Source != "" && d.Source != "the safe default" {
		skipped += fmt.Sprintf(" The policy is `%s`, read from %s.", d.Policy, d.Source)
	} else {
		skipped += fmt.Sprintf(" The policy applied is `%s`.", d.Policy)
	}
	return report.Run{
		Branch: branchName(e, branch), Commit: commitSHA(e),
		DocsBase: docsBase, Skipped: skipped, Notes: d.Notes,
	}
}

// skippedRun writes the report for a run that did not happen and exits zero.
//
// Zero on purpose, and it is the decision most worth arguing with, so the
// argument is here. `af ci` exits non zero only for a real finding about the
// change; a blocked run exits zero so that a gap on our side is never
// indistinguishable from broken code. A fork awaiting a maintainer is the same
// shape: nothing was learned about the change, so there is nothing to fail it
// for, and `fork_policy: never` would otherwise paint every fork pull request
// permanently red, which teaches a maintainer to stop reading the check.
//
// What stops that being a silent pass is the report: the headline is "Nothing
// ran." and the first line under it is bold and says the check did not run.
func skippedRun(e *Env, run report.Run, output string) error {
	writeReport(e, run, output)
	return nil
}

// commentEnabled reports whether a comment should be left on the pull request.
//
// `github.comment` was the second of the four settings in that block that
// nothing read. It defaults to true, `af explain` printed "comment on", and
// `comment: false` left the comment exactly where it was, because writeReport
// wrote the file whatever the manifest said and the workflow's own step posted
// whatever it found.
//
// Scoped to a run inside GitHub Actions on purpose. The setting is about a
// comment on a pull request, and on a workstation `af ci --report out.md` is
// somebody asking for a file with a flag, which is a different question that
// this must not answer. GITHUB_ACTIONS is the runner's own marker.
func commentEnabled(e *Env) bool {
	if e.Getenv("GITHUB_ACTIONS") == "" {
		return true
	}
	path, err := manifest.Find(e.WorkDir)
	if err != nil {
		return true
	}
	m, err := manifest.Load(path)
	if err != nil || m.GitHub == nil || m.GitHub.Comment == nil {
		return true
	}
	return *m.GitHub.Comment
}

// commentPath is the file a report should be written to, or empty when the
// manifest has asked for no comment.
//
// It removes a file that is already there, which is not tidiness. The workflow
// writes the change analysis to the same path in an earlier step and posts
// whatever it finds, so leaving a stale file behind would turn `comment: false`
// into "comment, with the wrong contents", which is worse than the bug being
// fixed.
func commentPath(e *Env, output string) string {
	if output == "" || commentEnabled(e) {
		return output
	}
	if err := os.Remove(output); err != nil && !os.IsNotExist(err) {
		e.Out.Printf("  could not remove %s, which github.comment: false asked for: %v\n", output, err)
		return ""
	}
	e.Out.Printf("  no comment written, because the manifest sets github.comment: false\n")
	return ""
}
