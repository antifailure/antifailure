// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package compliance

// The attestation the engine signs today, verified here.
//
// The fixtures in testdata were produced by the engine's signer on 2026-08-26
// and never again. Since then the engine added provenance, report.engine,
// report.unread and report.unruled to what it signs, and this package learned
// none of them, so it dropped each one on decode, re-encoded different bytes,
// and called every real attestation forged while those fixtures kept passing.
//
// engine/internal/verify signs attestation-signed.json with a fixed key and
// every field of the signed shape set, and fails when the shape changes without
// the file. Reading that file here is what turns a change on the engine side
// into a failure on this side, instead of a compliance report nobody can
// believe. It is read from the engine's tree because this module depends on
// the engine and not the other way round; the engine suite runs with ee
// removed, and must not need a file under it.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// engineAttestationFixture is the attestation engine/internal/verify signs with
// a fixed key, read from the engine's own tree.
//
// This reaches into engine on purpose, and it is the only direction allowed:
// this module depends on the engine, while the engine must build and pass its
// suite with ee removed, which tools/editioncheck enforces by removing ee and
// refusing any engine package that needs a file under it. So the fixture lives
// in engine and is read from here, never the reverse.
//
// A checkout without the engine tree cannot run this, and it fails rather than
// skips: a missing fixture read as a pass is a compliance verifier checked
// against nothing.
const engineAttestationFixture = "../../../engine/internal/verify/testdata/attestation-signed.json"

func engineSignedAttestation(t *testing.T) []byte {
	t.Helper()
	path := filepath.FromSlash(engineAttestationFixture)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the engine's signed attestation fixture could not be read at %s: %v. "+
			"It is written by engine/internal/verify with -update-attestation, and this test "+
			"fails rather than skips without it", path, err)
	}
	return raw
}

func TestAnAttestationTheEngineSignsTodayVerifies(t *testing.T) {
	a, err := ParseAttestation(engineSignedAttestation(t))
	require.NoError(t, err)
	require.Empty(t, a.Unverifiable)
	// The signature is the whole assertion, and it is deliberately not
	// accompanied by checks on individual fields. A field this package lacks
	// is exactly the defect, and naming one here would make its absence a
	// compile error instead of this failure, which is the failure a
	// compliance report actually shows.
	require.True(t, a.SignatureValid,
		"an attestation the engine signs today does not verify here, so the compliance "+
			"report would call every real masking scan forged. Mirror the engine's signed "+
			"shape in Attestation and in the struct verify() encodes")
}

func TestEveryFieldTheEngineSignsIsMirroredInOrder(t *testing.T) {
	raw := engineSignedAttestation(t)
	var a Attestation
	require.NoError(t, json.Unmarshal(raw, &a))
	mine, err := json.Marshal(a)
	require.NoError(t, err)

	var compacted bytes.Buffer
	require.NoError(t, json.Compact(&compacted, raw))
	require.Equal(t, compacted.String(), string(mine),
		"this package re-encodes the engine's attestation differently: a field is missing, "+
			"extra or out of order, and field order is part of the signature")
}
