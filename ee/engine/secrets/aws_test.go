// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package secrets

// What this adapter does without an AWS account.
//
// The signing lives in ee/engine/cloudauth now, and so does the proof of it:
// the canonical example AWS publishes is checked there, against the service's
// own published answer rather than against our own idea of the algorithm. What
// is left here is the part that belongs to the store, which is what it says
// when it cannot be built and what it says when no credentials answered.
//
// Everything else in this adapter needs a real account and is marked `written`
// rather than `proven` in STATUS.md until it has one.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// The key id below is AWS's own published example value. It is documented as an
// example, it authenticates nothing, and a message that quoted a real one would
// be the failure the assertion beneath it exists to catch.
const exampleKeyID = "AKIA" + "IOSFODNN7EXAMPLE"

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

// ---------------------------------------------------------------------------
// The conformance suite, against the documented wire format
// ---------------------------------------------------------------------------

// The secret key below is AWS's own published example value, from the same
// worked example as exampleKeyID. It signs nothing that exists.
const exampleSecretKey = "wJalrXUtnFEMI/K7MDENG" + "/bPxRfiCYEXAMPLEKEY"

// AWS had never been run through the conformance suite at all.
//
// Vault, Key Vault and Secret Manager each had a run; this store had four unit
// tests about what it says when it cannot be built, and nothing that put it
// into the states the contract is about. That is why its Reach could report an
// unreachable store as usable for as long as it did: the behaviour named "is
// unavailable with a reason when the store cannot be reached" exists, and no
// harness had ever pointed it at this adapter.
//
// As with Key Vault and Secret Manager, this is not a proof that AWS accepts
// these requests. It is a proof that the adapter treats each documented answer
// the way the contract requires. The live half is aws_live_test.go.
func TestAWSConformanceAgainstTheDocumentedWireFormat(t *testing.T) {
	server := fakeSecretsManager(t)

	// Through the environment rather than through AWSConfig.Credentials,
	// because credentials supplied directly are declared unrenewable and the
	// refresh behaviour would then skip. The refresh rule is one of the two
	// things this suite is really here for.
	env := func(name string) string {
		switch name {
		case "AWS_ACCESS_KEY_ID":
			return exampleKeyID
		case "AWS_SECRET_ACCESS_KEY":
			return exampleSecretKey
		}
		return ""
	}

	working, err := NewAWSSecretsManager(AWSConfig{
		Region: "eu-west-1", Endpoint: server.URL + "/ok/", Getenv: env})
	require.NoError(t, err)

	denied, err := NewAWSSecretsManager(AWSConfig{
		Region: "eu-west-1", Endpoint: server.URL + "/denied/", Getenv: env})
	require.NoError(t, err)
	rejecting := &countingAWS{AWSBackend: denied.backend.(*AWSBackend)}

	// A dead address with credentials that resolve without a network call, so
	// the store is unreachable and the credential is not. Pointing the whole
	// fake at a dead address, which is what the other two conformance runs do,
	// would break the credential and the store together and could not tell
	// which one this behaviour is about.
	unreachable, err := NewAWSSecretsManager(AWSConfig{
		Region: "eu-west-1", Endpoint: "http://127.0.0.1:1/", Getenv: env})
	require.NoError(t, err)

	result := Run(t.Context(), t, Harness{
		Name:         "AWS Secrets Manager (documented wire format, not a live account)",
		Working:      working,
		Present:      "DATABASE_URL",
		PresentValue: "postgres://aws",
		Empty:        "BLANK",
		Absent:       "NOT_IN_THE_ACCOUNT",
		Rejecting:    New(rejecting),
		Refreshes:    rejecting.count,
		Unreachable:  unreachable,
	})
	require.Empty(t, result.Failed)
	require.Empty(t, result.Skipped)
}

type countingAWS struct {
	*AWSBackend
	renewals int
}

func (c *countingAWS) Refresh(ctx context.Context) error {
	c.renewals++
	return c.AWSBackend.Refresh(ctx)
}
func (c *countingAWS) count() int { return c.renewals }

// awsListProbe is the key the fake files Reach's unsigned probe under.
const awsListProbe = "secretsmanager.ListSecrets"

func fakeSecretsManager(t *testing.T) *fakeServer {
	t.Helper()
	values := map[string]string{"DATABASE_URL": "postgres://aws", "BLANK": ""}
	fake := newFakeServer()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := r.Header.Get("X-Amz-Target")
		denied := strings.HasPrefix(r.URL.Path, "/denied")

		// Reach's probe. Unsigned, which the real service answers with
		// MissingAuthenticationTokenException, and any answer at all is the
		// proof being sought: what is in question is whether the host is there.
		if target == awsListProbe {
			fake.record(awsListProbe, r.Header)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"__type":"MissingAuthenticationTokenException"}`))
			return
		}

		if target != "secretsmanager.GetSecretValue" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"__type":"UnknownOperationException"}`))
			return
		}

		// The lookup is signed, and the fake insists on it. Without this the
		// suite would pass against an adapter that stopped signing, which is
		// the one thing about the request this can still check offline.
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"__type":"MissingAuthenticationTokenException"}`))
			return
		}

		if denied {
			// AWS reports a refused credential as a 400 carrying a type rather
			// than as a 403, which is why the adapter reads the type at all.
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"__type":"AccessDeniedException"}`))
			return
		}

		var payload struct {
			SecretID string `json:"SecretId"`
		}
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"__type":"InvalidRequestException"}`))
			return
		}
		value, found := values[payload.SecretID]
		if !found {
			// A 400 with a type, not a 404. An adapter that treated every 400
			// as a miss would swallow a malformed request, and one that treated
			// this as a failure would make every variable it does not hold
			// fatal.
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"__type":"ResourceNotFoundException"}`))
			return
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		_ = json.NewEncoder(w).Encode(map[string]string{"SecretString": value})
	}))
	t.Cleanup(server.Close)
	fake.URL = server.URL
	return fake
}

// ---------------------------------------------------------------------------
// A fake that remembers what it was sent
// ---------------------------------------------------------------------------

// fakeServer is an httptest server that keeps the headers of the requests it
// answered, so a test can assert on what actually went on the wire.
//
// It carries a URL field rather than embedding *httptest.Server so that the
// only thing a harness can reach for is the address. A test that reached into
// the server could assert on the fake instead of on the adapter.
type fakeServer struct {
	URL string

	mu   sync.Mutex
	seen map[string]http.Header
}

func newFakeServer() *fakeServer {
	return &fakeServer{seen: map[string]http.Header{}}
}

func (f *fakeServer) record(key string, headers http.Header) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen[key] = headers.Clone()
}

// probeHeaders returns the headers of the request filed under key, and fails
// when there is none.
//
// The absence is a failure rather than an empty header set on purpose. A test
// asserting that a probe carried no Authorization header would otherwise pass
// against an adapter that sent no probe at all, which is the exact defect this
// file was written for.
func probeHeaders(t *testing.T, server *fakeServer, key string) http.Header {
	t.Helper()
	server.mu.Lock()
	defer server.mu.Unlock()
	headers, ok := server.seen[key]
	require.Truef(t, ok, "no %s request reached the store, so Reach never probed it", key)
	return headers
}
