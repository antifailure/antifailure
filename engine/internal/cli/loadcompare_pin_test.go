package cli

import (
	"bytes"
	"testing"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/workload"

	"github.com/stretchr/testify/require"
)

// THE HTTP COMPARISON'S REPORT, PINNED CHARACTER FOR CHARACTER.
//
// This is not a test of whether the report is right. It is a test of whether
// it CHANGED, and it is here because the report is on film: a demo shows this
// table and reads its wording aloud, so a column heading gained, lost or
// respelled makes the film disagree with the product.
//
// The whole rendering is compared rather than a few substrings, deliberately.
// A `require.Contains` for "route" would pass through a column being renamed
// from "moved" to "direction", a column being reordered, a blank line being
// dropped between the tables, or a fourth table appearing. Every one of those
// is a change to what somebody sees, and only the whole string catches them
// all.
//
// WHEN THIS TEST FAILS, READ IT AS A QUESTION RATHER THAN AS A BUG. If the
// report was meant to change, the fix is to update the block below and to know
// that the film now needs a reshoot. If it was not, the change is unintended
// and the diff of this test's failure names the line.
func TestTheHTTPComparisonReportIsUnchanged(t *testing.T) {
	c := &workload.Comparison{
		Kind: workload.ObservedLoad,
		Measures: []workload.MeasureDifference{
			{Measure: "requests", Baseline: f(1200), Candidate: f(1180),
				Ratio: f(-0.0167), Direction: workload.DirectionWorse},
			{Measure: "p95_ms", Baseline: f(44), Candidate: f(61),
				Ratio: f(0.386), Direction: workload.DirectionWorse},
			{Measure: "achieved_rate", Baseline: f(40), Candidate: nil,
				Direction: workload.DirectionUnmeasurable},
		},
		Routes: []workload.RouteDifference{
			{Route: "GET /accounts", InBaseline: true, InCandidate: true,
				P95Baseline: f(44), P95Candidate: f(61), P95Ratio: f(0.386),
				Direction: workload.DirectionWorse,
				Resolution: workload.RouteResolution{Method: workload.ResolutionRounds,
					Rounds: 16, Family: 3, SmallestVisible: f(0.19),
					ChangeLow: f(0.2), ChangeHigh: f(0.6)}},
			{Route: "GET /health", InBaseline: true, InCandidate: true,
				P95Baseline: f(4), P95Candidate: f(9), P95Ratio: f(1.25),
				Direction: workload.DirectionWorse,
				Resolution: workload.RouteResolution{Method: workload.ResolutionRounds,
					Rounds: 16, Family: 3, SmallestVisible: f(2.4),
					ChangeLow: f(-0.4), ChangeHigh: f(3.1)}},
			{Route: "GET /statements", InBaseline: true, InCandidate: false,
				P95Baseline: f(80), Direction: workload.DirectionUnmeasurable},
		},
		Notes: []string{
			"two runs against two environments are not a controlled experiment",
			"both sides branched the same golden abc1234",
		},
	}
	judged := []workload.ComparisonVerdict{
		{Name: "p95_increase", Scope: "GET /accounts", Measure: "p95_ms", Threshold: 0.2,
			Value: workload.VerdictFail, Observed: f(0.386),
			Detail: "p95 went from 44ms on the base branch to 61ms on this one"},
		{Name: "p95_increase", Scope: "GET /health", Measure: "p95_ms", Threshold: 0.2,
			Value: workload.VerdictUnverified, Unresolvable: true,
			Detail: "the limit sits inside the interval"},
		{Name: "p95_increase", Scope: "GET /statements", Measure: "p95_ms", Threshold: 0.2,
			Value:  workload.VerdictUnverified,
			Detail: "this route was sent on the base branch and not on this one"},
	}
	res := &env.LoadCompareResult{
		Rev: "1111111111112222", CandidateRev: "3333333333334444",
		How: "the merge base with origin/main",
	}

	var buf bytes.Buffer
	e := &Env{Out: NewOutput(&buf, &buf)}
	renderLoadComparison(e, res, c, judged, workload.VerdictFail)

	const want = `
  333333333333 against 111111111111
  the base was resolved the merge base with origin/main

  measure        base     this build  change  moved
  requests       1.2e+03  1.18e+03    -1.7%   worse
  p95_ms         44       61          +38.6%  worse
  achieved_rate  40       none        none    unmeasurable

  route            base p95  this build p95  change   moved             can see
  GET /accounts    44        61              +38.6%   worse             19%
  GET /health      4         9               +125.0%  too close to say  240%
  GET /statements  80        none            none     unmeasurable      nothing

What crossed a declared threshold:
  p95 went from 44ms on the base branch to 61ms on this one

  2 declared thresholds could not be measured on both sides.

What this run could not resolve:
  GET /health: the limit sits inside the interval

What this comparison cannot see:
  two runs against two environments are not a controlled experiment
  both sides branched the same golden abc1234

  fail  the base branch comparison is fail
`
	require.Equal(t, want, buf.String(),
		"the HTTP comparison's report changed, and it is the one that is on film")
}
