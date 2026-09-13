// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds

// The parts of this provider that decide something before any request is sent.
//
// They are in the package rather than beside it because every one of them is
// unexported, and every one of them is a place where being wrong produces
// something that still works. A name that collides gives two environments one
// database. A truncated attestation still reads as verified. A fault code
// matched in one spelling and not the other reports "AWS refused" for a golden
// that merely does not exist.

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/managed/tagvalue"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// rdsIdentifier is what AWS accepts as a DB instance or DB snapshot
// identifier: it begins with a letter, holds letters, digits and hyphens, has
// no two consecutive hyphens and does not end with one.
var rdsIdentifier = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9]*(-[a-zA-Z0-9]+)*$`)

func TestBranchNameIsAValidRDSIdentifier(t *testing.T) {
	envs := []string{
		"env_conformance00001",
		"pr-42",
		"PR/1234--weird__name",
		"a",
		strings.Repeat("very-long-environment-name-", 8),
		"...",
		"9-starts-with-a-digit",
		"ends-with-a-hyphen-",
	}
	for _, env := range envs {
		name, err := branchName("scope-one", env)
		require.NoError(t, err, env)
		require.LessOrEqual(t, len(name), identifierLimit, "%s produced %s", env, name)
		require.Regexp(t, rdsIdentifier, name, "%s produced %s", env, name)
	}
}

// A name that sanitises to the same string as another must still be a
// different instance, because two environments sharing one database is the
// worst failure this file could have. The digest is what stops it, and it is
// appended ALWAYS rather than only on a truncation.
func TestBranchNameIsDistinctForNamesThatSanitiseTheSame(t *testing.T) {
	first, err := branchName("scope-one", "env_a-1")
	require.NoError(t, err)
	second, err := branchName("scope-one", "env_a__1")
	require.NoError(t, err)
	require.NotEqual(t, first, second,
		"two environment identifiers that sanitise to the same characters were given one "+
			"instance, so they would share one database")
}

// One environment identifier used against two source instances in one account
// names two instances. Without the scope in the digest both providers would
// derive the same identifier, and the second would find the first's branch and
// either adopt it or refuse to branch at all.
func TestBranchNameDiffersBetweenSources(t *testing.T) {
	first, err := branchName("scope-one", "env_shared")
	require.NoError(t, err)
	second, err := branchName("scope-two", "env_shared")
	require.NoError(t, err)
	require.NotEqual(t, first, second,
		"one environment identifier under two sources named one instance")
}

func TestBranchNameRefusesAnEmptyEnvironment(t *testing.T) {
	_, err := branchName("scope-one", "   ")
	require.Error(t, err)
}

func TestChunkAttestationRefusesRatherThanTruncating(t *testing.T) {
	tooLong := strings.Repeat("x", tagvalue.Limit*attestationChunks+1)
	_, err := chunkAttestation(tooLong)
	require.Error(t, err)
	require.Contains(t, err.Error(), "refused")
}

func TestChunkAndJoinAttestationRoundTrip(t *testing.T) {
	attestation := `{"scanner":"conformance","findings":0,"padding":"` +
		strings.Repeat("p", 900) + `"}`
	chunks, err := chunkAttestation(attestation)
	require.NoError(t, err)
	require.Greater(t, len(chunks), 1, "a 900 character attestation must span more than one tag")
	require.Equal(t, attestation, joinAttestation(chunks))
}

// A gap in the numbering stops the join rather than being skipped over.
// Concatenating across a missing chunk produces a document that looks whole and
// is not, and the attestation is the record of what was scanned.
func TestJoinAttestationStopsAtAGap(t *testing.T) {
	tags := map[string]string{
		tagAttestation + ".1": tagvalue.Encode("first"),
		tagAttestation + ".3": tagvalue.Encode("third"),
	}
	require.Equal(t, "first", joinAttestation(tags))
}

// Every value the provider writes into a tag is inside the characters AWS
// allows, for an attestation and a provenance full of what it does not.
func TestEveryFreeTextTagValueIsInsideAWSsCharacterSet(t *testing.T) {
	allowed := func(s string) bool {
		for _, r := range s {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsSpace(r) && !strings.ContainsRune("_.:/=+-@", r) {
				return false
			}
		}
		return true
	}
	attestation := `{"scanner": "conformance", "findings": 0, "tables": ["customers", "orders"], "note": "a, b; c!"}` +
		strings.Repeat("?", 700)
	require.False(t, allowed(attestation), "the attestation must contain refused characters, or this checks nothing")
	chunks, err := chunkAttestation(attestation)
	require.NoError(t, err)
	for key, value := range chunks {
		require.Truef(t, allowed(value), "%s holds a character AWS refuses: %q", key, value)
	}
	require.True(t, allowed(tagvalue.Encode("acme/production, eu (golden) #1")))
}

// Chunks that do not decode are not an attestation, and the reason says so.
func TestAnAttestationThatDoesNotDecodeIsNotAnAttestation(t *testing.T) {
	attestation, reason := readAttestation(map[string]string{tagAttestation + ".1": "{not base64, at all}"})
	require.Empty(t, attestation)
	require.Contains(t, reason, "do not decode")
}

func TestJoinAttestationOfNothingIsEmpty(t *testing.T) {
	require.Equal(t, "", joinAttestation(map[string]string{}))
}

// Both spellings of a fault code match, because nobody here has an account and
// which one arrives is not something this package can observe. The comment over
// the fault constants carries the reasoning.
func TestFaultCodesMatchWithAndWithoutTheSuffix(t *testing.T) {
	bare := &apiError{Code: "DBSnapshotNotFound"}
	suffixed := &apiError{Code: "DBSnapshotNotFoundFault"}
	require.True(t, isCode(bare, faultSnapshotNotFound))
	require.True(t, isCode(suffixed, faultSnapshotNotFound))
	require.False(t, isCode(bare, faultInstanceNotFound))
}

func TestPasswordIsPerInstance(t *testing.T) {
	p := &Provider{branchKey: secret.New("a-branch-key")}
	require.NotEqual(t, p.passwordFor("af-b-one"), p.passwordFor("af-b-two"),
		"one password for every instance means a preview's credential opens every preview")
}

func TestPasswordIsDerivableAgainRatherThanStored(t *testing.T) {
	p := &Provider{branchKey: secret.New("a-branch-key")}
	require.Equal(t, p.passwordFor("af-b-one"), p.passwordFor("af-b-one"),
		"nothing in this product writes a database password down, so a later process has "+
			"to be able to rebuild the same one from the same two inputs")
}

// RDS refuses a master password containing a slash, a double quote, an at sign
// or a space. The URL safe base64 alphabet contains none of them, and this is
// the assertion that notices if the encoding ever changes.
func TestPasswordCarriesNoCharacterRDSRefuses(t *testing.T) {
	got := (&Provider{branchKey: secret.New("a-branch-key")}).passwordFor("af-b-one")
	for _, refused := range []string{"/", `"`, "@", " "} {
		require.NotContains(t, got, refused)
	}
	require.GreaterOrEqual(t, len(got), 8)
	require.LessOrEqual(t, len(got), 128)
}

// A different branch key gives a different password for the same instance,
// which is what makes the key the thing that has to be kept rather than
// decoration on a derivation that ignored it.
func TestPasswordDependsOnTheBranchKey(t *testing.T) {
	one := (&Provider{branchKey: secret.New("first-key")}).passwordFor("af-b-one")
	two := (&Provider{branchKey: secret.New("second-key")}).passwordFor("af-b-one")
	require.NotEqual(t, one, two)
}

// The body that is signed and the body that is sent come from one expression,
// and this is the property that makes them agree: the encoding is sorted, so
// two calls with the same values produce the same bytes whatever order the map
// was built in.
func TestEncodeSortedIsStable(t *testing.T) {
	form := url.Values{"Zed": {"1"}, "Alpha": {"2"}, "Mid": {"3"}}
	require.Equal(t, "Alpha=2&Mid=3&Zed=1", encodeSorted(form))
}

// Tags go on the wire in Tags.Tag.N order sorted by key, for the same
// reason: Go's map iteration is deliberately random, so an unsorted version
// would have produced a different body, and therefore a different signature,
// on most runs.
func TestAddTagsIsSortedAndSkipsEmptyValues(t *testing.T) {
	params := url.Values{}
	addTags(params, map[string]string{"b": "two", "a": "one", "c": ""})
	require.Equal(t, "a", params.Get("Tags.Tag.1.Key"))
	require.Equal(t, "one", params.Get("Tags.Tag.1.Value"))
	require.Equal(t, "b", params.Get("Tags.Tag.2.Key"))
	require.Equal(t, "", params.Get("Tags.Tag.3.Key"),
		"an empty tag value is not sent; AWS refuses one and it carries nothing")
}

func TestMajorOfReadsAnEngineVersion(t *testing.T) {
	require.Equal(t, 17, majorOf("17.4"))
	require.Equal(t, 15, majorOf("15.6"))
	require.Equal(t, 0, majorOf(""))
	require.Equal(t, 0, majorOf("not-a-version"))
}

func TestWithKindDoesNotShareTheBaseMap(t *testing.T) {
	base := map[string]string{tagMarker: Name}
	first := withKind(base, kindCandidate)
	second := withKind(base, kindGolden)
	require.Equal(t, kindCandidate, first[tagKind])
	require.Equal(t, kindGolden, second[tagKind])
	require.Empty(t, base[tagKind], "the base tags must not carry a kind of their own")
}

// The catalog codes this provider has to be able to produce are recognised by
// errors.Is against an error built inside the engine module, which this one
// cannot import. coded.go explains why the recognition is by rendered text.
func TestCodedErrorRecognisesTheEngineRendering(t *testing.T) {
	ours := coded(codeNoSuchGolden, "there is no golden version gv_1")
	require.True(t, ours.Is(&renderedOnly{"AF-DB-004: the golden version does not exist"}))
	require.False(t, ours.Is(&renderedOnly{"AF-DB-006: too many branches"}))
	require.False(t, ours.Is(&renderedOnly{"a failure with no code in it at all"}))
}

// renderedOnly stands in for an engine catalog error, which this module cannot
// construct and can only meet as text.
type renderedOnly struct{ text string }

func (e *renderedOnly) Error() string { return e.text }

func TestCodedErrorWrapsItsCause(t *testing.T) {
	cause := &apiError{Code: "DBSnapshotNotFound", Message: "gone", Action: "DescribeDBSnapshots"}
	err := codedWrap(codeNoSuchGolden, "the golden is missing", cause)
	require.Contains(t, err.Error(), "AF-DB-004")
	require.True(t, isCode(err, faultSnapshotNotFound),
		"the cause has to survive, or the fault code it carries is unreadable")
}

func TestEndpointForUsesTheRegionUnlessOverridden(t *testing.T) {
	require.Equal(t, "https://rds.eu-west-1.amazonaws.com", endpointFor("eu-west-1", ""))
	require.Equal(t, "https://vpce.example", endpointFor("eu-west-1", "https://vpce.example/"))
}

func TestParseErrorKeepsAStatusWhenTheBodyIsNotXML(t *testing.T) {
	err := parseError("DescribeDBInstances", 503, []byte("<html>gateway</html>"))
	var api *apiError
	require.ErrorAs(t, err, &api)
	require.Equal(t, 503, api.Status)
	require.Contains(t, api.Error(), "503")
}
