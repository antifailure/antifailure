// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package compliance

// The audit chain verifier, checked against the implementation that wrote every
// hash it will ever see.
//
// This is the test that matters most in this package. A compliance report says
// whether the audit log has been rewritten, and it says so by recomputing every
// entry's hash. If this implementation and the control plane's disagree by one
// byte, the report is wrong on every installation, in one of two directions,
// and reading either implementation tells you nothing about which.
//
// So the agreement is proved rather than reasoned about, twice. Against
// recorded vectors that the real TypeScript produced, which always run. And, on
// a machine with node and the workspace's dependencies, by re-running that
// TypeScript and requiring it to produce the recorded vectors unchanged, which
// is the guard against either side drifting later.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type vector struct {
	Seq             int64           `json:"seq"`
	OrgID           string          `json:"orgId"`
	ActorUserID     *string         `json:"actorUserId"`
	ActorLabel      string          `json:"actorLabel"`
	Action          string          `json:"action"`
	TargetType      string          `json:"targetType"`
	TargetID        *string         `json:"targetId"`
	Origin          string          `json:"origin"`
	Detail          json.RawMessage `json:"detail"`
	OccurredAt      string          `json:"occurredAt"`
	PrevHash        *string         `json:"prevHash"`
	CanonicalDetail string          `json:"canonicalDetail"`
	Hash            string          `json:"hash"`
}

func (v vector) entry(t *testing.T) AuditEntry {
	t.Helper()
	occurred, err := time.Parse(time.RFC3339Nano, v.OccurredAt)
	require.NoError(t, err)
	return AuditEntry{
		Seq: v.Seq, OrgID: v.OrgID, ActorUserID: deref(v.ActorUserID),
		ActorLabel: v.ActorLabel, Action: v.Action, TargetType: v.TargetType,
		TargetID: deref(v.TargetID), Origin: v.Origin, Detail: v.Detail,
		OccurredAt: occurred, PrevHash: deref(v.PrevHash), EntryHash: v.Hash,
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func loadVectors(t *testing.T) []vector {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "audit-chain-vectors.json"))
	require.NoError(t, err)
	var out []vector
	require.NoError(t, json.Unmarshal(raw, &out))
	require.NotEmpty(t, out)
	return out
}

func TestTheGoHashAgreesWithTheControlPlanesOwn(t *testing.T) {
	for _, v := range loadVectors(t) {
		t.Run(v.Action, func(t *testing.T) {
			// The canonical form of the detail first, because when the hash
			// disagrees this is almost always why and comparing the hashes
			// alone tells you nothing about where.
			require.Equal(t, v.CanonicalDetail, canonicalJSON(v.Detail),
				"the canonical form of the detail differs from the control plane's")
			require.Equal(t, v.Hash, v.entry(t).hash(),
				"the entry hash differs from the control plane's, so every audit log "+
					"in the field would verify wrongly")
		})
	}
}

func TestTheRecordedVectorsStillMatchTheControlPlane(t *testing.T) {
	// The drift guard. The vectors are a snapshot of what audit.ts produced on
	// the day they were taken, and a snapshot goes stale silently. This
	// re-runs the real thing and requires it to produce the same document.
	//
	// It skips only when this machine cannot run it at all. Skipping on a
	// failure would be the same mistake as a test harness that treats a broken
	// container as a reason not to run.
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("skipped: node is not on the path, so the TypeScript side cannot be re-run")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "web"))
	require.NoError(t, err)
	if _, err := os.Stat(filepath.Join(root, "node_modules", "drizzle-orm")); err != nil {
		t.Skip("skipped: the web workspace's dependencies are not installed (npm ci in web/)")
	}

	generator, err := filepath.Abs(filepath.Join("testdata", "vectors.mjs"))
	require.NoError(t, err)
	cmd := exec.Command("node", generator)
	cmd.Dir = root
	produced, err := cmd.Output()
	require.NoError(t, err, "the reference generator failed")

	recorded, err := os.ReadFile(filepath.Join("testdata", "audit-chain-vectors.json"))
	require.NoError(t, err)

	// Compared as documents rather than as bytes, so that a difference in
	// trailing whitespace is not reported as a change to the canonical form.
	var fresh, stored []vector
	require.NoError(t, json.Unmarshal(produced, &fresh))
	require.NoError(t, json.Unmarshal(recorded, &stored))
	require.Equal(t, stored, fresh,
		"the control plane's canonical form has changed and the recorded vectors have not. "+
			"Every audit entry already written was hashed the old way, so this is a migration "+
			"and not a regeneration")
}

// ---------------------------------------------------------------------------

func TestAnIntactChainVerifies(t *testing.T) {
	entries := chainOf(t, 5)
	report := VerifyChain(nil, entries)
	require.Empty(t, report.Breaks)
	require.Equal(t, 5, report.Entries)
	require.Equal(t, int64(1), report.FirstSeq)
	require.Equal(t, int64(5), report.LastSeq)
	require.Equal(t, entries[4].EntryHash, report.Head)
}

func TestAnAlteredEntryIsFound(t *testing.T) {
	// The whole reason the chain exists. Somebody with a privileged connection
	// changes what an entry says; its stored hash no longer matches its
	// contents, and every entry after it still points at the old hash.
	entries := chainOf(t, 5)
	entries[2].Action = "something.else"

	report := VerifyChain(nil, entries)
	require.NotEmpty(t, report.Breaks)
	require.Equal(t, int64(3), report.Breaks[0].Seq)
	require.Equal(t, "altered", report.Breaks[0].Kind)
}

func TestARemovedEntryIsFound(t *testing.T) {
	// The other half. Deleting an entry leaves the one after it pointing at a
	// hash that is no longer there, and leaves a gap in the sequence.
	entries := chainOf(t, 5)
	entries = append(entries[:2], entries[3:]...)

	report := VerifyChain(nil, entries)
	kinds := map[string]bool{}
	for _, b := range report.Breaks {
		kinds[b.Kind] = true
	}
	require.True(t, kinds["broken_link"], "the link past the removed entry was not noticed")
	require.True(t, kinds["missing"], "the gap in the sequence was not noticed")
}

func TestAWindowIsVerifiedAgainstItsAnchor(t *testing.T) {
	// A report is about a period, and the first entry in any period points at
	// one outside it. Without an anchor every report ever produced would open
	// with a broken link at its first row, which would train whoever reads
	// these documents to ignore exactly the finding they are for.
	entries := chainOf(t, 6)
	anchor := entries[2]
	window := entries[3:]

	withAnchor := VerifyChain(&anchor, window)
	require.Empty(t, withAnchor.Breaks)
	require.Equal(t, 3, withAnchor.Entries, "the anchor must not be counted in the period")

	withoutAnchor := VerifyChain(nil, window)
	require.Empty(t, withoutAnchor.Breaks,
		"an unanchored window still verifies its own entries; only a link to what "+
			"precedes it cannot be checked")

	// And an anchor that does not match is reported, which is the case where
	// somebody removed the entries just before the period being audited.
	wrong := anchor
	wrong.EntryHash = "0000000000000000000000000000000000000000000000000000000000000000"
	require.NotEmpty(t, VerifyChain(&wrong, window).Breaks)
}

func TestAnEmptyLogIsNotABreak(t *testing.T) {
	report := VerifyChain(nil, nil)
	require.Empty(t, report.Breaks)
	require.Zero(t, report.Entries)
}

// chainOf builds a valid chain of n entries, hashed the way the control plane
// hashes them, which is the way this package's own verifier computes them. That
// is circular on its own and is not the proof: the proof is the vector test
// above, and this exists so the break tests have something to break.
func chainOf(t *testing.T, n int) []AuditEntry {
	t.Helper()
	var out []AuditEntry
	previous := ""
	base := time.Date(2026, 8, 27, 9, 0, 0, 0, time.UTC)
	for i := 1; i <= n; i++ {
		entry := AuditEntry{
			Seq: int64(i), OrgID: "11111111-1111-1111-1111-111111111111",
			ActorLabel: "someone", Action: "environment.created",
			TargetType: "environment", TargetID: "env-" + string(rune('a'+i-1)),
			Origin: "engine", Detail: json.RawMessage(`{"branch":"main"}`),
			OccurredAt: base.Add(time.Duration(i) * time.Minute), PrevHash: previous,
		}
		entry.EntryHash = entry.hash()
		previous = entry.EntryHash
		out = append(out, entry)
	}
	return out
}
