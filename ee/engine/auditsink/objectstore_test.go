// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package auditsink

// The object store sink: the key, the signature, and the refusal to replace.
//
// The signature is checked by recomputing it from the request the server
// received, with the same secret and an independent implementation of the
// canonical request. Asserting only that an Authorization header is present
// would pass for a signature over the wrong bytes, which is the failure that
// looks like working code until the first real PUT is rejected.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// store is an object store that keeps what it was PUT.
type store struct {
	*httptest.Server
	mu      sync.Mutex
	keys    []string
	bodies  [][]byte
	headers []http.Header
	hosts   []string
	// existing are keys that are already there, answered with 412 the way a
	// store answers If-None-Match on an object that exists.
	existing map[string]bool
}

func newStore(t *testing.T) *store {
	t.Helper()
	s := &store{existing: map[string]bool{}}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		s.mu.Lock()
		key := strings.TrimPrefix(req.URL.Path, "/")
		taken := s.existing[key]
		if !taken {
			s.keys = append(s.keys, key)
			s.bodies = append(s.bodies, body)
			s.headers = append(s.headers, req.Header.Clone())
			s.hosts = append(s.hosts, req.Host)
		}
		s.mu.Unlock()
		if taken {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *store) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.keys)
}

func (s *store) last() (string, []byte, http.Header, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := len(s.keys) - 1
	return s.keys[i], s.bodies[i], s.headers[i], s.hosts[i]
}

const (
	testAccessKey = "AKIAIOSFODNN7EXAMPLE"
	testSecretKey = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
)

func awsEnv(extra map[string]string) func(string) string {
	m := map[string]string{
		AWSAccessKeyIDEnv:     testAccessKey,
		AWSSecretAccessKeyEnv: testSecretKey,
		AWSRegionEnv:          "eu-west-1",
	}
	for k, v := range extra {
		m[k] = v
	}
	return func(name string) string { return m[name] }
}

// newObjectStore builds a sink writing into a test store, with a fixed key.
func newObjectStore(t *testing.T, s *store, bucketPath string) *ObjectStore {
	t.Helper()
	o, err := NewObjectStore(ObjectStoreConfig{
		URL:    s.URL + bucketPath,
		Getenv: awsEnv(nil),
		Client: s.Client(),
		Now:    at(forwarded),
		suffix: func() string { return "cafebabe" },
	})
	require.NoError(t, err)
	return o
}

// ---------------------------------------------------------------------------
// Where an entry lands
// ---------------------------------------------------------------------------

func TestTheKeyIsPartitionedByDateAndNamesTheAction(t *testing.T) {
	t.Parallel()
	// The date is a path rather than part of the filename so a bucket
	// lifecycle rule, an object lock policy and a partitioned query all work
	// on it without anybody writing a parser. The action is in the name
	// because the most common question asked of an archive like this is "show
	// me every golden.published", and a prefix listing answers it without
	// reading a single object.
	s := newStore(t)
	o := newObjectStore(t, s, "/acme-audit/engine")

	require.NoError(t, o.Write(licensed(t), entry()))
	key, _, _, _ := s.last()
	require.Equal(t,
		"acme-audit/engine/2026/09/07/112233.456789000-golden.published-cafebabe.json", key)
}

func TestTheKeyIsTakenFromWhenTheActionHappenedNotWhenItWasForwarded(t *testing.T) {
	t.Parallel()
	// A retry can put the forward minutes after the action, and an archive
	// partitioned by the forwarding time files an entry under a day the thing
	// did not happen on, which is exactly the query a retention audit runs.
	s := newStore(t)
	o := newObjectStore(t, s, "/acme-audit")

	e := entry()
	e.OccurredAt = time.Date(2026, 9, 6, 23, 59, 59, 0, time.UTC)
	require.NoError(t, o.Write(licensed(t), e))

	key, _, _, _ := s.last()
	require.True(t, strings.HasPrefix(key, "acme-audit/2026/09/06/235959"),
		"the object was filed under the forwarding time rather than the action's: %s", key)
}

func TestAnActionThatIsNotALegalKeyDoesNotBecomeAPathOfItsOwn(t *testing.T) {
	t.Parallel()
	// An action reaches this from the engine, but the field is a string and a
	// slash in it would turn one entry into a directory somewhere else in the
	// bucket, outside the prefix a retention policy is attached to.
	s := newStore(t)
	o := newObjectStore(t, s, "/acme-audit")

	e := entry()
	e.Action = "../../escaped/action"
	require.NoError(t, o.Write(licensed(t), e))

	key, _, _, _ := s.last()
	require.NotContains(t, strings.TrimPrefix(key, "acme-audit/2026/09/07/"), "/",
		"an action with a slash in it wrote outside its own partition: %s", key)
	// The dots survive as ordinary characters and that is correct: what makes
	// a traversal is the separator, and with none the whole action is one file
	// name. Cleaning the key has to be a no-op, which is the property rather
	// than the absence of a dot.
	require.Equal(t, key, path.Clean(key), "the key resolves somewhere other than where it says")
	require.True(t, strings.HasPrefix(key, "acme-audit/2026/09/07/"),
		"the entry left the prefix a retention policy is attached to: %s", key)
}

func TestTheStoredObjectIsTheSameBytesEveryOtherSinkWrites(t *testing.T) {
	t.Parallel()
	// So that an entry read out of a bucket and an entry read out of a SIEM
	// are the same document and a query written against one works against the
	// other.
	s := newStore(t)
	o := newObjectStore(t, s, "/acme-audit")
	require.NoError(t, o.Write(licensed(t), entry()))

	wanted, err := encode(entry(), forwarded)
	require.NoError(t, err)
	_, body, headers, _ := s.last()
	require.Equal(t, string(wanted), string(body))
	require.Equal(t, "application/json", headers.Get("Content-Type"))
}

func TestAnEntryNeverReplacesOneThatIsAlreadyThere(t *testing.T) {
	t.Parallel()
	// An audit archive whose writer overwrites is an audit archive somebody
	// can edit by replaying. Two entries that collide is a defect in the key
	// rather than a thing to resolve by losing one.
	s := newStore(t)
	o := newObjectStore(t, s, "/acme-audit")
	require.NoError(t, o.Write(licensed(t), entry()))

	key, _, headers, _ := s.last()
	require.Equal(t, "*", headers.Get("If-None-Match"))

	s.mu.Lock()
	s.existing[key] = true
	s.mu.Unlock()

	err := o.Write(licensed(t), entry())
	require.Error(t, err, "an entry silently replaced one that was already in the archive")
	require.ErrorContains(t, err, "412")
	require.Equal(t, 1, s.count())
}

func TestTwoEntriesInTheSameNanosecondAreTwoObjects(t *testing.T) {
	t.Parallel()
	// Without the random suffix the second silently replaces the first, which
	// loses exactly the entries that arrived together, which is exactly the
	// entries somebody is investigating.
	s := newStore(t)
	o, err := NewObjectStore(ObjectStoreConfig{
		URL: s.URL + "/acme-audit", Getenv: awsEnv(nil), Client: s.Client(), Now: at(forwarded),
	})
	require.NoError(t, err)

	require.NoError(t, o.Write(licensed(t), entry()))
	first, _, _, _ := s.last()
	require.NoError(t, o.Write(licensed(t), entry()))
	second, _, _, _ := s.last()

	require.NotEqual(t, first, second,
		"two entries with the same action at the same instant collided on one key")
	require.Equal(t, 2, s.count())
}

// ---------------------------------------------------------------------------
// The signature, recomputed rather than merely present
// ---------------------------------------------------------------------------

func TestTheSignatureIsOverTheRequestTheStoreActuallyReceived(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	o := newObjectStore(t, s, "/acme-audit/engine")
	require.NoError(t, o.Write(licensed(t), entry()))

	key, body, headers, host := s.last()
	auth := headers.Get("Authorization")
	require.NotEmpty(t, auth, "the request was not signed at all")

	// The payload hash is a real SHA-256 of the body rather than
	// UNSIGNED-PAYLOAD, which is the first of the four places a Signature
	// Version 4 implementation goes wrong.
	sum := sha256.Sum256(body)
	require.Equal(t, hex.EncodeToString(sum[:]), headers.Get("x-amz-content-sha256"))

	credential, signedHeaders, signature := parseAuthorization(t, auth)
	amzDate := headers.Get("x-amz-date")
	dateOnly := amzDate[:8]
	require.Equal(t, testAccessKey+"/"+dateOnly+"/eu-west-1/s3/aws4_request", credential,
		"the credential scope does not pin the date, the region and the service")

	// host and every x-amz- are signed, which is the second place it goes
	// wrong: a signature over a header set that omits x-amz-content-sha256 is
	// a signature a store rejects.
	require.Contains(t, strings.Split(signedHeaders, ";"), "host")
	require.Contains(t, strings.Split(signedHeaders, ";"), "x-amz-content-sha256")
	require.Contains(t, strings.Split(signedHeaders, ";"), "x-amz-date")

	canonicalHeaders := ""
	for _, name := range strings.Split(signedHeaders, ";") {
		value := headers.Get(name)
		if name == "host" {
			value = host
		}
		canonicalHeaders += name + ":" + value + "\n"
	}
	canonicalRequest := strings.Join([]string{
		http.MethodPut,
		"/" + key,
		"",
		canonicalHeaders,
		signedHeaders,
		hex.EncodeToString(sum[:]),
	}, "\n")

	crSum := sha256.Sum256([]byte(canonicalRequest))
	scope := strings.Join([]string{dateOnly, "eu-west-1", "s3", "aws4_request"}, "/")
	toSign := strings.Join([]string{
		"AWS4-HMAC-SHA256", amzDate, scope, hex.EncodeToString(crSum[:]),
	}, "\n")

	sign := func(key []byte, data string) []byte {
		m := hmac.New(sha256.New, key)
		m.Write([]byte(data))
		return m.Sum(nil)
	}
	derived := sign([]byte("AWS4"+testSecretKey), dateOnly)
	derived = sign(derived, "eu-west-1")
	derived = sign(derived, "s3")
	derived = sign(derived, "aws4_request")

	require.Equal(t, hex.EncodeToString(sign(derived, toSign)), signature,
		"the signature does not match the request the store received")
}

func TestASessionTokenIsSignedRatherThanSentBeside(t *testing.T) {
	t.Parallel()
	// A temporary credential without its token signed is rejected, and the
	// rejection reads as a bad secret, which sends whoever is debugging it to
	// the wrong place.
	s := newStore(t)
	o, err := NewObjectStore(ObjectStoreConfig{
		URL:    s.URL + "/acme-audit",
		Getenv: awsEnv(map[string]string{AWSSessionTokenEnv: "FwoGZXIvYXdzEExample"}),
		Client: s.Client(), Now: at(forwarded), suffix: func() string { return "cafebabe" },
	})
	require.NoError(t, err)
	require.NoError(t, o.Write(licensed(t), entry()))

	_, _, headers, _ := s.last()
	require.Equal(t, "FwoGZXIvYXdzEExample", headers.Get("x-amz-security-token"))
	_, signed, _ := parseAuthorization(t, headers.Get("Authorization"))
	require.Contains(t, strings.Split(signed, ";"), "x-amz-security-token",
		"a temporary credential's token was sent unsigned")
}

func TestTheEscapingIsAWSsRatherThanTheQueryEscaping(t *testing.T) {
	t.Parallel()
	// url.QueryEscape writes a space as + and leaves alone characters this has
	// to encode. The difference is invisible until a key with a space in it
	// fails to sign.
	require.Equal(t, "a%20b", awsEscape("a b"))
	require.Equal(t, "a%2Bb", awsEscape("a+b"))
	require.Equal(t, "-_.~", awsEscape("-_.~"))
	require.Equal(t, "%2F", awsEscape("/"))
}

// parseAuthorization pulls the three fields out of a Signature Version 4
// header, failing rather than returning silence when the shape is wrong.
func parseAuthorization(t *testing.T, auth string) (credential, signedHeaders, signature string) {
	t.Helper()
	require.True(t, strings.HasPrefix(auth, "AWS4-HMAC-SHA256 "), "unexpected scheme: %s", auth)
	for _, part := range strings.Split(strings.TrimPrefix(auth, "AWS4-HMAC-SHA256 "), ", ") {
		name, value, found := strings.Cut(part, "=")
		require.True(t, found, "unparsable field %q", part)
		switch name {
		case "Credential":
			credential = value
		case "SignedHeaders":
			signedHeaders = value
		case "Signature":
			signature = value
		}
	}
	require.NotEmpty(t, credential)
	require.NotEmpty(t, signedHeaders)
	require.NotEmpty(t, signature)
	return credential, signedHeaders, signature
}

// ---------------------------------------------------------------------------
// Azure Blob
// ---------------------------------------------------------------------------

func TestAnAzureContainerIsRecognisedByItsHostAndNeedsASignature(t *testing.T) {
	t.Parallel()
	// An Azure container URL and a self hosted S3 URL are both https and there
	// is otherwise no way to tell them apart.
	_, err := NewObjectStore(ObjectStoreConfig{
		URL: "https://acme.blob.core.windows.net/audit", Getenv: awsEnv(nil),
	})
	require.Error(t, err, "an Azure container with no signature was accepted")
	require.ErrorContains(t, err, "shared access signature")

	o, err := NewObjectStore(ObjectStoreConfig{
		URL:    "https://acme.blob.core.windows.net/audit?sv=2021-08-06&sig=notarealsignature",
		Getenv: func(string) string { return "" },
	})
	require.NoError(t, err, "an Azure container sink required AWS credentials it does not use")
	require.Equal(t, "the Azure Blob container https://acme.blob.core.windows.net/audit", o.Name())
	require.NotContains(t, o.Name(), "notarealsignature",
		"the shared access signature is a credential and it is printed at startup")
}

func TestAnAzureBlobIsWrittenAsABlockBlobThatCannotReplaceOne(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	// The test server is not a real Azure host, so the sink is built by hand
	// rather than through the host suffix, which is what isAzureBlob is for.
	base, err := url.Parse(s.URL + "/audit")
	require.NoError(t, err)
	o := &ObjectStore{
		kind: "azure_blob", client: s.Client(), now: at(forwarded),
		suffix: func() string { return "cafebabe" },
		base:   base, query: "sv=2021-08-06&sig=x", label: s.URL + "/audit",
	}

	require.NoError(t, o.Write(licensed(t), entry()))
	key, body, headers, _ := s.last()
	require.Equal(t, "audit/2026/09/07/112233.456789000-golden.published-cafebabe.json", key)
	require.Equal(t, "BlockBlob", headers.Get("x-ms-blob-type"))
	require.Equal(t, azureAPIVersion, headers.Get("x-ms-version"))
	require.Equal(t, "*", headers.Get("If-None-Match"))

	wanted, err := encode(entry(), forwarded)
	require.NoError(t, err)
	require.Equal(t, string(wanted), string(body))
}

// ---------------------------------------------------------------------------
// Refused at construction, and named without a credential
// ---------------------------------------------------------------------------

func TestAnObjectStoreWithNoCredentialIsRefusedRatherThanFailingPerEntry(t *testing.T) {
	t.Parallel()
	_, err := NewObjectStore(ObjectStoreConfig{
		URL: "s3://acme-audit/engine", Getenv: func(string) string { return "" },
	})
	require.Error(t, err)
	require.ErrorContains(t, err, AWSAccessKeyIDEnv)
	require.ErrorContains(t, err, AWSSecretAccessKeyEnv)
}

func TestAnObjectStoreURLThisSinkCannotSpeakNamesTheOnesItCan(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"gs://acme-audit", "file:///var/audit", ""} {
		t.Run(raw, func(t *testing.T) {
			_, err := NewObjectStore(ObjectStoreConfig{URL: raw, Getenv: awsEnv(nil)})
			require.Error(t, err)
		})
	}

	_, err := NewObjectStore(ObjectStoreConfig{
		URL: "https://minio.acme.example", Getenv: awsEnv(nil),
	})
	require.Error(t, err, "a URL with no bucket was accepted")
	require.ErrorContains(t, err, "https://<host>/<bucket>")
}

func TestAnS3BucketIsAddressedByTheRegionalEndpoint(t *testing.T) {
	t.Parallel()
	o, err := NewObjectStore(ObjectStoreConfig{
		URL: "s3://acme-audit/engine", Getenv: awsEnv(nil),
	})
	require.NoError(t, err)
	require.Equal(t, "the bucket s3://acme-audit/engine", o.Name())
	require.Equal(t, "eu-west-1", o.region)
	require.Equal(t, "s3.eu-west-1.amazonaws.com", o.endpoint.Host)
	require.False(t, o.pathStyle)
}

func TestASelfHostedStoreSaysWhichServerItIs(t *testing.T) {
	t.Parallel()
	// Two self hosted stores in one fleet are frequently the same bucket name
	// on different hosts, and the name is what an operator reads when a
	// forward fails.
	o, err := NewObjectStore(ObjectStoreConfig{
		URL: "https://minio.acme.example/audit/engine", Getenv: awsEnv(nil),
	})
	require.NoError(t, err)
	require.Equal(t, "the bucket https://minio.acme.example/audit/engine", o.Name())
	require.True(t, o.pathStyle)
}

func TestAPresignedURLIsNotPrintedBackWithItsCredential(t *testing.T) {
	t.Parallel()
	// Every message in the object store file that names the destination goes
	// through redact, because both shapes it takes can carry a credential in
	// the query.
	require.Equal(t, "https://acme.blob.core.windows.net/audit",
		redact("https://acme.blob.core.windows.net/audit?sig=secret"))
	require.NotContains(t, redact("https://user:pass@minio.acme.example/audit"), "pass")
}

func TestTheRegionFallsBackInTheOrderTheAWSToolsUse(t *testing.T) {
	t.Parallel()
	only := func(name, value string) func(string) string {
		return func(n string) string {
			if n == name {
				return value
			}
			if n == AWSAccessKeyIDEnv {
				return testAccessKey
			}
			if n == AWSSecretAccessKeyEnv {
				return testSecretKey
			}
			return ""
		}
	}
	for _, tc := range []struct{ env, want string }{
		{AWSRegionEnv, "ap-south-1"},
		{AWSDefaultRegionEnv, "ap-south-1"},
	} {
		o, err := NewObjectStore(ObjectStoreConfig{
			URL: "s3://acme-audit", Getenv: only(tc.env, "ap-south-1"),
		})
		require.NoError(t, err)
		require.Equal(t, tc.want, o.region)
	}

	o, err := NewObjectStore(ObjectStoreConfig{
		URL: "s3://acme-audit", Getenv: only("IRRELEVANT", "x"),
	})
	require.NoError(t, err)
	require.Equal(t, "us-east-1", o.region, "a machine with no region set has no working sink")
}
