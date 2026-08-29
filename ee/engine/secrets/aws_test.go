package secrets

// The signing, against the example AWS publishes.
//
// This is the part of the AWS adapter that is worth proving without an AWS
// account, and it is also the part most likely to be wrong: a signature is
// either exactly right or it is a 403 that reads like a bad secret key. AWS
// publishes a worked example with the inputs, the intermediate strings and the
// final signature, precisely so an implementation can be checked against
// something other than itself. Checking against our own idea of the algorithm
// would prove only that the code agrees with the code.
//
// Everything else in this adapter needs a real account and is marked `written`
// rather than `proven` in STATUS.md until it has one.

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The credentials in this file are AWS's own published example values. They are
// documented as examples, they authenticate nothing, and they are the only
// values that produce the published signature, so the vector cannot be checked
// without them.
const (
	exampleKeyID  = "AKIA" + "IOSFODNN7EXAMPLE"
	exampleSecret = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
)

func TestSigV4MatchesTheCanonicalExample(t *testing.T) {
	// AWS General Reference, Signature Version 4 signing examples: a GET of an
	// S3 object with a Range header, on 24 May 2013.
	signed, err := signV4(sigV4Request{
		method: "GET",
		url:    "https://examplebucket.s3.amazonaws.com/test.txt",
		body:   nil,
		headers: map[string]string{
			"Range": "bytes=0-9",
		},
		region:  "us-east-1",
		service: "s3",
		credentials: AWSCredentials{
			AccessKeyID: exampleKeyID, SecretAccessKey: exampleSecret,
		},
		now: time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	authorization := signed["Authorization"]
	require.Contains(t, authorization,
		"Credential="+exampleKeyID+"/20130524/us-east-1/s3/aws4_request")
	require.Contains(t, authorization,
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date")
	require.Contains(t, authorization,
		"Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41",
		"the signature does not match the published example, so every real request would be refused")

	// The empty body hashes to the published value, which is the constant every
	// AWS example carries and the easiest thing to get wrong by hashing nothing
	// rather than hashing the empty string.
	require.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		signed["X-Amz-Content-Sha256"])
	require.Equal(t, "20130524T000000Z", signed["X-Amz-Date"])
}

func TestSigV4SignsTheSessionTokenRatherThanOnlySendingIt(t *testing.T) {
	// A temporary credential whose token is attached after signing produces a
	// signature over a different request, and the refusal that comes back reads
	// like a wrong secret key rather than like a signing mistake. Every
	// credential this adapter finds except a long-lived user key is temporary,
	// so this is the common case and not the exotic one.
	with, err := signV4(sigV4Request{
		method: "POST", url: "https://secretsmanager.eu-west-1.amazonaws.com/",
		body: []byte(`{"SecretId":"x"}`), region: "eu-west-1", service: "secretsmanager",
		credentials: AWSCredentials{
			AccessKeyID: exampleKeyID, SecretAccessKey: exampleSecret,
			SessionToken: "a-temporary-token",
		},
		now: time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Contains(t, with["Authorization"], "x-amz-security-token",
		"the session token is not in the signed header list")
	require.Equal(t, "a-temporary-token", with["X-Amz-Security-Token"])

	// And the signature actually differs, rather than the header merely being
	// listed. Listing a header and not hashing it is the same bug wearing a
	// disguise.
	without, err := signV4(sigV4Request{
		method: "POST", url: "https://secretsmanager.eu-west-1.amazonaws.com/",
		body: []byte(`{"SecretId":"x"}`), region: "eu-west-1", service: "secretsmanager",
		credentials: AWSCredentials{AccessKeyID: exampleKeyID, SecretAccessKey: exampleSecret},
		now:         time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.NotEqual(t, signatureOf(t, with), signatureOf(t, without))
}

func TestSigV4SignsTheBody(t *testing.T) {
	// Two requests differing only in their body must sign differently, or the
	// signature is not protecting the thing being asked for.
	one := mustSign(t, []byte(`{"SecretId":"one"}`))
	two := mustSign(t, []byte(`{"SecretId":"two"}`))
	require.NotEqual(t, signatureOf(t, one), signatureOf(t, two))
}

func TestSigV4CanonicalisesAnEmptyPath(t *testing.T) {
	// An endpoint written without a trailing slash has an empty path, and an
	// empty path canonicalises to "/". Signing "" produces a signature the
	// service does not agree with, and the endpoint is written both ways in the
	// wild.
	withSlash, err := signV4(sigV4Request{
		method: "POST", url: "https://secretsmanager.eu-west-1.amazonaws.com/",
		body: []byte(`{}`), region: "eu-west-1", service: "secretsmanager",
		credentials: AWSCredentials{AccessKeyID: exampleKeyID, SecretAccessKey: exampleSecret},
		now:         time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	without, err := signV4(sigV4Request{
		method: "POST", url: "https://secretsmanager.eu-west-1.amazonaws.com",
		body: []byte(`{}`), region: "eu-west-1", service: "secretsmanager",
		credentials: AWSCredentials{AccessKeyID: exampleKeyID, SecretAccessKey: exampleSecret},
		now:         time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, signatureOf(t, withSlash), signatureOf(t, without))
}

func mustSign(t *testing.T, body []byte) map[string]string {
	t.Helper()
	signed, err := signV4(sigV4Request{
		method: "POST", url: "https://secretsmanager.eu-west-1.amazonaws.com/",
		body: body, region: "eu-west-1", service: "secretsmanager",
		credentials: AWSCredentials{AccessKeyID: exampleKeyID, SecretAccessKey: exampleSecret},
		now:         time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	return signed
}

func signatureOf(t *testing.T, headers map[string]string) string {
	t.Helper()
	_, after, found := strings.Cut(headers["Authorization"], "Signature=")
	require.True(t, found, "no signature in %q", headers["Authorization"])
	return after
}

// ---------------------------------------------------------------------------

func TestAWSRefusesToBeBuiltWithoutARegion(t *testing.T) {
	_, err := NewAWSSecretsManager(AWSConfig{Getenv: func(string) string { return "" }})
	require.ErrorIs(t, err, ErrNotConfigured)
	require.Contains(t, err.Error(), "region")
	require.Contains(t, err.Error(), "eu-west-1",
		"the message should say why a region is not optional")
}

func TestAWSSaysWhichPlacesItLookedForCredentials(t *testing.T) {
	// The message somebody reads when nothing answered. "No credentials" on its
	// own leaves them guessing which of four mechanisms was supposed to supply
	// them, and the answer is usually that the one they configured, a profile
	// or a web identity token file, is not one this source reads.
	source, err := NewAWSSecretsManager(AWSConfig{
		Region: "eu-west-1",
		// An environment with nothing in it, and no metadata service to reach,
		// which is what a laptop looks like.
		Getenv: func(string) string { return "" },
	})
	require.NoError(t, err)

	ok, why := source.Available(withFeatures(t.Context(), "enterprise_secrets"))
	require.False(t, ok)
	require.Contains(t, why, "AWS_ACCESS_KEY_ID")
	require.Contains(t, why, "~/.aws/credentials")
	t.Logf("reports: %s (%s)", source.Name(), why)
}

func TestAWSHalfSuppliedCredentialsAreNamedRatherThanIgnored(t *testing.T) {
	// A key id with no secret is a mistake somebody made, not an absence. Left
	// to fall through it would look identical to having configured nothing.
	source, err := NewAWSSecretsManager(AWSConfig{
		Region: "eu-west-1",
		Getenv: func(name string) string {
			if name == "AWS_ACCESS_KEY_ID" {
				return exampleKeyID
			}
			return ""
		},
	})
	require.NoError(t, err)
	ok, why := source.Available(withFeatures(t.Context(), "enterprise_secrets"))
	require.False(t, ok)
	require.Contains(t, why, "AWS_SECRET_ACCESS_KEY")
	require.NotContains(t, why, exampleKeyID, "a message must not quote a key id")
}

func TestAWSNamesTheSecretOrThePrefixSoTwoSourcesAreTellableApart(t *testing.T) {
	// The name appears in AF-SEC-001 next to every other source, and two AWS
	// sources for two accounts have to be distinguishable in that list.
	one, err := NewAWSSecretsManager(AWSConfig{
		Region: "eu-west-1", SecretID: "antifailure/production",
		Getenv: func(string) string { return "" },
	})
	require.NoError(t, err)
	two, err := NewAWSSecretsManager(AWSConfig{
		Region: "us-east-1", Prefix: "antifailure/staging/",
		Getenv: func(string) string { return "" },
	})
	require.NoError(t, err)
	require.NotEqual(t, one.Name(), two.Name())
	require.Contains(t, one.Name(), "antifailure/production")
	require.Contains(t, two.Name(), "us-east-1")
}
