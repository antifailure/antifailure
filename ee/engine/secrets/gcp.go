package secrets

// Google Cloud Secret Manager.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// The access call is a GET and the interesting part is the payload encoding:
// Secret Manager returns the secret base64 encoded, which is what lets a secret
// hold bytes that are not text. A reader that forgot to decode would hand the
// application a base64 string that looks plausible, connects to nothing, and
// produces an authentication failure at the far end rather than an error here.
//
// Two ways to get a token, matching where this actually runs. The metadata
// server, which is what a Cloud Run service, a GKE workload and a Compute
// Engine instance all have and which needs no key material at all. And a
// service account key, signed here into a JWT assertion and exchanged, which is
// what a CI runner outside Google has. The second is a key on disk and is worth
// avoiding where the first is available; the message says so when it is used.

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
	"os"
	"strings"
	"sync"
	"time"
)

// GCPConfig is what a Secret Manager source needs.
type GCPConfig struct {
	// Project is the project id or number holding the secrets.
	Project string
	// Prefix is prepended to every variable name to form the secret id.
	Prefix string
	// Version is which version to read. Empty means "latest", which is what a
	// rotation is for: the newest enabled version, chosen by the service.
	Version string
	// CredentialsJSON is a service account key. When empty the metadata server
	// is used, which is the better path wherever it exists.
	CredentialsJSON []byte
	// Endpoint overrides the API address. Google publishes regional endpoints
	// for data residency, as secretmanager.europe-west4.rep.googleapis.com, and
	// an organization required to keep secrets in one jurisdiction needs to
	// name one. Empty means the global endpoint.
	Endpoint string
	// Getenv is injected so a test does not have to mutate the process
	// environment.
	Getenv func(string) string
}

// GCPBackend reads from Secret Manager.
type GCPBackend struct {
	cfg GCPConfig
	// account is the parsed service account key, nil when the metadata server
	// is used.
	account *gcpServiceAccount

	mu      sync.Mutex
	token   string
	expires time.Time
	how     string
}

type gcpServiceAccount struct {
	Type        string `json:"type"`
	ProjectID   string `json:"project_id"`
	PrivateKey  string `json:"private_key"`
	ClientEmail string `json:"client_email"`
	TokenURI    string `json:"token_uri"`

	key *rsa.PrivateKey
}

// NewGCPSecretManager builds a Secret Manager source, or reports what it is
// missing.
func NewGCPSecretManager(cfg GCPConfig) (*Source, error) {
	if cfg.Getenv == nil {
		cfg.Getenv = os.Getenv
	}
	if strings.TrimSpace(cfg.Project) == "" {
		cfg.Project = cfg.Getenv("GOOGLE_CLOUD_PROJECT")
	}
	if strings.TrimSpace(cfg.Project) == "" {
		return nil, wrap(ErrNotConfigured,
			"Google Secret Manager needs a project (GOOGLE_CLOUD_PROJECT)")
	}
	if cfg.Version == "" {
		cfg.Version = "latest"
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = "https://secretmanager.googleapis.com"
	}
	cfg.Endpoint = strings.TrimRight(cfg.Endpoint, "/")

	backend := &GCPBackend{cfg: cfg}
	raw := cfg.CredentialsJSON
	if len(raw) == 0 {
		// The conventional variable, holding a path rather than the document.
		// Read here rather than left to the caller, because the point of the
		// convention is that nobody has to.
		if path := cfg.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); path != "" {
			read, err := os.ReadFile(path)
			if err != nil {
				return nil, wrap(ErrNotConfigured,
					"GOOGLE_APPLICATION_CREDENTIALS names %s, which could not be read", path)
			}
			raw = read
		}
	}
	if len(raw) > 0 {
		account, err := parseServiceAccount(raw)
		if err != nil {
			return nil, wrap(ErrNotConfigured, "the service account key is not usable: %s", err)
		}
		backend.account = account
	}
	return New(backend), nil
}

func parseServiceAccount(raw []byte) (*gcpServiceAccount, error) {
	var account gcpServiceAccount
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

func (g *GCPBackend) Describe() string {
	where := "Google Secret Manager in " + g.cfg.Project
	if g.cfg.Prefix != "" {
		where += " (" + g.cfg.Prefix + "*)"
	}
	return where
}

// Reach acquires a token, which is the thing that actually fails.
func (g *GCPBackend) Reach(ctx context.Context) error {
	_, err := g.bearer(ctx)
	return err
}

const gcpScope = "https://www.googleapis.com/auth/cloud-platform"

func (g *GCPBackend) bearer(ctx context.Context) (string, error) {
	g.mu.Lock()
	token, expires := g.token, g.expires
	g.mu.Unlock()
	if token != "" && time.Now().Add(time.Minute).Before(expires) {
		return token, nil
	}

	var (
		got      string
		lifetime time.Duration
		how      string
		err      error
	)
	if g.account != nil {
		got, lifetime, how, err = g.fromServiceAccount(ctx)
	} else {
		got, lifetime, how, err = g.fromMetadataServer(ctx)
	}
	if err != nil {
		return "", err
	}

	g.mu.Lock()
	g.token, g.expires, g.how = got, time.Now().Add(lifetime), how
	g.mu.Unlock()
	return got, nil
}

// fromServiceAccount signs a JWT and exchanges it for an access token.
func (g *GCPBackend) fromServiceAccount(ctx context.Context) (string, time.Duration, string, error) {
	assertion, err := g.signAssertion(time.Now())
	if err != nil {
		return "", 0, "", err
	}
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	resp, err := do(ctx, request{
		method: "POST", url: g.account.TokenURI, body: []byte(form.Encode()),
		headers: map[string]string{
			"Content-Type": "application/x-www-form-urlencoded",
			"Accept":       "application/json",
		},
	})
	if err != nil {
		return "", 0, "", fmt.Errorf("Google's token endpoint could not be reached: %s", err)
	}
	if resp.status != 200 {
		return "", 0, "", wrap(ErrRejected,
			"Google refused the service account assertion with %d %s",
			resp.status, gcpErrorStatus(resp.body))
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := resp.decode(&payload); err != nil {
		return "", 0, "", err
	}
	if payload.AccessToken == "" {
		return "", 0, "", fmt.Errorf("Google answered 200 and returned no token")
	}
	return payload.AccessToken, time.Duration(payload.ExpiresIn) * time.Second,
		"the service account " + g.account.ClientEmail, nil
}

// signAssertion builds the JWT a service account exchanges for a token.
//
// RS256 over a fixed header and a claim set with a one hour life. The audience
// is the token endpoint itself, which is what stops an assertion minted for one
// service being replayed against another.
func (g *GCPBackend) signAssertion(now time.Time) (string, error) {
	header := base64url([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iss":   g.account.ClientEmail,
		"scope": gcpScope,
		"aud":   g.account.TokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	if err != nil {
		return "", err
	}
	signing := header + "." + base64url(claims)
	digest := sha256.Sum256([]byte(signing))
	signature, err := rsa.SignPKCS1v15(rand.Reader, g.account.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signing + "." + base64url(signature), nil
}

// fromMetadataServer reads the token the platform already holds.
func (g *GCPBackend) fromMetadataServer(ctx context.Context) (string, time.Duration, string, error) {
	resp, err := do(ctx, request{
		method: "GET",
		url:    "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token",
		// Required, and its absence is the whole anti-forgery mechanism: the
		// metadata server refuses any request without it, so a browser or a
		// naive server-side fetch cannot reach it.
		headers: map[string]string{"Metadata-Flavor": "Google"},
		// A second, like the other two link-local metadata services. Off
		// Google this name does not resolve, and waiting the shared ten seconds
		// to learn that would be ten seconds on every af up.
		timeout: time.Second,
	})
	if err != nil {
		return "", 0, "", wrap(ErrNotConfigured,
			"no Google credentials: GOOGLE_APPLICATION_CREDENTIALS is unset and the "+
				"metadata server did not answer, so this is not running on Google Cloud. "+
				"Point GOOGLE_APPLICATION_CREDENTIALS at a service account key, or run "+
				"somewhere with a service account attached")
	}
	if resp.status != 200 {
		return "", 0, "", fmt.Errorf("the metadata server answered %d", resp.status)
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := resp.decode(&payload); err != nil {
		return "", 0, "", err
	}
	return payload.AccessToken, time.Duration(payload.ExpiresIn) * time.Second,
		"this host's attached service account", nil
}

// Refresh discards the token so the next lookup acquires a new one.
func (g *GCPBackend) Refresh(ctx context.Context) error {
	g.mu.Lock()
	g.token, g.expires = "", time.Time{}
	g.mu.Unlock()
	_, err := g.bearer(ctx)
	return err
}

// Fetch reads a variable.
func (g *GCPBackend) Fetch(ctx context.Context, name string) (string, bool, error) {
	token, err := g.bearer(ctx)
	if err != nil {
		return "", false, err
	}

	secret := g.cfg.Prefix + name
	resp, err := do(ctx, request{
		method: "GET",
		url: g.cfg.Endpoint + "/v1/projects/" + g.cfg.Project +
			"/secrets/" + url.PathEscape(secret) + "/versions/" + g.cfg.Version + ":access",
		headers: map[string]string{"Authorization": "Bearer " + token, "Accept": "application/json"},
	})
	if err != nil {
		return "", false, fmt.Errorf("cannot be reached: %s", err)
	}

	g.mu.Lock()
	how := g.how
	g.mu.Unlock()

	switch {
	case resp.status == 200:
	case resp.status == 404:
		return "", false, nil
	case resp.rejected():
		return "", false, wrap(ErrRejected, "Secret Manager answered %d %s, using %s",
			resp.status, gcpErrorStatus(resp.body), how)
	default:
		return "", false, fmt.Errorf("Secret Manager answered %d %s",
			resp.status, gcpErrorStatus(resp.body))
	}

	var payload struct {
		Payload struct {
			Data string `json:"data"`
		} `json:"payload"`
	}
	if err := resp.decode(&payload); err != nil {
		return "", false, err
	}
	// Base64, always. Secret Manager holds bytes rather than text, and handing
	// the application the encoded form would produce a value that looks
	// plausible and authenticates against nothing.
	decoded, err := base64.StdEncoding.DecodeString(payload.Payload.Data)
	if err != nil {
		return "", false, fmt.Errorf("the payload of %s is not the base64 the API documents", secret)
	}
	return string(decoded), true, nil
}

func base64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// gcpErrorStatus reads the status out of a Google error document.
//
// The status and never the message. Google's message quotes the resource name,
// and the resource name is the secret.
func gcpErrorStatus(body []byte) string {
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
