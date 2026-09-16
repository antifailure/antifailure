package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/internal/security"
	"github.com/antifailure/antifailure/engine/pkg/edition"
)

// changeReader is the part of the orchestrator the security collector needs: a
// diff classified against the manifest, and the messages the run captured.
//
// It is an interface for the reason exploreConfigured takes an explorer rather
// than the orchestrator itself: a call site reachable only through Docker is a
// call site nothing can assert, and this collector is the whole point of the
// lane, so it has a test that stands up a fake reader instead of an
// environment. *env.Orchestrator satisfies it.
type changeReader interface {
	Change(ctx context.Context, opts env.ChangeOptions) (*change.Profile, error)
	Messages(ctx context.Context, limit int) ([]local.Message, error)
	// DependencyFiles is the changed dependency manifests and lockfiles with
	// their added lines, which the supply_chain family reads. It reads the same
	// diff Change classifies; the profile Change returns keeps the added lines
	// off, so a family that reasons about what a dependency change adds needs
	// this beside it.
	DependencyFiles(ctx context.Context, opts env.ChangeOptions) ([]change.File, error)
	// BaselineTwin brings a second environment up from the base revision and
	// drives it, so the side_effect family can diff what this change made against
	// what the base branch made. It is on the interface, not called through the
	// concrete orchestrator, so the collector's before/after wiring can be tested
	// with a fake base twin rather than only through Docker, the same reason
	// Change and Messages are here.
	BaselineTwin(ctx context.Context, opts env.BaselineTwinOptions) (*env.BaselineTwin, error)
}

// securityFindings runs the registered security families against the change and
// returns their findings, in one place, so a security finding is an ordinary
// report.Finding that rides the same verdict, exit code, pull request comment
// and MCP surface as everything else.
//
// This is the collector the whole security suite plugs into, and its failure to
// exist was the gap this lane closes: the security contract shipped with a
// Family interface, a registry and a read tool, and NOTHING ever called Probe,
// so the entire suite was capability that looked shipped and did nothing. Here
// is the call site. It is wired into ci's finish in one append line, beside the
// migration, egress, masking, load and cleanup collectors.
//
// It runs while the environment is still up, before teardown, because the
// active families (authz, injection) drive the twin at Env.BaseURL and the
// reader families (ssrf, side_effect, canary_leak) read what the run captured.
// The findings it returns are appended after teardown with the rest, exactly as
// the migration findings are computed during the run and appended in finish.
//
// Nothing here fails the run on its own account: a change the router cannot read
// and a family that cannot complete are both recorded as notes and produce no
// finding, because a gap in our tooling is a fact about us and must never redden
// somebody's build. Only a family that PROVES something about the change emits a
// finding, and that finding's level and exit are the manifest's and the
// family's to decide, not this collector's.
//
// The edition boundary is enforced HERE, at the Probe call site, and nowhere
// else: a family the edition does not license is skipped with a note that names
// the feature, never dropped from the registry, so an unlicensed family and one
// that found nothing never look the same. The security package imports nothing
// under ee for exactly this reason; the licence check lives in the command.
func securityFindings(
	ctx context.Context,
	e *Env,
	o changeReader,
	reg *security.Registry,
	gate report.Policy,
	run *report.Run,
	decisions []local.Decision,
	branch string,
	runner string,
	baseTTL time.Duration,
) []report.Finding {
	// No family, no work: the spine ships an empty registry, and until a family
	// lands this returns immediately without reading the diff or the twin, so
	// the product plans and reports exactly as it did before a security family
	// existed. This is also what keeps a docs-only or configuration-only change
	// paying nothing for a suite none of its files routed.
	if reg == nil || len(reg.Families()) == 0 {
		return nil
	}

	profile, err := o.Change(ctx, env.ChangeOptions{Head: branch, Getenv: e.Getenv})
	if err != nil {
		// The router could not read the change, which is not evidence about the
		// change. It is said in a note and no security check runs, the same way
		// a load run that could not complete is a note rather than a failure.
		run.Notes = append(run.Notes,
			"the security router could not read the change, so no security check ran: "+err.Error())
		return nil
	}

	selections := security.Select(reg, profile)
	if len(selections) == 0 {
		return nil
	}

	// The reader artifacts, gathered once and handed to every family. Messages
	// are read here, while the twin is up; the decisions were already read for
	// the egress summary and are passed in rather than fetched twice; the
	// browser evidence is folded out of what exploration already captured.
	//
	// Two of these three are sourced now, and one stays honestly absent. Routes
	// is sourced from the exploration the run observed: observedRoutes returns
	// the routes the browser reached and nil when there was no exploration to
	// read, which the injection family reads as UNAVAILABLE rather than as a
	// clean pass. The base twin is built here, but only when a selected family
	// reads it and only when there is a change to compare: baselineArtifact
	// returns nil for a change that routes no baseline reader, which a reader
	// reads as "not measured" and skips its baseline comparison rather than
	// diffing against a base of zero. Observations stays nil until the runner
	// emits structured per-persona observations, which authz fails closed on.
	// Nil here is absent, never a misleading empty.
	messages, _ := o.Messages(ctx, securityLogLimit)
	artifacts := security.RunArtifacts{
		Decisions:    decisions,
		Messages:     messages,
		Observations: nil,
		Evidence:     explorationEvidence(run.Exploration),
		Routes:       observedRoutes(run),
		Baseline:     baselineArtifact(ctx, o, selections, run, runner, baseTTL),
		// The dependency diff, read only when the change touched the dependency
		// surface, so a code-only change does not pay for a second read of the
		// diff. Nil otherwise, which the supply family reads as "not measured".
		DependencyFiles: dependencyArtifacts(ctx, o, e, profile, branch, run),
	}

	base := security.Input{
		Env:    security.Environment{BaseURL: run.URL},
		Golden: security.GoldenView{},
		Policy: resolveSecurityPolicy(gate, reg),
		Clock:  e.Clock,
	}

	var out []report.Finding
	for _, sel := range selections {
		fam := sel.Family
		if feature := fam.Licensed(); feature != "" && !edition.Permits(ctx, feature) {
			// Refused, not absent. The family stays in the plan and this run
			// records that the edition withheld it, naming the feature, so a
			// reader can tell "not licensed here" from "ran and found nothing".
			run.Notes = append(run.Notes, fmt.Sprintf(
				"the %s security family was not run: this edition does not license %q.",
				fam.Name(), feature))
			continue
		}

		in := base
		in.Targets = sel.Targets
		in = in.WithRunArtifacts(artifacts)

		findings, probeErr := fam.Probe(ctx, in)
		if probeErr != nil {
			// A blocked probe is a fact about our tooling, never a verdict about
			// the change, so it is a note and not a finding.
			run.Notes = append(run.Notes, fmt.Sprintf(
				"the %s security family could not complete, so it reached no verdict: %s",
				fam.Name(), probeErr.Error()))
			continue
		}
		out = append(out, findings...)
	}
	return out
}

// baselineArtifact builds the base twin the side_effect family diffs against,
// but only when it is worth the second environment: a change that routes no
// baseline-reading family gets nil at once, so a docs change, a config change,
// or a code change that touched no baseline-reading surface never pays for a
// base twin. When one is worth building and the base is faithfully measured, the
// bundle is returned; when the base is the same commit, cannot be built, or
// cannot be measured, that is a fact about our tooling and the change, said in a
// note, and the artifact stays nil, which the family reads as "not measured" and
// skips its comparison rather than diffing against a base of zero.
//
// The golden is the candidate's own, read off the run, so both sides branch one
// golden and a difference is the code's rather than two databases'. The ttl and
// the runner are af ci's, so the base env is reaped on the same terms as the
// candidate and driven through the same runner.
func baselineArtifact(
	ctx context.Context, o changeReader, selections []security.Selection,
	run *report.Run, runner string, baseTTL time.Duration,
) *security.Baseline {
	if !security.SelectionsWantBaseline(selections) {
		// No family reads a base twin, so a second environment would be built and
		// torn down for nothing. Silent rather than noted: nothing was expected,
		// so its absence owes the reader no sentence.
		return nil
	}

	twin, err := o.BaselineTwin(ctx, env.BaselineTwinOptions{
		Golden:     run.Golden,
		RunnerPath: runner,
		TTL:        baseTTL,
		Limit:      securityLogLimit,
	})
	if err != nil {
		if errors.Is(err, env.ErrBaselineSameCommit) {
			run.Notes = append(run.Notes,
				"the base and this change are the same commit, so the side-effect comparison had nothing to compare")
		} else {
			run.Notes = append(run.Notes,
				"the baseline could not be measured, so the side-effect comparison did not run: "+err.Error())
		}
		return nil
	}
	if twin != nil && !twin.TornDown {
		// The base env came up and was not removed, which is the leak this
		// product exists to prevent. Named with the exact command, because the
		// base env carries a branch suffix a reader would not otherwise guess.
		run.Notes = append(run.Notes,
			"the side-effect base environment is still up; run 'af down --branch \""+twin.Branch+"\"' where this ran")
	}
	return &security.Baseline{Decisions: twin.Decisions, Messages: twin.Messages}
}

// dependencyArtifacts reads the changed dependency files, with their added
// lines, for the supply_chain family, but only when the change touched the
// dependency surface: a code-only change routes no supply family and must not
// pay for a second read of the diff. A read that fails is a fact about our
// tooling, so it is a note and leaves the artifact absent, which the family
// reads as "not measured" rather than as a clean dependency change.
func dependencyArtifacts(
	ctx context.Context, o changeReader, e *Env, profile *change.Profile, branch string, run *report.Run,
) []security.DependencyFile {
	if !profileTouchesDependency(profile) {
		return nil
	}
	files, err := o.DependencyFiles(ctx, env.ChangeOptions{Head: branch, Getenv: e.Getenv})
	if err != nil {
		run.Notes = append(run.Notes,
			"the supply-chain family could not read the dependency diff, so it did not run: "+err.Error())
		return nil
	}
	// A non-nil slice, even an empty one, is the honest "measured" state the
	// family's DependencyDiff reads as ok=true.
	out := make([]security.DependencyFile, 0, len(files))
	for _, f := range files {
		added := make([]string, 0, len(f.AddedLines))
		for _, al := range f.AddedLines {
			added = append(added, al.Text)
		}
		out = append(out, security.DependencyFile{
			Path: f.Path, Status: string(f.Status), Added: added,
		})
	}
	return out
}

// profileTouchesDependency reports whether the classified change touched a
// dependency manifest or lockfile, so the diff is read a second time only when
// a supply family will read it.
func profileTouchesDependency(profile *change.Profile) bool {
	if profile == nil {
		return false
	}
	for _, f := range profile.Facts {
		if f.Surface == change.SurfaceDependency {
			return true
		}
	}
	return false
}

// securityLogLimit is how many captured messages a family reads. It matches the
// limit the egress summary uses for decisions, so a reader family sees the same
// window of the run the report does.
const securityLogLimit = 500

// resolveSecurityPolicy overlays the registered families' default levels under
// the manifest's overrides, so a family reading in.Policy.Level(key) gets the
// level the manifest set when it set one and the family's declared default
// otherwise.
//
// It is the seam the policy package's comment names: Configure fills the
// manifest overrides, and the router resolves the family defaults from the
// registry, which is here. Without it a declared-but-unconfigured key would
// resolve to ignore and a family's own default would never apply, so a fail-by
// default finding would be silenced by the absence of a manifest line rather
// than by a present one. The overrides win: a key the manifest set keeps its
// value, and a key only the family declared takes the default.
func resolveSecurityPolicy(gate report.Policy, reg *security.Registry) report.Policy {
	merged := map[report.PolicyKey]report.Level{}
	for _, k := range reg.Keys() {
		merged[k.Key] = k.Default
	}
	for key, lvl := range gate.Security {
		merged[key] = lvl
	}
	resolved := gate
	resolved.Security = merged
	return resolved
}

// explorationEvidence folds the DOM and response bodies the exploration
// captured into the flat evidence a leak family scans. It reads what the runner
// already recorded against the twin, so canary_leak has something to scan
// without a second pass over the environment. Empty when nothing explored,
// which a family reads as "not handed evidence" rather than "found none". The
// bodies stay inside the engine here and reach no finding.
func explorationEvidence(x *report.Exploration) security.Evidence {
	if x == nil {
		return security.Evidence{}
	}
	var ev security.Evidence
	for _, r := range x.Results {
		ev.DOM = append(ev.DOM, r.Evidence.DOM...)
		ev.Responses = append(ev.Responses, r.Evidence.Responses...)
	}
	return ev
}
