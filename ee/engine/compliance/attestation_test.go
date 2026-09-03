// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package compliance

// The attestation verifier, checked against attestations the engine signed.
//
// Same problem as the audit chain and the same answer. A compliance report
// repeats what an attestation says, so it has to check the signature first, and
// a verifier that only agrees with itself would report every real attestation
// as forged or every forged one as real.
//
// The coupling here is tighter than it looks and worth naming. The engine signs
// the SHA-256 of its own struct marshalled by encoding/json, so the FIELD ORDER
// of that struct is part of the signature. This package cannot import the
// engine's type, because a separate module cannot reach engine/internal, so it
// mirrors it. The fixtures below were produced by the engine's own signer, and
// the field-order test compares the bytes this package would sign against the
// bytes the engine actually signed. A reordering on either side fails here
// rather than turning every attestation in the field into a forgery.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return raw
}

func TestAnAttestationTheEngineSignedVerifies(t *testing.T) {
	a, err := ParseAttestation(fixture(t, "attestation-clean.json"))
	require.NoError(t, err)
	require.True(t, a.SignatureValid,
		"an attestation produced by the engine's own signer does not verify here, "+
			"so every masking scan would be reported as altered")
	require.Empty(t, a.Unverifiable)
	require.True(t, a.Clean)
	require.Equal(t, "gv_20260827090041", a.Golden)
	require.Equal(t, 318, a.Report.Columns)
	require.Equal(t, int64(318000), a.Report.RowsSampled)
	require.Len(t, a.Report.Skipped, 1)
	t.Logf("%s", a.Summary())
}

func TestAnAttestationWithFindingsVerifiesAndIsNotClean(t *testing.T) {
	// A scan that found real data is the control working: it is what stops the
	// golden being published. The signature still has to verify, and the two
	// facts are separate.
	a, err := ParseAttestation(fixture(t, "attestation-with-findings.json"))
	require.NoError(t, err)
	require.True(t, a.SignatureValid)
	require.False(t, a.Clean)
	require.Len(t, a.Report.Findings, 1)
	require.Equal(t, "users", a.Report.Findings[0].Table)
	// The example in a finding is redacted by the engine and must stay that
	// way through here: a compliance report is a document that gets emailed.
	require.NotContains(t, a.Summary(), "@")
}

func TestTheFieldOrderMatchesWhatTheEngineSigned(t *testing.T) {
	// The drift guard for the tightest coupling in this package. The engine
	// signs its struct marshalled by encoding/json, so field order is part of
	// the signature; this asserts that the document this package would produce
	// is byte for byte the document the engine produced.
	raw := fixture(t, "attestation-clean.json")
	var a Attestation
	require.NoError(t, json.Unmarshal(raw, &a))

	mine, err := json.Marshal(a)
	require.NoError(t, err)

	// The fixture is indented for reading, so it is re-marshalled through a
	// generic decode to compare shape rather than whitespace. Key ORDER
	// survives that only because both sides are re-encoded from ordered
	// structures, which is exactly what is being compared.
	var compacted bytes.Buffer
	require.NoError(t, json.Compact(&compacted, raw))
	require.Equal(t, compacted.String(), string(mine),
		"the attestation's field order or set has changed on one side. Field order is "+
			"part of the signature, so this would report every attestation as forged")
}

func TestATamperedAttestationDoesNotVerify(t *testing.T) {
	// One byte. The whole reason the signature is checked rather than the
	// document being repeated on trust: an attestation says a golden was
	// scanned and found clean, and a report that repeated an altered one would
	// launder it into evidence.
	raw := fixture(t, "attestation-with-findings.json")
	altered := bytes.Replace(raw, []byte(`"rows": 37`), []byte(`"rows": 36`), 1)
	require.NotEqual(t, raw, altered, "the fixture no longer contains what this test alters")

	a, err := ParseAttestation(altered)
	require.NoError(t, err, "an altered attestation still has to decode, so it can be named")
	require.False(t, a.SignatureValid)
	require.Contains(t, a.Summary(), "SIGNATURE DOES NOT VERIFY")
}

func TestAnAttestationClaimingCleanWhenItIsNotDoesNotVerify(t *testing.T) {
	// The specific forgery this exists to catch: somebody takes an attestation
	// that found real data and removes the findings, so the document reads as a
	// clean scan of a golden that was not clean.
	raw := fixture(t, "attestation-with-findings.json")
	var document map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &document))
	var report map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(document["report"], &report))
	report["findings"] = json.RawMessage("null")
	encoded, err := json.Marshal(report)
	require.NoError(t, err)
	document["report"] = encoded
	forged, err := json.Marshal(document)
	require.NoError(t, err)

	a, err := ParseAttestation(forged)
	require.NoError(t, err)
	require.True(t, a.Clean, "the forgery does claim to be clean")
	require.False(t, a.SignatureValid, "the forgery was accepted")
}

func TestAnAttestationWithNoKeyIsUnverifiableRatherThanForged(t *testing.T) {
	// Different problems and different next actions. A document with a
	// malformed key was probably corrupted in storage; one that fails
	// verification was probably changed.
	a, err := ParseAttestation([]byte(`{"report":{},"golden":"g","rules_hash":"r",` +
		`"public_key":"not base64 at all","signature":""}`))
	require.NoError(t, err)
	require.False(t, a.SignatureValid)
	require.Contains(t, a.Unverifiable, "public key")
}

func TestSomethingThatIsNotAnAttestationIsRefused(t *testing.T) {
	_, err := ParseAttestation([]byte(`this is not json`))
	require.Error(t, err)
}
