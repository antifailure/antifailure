package env

// What a golden.ready event has to say for the control plane to record it.
//
// The event existed before this file did, and it carried three fields: a
// phase, a version string and a boolean. The control plane accepts the type,
// maps it from golden.ready, stores it, and projected nothing, because
// golden_versions is keyed on (org_id, repository_id, version) and no event
// had ever carried a repository. So the table's only writers were the test
// harness and the staging seeder, and three things a customer meets read it:
// the console's attestation table, the goldens quota on the plan page, and the
// compliance pack's masking control, which reported "the check ran and found
// nothing to show" for an organization that signed an attestation every night.
//
// The identity is the same set every environment lifecycle event carries and
// for the same reason, which identity.go states in full: the control plane
// cannot name a row it has not been told the repository of, and an identity
// carried on only one event is an identity that goes missing exactly when the
// sink drops the oldest events of a failed batch.
//
// The attestation travels with it rather than being fetched later. It is the
// document the compliance control is about, it is bounded (a golden with
// findings is refused rather than published, so the findings list is empty in
// every case that produces one of these), and there is no second channel: the
// control plane cannot reach the customer's database provider to ask.

import (
	"time"

	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// goldenFields describes a golden version to whatever is listening.
//
// source_digest is deliberately absent. The column exists and nothing the
// engine holds is that digest: GoldenVersion carries the provider's own opaque
// reference and the hash of the masking rules, and neither is a digest of the
// source database. Sending one of them under that name would put a value in a
// compliance record that means something other than what the column says.
func (o *Orchestrator) goldenFields(gv provider.GoldenVersion) []events.Field {
	fields := append(o.identity(),
		events.F("phase", "golden"),
		events.F("version", gv.ID),
		events.F("verified", gv.Verified),
	)
	if gv.RulesHash != "" {
		fields = append(fields, events.F("rules_digest", gv.RulesHash))
	}
	if gv.SizeBytes > 0 {
		fields = append(fields, events.F("size_bytes", gv.SizeBytes))
	}
	if gv.Attestation != "" {
		fields = append(fields, events.F("attestation", gv.Attestation))
	}
	if !gv.CreatedAt.IsZero() {
		// When the golden was made, which is not when this event fired. A
		// version branched every day for a week would otherwise be recorded
		// as seven different ages of the same thing, and the compliance pack
		// selects on created_at within a reporting period.
		fields = append(fields, events.F("created_at", gv.CreatedAt.UTC().Format(time.RFC3339Nano)))
	}
	return fields
}

// findGolden picks a version out of a listing by its identifier.
//
// Returns whether it was found rather than a zero value, because a zero
// GoldenVersion has Verified false, and announcing a verified golden as
// unverified is the one mistake in this area that a reader cannot detect.
func findGolden(goldens []provider.GoldenVersion, version string) (provider.GoldenVersion, bool) {
	for _, g := range goldens {
		if g.ID == version {
			return g, true
		}
	}
	return provider.GoldenVersion{}, false
}

// announceGolden emits golden.ready for a version chosen from a listing.
//
// Falls back to the identifier alone when the listing does not hold it, which
// happens when a refresh replaced the version between the listing and here.
// The control plane can still record the row: the repository comes from the
// identity and the version is the key, so a later announcement of the same
// version fills in what this one could not.
func (o *Orchestrator) announceGolden(
	s *session, goldens []provider.GoldenVersion, version, msg string,
) {
	if gv, ok := findGolden(goldens, version); ok {
		o.event(s, events.GoldenReady, msg, o.goldenFields(gv)...)
		return
	}
	o.event(s, events.GoldenReady, msg,
		o.goldenFields(provider.GoldenVersion{ID: version, Verified: true})...)
}
