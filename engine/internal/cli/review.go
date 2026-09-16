package cli

import (
	"context"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/model"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/review"
)

// reviewReader is the part of the orchestrator the code reviewer needs: the
// classified diff, to decide whether the change touched code at all, and the
// changed code files with their added lines, to review.
//
// It is a small interface for the same reason changeReader is: the reviewer is
// the whole point of this lane, so it has a test that stands up a fake reader
// instead of an environment, and *env.Orchestrator satisfies it. Neither method
// opens a session or touches a database; both read the diff.
type reviewReader interface {
	Change(ctx context.Context, opts env.ChangeOptions) (*change.Profile, error)
	CodeFiles(ctx context.Context, opts env.ChangeOptions) ([]change.File, error)
	// FileContent returns the head side contents of a changed path, so the
	// reviewer shows the model the whole file as context around the lines a
	// change added. A path absent at head returns ok=false, never an error, so a
	// file whose context cannot be fetched is reviewed on its added lines alone.
	FileContent(ctx context.Context, head, path string) (string, bool, error)
}

// reviewContext adapts the orchestrator's FileContent, which needs the head ref,
// to the review package's FileReader, which asks by path alone. It carries the
// head the change is being read at, so the file content the model sees is the
// same side of the comparison the added lines came from.
type reviewContext struct {
	o    reviewReader
	head string
}

func (r reviewContext) FullFile(ctx context.Context, path string) (string, bool, error) {
	return r.o.FileContent(ctx, r.head, path)
}

// reviewFindings runs the static, model-backed code reviewer against the change
// and returns its findings, beside the migration, egress, masking, load,
// cleanup and security collectors, so a review finding is an ordinary
// report.Finding that rides the same verdict, exit code and pull request comment
// as everything else.
//
// It reads the diff, not the twin: a correctness bug is in the change whether or
// not the environment came up, so this runs before the environment is brought up
// and is worth having even on a run whose environment never started.
//
// Nothing here fails the run on its own account. A change the router cannot read,
// a diff the reviewer cannot fetch, a model that could not be reached, and a
// model that answered with something unreadable are all recorded as notes and
// produce no finding, because a gap in our tooling is a fact about us and must
// never redden somebody's build. Only a defect the model actually reported
// becomes a finding, and that finding's level is the manifest's policy, not this
// collector's: a probabilistic reviewer defaults to warn and the founder raises
// it, so the reviewer advises by default and never blocks on its own say-so.
//
// The client is nil when no model key resolved. That is the honest skip: an LLM
// reviewer with no model to call did not run, and the absence is a note, never a
// fabricated clean pass. The note is recorded only when there was code to review,
// so a docs-only or configuration-only change pays nothing and says nothing.
func reviewFindings(
	ctx context.Context,
	e *Env,
	o reviewReader,
	client review.Client,
	gate report.Policy,
	run *report.Run,
	branch string,
) []report.Finding {
	opts := env.ChangeOptions{Head: branch, Getenv: e.Getenv}

	profile, err := o.Change(ctx, opts)
	if err != nil {
		// The router could not read the change, which is not evidence about the
		// change. It is a note and no review runs, the same way securityFindings
		// treats a diff it could not classify.
		run.Notes = append(run.Notes,
			"the code reviewer could not read the change, so no review ran: "+err.Error())
		return nil
	}

	// A docs-only or configuration-only change routes no reviewer and pays
	// nothing: no diff read, no model call, no note. The same rule keeps the
	// security suite off a change that touched none of its surfaces.
	if !profileTouchesCode(profile) {
		return nil
	}

	if client == nil {
		// There is code to review and no model to review it with. Named rather
		// than silent, because "skipped, no key" and "ran, found nothing" are
		// different facts and only one of them is a clean bill.
		run.Notes = append(run.Notes,
			"code review skipped: no model key configured. Set one with 'af model set' to review "+
				"the change's added lines for correctness defects.")
		return nil
	}

	files, err := o.CodeFiles(ctx, opts)
	if err != nil {
		run.Notes = append(run.Notes,
			"the code reviewer could not read the code diff, so no review ran: "+err.Error())
		return nil
	}

	res, err := review.Review(ctx, client, reviewContext{o: o, head: branch},
		files, gate.Review, review.DefaultCaps)
	// The reviewer's own notes ride along whether or not the call succeeded: a
	// cap note is a fact about what was read, and it is true even when the model
	// then failed to answer.
	run.Notes = append(run.Notes, res.Notes...)
	if err != nil {
		run.Notes = append(run.Notes,
			"the code reviewer could not complete, so it reached no verdict: "+err.Error())
		return nil
	}
	return res.Findings
}

// profileTouchesCode reports whether the classified change touched a code,
// authorization-code or schema surface, so the reviewer runs only when there is
// code to read. It mirrors profileTouchesDependency and the surfaces CodeFiles
// selects, so the routing decision and the file selection cannot disagree.
func profileTouchesCode(profile *change.Profile) bool {
	if profile == nil {
		return false
	}
	for _, f := range profile.Facts {
		switch f.Surface {
		case change.SurfaceCode, change.SurfaceAuth, change.SurfaceSchema:
			return true
		}
	}
	return false
}

// reviewClient builds the code reviewer's model client, or returns nil when no
// key resolved. A nil client is the reviewer's skip signal: reviewFindings notes
// it when there was code to review, and no model call is made.
//
// The resolution is the one every other model path uses, so a key exported for
// af model test or stored in the keyring is the key the reviewer uses, with no
// second configuration to learn. A resolution error is treated as no key, since
// the outcome for the reviewer is the same: it cannot call a model, and it says
// so rather than pretending it ran.
func reviewClient(ctx context.Context, e *Env) review.Client {
	cfg, err := model.Resolve(ctx, modelChain(e))
	if err != nil || cfg == nil {
		return nil
	}
	return review.NewProviderClient(*cfg)
}
