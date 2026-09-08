package fidelity

import (
	"fmt"

	"github.com/antifailure/antifailure/engine/internal/traffic"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The traffic dimension: whether the load a run sends is the load production
// actually serves.
//
// It used to answer that question without ever asking production. Any shape a
// source produced was Reproduced, so four routes somebody wrote by hand in
// safe_routes were reported in the same words and with the same verdict as a
// mix read from a week of production telemetry. On this repository's own
// manifest the sentence it printed was "4 routes read from , at 5 requests a
// second", with the source name missing because there was no source, and the
// verdict beside it was reproduced.
//
// That is not a cosmetic defect. Measured here on 2026-09-06: a migration held
// AccessExclusiveLock on nine relations for thirty seconds, and af load smoke
// ran through the whole window reporting 0.0 percent failed with p95 improving
// from 41ms to 17ms, because none of the four hand written routes reads the
// locked table. A hand written route list cannot know which routes touch which
// tables. The report is the one thing whose job is to say that the twin is not
// production, and it was calling the list a reproduction of it.
//
// So the mix is measured against the committed traffic profile, and the three
// outcomes are kept separate on purpose, exactly as the database dimension
// keeps them for rows:
//
//   - no profile, or one too old to quote, is UNMEASURED. Not a smaller pass.
//     Nothing has been shown about whether this run exercises production, and
//     the reason names how to find out.
//   - a run that never sends routes production leans on is SUBSTITUTED, with
//     the share of production's requests it does reach and the heaviest route
//     it misses named.
//   - a run that reaches everything production leans on is REPRODUCED, and now
//     says what it was measured against rather than asserting it.
func trafficDimension(obs Observation) Dimension {
	d := Dimension{Name: schema.FidelityTraffic}
	l := obs.Manifest.Load
	if l == nil || !l.Enabled {
		d.NotApplicable = "the manifest does not ask for traffic, so there is none to reproduce"
		return d
	}
	d.Components = append(d.Components, endpointMixComponent(obs), arrivalRateComponent(obs))
	return d
}

// endpointMixComponent answers whether the run sends what production serves.
func endpointMixComponent(obs Observation) Component {
	c := Component{Name: "endpoint mix"}
	switch {
	case obs.TrafficReason != "":
		c.State, c.Detail = Absent, obs.TrafficReason
	// Keyed on whether a shape was actually read, not on which source the
	// manifest named. This arm used to test l.Source == LoadAccessLog, which
	// was every connected source at the time it was written. OpenTelemetry
	// became a real source afterwards, and an otel run would have fallen to
	// the default arm below and been reported as the engine's own shape while
	// carrying production's routes and production's rate. A report that calls
	// real traffic a default is worse than one that says nothing.
	case obs.Traffic != "":
		c.State, c.Detail = Reproduced, obs.Traffic
	default:
		c.State = Absent
		c.Detail = "the traffic is the engine's own default shape, not production's"
	}
	return againstProductionTraffic(c, obs)
}

// againstProductionTraffic folds the traffic profile into the endpoint mix.
//
// It never improves a verdict, for the reason its counterpart in the database
// dimension does not: a source that produced nothing is still absent, and what
// the profile adds to that is the size of what is being missed. The only
// verdict it changes is the arm that was never checked against anything.
func againstProductionTraffic(c Component, obs Observation) Component {
	if obs.TrafficProfile == nil {
		reason := obs.TrafficProfileReason
		if reason == "" {
			reason = "no traffic profile says what production serves, so whether this run " +
				"exercises it is unknown. Declare one under load.traffic and record it with " +
				"af traffic record"
		}
		if c.State == Reproduced {
			// The whole defect, in one line. A mix nothing was compared
			// against has not been shown to reproduce anything, and calling
			// that a reproduction is what let four hand written routes stand
			// in for everything production serves.
			c.State = Unmeasured
		}
		c.Detail += ", and " + reason
		return c
	}

	cov := traffic.Compare(obs.Sent, *obs.TrafficProfile)
	c.Detail += fmt.Sprintf(". Measured against the traffic profile collected on %s, %s",
		obs.TrafficProfile.CollectedAt.UTC().Format("2006-01-02"), cov.Describe())
	if _, ok := cov.Share(); !ok {
		// A profile that counted no request answers no question about the run,
		// and it is not evidence either way.
		if c.State == Reproduced {
			c.State = Unmeasured
		}
		return c
	}
	if !cov.Covers() {
		if c.State == Reproduced {
			c.State = Substituted
		}
		c.Detail += ". A run cannot fail on a route it never sends"
	}
	return c
}

// arrivalRateComponent answers whether the run sends as fast as production.
//
// A separate component from the mix because it is a separate question and a
// reviewer asks it separately. A run that reaches every route production
// serves and sends them at two percent of production's rate has exercised the
// code and not the contention, and a single verdict over both would hide
// whichever failed. It is the same split the database dimension draws between
// its data and its provenance.
//
// It states and does not adjust. Nothing here changes load.scale to match
// production, because everything else in this engine refuses to substitute
// quietly: a provider named and not built is refused, an unverified golden
// cannot be branched, a capability not declared is skipped by name. A number
// that silently corrected itself would be the first thing here that guesses.
func arrivalRateComponent(obs Observation) Component {
	c := Component{Name: "arrival rate"}
	if obs.TrafficProfile == nil {
		reason := obs.TrafficProfileReason
		if reason == "" {
			reason = "no traffic profile says how fast production serves, so whether this run " +
				"reaches its rate is unknown. Declare one under load.traffic and record it with " +
				"af traffic record"
		}
		c.State, c.Detail = Unmeasured, reason
		return c
	}
	production, ok := obs.TrafficProfile.Rate()
	if !ok {
		c.State = Unmeasured
		c.Detail = "the traffic profile covers too short a window to carry a rate, so there is " +
			"nothing to compare this run's against"
		return c
	}
	cmp := traffic.RateComparison{
		Run: obs.SentRate, Production: production,
		PeakConcurrency: obs.TrafficProfile.PeakConcurrency,
	}
	c.Detail = cmp.Describe()
	if cmp.Reaches() {
		c.State = Reproduced
		return c
	}
	c.State = Substituted
	c.Detail += ". Contention this environment never sees is contention production still has"
	return c
}
