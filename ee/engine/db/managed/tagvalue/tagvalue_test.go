// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package tagvalue

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// An attestation full of what a tag value refuses survives the round trip, and
// every chunk it is stored in is a value AWS accepts.
func TestADocumentWithCommasAndBracesRoundTripsThroughValidChunks(t *testing.T) {
	attestation := `{"scanner": "conformance", "findings": 0, "tables": ["customers", "orders"]}` +
		strings.Repeat(", {x}", 200)
	require.False(t, Valid(attestation), "the attestation must contain refused characters, or this checks nothing")

	chunks, err := Chunk("rds", attestation, 32)
	require.NoError(t, err)
	require.Greater(t, len(chunks), 1, "a long attestation must span more than one chunk")
	for i, chunk := range chunks {
		require.LessOrEqual(t, len(chunk), Limit, "chunk %d is longer than a tag value allows", i+1)
		require.Truef(t, Valid(chunk), "chunk %d holds a character AWS refuses: %q", i+1, chunk)
	}

	joined, err := Join(chunks)
	require.NoError(t, err)
	require.Equal(t, attestation, joined)
}

// Valid refuses the characters the live run was refused for, and accepts every
// character AWS lists.
func TestValidRefusesPunctuationAWSRefusesAndAcceptsWhatItAllows(t *testing.T) {
	for _, refused := range []string{",", "{", "}", `"`, "[", ";", "!", "#"} {
		require.Falsef(t, Valid("a"+refused+"b"), "%q was accepted", refused)
	}
	require.True(t, Valid("Az09 _.:/=+-@ éñ"), "a letter, digit, space or listed symbol was refused")
	require.True(t, Valid(""), "an empty value is allowed")
}

// Past the bound, Chunk refuses with the sentence naming the provider and the
// encoded length, rather than truncating.
func TestChunkRefusesPastTheBoundWithTheSentence(t *testing.T) {
	value := strings.Repeat("x", Limit*2)
	encodedLength := len(Encode(value))
	_, err := Chunk("aurora", value, 2)
	require.EqualError(t, err, fmt.Sprintf(
		"aurora: the verification attestation is %d characters, %d once encoded for tag values, "+
			"and does not fit in the %d tags of %d characters this provider reserves for it. It is "+
			"refused rather than truncated: half an attestation still reads as verified",
		Limit*2, encodedLength, 2, Limit))

	chunks, err := Chunk("rds", "", 2)
	require.NoError(t, err)
	require.Nil(t, chunks, "an empty value is no chunks")
}

// Join names both ways there is no attestation.
func TestJoinNamesNoChunksAndUndecodableChunks(t *testing.T) {
	_, err := Join(nil)
	require.True(t, errors.Is(err, ErrNoChunks), "got %v", err)

	_, err = Join([]string{"{not base64, at all}"})
	require.True(t, errors.Is(err, ErrUndecodable), "got %v", err)
}

// stored is an attestation written the way a provider writes it, and chunkAt
// reads chunks from a map the way a provider reads its tags.
func stored(t *testing.T, attestation string) (map[int]string, string, string) {
	t.Helper()
	chunks, err := Chunk("rds", attestation, 32)
	require.NoError(t, err)
	out := map[int]string{}
	for i, c := range chunks {
		out[i] = c
	}
	return out, fmt.Sprint(len(chunks)), Digest(attestation)
}

func chunkAt(chunks map[int]string) func(int) (string, bool) {
	return func(i int) (string, bool) {
		c, ok := chunks[i]
		return c, ok
	}
}

// Three chunks: two full ones of 256 characters and a shorter last one.
var threeChunks = strings.Repeat(`{"row": 1}, `, 42)

// Written through Chunk and Digest, an attestation comes back whole through
// Assemble, and a chunk past the count is not read.
func TestAnAttestationRoundTripsThroughChunkDigestAndAssemble(t *testing.T) {
	require.Equal(t, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", Digest("abc"),
		"Digest must be the lowercase hex sha256 of the text")

	chunks, count, digest := stored(t, threeChunks)
	require.Equal(t, "3", count, "the fixture must span three chunks")
	chunks[3] = "not read, because the count is three"
	plain, err := Assemble(count, digest, 32, chunkAt(chunks))
	require.NoError(t, err)
	require.Equal(t, threeChunks, plain)
}

// No count, no chunk and no digest is no attestation at all.
func TestAssembleOfNoTagsIsNoChunks(t *testing.T) {
	_, err := Assemble("", "", 32, chunkAt(nil))
	require.ErrorIs(t, err, ErrNoChunks)
}

// Chunks without the count that says how many were written are refused by
// name, rather than joined as far as they go.
func TestAssembleOfChunksWithoutACountNamesTheCount(t *testing.T) {
	chunks, _, digest := stored(t, threeChunks)
	_, err := Assemble("", digest, 32, chunkAt(chunks))
	require.ErrorIs(t, err, ErrNoCount)
	require.EqualError(t, err, "its attestation tags do not record a chunk count this provider writes")
}

// A count this provider would never write is refused before anything is
// gathered: signed, zero padded, zero, past the bound, or not a number.
func TestAssembleRefusesACountThisProviderDoesNotWrite(t *testing.T) {
	chunks, _, digest := stored(t, threeChunks)
	for _, count := range []string{"+3", "03", "0", "-3", "33", "three"} {
		_, err := Assemble(count, digest, 32, chunkAt(chunks))
		require.ErrorIsf(t, err, ErrNoCount, "count %q", count)
	}
}

// A missing middle chunk and a missing last chunk are both named, with how
// many of the recorded chunks are there.
func TestAssembleNamesAMissingMiddleOrLastChunk(t *testing.T) {
	for _, missing := range []int{1, 2} {
		chunks, count, digest := stored(t, threeChunks)
		delete(chunks, missing)
		_, err := Assemble(count, digest, 32, chunkAt(chunks))
		require.EqualErrorf(t, err, "its attestation tags are incomplete: 2 of 3 chunks are present", "chunk %d deleted", missing)
	}
}

// Every chunk there and no digest is refused rather than trusted.
func TestAssembleWithoutADigestNamesTheDigest(t *testing.T) {
	chunks, count, _ := stored(t, threeChunks)
	_, err := Assemble(count, "", 32, chunkAt(chunks))
	require.ErrorIs(t, err, ErrNoDigest)
	require.EqualError(t, err, "its attestation tags do not record a digest to check it against")
}

// Chunks that do not decode are named as not this provider's, before the
// digest is compared.
func TestAssembleNamesChunksThatDoNotDecode(t *testing.T) {
	_, err := Assemble("1", Digest("anything"), 32, chunkAt(map[int]string{0: "{not base64, at all}"}))
	require.ErrorIs(t, err, ErrUndecodable)
}

// A chunk shortened by a whole base64 quantum, or rewritten to other valid
// base64 of the same length, still decodes. The digest is what refuses both.
func TestAssembleRefusesAShortenedOrRewrittenChunkByItsDigest(t *testing.T) {
	chunks, count, digest := stored(t, threeChunks)
	chunks[0] = chunks[0][:Limit-4]
	_, err := Join([]string{chunks[0], chunks[1], chunks[2]})
	require.NoError(t, err, "the shortened chunk must still decode, or this is the undecodable case")
	_, err = Assemble(count, digest, 32, chunkAt(chunks))
	require.ErrorIs(t, err, ErrDigestMismatch)
	require.EqualError(t, err, "its attestation does not match the digest this provider recorded")

	chunks, count, digest = stored(t, threeChunks)
	chunks[1] = strings.Repeat("A", len(chunks[1]))
	_, err = Assemble(count, digest, 32, chunkAt(chunks))
	require.ErrorIs(t, err, ErrDigestMismatch)
}
