// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package aurora

// The pieces that have no exported surface and would otherwise be checked only
// through something that uses them.
//
// Two of them decide whether a golden reads as verified, which is the
// product's central promise, and neither is reachable from outside this
// package. A test that could only reach them through RefreshGolden would be
// asserting on the promise through four network calls and a Postgres.

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/secret"
)

func TestAnOversizeAttestationIsRefusedRatherThanTruncated(t *testing.T) {
	// A golden carrying half an attestation still reads as verified, because
	// verified means an attestation is present. Truncating would therefore
	// publish a golden whose record of what was scanned is a fragment, and
	// nothing downstream could tell.
	_, err := chunkAttestation(strings.Repeat("x", tagValueLimit*attestationChunks+1))
	require.Error(t, err)
	require.Contains(t, err.Error(), "refused rather than truncated")

	full, err := chunkAttestation(strings.Repeat("x", tagValueLimit*attestationChunks))
	require.NoError(t, err)
	require.Len(t, full, attestationChunks)
}

func TestAnAttestationSurvivesBeingSplitAndRejoined(t *testing.T) {
	attestation := `{"scanner":"verify","findings":0,"signature":"` +
		strings.Repeat("ab", 400) + `"}`
	chunks, err := chunkAttestation(attestation)
	require.NoError(t, err)
	require.Greater(t, len(chunks), 1, "the value is short enough to fit in one tag, so "+
		"this is not testing the split")
	require.Equal(t, attestation, joinAttestation(chunks))
}

func TestAnAttestationWithAMissingChunkIsNotAnAttestation(t *testing.T) {
	// Half an attestation is not a shorter attestation, it is an unverified
	// golden. Returning the prefix would make Verified true for a record
	// nothing can check.
	attestation := strings.Repeat("y", tagValueLimit*3)
	chunks, err := chunkAttestation(attestation)
	require.NoError(t, err)
	require.Len(t, chunks, 3)

	// The middle one lost, which is what a tag write that partly failed looks
	// like: the numbering still starts at zero and the value is shorter than
	// a full chunk where the hole is.
	chunks[tagAttestation+":1"] = "short"
	require.Empty(t, joinAttestation(chunks),
		"a golden missing a chunk of its attestation reported as verified")
}

func TestNoAttestationIsNotVerified(t *testing.T) {
	require.Empty(t, joinAttestation(map[string]string{tagKind: kindGolden}))
}

func TestAMasterPasswordIsDerivedAndAcceptableToAurora(t *testing.T) {
	// Aurora refuses a master password containing a slash, a double quote, an
	// at sign or a space, and requires eight to a hundred and a first
	// character that is not a slash. base64url's alphabet avoids all four, and
	// this is the assertion that keeps that true if the encoding ever changes.
	p := &Provider{branchKey: secret.New("a-key")}
	password := p.passwordFor("af-b-0123456789ab")
	require.GreaterOrEqual(t, len(password), 8)
	require.LessOrEqual(t, len(password), 100)
	for _, forbidden := range []string{"/", `"`, "@", " "} {
		require.NotContains(t, password, forbidden,
			"Aurora refuses a master password containing %q", forbidden)
	}
}

func TestTwoClustersGetDifferentPasswordsAndOneClusterGetsTheSameOneTwice(t *testing.T) {
	p := &Provider{branchKey: secret.New("a-key")}
	require.NotEqual(t, p.passwordFor("af-b-one"), p.passwordFor("af-b-two"),
		"two clusters share a password, so one environment's credential opens another's")
	require.Equal(t, p.passwordFor("af-b-one"), p.passwordFor("af-b-one"),
		"the derivation is not deterministic, so a second process cannot rebuild a "+
			"connection string")
}

func TestChangingTheKeyChangesEveryPassword(t *testing.T) {
	// Which is the operator's rotation mechanism and the reason the key is a
	// declared variable rather than a generated file.
	one := (&Provider{branchKey: secret.New("first")}).passwordFor("af-b-one")
	two := (&Provider{branchKey: secret.New("second")}).passwordFor("af-b-one")
	require.NotEqual(t, one, two)
}

func TestAClusterIdentifierIsOneAWSWouldAccept(t *testing.T) {
	// A golden version identifier carries underscores and an environment
	// identifier is not this package's to shorten, so both are hashed. AWS
	// wants letters, digits and single hyphens, a first character that is a
	// letter, and at most sixty three characters.
	for _, input := range []string{
		"gv_20260907123456123456_deadbeef",
		"env_conformance00005",
		strings.Repeat("a-very-long-environment-identifier", 4),
		"",
	} {
		for _, prefix := range []string{goldenPrefix, branchPrefix} {
			name := prefix + shortHash(input)
			require.Regexp(t, `^[a-zA-Z][a-zA-Z0-9-]*$`, name)
			require.NotContains(t, name, "--")
			require.False(t, strings.HasSuffix(name, "-"))
			require.LessOrEqual(t, len(name), 63)
		}
	}
}

func TestTheCodedErrorsMatchTheEngineCatalogsCodes(t *testing.T) {
	// The workaround in coded.go is what makes three conformance behaviours
	// passable from outside the engine module. If it stops recognising the
	// engine's rendering, those three go red with a message about a code
	// rather than about the behaviour, so it is checked directly here against
	// the exact shape engine/internal/errors produces.
	rendered := map[string]string{
		codeUnverifiedGolden: "AF-MSK-001: The golden gv_1 has no valid verification " +
			"attestation and cannot be branched.",
		codeNoSuchGolden: "AF-DB-004: The golden version gv_1 no longer exists.",
		codeBranchLimit:  "AF-DB-006: The provider's concurrent branch limit (4) is reached.",
		// With an operation prefix, which engine/internal/errors adds when a
		// call site records where the error passed through.
		codeGoldenReferenced: "db.aurora: golden: AF-DB-005: The golden version gv_1 is " +
			"still referenced by 2 environments and cannot be collected.",
	}
	for code, text := range rendered {
		require.Equal(t, code, codeOf(text))
		require.True(t, coded(code, "").Is(fakeCatalogError(text)),
			"the provider's %s no longer matches the engine's rendering of it", code)
	}
	require.False(t, coded(codeNoSuchGolden, "").Is(fakeCatalogError(
		"AF-MSK-001: The golden gv_1 has no valid verification attestation.")))
}

// fakeCatalogError stands in for engine/internal/errors.Error, which this
// module cannot import. What matters to coded.Is is the rendering, and the
// rendering is what is reproduced above.
type fakeCatalogError string

func (e fakeCatalogError) Error() string { return string(e) }

func TestAnAttestationIsSplitAtTheTagCeilingAndNotBefore(t *testing.T) {
	// Off by one in either direction wastes a tag or exceeds the limit, and
	// AWS refuses a tag value over 256 characters rather than truncating it,
	// so exceeding it fails the whole publish.
	chunks, err := chunkAttestation(strings.Repeat("z", tagValueLimit))
	require.NoError(t, err)
	require.Len(t, chunks, 1)

	chunks, err = chunkAttestation(strings.Repeat("z", tagValueLimit+1))
	require.NoError(t, err)
	require.Len(t, chunks, 2)
	for i := range chunks {
		require.LessOrEqual(t, len(chunks[i]), tagValueLimit,
			"a chunk is longer than AWS allows in one tag value")
	}
	require.Equal(t, tagValueLimit, len(chunks[tagAttestation+":"+strconv.Itoa(0)]))
}
