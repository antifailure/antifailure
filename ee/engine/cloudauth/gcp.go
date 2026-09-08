package cloudauth

// Google access tokens, two ways.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Two ways, matching where this actually runs. The metadata server, which is
// what a Cloud Run service, a GKE workload and a Compute Engine instance all
// have and which needs no key material at all. And a service account key,
// signed here into a JWT assertion and exchanged, which is what a CI runner
// outside Google has. The second is a key on disk and is worth avoiding where
// the first is available; the message says which one was used when a request is
// refused, so that nobody has to guess.

import (
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
	"net/url"
	"sync"
	"time"
)

// ScopeGoogleCloudPlatform is the scope an assertion asks for.
//
// The broad one, because the permission that decides what may be read is the
// IAM role on the service account rather than the scope, and a narrower scope
// here would only add a second place to get it wrong.
const ScopeGoogleCloudPlatform = "https://www.googleapis.com/auth/cloud-platform"

// GCPServiceAccount is a parsed service account key.
type GCPServiceAccount struct {
	Type        string `json:"type"`
	ProjectID   string `json:"project_id"`
	PrivateKey  string `json:"private_key"`
	ClientEmail string `json:"client_email"`
	TokenURI    string `json:"token_uri"`

	key *rsa.PrivateKey
}

// ParseGCPServiceAccount reads a service account key document.
func ParseGCPServiceAccount(raw []byte) (*GCPServiceAccount, error) {
	var account GCPServiceAccount
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

// SignAssertion builds the JWT a service account exchanges for a token.
//
// RS256 over a fixed header and a claim set with a one hour life. The audience
// is the token endpoint itself, which is what stops an assertion minted for one
// service being replayed against another.
func (a *GCPServiceAccount) SignAssertion(now time.Time, scope string) (string, error) {
	if scope == "" {
		scope = ScopeGoogleCloudPlatform
	}
	header := base64url([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iss":   a.ClientEmail,
		"scope": scope,
		"aud":   a.TokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	if err != nil {
		return "", err
	}
	signing := header + "." + base64url(claims)
	digest := sha256.Sum256([]byte(signing))
	signature, err := rsa.SignPKCS1v15(rand.Reader, a.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signing + "." + base64url(signature), nil
}

// GCPTokenSource hands out an access token and holds it until it expires.
type GCPTokenSource struct {
	// account is the parsed service account key, nil when the metadata server
	// is used.
	account *GCPServiceAccount
	scope   string

	mu      sync.Mutex
	token   string
	expires time.Time
	how     string
}

// NewGCPTokenSource builds a token source. A nil account takes the metadata
// server path, which is the better one wherever it exists.
func NewGCPTokenSource(account *GCPServiceAccount, scope string) *GCPTokenSource {
	if scope == "" {
		scope = ScopeGoogleCloudPlatform
	}
	return &GCPTokenSource{account: account, scope: scope}
}

// Account returns the parsed key, or nil when the metadata server is used.
func (s *GCPTokenSource) Account() *GCPServiceAccount { return s.account }

// How names where the current token came from, for a refusal message.
func (s *GCPTokenSource) How() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.how
}

// Reset discards the token so the next call acquires a new one.
func (s *GCPTokenSource) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token, s.expires = "", time.Time{}
}

// Token returns an access token, acquiring one when the held token is missing
// or nearly expired.
func (s *GCPTokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	token, expires := s.token, s.expires
	s.mu.Unlock()
	// A minute early, so a token that expires between being read and being used
	// does not produce a rejection a renewal would have avoided.
	if token != "" && time.Now().Add(time.Minute).Before(expires) {
		return token, nil
	}

	var (
		got      string
		lifetime time.Duration
		how      string
		err      error
	)
	if s.account != nil {
		got, lifetime, how, err = s.fromServiceAccount(ctx)
	} else {
		got, lifetime, how, err = s.fromMetadataServer(ctx)
	}
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	s.token, s.expires, s.how = got, time.Now().Add(lifetime), how
	s.mu.Unlock()
	return got, nil
}

// fromServiceAccount signs a JWT and exchanges it for an access token.
func (s *GCPTokenSource) fromServiceAccount(ctx context.Context) (string, time.Duration, string, error) {
	assertion, err := s.account.SignAssertion(time.Now(), s.scope)
	if err != nil {
		return "", 0, "", err
	}
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	resp, err := Do(ctx, Request{
		Method: "POST", URL: s.account.TokenURI, Body: []byte(form.Encode()),
		Headers: map[string]string{
			"Content-Type": "application/x-www-form-urlencoded",
			"Accept":       "application/json",
		},
	})
	if err != nil {
		return "", 0, "", fmt.Errorf("Google's token endpoint could not be reached: %s", err)
	}
	if resp.Status != 200 {
		return "", 0, "", Wrap(ErrRejected,
			"Google refused the service account assertion with %d %s",
			resp.Status, GCPErrorStatus(resp.Body))
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := resp.Decode(&payload); err != nil {
		return "", 0, "", err
	}
	if payload.AccessToken == "" {
		return "", 0, "", fmt.Errorf("Google answered 200 and returned no token")
	}
	return payload.AccessToken, time.Duration(payload.ExpiresIn) * time.Second,
		"the service account " + s.account.ClientEmail, nil
}

// fromMetadataServer reads the token the platform already holds.
func (s *GCPTokenSource) fromMetadataServer(ctx context.Context) (string, time.Duration, string, error) {
	resp, err := Do(ctx, Request{
		Method: "GET",
		URL:    "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token",
		// Required, and its absence is the whole anti-forgery mechanism: the
		// metadata server refuses any request without it, so a browser or a
		// naive server-side fetch cannot reach it.
		Headers: map[string]string{"Metadata-Flavor": "Google"},
		// A second, like the other two link-local metadata services. Off
		// Google this name does not resolve, and waiting the shared ten seconds
		// to learn that would be ten seconds on every af up.
		Timeout: time.Second,
	})
	if err != nil {
		return "", 0, "", Wrap(ErrNotConfigured,
			"no Google credentials: GOOGLE_APPLICATION_CREDENTIALS is unset and the "+
				"metadata server did not answer, so this is not running on Google Cloud. "+
				"Point GOOGLE_APPLICATION_CREDENTIALS at a service account key, or run "+
				"somewhere with a service account attached")
	}
	if resp.Status != 200 {
		return "", 0, "", fmt.Errorf("the metadata server answered %d", resp.Status)
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := resp.Decode(&payload); err != nil {
		return "", 0, "", err
	}
	return payload.AccessToken, time.Duration(payload.ExpiresIn) * time.Second,
		"this host's attached service account", nil
}

func base64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// GCPErrorStatus reads the status out of a Google error document.
//
// The status and never the message. Google's message quotes the resource name,
// and the resource name is the secret.
func GCPErrorStatus(body []byte) string {
	var payload struct {
		Error struct {
			Status string `json:"status"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Error.Status == "" {
		return "with no status"
	}
	return payload.Error.Status
}
