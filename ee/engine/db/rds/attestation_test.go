// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds_test

// A golden whose attestation is not whole is unverified, and Branch refuses it
// naming why.
//
// The attestation is stored in numbered tags, and every full chunk is 256
// characters, a multiple of four. Padded base64 cut at a multiple of four
// decodes without error to a prefix of the original, so the provider used to
// read a golden missing its middle or last chunk, or carrying a shortened one,
// as a shorter attestation that was still verified, and branch from it. The
// count of chunks and the digest of the whole now go beside them, and these
// tests take each away or change a chunk through the fake after publishing.

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/managed/tagvalue"
	"github.com/antifailure/antifailure/ee/engine/db/rds"
	"github.com/antifailure/antifailure/ee/engine/db/rds/fakerds"
	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// Four chunks once encoded: three full ones of 256 characters and a short last.
var fourChunks = `{"scanner":"conformance","findings":0,"padding":"` + strings.Repeat("p", 551) + `"}`

// publishedGolden publishes a golden with a four chunk attestation, requires it
// to read as verified before anything is changed, and answers what the tests
// change.
func publishedGolden(t *testing.T) (*fakerds.Server, *rds.Provider, provider.GoldenVersion) {
	t.Helper()
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)
	var masked, verified int
	gv, err := p.RefreshGolden(context.Background(), spec(&masked, &verified, fourChunks))
	require.NoError(t, err)
	tags, ok := server.TagsOf(gv.ProviderRef)
	require.True(t, ok)
	require.Equal(t, "4", tags["antifailure:attestation.count"], "the fixture must span four chunks")
	require.Equal(t, tagvalue.Digest(fourChunks), tags["antifailure:attestation.sha256"])
	requireVerified(t, p, gv)
	return server, p, gv
}

func requireVerified(t *testing.T, p *rds.Provider, gv provider.GoldenVersion) {
	t.Helper()
	goldens, err := p.ListGoldens(context.Background())
	require.NoError(t, err)
	require.Len(t, goldens, 1)
	require.True(t, goldens[0].Verified, "the untouched golden must read as verified, or the change below proves nothing")
	require.Equal(t, fourChunks, goldens[0].Attestation)
}

// requireUnverified is the one outcome every changed attestation must reach:
// still listed, because its receipt is intact, and neither verified nor
// branchable, with the reason in Branch's refusal.
func requireUnverified(t *testing.T, p *rds.Provider, gv provider.GoldenVersion, reason string) {
	t.Helper()
	ctx := context.Background()
	goldens, err := p.ListGoldens(ctx)
	require.NoError(t, err)
	require.Len(t, goldens, 1, "the golden must still be listed, so the listing shows it unverified rather than hiding it")
	require.False(t, goldens[0].Verified, "a golden whose attestation is not whole was listed as verified")
	require.Empty(t, goldens[0].Attestation, "a golden whose attestation is not whole carried part of one")

	_, err = p.Branch(ctx, gv.ID, "env_partial_attestation")
	require.Error(t, err, "a golden whose attestation is not whole was branched")
	require.Contains(t, err.Error(), "carries no verification attestation this provider published")
	require.Contains(t, err.Error(), reason)
}

func TestAGoldenMissingAMiddleAttestationChunkIsUnverified(t *testing.T) {
	server, p, gv := publishedGolden(t)
	server.DeleteTags(gv.ProviderRef, "antifailure:attestation.2")
	requireUnverified(t, p, gv, "its attestation tags are incomplete: 3 of 4 chunks are present")
}

func TestAGoldenMissingItsLastAttestationChunkIsUnverified(t *testing.T) {
	server, p, gv := publishedGolden(t)
	server.DeleteTags(gv.ProviderRef, "antifailure:attestation.4")
	requireUnverified(t, p, gv, "its attestation tags are incomplete: 3 of 4 chunks are present")
}

// Shortened by a whole base64 quantum, so it still decodes.
func TestAGoldenWithAShortenedAttestationChunkIsUnverified(t *testing.T) {
	server, p, gv := publishedGolden(t)
	tags, _ := server.TagsOf(gv.ProviderRef)
	server.SetTags(gv.ProviderRef, map[string]string{"antifailure:attestation.1": tags["antifailure:attestation.1"][:tagvalue.Limit-4]})
	requireUnverified(t, p, gv, "its attestation does not match the digest this provider recorded")
}

// Rewritten to other valid base64 of the same length, so only the digest can
// tell.
func TestAGoldenWithAnAttestationChunkRewrittenToTheSameLengthIsUnverified(t *testing.T) {
	server, p, gv := publishedGolden(t)
	server.SetTags(gv.ProviderRef, map[string]string{"antifailure:attestation.2": strings.Repeat("A", tagvalue.Limit)})
	requireUnverified(t, p, gv, "its attestation does not match the digest this provider recorded")
}

func TestAGoldenWithoutItsAttestationDigestIsUnverified(t *testing.T) {
	server, p, gv := publishedGolden(t)
	server.DeleteTags(gv.ProviderRef, "antifailure:attestation.sha256")
	requireUnverified(t, p, gv, "its attestation tags do not record a digest to check it against")
}

func TestAGoldenWithoutItsAttestationCountIsUnverified(t *testing.T) {
	server, p, gv := publishedGolden(t)
	server.DeleteTags(gv.ProviderRef, "antifailure:attestation.count")
	requireUnverified(t, p, gv, "its attestation tags do not record a chunk count this provider writes")
}

// An attestation at the bound fills every chunk tag, and with the count, the
// digest and the rest of the metadata the golden is still inside the fifty
// tags AWS allows on one resource.
func TestAGoldenAtTheAttestationBoundCarriesNoMoreThanFiftyTags(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)
	var masked, verified int
	atBound := strings.Repeat("x", 32*tagvalue.Limit/4*3)
	gv, err := p.RefreshGolden(context.Background(), spec(&masked, &verified, atBound))
	require.NoError(t, err)
	tags, ok := server.TagsOf(gv.ProviderRef)
	require.True(t, ok)
	require.Equal(t, "32", tags["antifailure:attestation.count"], "the fixture must fill every chunk tag")
	require.LessOrEqual(t, len(tags), 50, "a golden at the attestation bound carries more tags than AWS allows: %v", keysOf(tags))
}

func keysOf(tags map[string]string) []string {
	out := make([]string, 0, len(tags))
	for k := range tags {
		out = append(out, k)
	}
	return out
}
