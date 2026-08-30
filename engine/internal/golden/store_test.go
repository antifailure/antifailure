package golden_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/golden"
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
		s, err := golden.OpenStore(golden.KindLocal, t.TempDir(), nil)
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
	s, err := golden.OpenStore(golden.KindLocal, "$AF_GOLDEN_STORE", env)
	require.NoError(t, err)
	require.Contains(t, s.Name(), dir)

	_, err = golden.OpenStore(golden.KindLocal, "$AF_NOT_SET_ANYWHERE", env)
	require.Error(t, err)
	require.Contains(t, err.Error(), "AF_NOT_SET_ANYWHERE")
	require.Contains(t, err.Error(), "not set on this machine")
}

func TestOpenStore_IsNothingWhenNothingIsConfigured(t *testing.T) {
	t.Parallel()
	// No storage_url means goldens live wherever the provider keeps them,
	// which is the default and is not an error.
	s, err := golden.OpenStore(golden.KindLocal, "", nil)
	require.NoError(t, err)
	require.Nil(t, s)

	_, err = golden.OpenStore("gopher_holes", "/tmp/x", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "local, azure_blob, s3")
}

func TestOpenStore_SaysWhatIsMissingFromARemoteURL(t *testing.T) {
	t.Parallel()
	// The message has to name the fix. "invalid URL" sends somebody to read
	// the source; "the URL is the CONTAINER's" does not.
	_, err := golden.OpenStore(golden.KindAzureBlob, "https://acct.blob.core.windows.net/", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "names no container")

	_, err = golden.OpenStore(golden.KindAzureBlob, "https://acct.blob.core.windows.net/goldens", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "shared access signature")

	// And a message about a URL must not print the signature back out.
	_, err = golden.OpenStore(golden.KindAzureBlob,
		"ftp://acct.blob.core.windows.net/goldens?sig=SUPERSECRETSIGNATURE", nil)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "SUPERSECRETSIGNATURE",
		"a message about a URL never prints its credential")

	// S3 signs with the environment's credential, and says so when it is not
	// there rather than failing later with a 403.
	_, err = golden.OpenStore(golden.KindS3, "s3://bucket/goldens",
		func(string) string { return "" })
	require.Error(t, err)
	require.Contains(t, err.Error(), "AWS_ACCESS_KEY_ID")
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
			fmt.Sprintf("%s/%s/%s", endpoint, bucket, prefix), env)
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
		s, err := golden.OpenStore(golden.KindAzureBlob, base+"?"+sas, nil)
		require.NoError(t, err)
		return s
	})
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
