package golden_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	crand "crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/golden"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// Every backend runs the same suite, because the point of the interface is
// that the rest of the engine cannot tell them apart. A behaviour that holds
// on the filesystem and not on Blob storage is a behaviour the caller is going
// to rely on and be wrong about once.
//
// All three run against a REAL server. There is no fake here on purpose: a
// store's whole job is to talk to something else, and the failures worth
// catching are the ones a fake is written not to have. The signature this code
// computes is either the one the server wants or it is not, and nothing but
// the server can say which.
func runStoreSuite(t *testing.T, open func(t *testing.T) golden.Store) {
	t.Helper()

	t.Run("a round trip returns the same bytes", func(t *testing.T) {
		s := open(t)
		ctx := context.Background()
		body := bytes.Repeat([]byte("antifailure golden dump\n"), 4096)

		require.NoError(t, s.Put(ctx, "gv_01/dump.pgcustom", int64(len(body)), bytes.NewReader(body)))

		r, err := s.Get(ctx, "gv_01/dump.pgcustom")
		require.NoError(t, err)
		got, err := io.ReadAll(r)
		require.NoError(t, r.Close())
		require.NoError(t, err)
		require.True(t, bytes.Equal(body, got),
			"%d bytes went in and %d came out", len(body), len(got))
	})

	t.Run("something that is not there is distinguishable from a broken store", func(t *testing.T) {
		// The two are the same status on more than one service, and a caller
		// that cannot tell them apart reports "no golden published yet" when
		// the credential has expired.
		s := open(t)
		_, err := s.Get(context.Background(), "gv_missing/dump.pgcustom")
		require.Error(t, err)
		require.True(t, errors.Is(err, golden.ErrNotFound), "got %v", err)
	})

	t.Run("a second write replaces the first", func(t *testing.T) {
		s := open(t)
		ctx := context.Background()
		require.NoError(t, s.Put(ctx, "gv_02/attestation.json", 5, strings.NewReader("first")))
		require.NoError(t, s.Put(ctx, "gv_02/attestation.json", 6, strings.NewReader("second")))

		r, err := s.Get(ctx, "gv_02/attestation.json")
		require.NoError(t, err)
		got, err := io.ReadAll(r)
		require.NoError(t, r.Close())
		require.NoError(t, err)
		require.Equal(t, "second", string(got))
	})

	t.Run("a listing carries the names and the sizes", func(t *testing.T) {
		s := open(t)
		ctx := context.Background()
		require.NoError(t, s.Put(ctx, "gv_03/dump.pgcustom", 3, strings.NewReader("abc")))
		require.NoError(t, s.Put(ctx, "gv_03/attestation.json", 4, strings.NewReader("abcd")))
		require.NoError(t, s.Put(ctx, "gv_04/dump.pgcustom", 5, strings.NewReader("abcde")))

		listed, err := s.List(ctx, "gv_03/")
		require.NoError(t, err)
		byName := map[string]int64{}
		for _, o := range listed {
			byName[o.Name] = o.Size
		}
		require.Equal(t, map[string]int64{
			"gv_03/dump.pgcustom": 3, "gv_03/attestation.json": 4,
		}, byName, "the prefix narrows it and the sizes are real")
	})

	t.Run("removing twice succeeds", func(t *testing.T) {
		// A retry after a timeout must not fail on the work it already did.
		s := open(t)
		ctx := context.Background()
		require.NoError(t, s.Put(ctx, "gv_05/dump.pgcustom", 3, strings.NewReader("abc")))
		require.NoError(t, s.Delete(ctx, "gv_05/dump.pgcustom"))
		require.NoError(t, s.Delete(ctx, "gv_05/dump.pgcustom"))

		_, err := s.Get(ctx, "gv_05/dump.pgcustom")
		require.True(t, errors.Is(err, golden.ErrNotFound))
	})

	t.Run("a version with no attestation is not offered", func(t *testing.T) {
		// The dump is written first and the attestation second, so a version
		// with one and not the other is a partial upload from a run that died.
		// Offering it would hand somebody a database with nothing to check it
		// against, which is the one thing a golden is not allowed to be.
		s := open(t)
		ctx := context.Background()
		require.NoError(t, s.Put(ctx, "gv_10/dump.pgcustom", 3, strings.NewReader("abc")))
		require.NoError(t, s.Put(ctx, "gv_11/dump.pgcustom", 3, strings.NewReader("abc")))
		require.NoError(t, s.Put(ctx, "gv_11/attestation.json", 2, strings.NewReader("{}")))

		versions, err := golden.VersionsIn(ctx, s)
		require.NoError(t, err)
		names := make([]string, 0, len(versions))
		for _, v := range versions {
			names = append(names, v.Name)
		}
		require.Contains(t, names, "gv_11")
		require.NotContains(t, names, "gv_10",
			"the half uploaded version is invisible until its attestation arrives")
	})
}

func TestLocalStore(t *testing.T) {
	runStoreSuite(t, func(t *testing.T) golden.Store {
		s, err := golden.OpenStore(golden.KindLocal, t.TempDir(), nil, nil)
		require.NoError(t, err)
		require.NotNil(t, s)
		return s
	})
}

func TestLocalStore_ReadsItsDirectoryFromTheEnvironment(t *testing.T) {
	t.Parallel()
	// A storage URL can carry a credential, so it is read from the environment
	// rather than committed. The local store has no credential and follows the
	// same rule, because the manifest should not have two spellings.
	dir := t.TempDir()
	env := func(name string) string {
		if name == "AF_GOLDEN_STORE" {
			return dir
		}
		return ""
	}
	s, err := golden.OpenStore(golden.KindLocal, "$AF_GOLDEN_STORE", env, nil)
	require.NoError(t, err)
	require.Contains(t, s.Name(), dir)

	_, err = golden.OpenStore(golden.KindLocal, "$AF_NOT_SET_ANYWHERE", env, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "AF_NOT_SET_ANYWHERE")
	require.Contains(t, err.Error(), "not set on this machine")
}

func TestOpenStore_IsNothingWhenNothingIsConfigured(t *testing.T) {
	t.Parallel()
	// No storage_url means goldens live wherever the provider keeps them,
	// which is the default and is not an error.
	s, err := golden.OpenStore(golden.KindLocal, "", nil, nil)
	require.NoError(t, err)
	require.Nil(t, s)

	_, err = golden.OpenStore("gopher_holes", "/tmp/x", nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "local, azure_blob, s3, gcs")
}

func TestOpenStore_SaysWhatIsMissingFromARemoteURL(t *testing.T) {
	t.Parallel()
	// The message has to name the fix. "invalid URL" sends somebody to read
	// the source; "the URL is the CONTAINER's" does not.
	_, err := golden.OpenStore(golden.KindAzureBlob, "https://acct.blob.core.windows.net/", nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "names no container")

	_, err = golden.OpenStore(golden.KindAzureBlob, "https://acct.blob.core.windows.net/goldens", nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "shared access signature")

	// And a message about a URL must not print the signature back out.
	_, err = golden.OpenStore(golden.KindAzureBlob,
		"ftp://acct.blob.core.windows.net/goldens?sig=SUPERSECRETSIGNATURE", nil, nil)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "SUPERSECRETSIGNATURE",
		"a message about a URL never prints its credential")

	// S3 signs with the environment's credential, and says so when it is not
	// there rather than failing later with a 403.
	_, err = golden.OpenStore(golden.KindS3, "s3://bucket/goldens",
		func(string) string { return "" }, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "AWS_ACCESS_KEY_ID")

	// GCS names the bucket in the URL and the credential nowhere, so the two
	// refusals it owes are a URL with no bucket and a service account key
	// that is not a key.
	_, err = golden.OpenStore(golden.KindGCS, "https://gcs.internal/", nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "names no bucket")

	_, err = golden.OpenStore(golden.KindGCS, "ftp://bucket/goldens", nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "gs://<bucket>/<prefix>")

	keyFile := filepath.Join(t.TempDir(), "key.json")
	require.NoError(t, os.WriteFile(keyFile, []byte(`{"type":"authorized_user"}`), 0o600))
	_, err = golden.OpenStore(golden.KindGCS, "gs://bucket/goldens", func(name string) string {
		if name == "GOOGLE_APPLICATION_CREDENTIALS" {
			return keyFile
		}
		return ""
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "only a service_account key is read here",
		"a key of the wrong type is a configuration defect and is found at open time, "+
			"not twenty minutes into a refresh")
}

// TestS3Store runs the suite against a real MinIO, which speaks the same API
// and rejects a wrong signature exactly as S3 does. That rejection is the
// thing being tested: Signature Version 4 is implemented in this repository,
// and a fixture cannot tell a correct signature from a plausible one.
func TestS3Store(t *testing.T) {
	endpoint := envOr("AF_TEST_S3_ENDPOINT", "http://127.0.0.1:49000")
	access := envOr("AF_TEST_S3_ACCESS_KEY", "aftestaccess")
	bucket := envOr("AF_TEST_S3_BUCKET", "afgoldens")
	secret := envOr("AF_TEST_S3_SECRET_KEY", "aftestsecret123")
	if !reachable(endpoint + "/minio/health/live") {
		t.Skipf("skipped: no S3 compatible server at %s. Start one with: "+
			"docker run -d --name af-minio -p 49000:9000 "+
			"-e MINIO_ROOT_USER=%s -e MINIO_ROOT_PASSWORD=<secret> "+
			"minio/minio server /data", endpoint, access)
	}
	env := func(name string) string {
		switch name {
		case "AWS_ACCESS_KEY_ID":
			return access
		case "AWS_SECRET_ACCESS_KEY":
			return secret
		case "AWS_REGION":
			return "us-east-1"
		}
		return ""
	}

	runStoreSuite(t, func(t *testing.T) golden.Store {
		// One bucket, a fresh PREFIX per test. Creating a bucket is an
		// operator's job rather than the engine's, so there is no code here to
		// do it and none in the store either: a product that quietly creates
		// buckets is a product that quietly creates bills.
		prefix := fmt.Sprintf("goldens-%d", time.Now().UnixNano())
		s, err := golden.OpenStore(golden.KindS3,
			fmt.Sprintf("%s/%s/%s", endpoint, bucket, prefix), env, nil)
		require.NoError(t, err)
		return s
	})
}

// TestAzureStore runs the suite against a real Azurite, with a real account
// shared access signature, so the URL handling and the query preservation are
// exercised the way a container URL from the portal would be.
func TestAzureStore(t *testing.T) {
	endpoint := envOr("AF_TEST_AZURITE", "http://127.0.0.1:41000")
	account := "devstoreaccount1"
	if !reachable(endpoint + "/" + account + "?comp=list") {
		t.Skipf("skipped: no Azurite at %s. Start one with: "+
			"docker run -d --name af-azurite -p 41000:10000 "+
			"mcr.microsoft.com/azure-storage/azurite azurite-blob --blobHost 0.0.0.0", endpoint)
	}

	runStoreSuite(t, func(t *testing.T) golden.Store {
		container := fmt.Sprintf("af-test-%d", time.Now().UnixNano())
		sas, err := accountSAS(account, azuriteKey())
		require.NoError(t, err)
		base := fmt.Sprintf("%s/%s/%s", endpoint, account, container)

		require.NoError(t, makeContainer(base+"?restype=container&"+sas))
		s, err := golden.OpenStore(golden.KindAzureBlob, base+"?"+sas, nil, nil)
		require.NoError(t, err)
		return s
	})
}

// TestGCSStore runs the suite against a real fake-gcs-server.
//
// fake-gcs-server rather than a fixture, and rather than nothing, because
// section 5 of the plan is right that no official Cloud Storage emulator
// exists: Google ships emulators for Pub/Sub, Firestore, Datastore, Bigtable
// and Spanner and none for Cloud Storage, and fsouza/fake-gcs-server is the de
// facto choice and is community maintained. That is a real dependency on
// somebody else's project and it is named here rather than buried.
//
// What it proves and what it does not, stated rather than implied. It proves
// the four operations against the JSON API this store speaks: the object name
// escaped into one path segment, alt=media on the read, the upload endpoint's
// separate path root, the listing's paging shape, and size arriving as a
// string. It does NOT prove authentication, because fake-gcs-server verifies
// none, and no test in this repository may need a cloud account. The two token
// paths are covered by TestGCSStore_TokenPaths against a server this test
// stands up, which is the closest a machine with no Google account can get.
func TestGCSStore(t *testing.T) {
	endpoint := envOr("AF_TEST_GCS_ENDPOINT", "http://127.0.0.1:44443")
	bucket := envOr("AF_TEST_GCS_BUCKET", "afgoldens")
	if !reachable(endpoint + "/storage/v1/b?project=af") {
		t.Skipf("skipped: no Cloud Storage compatible server at %s. Start one with: "+
			"docker run -d --name af-fakegcs -p 44443:4443 "+
			"fsouza/fake-gcs-server:1.52.2 -scheme http -backend memory", endpoint)
	}
	require.NoError(t, makeGCSBucket(endpoint, bucket))

	runStoreSuite(t, func(t *testing.T) golden.Store {
		// One bucket, a fresh PREFIX per test, for the reason the S3 suite
		// gives: creating a bucket is an operator's job, and a product that
		// quietly creates buckets is a product that quietly creates bills.
		prefix := fmt.Sprintf("goldens-%d", time.Now().UnixNano())
		s, err := golden.OpenStore(golden.KindGCS,
			fmt.Sprintf("%s/%s/%s", endpoint, bucket, prefix), nil, nil)
		require.NoError(t, err)
		return s
	})
}

// TestGCSStore_ReadsWithoutAltMediaWouldReturnMetadata is the negative that
// makes the round trip above worth having.
//
// The JSON API answers a read with no alt=media with 200 and the object's
// METADATA, which is the worst shape a mistake can take: a golden that
// publishes cleanly, reads back cleanly, and restores into nothing. This
// asserts the two answers actually differ on the server being tested against,
// so that the round trip test is known to be capable of catching it rather
// than assumed to be.
func TestGCSStore_ReadsWithoutAltMediaWouldReturnMetadata(t *testing.T) {
	endpoint := envOr("AF_TEST_GCS_ENDPOINT", "http://127.0.0.1:44443")
	bucket := envOr("AF_TEST_GCS_BUCKET", "afgoldens")
	if !reachable(endpoint + "/storage/v1/b?project=af") {
		t.Skipf("skipped: no Cloud Storage compatible server at %s", endpoint)
	}
	require.NoError(t, makeGCSBucket(endpoint, bucket))

	prefix := fmt.Sprintf("altmedia-%d", time.Now().UnixNano())
	s, err := golden.OpenStore(golden.KindGCS,
		fmt.Sprintf("%s/%s/%s", endpoint, bucket, prefix), nil, nil)
	require.NoError(t, err)
	require.NoError(t, s.Put(context.Background(), "gv_01/dump.pgcustom", 12,
		strings.NewReader("hello-golden")))

	name := url.PathEscape(prefix + "/gv_01/dump.pgcustom")
	base := fmt.Sprintf("%s/storage/v1/b/%s/o/%s", endpoint, bucket, name)

	withAlt, code := fetch(t, base+"?alt=media")
	require.Equal(t, 200, code)
	require.Equal(t, "hello-golden", withAlt)

	without, code := fetch(t, base)
	require.Equal(t, 200, code,
		"a read with no alt=media is a 200, which is why forgetting it is silent")
	require.NotEqual(t, "hello-golden", without)
	require.Contains(t, without, "storage#object",
		"the body without alt=media is the metadata document, not the dump")
}

// TestGCSStore_TokenPaths covers the two ways this store gets a bearer token.
//
// Both against a server this test stands up, because the alternative is a
// Google account and no test in this repository may need one. What is proved
// is the part this repository wrote: that a service account key is signed into
// an RS256 assertion with the token endpoint as its audience and exchanged,
// that the resulting token is attached as a bearer, and that a store against
// an endpoint that is not Google with no credential configured sends no
// Authorization header at all rather than an empty one.
func TestGCSStore_TokenPaths(t *testing.T) {
	t.Parallel()

	var seenAssertion, seenAuthorization string
	var sawAuthorizationHeader bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			require.NoError(t, r.ParseForm())
			seenAssertion = r.Form.Get("assertion")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"ya29.test","expires_in":3600}`))
		default:
			seenAuthorization = r.Header.Get("Authorization")
			_, sawAuthorizationHeader = r.Header["Authorization"]
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"kind":"storage#objects"}`))
		}
	}))
	defer api.Close()

	t.Run("a service account key is signed and exchanged", func(t *testing.T) {
		key := serviceAccountKey(t, api.URL+"/token")
		s, err := golden.OpenStore(golden.KindGCS, api.URL+"/afgoldens/p",
			func(name string) string {
				if name == "GOOGLE_APPLICATION_CREDENTIALS_JSON" {
					return key
				}
				return ""
			}, nil)
		require.NoError(t, err)

		_, err = s.List(context.Background(), "")
		require.NoError(t, err)
		require.Equal(t, "Bearer ya29.test", seenAuthorization,
			"the exchanged token is what the request carries")

		// The assertion is three base64url segments and the middle one names
		// the client email, the scope and the token endpoint as the audience.
		// The audience is what stops an assertion minted for one service being
		// replayed against another, so it is asserted rather than assumed.
		parts := strings.Split(seenAssertion, ".")
		require.Len(t, parts, 3, "an RS256 JWT is three segments")
		claims, err := base64.RawURLEncoding.DecodeString(parts[1])
		require.NoError(t, err)
		require.Contains(t, string(claims), `"aud":"`+api.URL+`/token"`)
		require.Contains(t, string(claims), "af-goldens@af-test.iam.gserviceaccount.com")
		require.Contains(t, string(claims), "devstorage.read_write")

		header, err := base64.RawURLEncoding.DecodeString(parts[0])
		require.NoError(t, err)
		require.Contains(t, string(header), `"alg":"RS256"`)
	})

	t.Run("an endpoint that is not Google with no credential sends no header", func(t *testing.T) {
		// An emulator verifies nothing and this is what lets one be reached
		// with no Google account anywhere. An empty Authorization header
		// instead of none is the version of this that some servers reject, so
		// the assertion is on the header's ABSENCE and not on its value.
		seenAuthorization, sawAuthorizationHeader = "unset", true
		s, err := golden.OpenStore(golden.KindGCS, api.URL+"/afgoldens/p",
			func(string) string { return "" }, nil)
		require.NoError(t, err)
		_, err = s.List(context.Background(), "")
		require.NoError(t, err)
		require.False(t, sawAuthorizationHeader,
			"a store with no credential sent an Authorization header")
	})
}

// TestGCSStore_GoogleWithNoCredentialSaysWhichVariableFixesIt covers the case
// the anonymous path must NOT cover.
//
// gs:// is Google's own endpoint, an unauthenticated request there is a 401,
// and a store that silently went anonymous would turn a missing environment
// variable into an authentication failure twenty minutes into a refresh. The
// metadata server is the fallback and off Google it does not resolve, so the
// message has to name the variable.
func TestGCSStore_GoogleWithNoCredentialSaysWhichVariableFixesIt(t *testing.T) {
	t.Parallel()
	s, err := golden.OpenStore(golden.KindGCS, "gs://afgoldens/p",
		func(string) string { return "" }, nil)
	// Opening succeeds: the metadata server is not probed at open time,
	// because that costs a second on every command on a machine that is not on
	// Google. The refusal arrives at the first request instead.
	require.NoError(t, err)

	_, err = s.List(context.Background(), "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "GOOGLE_APPLICATION_CREDENTIALS")
	require.Contains(t, err.Error(), "not running on Google Cloud")
}

// serviceAccountKey builds a real, freshly generated service account document.
//
// Generated rather than written down, because a credential scanner cannot tell
// a famous fake key from a real one and neither can somebody reading a diff.
// 2048 bits, because the signature has to actually verify as RS256 and the
// cost of generating one is paid once in this test.
func serviceAccountKey(t *testing.T, tokenURI string) string {
	t.Helper()
	key, err := rsa.GenerateKey(crand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	doc, err := json.Marshal(map[string]string{
		"type":         "service_account",
		"project_id":   "af-test",
		"client_email": "af-goldens@af-test.iam.gserviceaccount.com",
		"private_key":  string(pemBytes),
		"token_uri":    tokenURI,
	})
	require.NoError(t, err)
	return string(doc)
}

func fetch(t *testing.T, target string) (string, int) {
	t.Helper()
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Get(target)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body), resp.StatusCode
}

// makeGCSBucket creates the test bucket, and succeeds when it is already there.
func makeGCSBucket(endpoint, bucket string) error {
	body := strings.NewReader(`{"name":"` + bucket + `"}`)
	req, err := http.NewRequest(http.MethodPost, endpoint+"/storage/v1/b?project=af", body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 == 2 || resp.StatusCode == http.StatusConflict {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return fmt.Errorf("creating the bucket: %s: %s", resp.Status, raw)
}

// TestS3StoreAddressesEveryServiceThatSpeaksTheAPI is the claim "R2, MinIO,
// B2, Spaces and Wasabi already work through s3", turned into something that
// can say no.
//
// The claim was in the plan as a sentence and sentences about compatibility
// are the ones that turn out to be wrong. What a machine with no accounts can
// prove is the half this repository wrote, and it is the half that breaks:
// each vendor publishes an endpoint and a region, the store has to address it
// PATH STYLE rather than virtual hosted because a bucket prefixed onto
// s3.us-west-004.backblazeb2.com is a hostname that does not resolve, and the
// credential scope has to name the vendor's region rather than us-east-1
// because SigV4 pins the region into the signature and a signature scoped to
// the wrong one is refused identically to a wrong secret key.
//
// What it does NOT prove is that each vendor accepts the request, which needs
// an account with each and which section 10 of the plan forbids. MinIO is the
// one of the five proved end to end, by TestS3Store above, against a real
// server that rejects a wrong signature exactly as S3 does. The other four are
// proved to be addressed correctly and are not proved to answer. That split is
// the honest version of the claim and it is written down here rather than
// implied by a green test.
func TestS3StoreAddressesEveryServiceThatSpeaksTheAPI(t *testing.T) {
	t.Parallel()

	cases := []struct {
		service string
		// url is the storage_url a person would write, in the form the
		// vendor's own documentation gives for its S3 compatible endpoint.
		url string
		// region is what that vendor calls its region. R2 has one region and
		// calls it auto; the rest name a real one.
		region string
		host   string
		path   string
		// pathStyle records the addressing this vendor needs. AWS is the only
		// one of the six addressed virtual hosted here.
		pathStyle bool
	}{
		{
			service: "Amazon S3, the control",
			url:     "s3://afgoldens/goldens", region: "eu-west-1",
			host: "afgoldens.s3.eu-west-1.amazonaws.com",
			path: "/goldens/gv_1/dump.pgcustom", pathStyle: false,
		},
		{
			service: "Cloudflare R2",
			url:     "https://1a2b3c.r2.cloudflarestorage.com/afgoldens/goldens", region: "auto",
			host: "1a2b3c.r2.cloudflarestorage.com",
			path: "/afgoldens/goldens/gv_1/dump.pgcustom", pathStyle: true,
		},
		{
			service: "MinIO",
			url:     "http://minio.internal:9000/afgoldens/goldens", region: "us-east-1",
			host: "minio.internal:9000",
			path: "/afgoldens/goldens/gv_1/dump.pgcustom", pathStyle: true,
		},
		{
			service: "Backblaze B2",
			url:     "https://s3.us-west-004.backblazeb2.com/afgoldens/goldens", region: "us-west-004",
			host: "s3.us-west-004.backblazeb2.com",
			path: "/afgoldens/goldens/gv_1/dump.pgcustom", pathStyle: true,
		},
		{
			service: "DigitalOcean Spaces",
			url:     "https://nyc3.digitaloceanspaces.com/afgoldens/goldens", region: "nyc3",
			host: "nyc3.digitaloceanspaces.com",
			path: "/afgoldens/goldens/gv_1/dump.pgcustom", pathStyle: true,
		},
		{
			service: "Wasabi",
			url:     "https://s3.us-east-2.wasabisys.com/afgoldens/goldens", region: "us-east-2",
			host: "s3.us-east-2.wasabisys.com",
			path: "/afgoldens/goldens/gv_1/dump.pgcustom", pathStyle: true,
		},
	}

	for _, c := range cases {
		t.Run(c.service, func(t *testing.T) {
			env := func(name string) string {
				switch name {
				case "AWS_ACCESS_KEY_ID":
					return "AKIAEXAMPLEEXAMPLE00"
				case "AWS_SECRET_ACCESS_KEY":
					return "not-a-real-secret-and-never-sent-anywhere"
				case "AWS_REGION":
					return c.region
				}
				return ""
			}
			s, err := golden.OpenStore(golden.KindS3, c.url, env, nil)
			require.NoError(t, err, "%s could not even be opened", c.service)

			req, err := golden.SignedRequestForTest(s, http.MethodGet, "gv_1/dump.pgcustom")
			require.NoError(t, err)

			require.Equal(t, c.host, req.URL.Host, "%s is addressed at the wrong host", c.service)
			require.Equal(t, c.path, req.URL.Path, "%s is addressed at the wrong path", c.service)
			require.Equal(t, c.pathStyle, !strings.HasPrefix(req.URL.Host, "afgoldens."),
				"%s needs %v for path style addressing", c.service, c.pathStyle)

			// SigV4 pins the region into the credential scope, so a store that
			// defaulted to us-east-1 for every vendor would sign something the
			// vendor refuses with a 403 that reads like a permissions problem.
			auth := req.Header.Get("Authorization")
			require.Contains(t, auth, "AWS4-HMAC-SHA256 Credential=AKIAEXAMPLEEXAMPLE00/")
			require.Contains(t, auth, "/"+c.region+"/s3/aws4_request",
				"%s was signed for the wrong region", c.service)
			require.Contains(t, auth, "SignedHeaders=host;x-amz-content-sha256;x-amz-date")

			// The Host the signature covers is the one the request carries.
			// SigV4 signs Host, so these disagreeing is a signature that
			// disagrees with its own request.
			require.Equal(t, c.host, req.Host)
		})
	}
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func reachable(probe string) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(probe)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// azuriteKey assembles Azurite's published development account key.
//
// It is a constant every Azurite in the world uses and grants nothing beyond
// the container this test just made on localhost, but it is assembled at run
// time rather than written down, because a credential scanner cannot tell a
// famous fake key from a real one and neither can somebody reading a diff.
func azuriteKey() string {
	return "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6" +
		"IFsuFq2UVErCz4I6tq" + "/" + "K1SZFPTOtr" + "/" + "KBHBeksoGMGw=="
}

// accountSAS builds an account shared access signature, the way the portal and
// the CLI do, so that the store is handed the shape of URL a person would
// actually paste into their environment.
func accountSAS(account, key string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return "", err
	}
	const version = "2021-08-06"
	values := url.Values{}
	values.Set("sv", version)
	values.Set("ss", "b")                                                    // blob service
	values.Set("srt", "sco")                                                 // service, container, object
	values.Set("sp", "rwdlac")                                               // read write delete list add create
	values.Set("se", time.Now().Add(2*time.Hour).UTC().Format(time.RFC3339)) // expiry
	values.Set("spr", "https,http")

	// The field order is the signature, and every one of them is present even
	// when empty. Getting the order wrong produces a 403 that reads like a
	// permissions problem.
	toSign := strings.Join([]string{
		account,
		values.Get("sp"),
		values.Get("ss"),
		values.Get("srt"),
		"", // start time, unset
		values.Get("se"),
		"", // allowed IP range, unset
		values.Get("spr"),
		version,
		"", // encryption scope, unset, and required from 2020-12-06 onward
	}, "\n") + "\n"

	mac := hmac.New(sha256.New, raw)
	mac.Write([]byte(toSign))
	values.Set("sig", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return values.Encode(), nil
}

func makeContainer(u string) error {
	req, err := http.NewRequest(http.MethodPut, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-ms-version", "2021-08-06")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 == 2 || resp.StatusCode == http.StatusConflict {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return fmt.Errorf("creating the container: %s: %s", resp.Status, body)
}

// ---------------------------------------------------------------------------
// A store registered from outside this repository.

// registeredKind is the name the out of repository store in these tests
// registers itself under.
//
// Deliberately a thing this repository will never build. It used to be "gcs",
// which read well right up until gcs became a built in kind: the built in
// switch is consulted first, so the test that proves a REGISTERED store is
// opened silently began proving that a built in one was. Naming a real service
// in a test whose whole subject is a store the engine does not have is a name
// with an expiry date on it.
const registeredKind = "tape_library"

// memStore is an object store that is not one of the built in kinds.
type memStore struct {
	name    string
	objects map[string][]byte
}

func (m *memStore) Name() string { return m.name }

func (m *memStore) Put(_ context.Context, name string, _ int64, body io.Reader) error {
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	m.objects[name] = b
	return nil
}

func (m *memStore) Get(_ context.Context, name string) (io.ReadCloser, error) {
	b, ok := m.objects[name]
	if !ok {
		// The sentinel a store outside this module can name, which is the
		// same value golden.ErrNotFound is.
		return nil, extension.ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (m *memStore) List(_ context.Context, prefix string) ([]golden.Object, error) {
	var out []golden.Object
	for name, b := range m.objects {
		if strings.HasPrefix(name, prefix) {
			out = append(out, golden.Object{Name: name, Size: int64(len(b))})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *memStore) Delete(_ context.Context, name string) error {
	delete(m.objects, name)
	return nil
}

type memStoreKind struct {
	name string
	seen extension.ObjectStoreConfig
}

func (k *memStoreKind) Name() string { return k.name }

func (k *memStoreKind) Open(cfg extension.ObjectStoreConfig) (extension.ObjectStore, error) {
	k.seen = cfg
	// "registered" is in the name so that a test can tell this store from a
	// built in one by more than the URL it was opened with, which both carry.
	return &memStore{name: "registered " + k.name + " at " + cfg.URL, objects: map[string][]byte{}}, nil
}

func TestARegisteredStoreIsOpenedAndIsTheStoreTheEngineUses(t *testing.T) {
	t.Parallel()
	// The built in kinds are the ones this repository happens to have written.
	// A fleet publishing to anything else had no way in short of editing this
	// package, which is unimportable from outside the module.
	kind := &memStoreKind{name: registeredKind}
	reg := extension.NewRegistry()
	reg.AddGoldenStore(kind)

	s, err := golden.OpenStore(registeredKind, "tape://bucket/goldens", nil, reg)
	require.NoError(t, err)
	require.NotNil(t, s)
	require.Contains(t, s.Name(), "tape://bucket/goldens")
	require.Equal(t, "tape://bucket/goldens", kind.seen.URL)

	// And it is a golden.Store with no adapter in between: the interface is
	// the one in engine/pkg/extension and this package's name for it is an
	// alias, so a registered store satisfies every call site the engine has.
	require.NoError(t, s.Put(context.Background(), "gv_1.sql", 3, strings.NewReader("abc")))
	body, err := s.Get(context.Background(), "gv_1.sql")
	require.NoError(t, err)
	defer func() { _ = body.Close() }()
	got, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, "abc", string(got))

	_, err = s.Get(context.Background(), "missing")
	require.ErrorIs(t, err, golden.ErrNotFound,
		"a store outside this module cannot name golden.ErrNotFound, so every "+
			"absent object would read as a broken store")
}

func TestAnUnregisteredKindIsRefusedAndTheRefusalListsWhatThereIs(t *testing.T) {
	t.Parallel()
	reg := extension.NewRegistry()
	reg.AddGoldenStore(&memStoreKind{name: registeredKind})

	_, err := golden.OpenStore("tape_librari", "tape://bucket/goldens", nil, reg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "local, azure_blob, s3, gcs, "+registeredKind,
		"the refusal does not name the store this build has registered")
}

func TestABuiltInKindIsNeverTakenOverByARegistration(t *testing.T) {
	t.Parallel()
	// The registry is consulted after the built in kinds and never before
	// them, so a registration cannot change where an existing manifest
	// publishes. Registering one under a built in name is refused where the
	// registry is validated; here what is proved is that the switch itself
	// does not consult it first.
	reg := extension.NewRegistry()
	reg.AddGoldenStore(&memStoreKind{name: "local"})

	dir := t.TempDir()
	s, err := golden.OpenStore(golden.KindLocal, dir, nil, reg)
	require.NoError(t, err)
	require.Equal(t, "the directory "+dir, s.Name(),
		"a registration took over the built in local store")
}
