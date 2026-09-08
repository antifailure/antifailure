package golden

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// gcsStore keeps goldens in a Google Cloud Storage bucket.
//
// MIT and in the engine, beside s3 and azure_blob, because of the editions
// rule: a store one developer can use with their own account and their own
// card is not an enterprise feature, and its two peers are already here. The
// only Google specific thing that is enterprise licensed is Secret Manager,
// which is a different thing entirely.
//
// It speaks the JSON API directly rather than through cloud.google.com/go/
// storage, for the reason store_azure.go gives about the Azure SDK: four
// operations against a stable, versioned, fully specified API are not worth a
// dependency tree in a binary otherwise built from a handful of libraries.
// google-cloud-go's storage module alone pulls the whole google-api-go-client
// and grpc stack.
//
// Three things about this API are easy to get subtly wrong, and each is
// wrong in the direction that produces a plausible answer rather than an
// error:
//
//   - A read without alt=media returns the object's METADATA as JSON, with a
//     200 and a body. A store that forgot it would publish a golden and read
//     back a JSON document describing the golden, which is not a dump and
//     which pg_restore would reject a long way from here.
//   - The object name is one path segment, so the slash in
//     gv_1/dump.pgcustom has to be percent encoded. Left raw it addresses a
//     different resource, and on the write side it produces an object whose
//     name is not the name that was asked for.
//   - size in a listing is a STRING, not a number, because the API returns
//     int64 fields as strings so that a JavaScript client cannot lose the top
//     bits. A struct declaring int64 does not silently read zero, it fails to
//     decode, which is better, but the whole listing fails with it.
type gcsStore struct {
	// endpoint is the API root, with no trailing slash. Google's, or a
	// server that speaks the same API.
	endpoint string
	bucket   string
	prefix   string
	client   *http.Client
	label    string
	getenv   func(string) string

	// account is a parsed service account key, when one was configured. Nil
	// means the token comes from the metadata server, which is what a Cloud
	// Run service, a GKE workload or a Compute Engine instance has.
	account *gcsServiceAccount
	// anonymous is set for an endpoint that is not Google's and for which no
	// credential was configured, which is what an emulator wants. Google's own
	// endpoint never gets this: an unauthenticated request there is a 401 at
	// the far end, and refusing here with the variable named is a better
	// answer than a 401 twenty minutes into a refresh.
	anonymous bool

	mu      sync.Mutex
	token   string
	expires time.Time
}

// gcsAPI is Google's own endpoint. Named once so the test for "is this
// Google" and the default cannot disagree.
const gcsAPI = "https://storage.googleapis.com"

// gcsScope is the smallest scope that covers read, write, list and delete on
// one bucket. cloud-platform would also work and is broader than this needs.
const gcsScope = "https://www.googleapis.com/auth/devstorage.read_write"

type gcsServiceAccount struct {
	Type        string `json:"type"`
	ProjectID   string `json:"project_id"`
	PrivateKey  string `json:"private_key"`
	ClientEmail string `json:"client_email"`
	TokenURI    string `json:"token_uri"`

	key *rsa.PrivateKey
}

// newGCSStore reads gs://bucket/prefix, or the https URL of a server that
// speaks the same API.
//
// The credential never comes from the URL. It comes from the environment, by
// the name Google's own tools already use, so that a machine already set up
// for gcloud needs nothing else and a manifest carries no secret.
func newGCSStore(raw string, getenv func(string) string) (Store, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("golden: %q is not a usable bucket URL: %w", redactURL(raw), err)
	}

	s := &gcsStore{
		// The same half hour the other two remote stores allow, because the
		// thing being moved is a database dump and not a web page.
		client: &http.Client{Timeout: 30 * time.Minute},
		getenv: getenv,
	}

	switch u.Scheme {
	case "gs":
		s.bucket = u.Host
		s.prefix = strings.Trim(u.Path, "/")
		s.endpoint = gcsAPI
	case "http", "https":
		// A full URL points at a server that is not Google, which in practice
		// means fake-gcs-server in a test or an internal gateway. The first
		// path segment is the bucket, the same shape store_s3.go accepts for
		// MinIO, so that a manifest with both does not have two spellings of
		// the same idea.
		parts := strings.SplitN(strings.Trim(u.Path, "/"), "/", 2)
		if parts[0] == "" {
			return nil, fmt.Errorf(
				"golden: %s names no bucket. For a server that is not Google the URL is "+
					"https://<host>/<bucket>", redactURL(raw))
		}
		s.bucket = parts[0]
		if len(parts) > 1 {
			s.prefix = strings.Trim(parts[1], "/")
		}
		s.endpoint = strings.TrimRight((&url.URL{Scheme: u.Scheme, Host: u.Host}).String(), "/")
	default:
		return nil, fmt.Errorf(
			"golden: a gcs storage_url is gs://<bucket>/<prefix>, or the https URL of a "+
				"server that speaks the same API, and this one is %q", u.Scheme)
	}
	if s.bucket == "" {
		return nil, fmt.Errorf("golden: %s names no bucket", redactURL(raw))
	}

	if err := s.readCredentials(); err != nil {
		return nil, err
	}
	s.label = fmt.Sprintf("gs://%s/%s", s.bucket, s.prefix)
	return s, nil
}

// readCredentials decides how this store will authenticate, at open time.
//
// A malformed service account key is found here rather than at the first
// request, because a key that is not a key is a configuration defect and the
// moment to report one is before anything depends on the answer. The metadata
// server is NOT probed here: that costs a second on every af up on a machine
// that is not on Google, and the failure it would find is reported just as
// clearly at the first request with the same message.
func (s *gcsStore) readCredentials() error {
	raw := []byte(s.getenv("GOOGLE_APPLICATION_CREDENTIALS_JSON"))
	if len(raw) == 0 {
		// The conventional variable, holding a PATH rather than the document.
		// Read here rather than left to the caller, because the point of the
		// convention is that nobody has to.
		if path := s.getenv("GOOGLE_APPLICATION_CREDENTIALS"); path != "" {
			read, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf(
					"golden: GOOGLE_APPLICATION_CREDENTIALS names %s, which could not be read", path)
			}
			raw = read
		}
	}
	if len(raw) > 0 {
		account, err := parseGCSServiceAccount(raw)
		if err != nil {
			return fmt.Errorf("golden: the Google service account key is not usable: %w", err)
		}
		s.account = account
		return nil
	}
	// No key. On Google the metadata server answers and this is the better
	// path, so it is not an error. Off Google against Google's own endpoint it
	// is going to be a 401, and saying so now names the fix.
	if s.endpoint != gcsAPI {
		s.anonymous = true
	}
	return nil
}

func parseGCSServiceAccount(raw []byte) (*gcsServiceAccount, error) {
	var account gcsServiceAccount
	if err := json.Unmarshal(raw, &account); err != nil {
		return nil, fmt.Errorf("it is not JSON")
	}
	if account.Type != "service_account" {
		return nil, fmt.Errorf("it is a %q key and only a service_account key is read here", account.Type)
	}
	if account.ClientEmail == "" || account.PrivateKey == "" {
		return nil, fmt.Errorf("it has no client_email or no private_key")
	}
	if account.TokenURI == "" {
		account.TokenURI = "https://oauth2.googleapis.com/token"
	}
	block, _ := pem.Decode([]byte(account.PrivateKey))
	if block == nil {
		return nil, fmt.Errorf("its private_key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("its private_key is not a PKCS#8 key")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("its private_key is not RSA")
	}
	account.key = key
	// The PEM is dropped now that the key is parsed, so the plaintext of a
	// private key is not held in a struct for the life of the process.
	account.PrivateKey = ""
	return &account, nil
}

func (s *gcsStore) Name() string { return "the bucket " + s.label }

func (s *gcsStore) key(name string) string {
	if s.prefix == "" {
		return name
	}
	return s.prefix + "/" + name
}

// objectURL addresses one object.
//
// url.PathEscape and not a raw join, because the object name carries the
// slash between the version and the file and the API reads that path segment
// as ONE object name. Left raw, gv_1/dump.pgcustom addresses a resource that
// does not exist and the store reports every golden missing.
func (s *gcsStore) objectURL(name string, query url.Values) string {
	u := s.endpoint + "/storage/v1/b/" + url.PathEscape(s.bucket) +
		"/o/" + url.PathEscape(s.key(name))
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return u
}

// bearer returns an access token, acquiring one when the held one is close to
// expiry. Empty for an anonymous store, which is an emulator.
func (s *gcsStore) bearer(ctx context.Context) (string, error) {
	if s.anonymous {
		return "", nil
	}
	s.mu.Lock()
	token, expires := s.token, s.expires
	s.mu.Unlock()
	// A minute of headroom, so a token that is valid when the request is built
	// is still valid when it arrives.
	if token != "" && time.Now().Add(time.Minute).Before(expires) {
		return token, nil
	}

	var (
		got      string
		lifetime time.Duration
		err      error
	)
	if s.account != nil {
		got, lifetime, err = s.fromServiceAccount(ctx)
	} else {
		got, lifetime, err = s.fromMetadataServer(ctx)
	}
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.token, s.expires = got, time.Now().Add(lifetime)
	s.mu.Unlock()
	return got, nil
}

// fromServiceAccount signs a JWT and exchanges it for an access token.
//
// RS256 over a fixed header and a claim set with a one hour life. The audience
// is the token endpoint itself, which is what stops an assertion minted for
// one service being replayed against another.
func (s *gcsStore) fromServiceAccount(ctx context.Context) (string, time.Duration, error) {
	assertion, err := s.signAssertion(time.Now())
	if err != nil {
		return "", 0, err
	}
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.account.TokenURI,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("golden: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("golden: Google's token endpoint could not be reached: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		// The status and never the message. Google's message quotes the
		// resource, and quoting it back would put it in a log.
		return "", 0, fmt.Errorf(
			"golden: Google refused the service account %s with %s %s",
			s.account.ClientEmail, resp.Status, gcsErrorStatus(body))
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", 0, fmt.Errorf("golden: Google's token response did not parse: %w", err)
	}
	if payload.AccessToken == "" {
		return "", 0, fmt.Errorf("golden: Google answered 200 and returned no token")
	}
	return payload.AccessToken, time.Duration(payload.ExpiresIn) * time.Second, nil
}

// signAssertion builds the JWT a service account exchanges for a token.
func (s *gcsStore) signAssertion(now time.Time) (string, error) {
	header := gcsBase64URL([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iss":   s.account.ClientEmail,
		"scope": gcsScope,
		"aud":   s.account.TokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	if err != nil {
		return "", err
	}
	signing := header + "." + gcsBase64URL(claims)
	digest := sha256.Sum256([]byte(signing))
	signature, err := rsa.SignPKCS1v15(rand.Reader, s.account.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signing + "." + gcsBase64URL(signature), nil
}

// fromMetadataServer reads the token the platform already holds.
func (s *gcsStore) fromMetadataServer(ctx context.Context) (string, time.Duration, error) {
	// A second, like the other link local metadata services. Off Google this
	// name does not resolve, and waiting the client's half hour to learn that
	// would be half an hour on every publish.
	inner, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(inner, http.MethodGet,
		"http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token", nil)
	if err != nil {
		return "", 0, fmt.Errorf("golden: %w", err)
	}
	// Required, and its absence is the whole anti forgery mechanism: the
	// metadata server refuses any request without it, so a browser or a naive
	// server side fetch cannot reach it.
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf(
			"golden: no Google credentials for %s: GOOGLE_APPLICATION_CREDENTIALS is unset "+
				"and the metadata server did not answer, so this is not running on Google "+
				"Cloud. Point GOOGLE_APPLICATION_CREDENTIALS at a service account key, or "+
				"run somewhere with a service account attached", s.label)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("golden: the metadata server answered %s", resp.Status)
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", 0, fmt.Errorf("golden: the metadata server's response did not parse: %w", err)
	}
	return payload.AccessToken, time.Duration(payload.ExpiresIn) * time.Second, nil
}

// authorize attaches the bearer token, when there is one.
func (s *gcsStore) authorize(ctx context.Context, req *http.Request) error {
	token, err := s.bearer(ctx)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return nil
}

func (s *gcsStore) Put(ctx context.Context, name string, size int64, body io.Reader) error {
	// The upload endpoint is a different path root from every other call, and
	// the object name travels in the QUERY rather than in the path. Encoded by
	// url.Values, so the slash in the name is handled by the same rule as
	// everything else in a query string.
	q := url.Values{}
	q.Set("uploadType", "media")
	q.Set("name", s.key(name))
	target := s.endpoint + "/upload/storage/v1/b/" + url.PathEscape(s.bucket) + "/o?" + q.Encode()

	// Read into memory when the length is not known, because a PUT with an
	// unknown length becomes a chunked request. Where the caller knows the
	// size the reader is streamed, which is the case that matters: a golden
	// dump is the large thing here.
	var req *http.Request
	var err error
	if size >= 0 {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, target, body)
		if err != nil {
			return fmt.Errorf("golden: %w", err)
		}
		req.ContentLength = size
	} else {
		buf, readErr := io.ReadAll(body)
		if readErr != nil {
			return fmt.Errorf("golden: reading %s to upload: %w", name, readErr)
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(buf))
		if err != nil {
			return fmt.Errorf("golden: %w", err)
		}
		req.ContentLength = int64(len(buf))
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if err := s.authorize(ctx, req); err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("golden: uploading %s to %s: %w", name, s.label, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return s.statusError("uploading "+name, resp)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func (s *gcsStore) Get(ctx context.Context, name string) (io.ReadCloser, error) {
	// alt=media, and it is the whole difference between the object and a JSON
	// document describing the object. Without it this returns 200 and a body,
	// which is the worst shape a mistake can take: a golden that restores into
	// nothing, found by pg_restore rather than here.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		s.objectURL(name, url.Values{"alt": {"media"}}), nil)
	if err != nil {
		return nil, fmt.Errorf("golden: %w", err)
	}
	if err := s.authorize(ctx, req); err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("golden: reading %s from %s: %w", name, s.label, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if resp.StatusCode/100 != 2 {
		defer func() { _ = resp.Body.Close() }()
		return nil, s.statusError("reading "+name, resp)
	}
	return resp.Body, nil
}

// gcsListing is the objects.list response.
//
// Size is a STRING and that is not a mistake in this declaration. The JSON API
// renders every int64 field as a string so that a JavaScript client cannot
// lose the top bits of a large one, and a struct that declared int64 here
// would fail to decode the whole page rather than one object.
type gcsListing struct {
	Items []struct {
		Name    string `json:"name"`
		Size    string `json:"size"`
		Updated string `json:"updated"`
	} `json:"items"`
	NextPageToken string `json:"nextPageToken"`
}

func (s *gcsStore) List(ctx context.Context, prefix string) ([]Object, error) {
	var out []Object
	token := ""
	for {
		q := url.Values{}
		if p := s.key(prefix); p != "" {
			q.Set("prefix", p)
		}
		if token != "" {
			q.Set("pageToken", token)
		}
		target := s.endpoint + "/storage/v1/b/" + url.PathEscape(s.bucket) + "/o"
		if len(q) > 0 {
			target += "?" + q.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, fmt.Errorf("golden: %w", err)
		}
		if err := s.authorize(ctx, req); err != nil {
			return nil, err
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("golden: listing %s: %w", s.label, err)
		}
		if resp.StatusCode/100 != 2 {
			err = s.statusError("listing "+s.label, resp)
			_ = resp.Body.Close()
			return nil, err
		}
		var doc gcsListing
		err = json.NewDecoder(resp.Body).Decode(&doc)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("golden: the bucket listing did not parse: %w", err)
		}
		for _, item := range doc.Items {
			name := item.Name
			if s.prefix != "" {
				name = strings.TrimPrefix(strings.TrimPrefix(name, s.prefix), "/")
			}
			// An unparseable size is zero rather than a failed listing. One
			// odd object must not blank the whole store, which is the shape
			// of an outage this repository has already had once.
			size, _ := strconv.ParseInt(item.Size, 10, 64)
			modified, _ := time.Parse(time.RFC3339, item.Updated)
			out = append(out, Object{Name: name, Size: size, Modified: modified.UTC()})
		}
		// Paged, because a bucket that has been running for a year holds more
		// than one page and a client that reads the first one reports a store
		// with no goldens in it.
		if doc.NextPageToken == "" {
			return out, nil
		}
		token = doc.NextPageToken
	}
}

func (s *gcsStore) Delete(ctx context.Context, name string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.objectURL(name, nil), nil)
	if err != nil {
		return fmt.Errorf("golden: %w", err)
	}
	if err := s.authorize(ctx, req); err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("golden: removing %s: %w", name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Removing what is not there succeeds, because a retry after a timeout
	// must not fail on the work it already did. Google answers 404 for that,
	// unlike S3, so the case is explicit here.
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode/100 == 2 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return s.statusError("removing "+name, resp)
}

// statusError turns a response into a message somebody can act on.
func (s *gcsStore) statusError(what string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	detail := gcsErrorStatus(body)
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		detail += " (a 401 here is the token: no credential was found, or the one that " +
			"was found has expired)"
	case http.StatusForbidden:
		detail += " (a 403 here is the grant rather than the token: the service account " +
			"authenticated and does not have storage.objects on this bucket)"
	}
	return fmt.Errorf("golden: %s: %s: %s", what, resp.Status, strings.TrimSpace(detail))
}

// gcsErrorStatus reads the message out of a Google error document.
//
// Google's errors nest as {"error":{"code":403,"message":"...","status":"..."}}
// and the useful half for an operator is the status. The message quotes the
// resource name, which for Secret Manager would be the secret; here it is a
// bucket and an object, so the message is safe to show and is shown, with the
// status when there is one.
func gcsErrorStatus(body []byte) string {
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) != nil {
		trimmed := strings.TrimSpace(string(body))
		if trimmed == "" {
			return "with no detail"
		}
		return trimmed
	}
	switch {
	case payload.Error.Status != "" && payload.Error.Message != "":
		return payload.Error.Status + ": " + payload.Error.Message
	case payload.Error.Message != "":
		return payload.Error.Message
	case payload.Error.Status != "":
		return payload.Error.Status
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return "with no detail"
	}
	return trimmed
}

func gcsBase64URL(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
