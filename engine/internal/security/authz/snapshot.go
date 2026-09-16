package authz

import (
	"fmt"
	"sort"

	"github.com/antifailure/antifailure/engine/internal/security"
)

// BuildSnapshot adapts the runner's structured per-persona observations into the
// authz differential's own Snapshot, so AssessDetailed can compare a candidate
// against a base twin. It is the caller-side of the observation contract: the
// runner emits security.RawObservation over the JSON boundary, the collector
// hands them here, and this turns each bounded reach into one authorization
// probe. It never sees a raw value: an observation carries a route template, an
// identity comparison and a content-presence flag the runner decided against the
// golden's canary, and this maps those into a probe and an outcome.
//
// The distinction the whole family turns on lives here. A nil slice is NOT
// MEASURED: the runner emitted no observations, so there is nothing to assess
// and BuildSnapshot returns nil, which the caller reads as "the authenticated
// classes were not exercised" rather than as a clean pass. A non-nil slice, even
// an empty one, is MEASURED: the twin was reached and the snapshot is Reachable,
// so an empty slice is the honest "reached and found no cross-owner probe" and
// a populated one is assessed. Returning a zero-valued Snapshot for the absent
// case would read as "reached, found nothing", which is the exact banned defect
// this product keeps finding in its own instruments, so absent is nil and
// present is a real Snapshot.
//
// DetectorLive is the content matcher's own proof, carried from the golden: the
// runner decided VictimContentPresent by matching the golden's planted canary,
// so the differential can only trust that flag if the golden actually carried a
// usable canary. Without one there is no marker to have matched, VictimContentPresent
// is a default false rather than a decided one, and AssessDetailed reports
// inconclusive rather than reading a leak as absent. Proving it over the run's
// real markers, exactly as the anonymous path does, is what keeps a golden with
// no canary from turning every authenticated leak into a silent pass.
func BuildSnapshot(obs []security.RawObservation, golden security.GoldenView) *Snapshot {
	if obs == nil {
		return nil
	}
	snap := &Snapshot{
		Observations: map[string]Observation{},
		// A non-nil slice means the runner reached the twin to make its
		// observations, so the run is reachable. An empty slice is reachable with
		// nothing to probe, which is a quiet legitimate pass, not a blocked run.
		Reachable: true,
		// The matcher is proven over the golden's canaries, the same markers the
		// runner matched VictimContentPresent against, so a golden with no usable
		// canary is DetectorLive false and the authenticated classes go
		// inconclusive rather than passing on a flag nothing could have set.
		DetectorLive: detectorLive(markersOf(golden)),
	}
	for _, o := range obs {
		class, ok := classify(o)
		if !ok {
			// The actor reached its own object: actor and owner are the same
			// identity, so no boundary was crossed and there is nothing to probe.
			// Dropping it is correct, not a gap: a self-read is the control
			// working, and manufacturing a probe for it would invent a question
			// the reach never asked.
			continue
		}
		id := probeID(o, class)
		if _, seen := snap.Observations[id]; seen {
			// Two identical reaches collapse to one probe: the id lines a probe up
			// across the base and candidate twins, and a duplicate would only
			// re-add the same question. AssessDetailed already counts repeats per
			// (rule, location), so the finding's Count carries the multiplicity.
			continue
		}
		snap.Probes = append(snap.Probes, Probe{
			ID:          id,
			Class:       class,
			Where:       o.Route,
			Method:      o.Method,
			ObjectClass: o.ObjectClass,
		})
		snap.Observations[id] = Observation{
			ProbeID: id,
			Arm:     armFor(o),
		}
	}
	// A stable order so the two twins' probes line up and the findings are
	// deterministic across runs.
	sort.SliceStable(snap.Probes, func(i, j int) bool {
		return snap.Probes[i].ID < snap.Probes[j].ID
	})
	return snap
}

// classify decides which authorization boundary a reach crossed, from the actor
// and owner identities alone, and reports ok=false when the reach crossed no
// boundary at all. It is deliberately identity-driven rather than status-driven:
// the class selects the policy key and the severity, and those are facts about
// WHO reached WHOSE object, not about what the twin answered, which is the
// outcome's job.
//
// The order is by severity, most severe first, because a reach can cross more
// than one boundary at once and the finding must land on the sharpest key: a
// reach into another tenant is a cross-tenant break even though the users also
// differ, so cross_tenant is decided before idor.
func classify(o security.RawObservation) (Class, bool) {
	switch {
	case o.Anonymous:
		// No session was carried: the no-session probe. It reaching content is the
		// unauthenticated class, the same shape the anonymous live path owns but
		// decided here from a structured observation rather than a Go-driven reach.
		return ClassUnauthenticated, true
	case o.ActorTenant != "" && o.OwnerTenant != "" && o.ActorTenant != o.OwnerTenant:
		// Actor and owner are in different tenants: a cross-tenant isolation break,
		// the highest severity horizontal reach.
		return ClassCrossTenant, true
	case o.ActorUser != "" && o.OwnerUser != "" && o.ActorUser != o.OwnerUser:
		// Same tenant, different users: a horizontal cross-user object reference.
		return ClassIDOR, true
	case o.ActorRole != "" && o.OwnerRole != "" && o.ActorRole != o.OwnerRole:
		// Same tenant and user identifier but a different role owns the object,
		// which is a lower identity reaching a capability a higher role's object
		// gates: the privilege-escalation shape.
		return ClassPrivilegeEscalation, true
	default:
		// Actor and owner are the same identity, or the observation named no
		// distinguishing identity at all. Either way no boundary was crossed, so
		// there is no probe to raise.
		return "", false
	}
}

// armFor builds the probe's reading from one observation. The actor outcome is
// the twin's answer classified by content presence, exactly as the anonymous
// path classifies its own reach, so the two paths funnel through the one
// StatusOutcome seam and cannot drift.
//
// The liveness arm is the object's own seeding. SetupConfirmed reports that the
// object existed before the reach, which is what makes a refusal meaningful: a
// 404 for an object that was confirmed seeded proves the boundary dropped a real
// row, while a 404 for an object that was never seeded proves only that the id
// was invented. So SetupConfirmed both arms the liveness (LivenessArmed) and
// decides the liveness outcome: a confirmed object is one the owner can read,
// which is the allowed reading a live arm needs, and an unconfirmed one leaves
// the arm unable to tell a held boundary from an absent object. A leak needs no
// arm at all, because "allowed" is decided by the victim's content coming back,
// which a dead control cannot fake; AssessDetailed enforces that, so this arm
// matters only for the refusal reading it must not mistake for a pass.
func armFor(o security.RawObservation) Arm {
	liveness := OutcomeDenied
	if o.SetupConfirmed {
		liveness = OutcomeAllowed
	}
	return Arm{
		Actor:         StatusOutcome(o.Status, o.VictimContentPresent),
		Liveness:      liveness,
		LivenessArmed: o.SetupConfirmed,
	}
}

// probeID is the stable identity that lines one probe up across the base and
// candidate twins. It is built from the boundary crossed and the location, not
// from any value, so the same reach on two revisions carries the same id and the
// differential can pair the two readings.
func probeID(o security.RawObservation, class Class) string {
	return fmt.Sprintf("auth %s %s %s actor=%s/%s/%s owner=%s/%s/%s",
		class, o.Method, o.Route,
		o.ActorTenant, o.ActorUser, o.ActorRole,
		o.OwnerTenant, o.OwnerUser, o.OwnerRole)
}
