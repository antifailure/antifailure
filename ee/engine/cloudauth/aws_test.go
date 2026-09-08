// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package cloudauth

// The signing, against the example AWS publishes.
//
// This is the part of the AWS credential path that is worth proving without an
// AWS account, and it is also the part most likely to be wrong: a signature is
// either exactly right or it is a 403 that reads like a bad secret key. AWS
// publishes a worked example with the inputs, the intermediate strings and the
// final signature, precisely so an implementation can be checked against
// something other than itself. Checking against our own idea of the algorithm
// would prove only that the code agrees with the code.
//
// It matters more here than it did in ee/engine/secrets, where this used to
// live. One store signed with it there. Every managed database provider and
// every runtime that reaches an AWS API signs with it now, so a regression
// would be one commit breaking all of them at once, in a way that reads to each
// of them as somebody else's credentials being wrong.

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
	signed, err := SignV4(SigV4Request{
		Method: "GET",
		URL:    "https://examplebucket.s3.amazonaws.com/test.txt",
		Body:   nil,
		Headers: map[string]string{
			"Range": "bytes=0-9",
		},
		Region:  "us-east-1",
		Service: "s3",
		Credentials: AWSCredentials{
			AccessKeyID: exampleKeyID, SecretAccessKey: exampleSecret,
		},
		Now: time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC),
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
	// credential the chain finds except a long-lived user key is temporary, so
	// this is the common case and not the exotic one.
	with, err := SignV4(SigV4Request{
		Method: "POST", URL: "https://secretsmanager.eu-west-1.amazonaws.com/",
		Body: []byte(`{"SecretId":"x"}`), Region: "eu-west-1", Service: "secretsmanager",
		Credentials: AWSCredentials{
			AccessKeyID: exampleKeyID, SecretAccessKey: exampleSecret,
			SessionToken: "a-temporary-token",
		},
		Now: time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Contains(t, with["Authorization"], "x-amz-security-token",
		"the session token is not in the signed header list")
	require.Equal(t, "a-temporary-token", with["X-Amz-Security-Token"])

	// And the signature actually differs, rather than the header merely being
	// listed. Listing a header and not hashing it is the same bug wearing a
	// disguise.
	without, err := SignV4(SigV4Request{
		Method: "POST", URL: "https://secretsmanager.eu-west-1.amazonaws.com/",
		Body: []byte(`{"SecretId":"x"}`), Region: "eu-west-1", Service: "secretsmanager",
		Credentials: AWSCredentials{AccessKeyID: exampleKeyID, SecretAccessKey: exampleSecret},
		Now:         time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC),
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
	withSlash, err := SignV4(SigV4Request{
		Method: "POST", URL: "https://secretsmanager.eu-west-1.amazonaws.com/",
		Body: []byte(`{}`), Region: "eu-west-1", Service: "secretsmanager",
		Credentials: AWSCredentials{AccessKeyID: exampleKeyID, SecretAccessKey: exampleSecret},
		Now:         time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	without, err := SignV4(SigV4Request{
		Method: "POST", URL: "https://secretsmanager.eu-west-1.amazonaws.com",
		Body: []byte(`{}`), Region: "eu-west-1", Service: "secretsmanager",
		Credentials: AWSCredentials{AccessKeyID: exampleKeyID, SecretAccessKey: exampleSecret},
		Now:         time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, signatureOf(t, withSlash), signatureOf(t, without))
}

func mustSign(t *testing.T, body []byte) map[string]string {
	t.Helper()
	signed, err := SignV4(SigV4Request{
		Method: "POST", URL: "https://secretsmanager.eu-west-1.amazonaws.com/",
		Body: body, Region: "eu-west-1", Service: "secretsmanager",
		Credentials: AWSCredentials{AccessKeyID: exampleKeyID, SecretAccessKey: exampleSecret},
		Now:         time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC),
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

func TestAWSChainNamesEveryPlaceItLookedForCredentials(t *testing.T) {
	// The message somebody reads when nothing answered. "No credentials" on its
	// own leaves them guessing which of four mechanisms was supposed to supply
	// them, and the answer is usually that the one they configured, a profile
	// or a web identity token file, is not one this chain reads.
	//
	// An environment with nothing in it and no metadata service to reach, which
	// is what a laptop looks like.
	chain := NewAWSChain(func(string) string { return "" }, nil)
	_, err := chain.Credentials(t.Context())
	require.ErrorIs(t, err, ErrNotConfigured)
	require.Contains(t, err.Error(), "AWS_ACCESS_KEY_ID")
	require.Contains(t, err.Error(), "~/.aws/credentials")
}

func TestAWSChainHoldsSuppliedCredentialsWithoutWalkingTheChain(t *testing.T) {
	// Supplied credentials are used as they are. Walking the chain anyway would
	// mean an explicit key in a manifest silently losing to whatever the
	// environment happened to export.
	supplied := &AWSCredentials{
		AccessKeyID: exampleKeyID, SecretAccessKey: exampleSecret, Source: "the manifest",
	}
	chain := NewAWSChain(func(string) string {
		t.Fatal("the environment was read even though credentials were supplied")
		return ""
	}, supplied)
	got, err := chain.Credentials(t.Context())
	require.NoError(t, err)
	require.Equal(t, "the manifest", got.Source)
}

func TestAWSChainReadsTheEnvironmentBeforeAnyEndpoint(t *testing.T) {
	// Order matters and it is AWS's own: an exported key wins over a container
	// endpoint, so somebody debugging against another account by exporting keys
	// is not silently signed as the task's role.
	chain := NewAWSChain(func(name string) string {
		switch name {
		case "AWS_ACCESS_KEY_ID":
			return exampleKeyID
		case "AWS_SECRET_ACCESS_KEY":
			return exampleSecret
		case "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI":
			return "/v2/credentials/should-not-be-read"
		}
		return ""
	}, nil)
	got, err := chain.Credentials(t.Context())
	require.NoError(t, err)
	require.Equal(t, "the environment", got.Source)
	require.Equal(t, exampleKeyID, got.AccessKeyID)
}

func TestAWSChainNamesAHalfSuppliedEnvironmentRatherThanIgnoringIt(t *testing.T) {
	// A key id with no secret is a mistake somebody made, not an absence. Left
	// to fall through it would look identical to having configured nothing.
	chain := NewAWSChain(func(name string) string {
		if name == "AWS_ACCESS_KEY_ID" {
			return exampleKeyID
		}
		return ""
	}, nil)
	_, err := chain.Credentials(t.Context())
	require.ErrorIs(t, err, ErrNotConfigured)
	require.Contains(t, err.Error(), "AWS_SECRET_ACCESS_KEY")
	require.NotContains(t, err.Error(), exampleKeyID, "a message must not quote a key id")
}
