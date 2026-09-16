package authz

import (
	"sort"

	"github.com/antifailure/antifailure/engine/internal/report"
)

// Probe is one authorization question, independent of any environment. It is
// what the baseline and the candidate are each measured against, so the same
// probe id lines up the two readings the differential compares.
type Probe struct {
	// ID lines a probe up across the two environments. Two runs of the same
	// probe against baseline and candidate carry the same ID, which is how a
	// (baseline, candidate) pair is formed without matching on anything fuzzy.
	ID string
	// Class is the shape of the question, which selects the policy key.
	Class Class
	// Where is the location the finding points at: a route template, a table,
	// a header. It is a LOCATION and never a value, so it can cross the data
	// boundary unchanged.
	Where string
	// Method is the HTTP method exercised, for the reproduction recipe.
	Method string
	// ObjectClass is a CATEGORY label for the object under test, never a raw
	// id: "another tenant's object", "an admin only resource". Ids come from the
	// masked golden and must not leave the run, so the finding names the class.
	ObjectClass string
	// Because names the facts that put this endpoint under test, each carrying
	// its path and the rule that fired, so a reviewer sees which changed line
	// caused the probe.
	Because []string
	// Public marks a target the application declares unauthenticated. An
	// anonymous identity reaching a declared public endpoint is expected and
	// never flags; the declaration is a diffable manifest fact, not a guess.
	Public bool
}

// Arm is one probe's reading on one environment: the adversarial identity's
// outcome, plus the liveness arm that proves the outcome means refusal rather
// than absence.
type Arm struct {
	// Actor is the outcome for the identity that SHOULD be refused. For every
	// class this family probes, the pass condition is that this reads denied.
	Actor Outcome
	// Liveness is the outcome for an identity that SHOULD be allowed on the
	// same endpoint through the same mechanism. It must read allowed, or the
	// Actor's denial proves nothing: a broken, absent, unreachable or unreached
	// control returns denied too, and only a working liveness arm tells refusal
	// from absence.
	Liveness Outcome
	// LivenessArmed reports that the liveness arm's own privileged setup was
	// positively confirmed, not merely un-errored. The arm is only evidence if
	// its setup took effect, and the setup is itself an authorized mutation that
	// can fail silently. A liveness arm whose setup was never confirmed reads
	// denied for a SETUP reason, and mapping that to a verdict about the control
	// is how the arm meant to stop a false pass becomes the new blind spot. When
	// no privileged setup was needed (the owner reading its own object), this is
	// true because there was nothing to confirm.
	LivenessArmed bool
}

// ok reports whether the liveness arm proves the boundary is live: an identity
// that should be allowed read allowed, AND its setup was confirmed.
func (a Arm) livenessOK() bool {
	return a.Liveness == OutcomeAllowed && a.LivenessArmed
}

// Observation is a probe's reading on one environment.
type Observation struct {
	ProbeID string
	Arm     Arm
}

// Snapshot is one environment's readings plus the two run-level controls that
// decide whether any reading can be trusted at all.
type Snapshot struct {
	// Probes are the questions asked on this environment, in a stable order.
	Probes []Probe
	// Observations maps a probe id to its reading. A probe with no observation
	// was not exercised here, which the differential reads as absence rather
	// than as a refusal.
	Observations map[string]Observation
	// Reachable reports the twin answered at all. False means the whole run is
	// blocked and every reading is meaningless; it is inconclusive, never a
	// pass, because a check that could not look must say so.
	Reachable bool
	// DetectorLive reports the family proved its OWN denial detector on this
	// environment before trusting any reading: a value it knows is a planted
	// marker was recognised, and an absent one was not. Without it a "denied"
	// and an "allowed" say nothing, so the family reports inconclusive rather
	// than a green it did not earn.
	DetectorLive bool
}

// Inconclusive is a probe that could not be decided, reported rather than
// dropped: a check that cannot tell "refused" from "never ran" must say which it
// is, not print a pass. It is the family's honest empty cell, and it is a
// distinct output from a finding so that a dead control never reads as a pass.
type Inconclusive struct {
	// ProbeID and Where locate what could not be decided.
	ProbeID string
	Where   string
	// Rule is the class's policy key, so a reader sees which check went
	// inconclusive rather than which one passed.
	Rule string
	// Reason is a bounded sentence naming why, never a value.
	Reason string
	// Setup distinguishes "the probe could not be armed" (a privileged setup
	// step's own confirmation was missing) from "the probe ran and could not
	// decide". The two have different fixes and conflating them is how a suite
	// accumulates permanently inconclusive cells nobody investigates.
	Setup bool
}

// Assessment is the whole answer for a differential run: the findings the
// difference proved, and the probes that could not be decided. Both are first
// class, because a run that reported only its findings would let a dead control
// pass silently, which is the exact failure the liveness discipline exists to
// stop.
type Assessment struct {
	Findings     []report.Finding
	Inconclusive []Inconclusive
}

// Assess compares a baseline against a candidate and returns just the findings.
// It is the convenience the live probe uses; a caller that needs the
// inconclusive cells too calls AssessDetailed.
func Assess(base, cand *Snapshot, pol report.Policy) []report.Finding {
	return AssessDetailed(base, cand, pol).Findings
}

// AssessDetailed compares a baseline snapshot against a candidate one and
// returns the findings the difference proves plus the probes it could not
// decide, bounded, findings worst first, one finding per (rule, location).
//
// A nil baseline means there is nothing on the base revision to differ from,
// which is the state the family is in until the router supplies the second
// environment. With no baseline, every candidate leak is judged against the
// absolute expectation instead of a diff: an anonymous or wrong-identity reach
// of an endpoint is a regression unless the endpoint is declared public. This
// is the one place the family asserts against a fixed expectation rather than a
// difference, and it exists because "differential only" would give every newly
// added unprotected endpoint a free pass: there is nothing on the baseline for
// it to differ from.
//
// The verdict for an existing endpoint is never "200 is bad." It is "the
// baseline refused this and the candidate allowed it," which is the regression
// the change introduced. Both allowing it is a pre-existing exposure this change
// neither caused nor fixed, and failing a build for it would false-red every
// pull request that merely touched a pre-existingly open endpoint, so it is not
// emitted as a finding here.
func AssessDetailed(base, cand *Snapshot, pol report.Policy) Assessment {
	var out Assessment
	if cand == nil {
		return out
	}
	if !cand.Reachable {
		out.Inconclusive = append(out.Inconclusive, Inconclusive{
			Reason: "the twin did not answer, so no authorization reading was possible",
		})
		return out
	}
	if !cand.DetectorLive {
		out.Inconclusive = append(out.Inconclusive, Inconclusive{
			Reason: "the content detector could not be proven on this run, so a reading of the twin could not be trusted",
		})
		return out
	}

	type key struct{ rule, where string }
	agg := map[key]*report.Finding{}
	var order []key

	for _, p := range cand.Probes {
		co, ok := cand.Observations[p.ID]
		if !ok {
			continue
		}
		candOutcome := co.Arm.Actor
		rule := p.Class.ruleFor()

		// A candidate that could not be exercised, or that answered under a rate
		// limiter, is inconclusive for this probe. Neither is a verdict about
		// authorization, and normalising a 429 to a denial would report a green
		// exactly when the target was too busy to enforce anything.
		if candOutcome == OutcomeError {
			out.Inconclusive = append(out.Inconclusive, Inconclusive{
				ProbeID: p.ID, Where: p.Where, Rule: rule,
				Reason: "the endpoint could not be exercised on the candidate, which a different check owns",
			})
			continue
		}
		if candOutcome == OutcomeRateLimited {
			out.Inconclusive = append(out.Inconclusive, Inconclusive{
				ProbeID: p.ID, Where: p.Where, Rule: rule,
				Reason: "the candidate answered under a rate limiter, so a refusal here does not prove the boundary held",
			})
			continue
		}

		// A declared public endpoint reached by an anonymous identity is
		// expected. The declaration is reviewed in the same diff that adds the
		// route, so this is an auditable decision rather than a silent
		// suppression.
		if p.Public && p.Class == ClassUnauthenticated {
			continue
		}

		var baseOutcome Outcome
		hasBase := false
		if base != nil && base.Reachable && base.DetectorLive {
			if bo, ok := base.Observations[p.ID]; ok {
				baseOutcome, hasBase = bo.Arm.Actor, true
			}
		}

		switch {
		case candOutcome == OutcomeAllowed && (!hasBase || baseOutcome.isRefusal()):
			// A leak. Either the baseline refused and the candidate allowed (the
			// regression), or there is no baseline and the absolute expectation
			// is that the boundary holds. A leak needs no liveness arm, because
			// "allowed" is decided by the victim's content coming back, which is
			// not a state a dead control can fake.
			lvl := pol.Level(report.PolicyKey(rule))
			if lvl == report.LevelIgnore {
				// The manifest turned this key off; emitting it would put back
				// exactly what was silenced.
				continue
			}
			k := key{rule: rule, where: p.Where}
			if f, ok := agg[k]; ok {
				f.Count++
				continue
			}
			f := &report.Finding{
				Rule:   rule,
				Level:  lvl,
				Title:  titleFor(p.Class),
				Detail: detailFor(p, baseOutcome, candOutcome, hasBase),
				Fix:    fixFor(p.Class),
				Count:  1,
				Where:  p.Where,
			}
			agg[k] = f
			order = append(order, k)
		case candOutcome == OutcomeAllowed && baseOutcome == OutcomeAllowed:
			// Pre-existing exposure. Not this change's regression; not emitted,
			// so a pull request that merely touched the endpoint is not
			// false-red. Baseline agreement is the family's primary false
			// positive control.
		default:
			// The candidate refused. That is the safe reading ONLY if the
			// liveness arm proves the refusal is a refusal and not an absence: a
			// renamed route, a session that never established, a middleware that
			// threw and failed closed, and a rate limiter all read denied and
			// are indistinguishable from the control working by the response
			// alone. Without a live arm the probe is inconclusive, not a pass,
			// and it says which arm failed so a reader can tell "the probe could
			// not be armed" from "the probe ran and could not decide".
			// The liveness arm is required only for a class whose pass condition
			// is a refusal, which is every class this family probes today; the
			// guard names the invariant rather than assuming it, so a future
			// class whose pass is "allowed" would not be held to a liveness arm
			// it does not need.
			if p.Class.passIsDenied() && !co.Arm.livenessOK() {
				reason := "the boundary held on the candidate, but the liveness arm did not read allowed, so a refusal cannot be told from a dead or absent control"
				setup := false
				if co.Arm.Liveness == OutcomeAllowed && !co.Arm.LivenessArmed {
					reason = "the liveness arm's own privileged setup was not confirmed, so the arm proves nothing about the control"
					setup = true
				}
				out.Inconclusive = append(out.Inconclusive, Inconclusive{
					ProbeID: p.ID, Where: p.Where, Rule: rule,
					Reason: reason, Setup: setup,
				})
			}
		}
	}

	out.Findings = make([]report.Finding, 0, len(order))
	for _, k := range order {
		out.Findings = append(out.Findings, *agg[k])
	}
	sort.SliceStable(out.Findings, func(i, j int) bool {
		return findingRank(out.Findings[i].Level) < findingRank(out.Findings[j].Level)
	})
	return out
}

// findingRank orders findings for display, failures first, mirroring the report
// package so the family's own ordering agrees with the run's.
func findingRank(l report.Level) int {
	switch l {
	case report.LevelFail:
		return 0
	case report.LevelWarn:
		return 1
	default:
		return 2
	}
}
