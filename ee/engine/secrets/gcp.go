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
// Getting the token is not this file's job any more. The metadata server and
// the service account assertion both live in ee/engine/cloudauth, because a
// Cloud SQL provider needs the identical exchange and a second copy of a JWT
// signer is a second thing to get subtly wrong. What is chosen here is which
// of the two applies: a key on disk is worth avoiding where the metadata server
// exists, and the refusal message says which one was used.

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/antifailure/antifailure/ee/engine/cloudauth"
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
	// tokens holds the access token and knows which of the two ways it was
	// obtained, which is what the refusal message names.
	tokens *cloudauth.GCPTokenSource
}

// newGCPBackend wires a config and an optional service account key to a token
// source. A nil account takes the metadata server path.
func newGCPBackend(cfg GCPConfig, account *cloudauth.GCPServiceAccount) *GCPBackend {
	return &GCPBackend{
		cfg:    cfg,
		tokens: cloudauth.NewGCPTokenSource(account, cloudauth.ScopeGoogleCloudPlatform),
	}
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

	var account *cloudauth.GCPServiceAccount
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
		parsed, err := cloudauth.ParseGCPServiceAccount(raw)
		if err != nil {
			return nil, wrap(ErrNotConfigured, "the service account key is not usable: %s", err)
		}
		account = parsed
	}
	return New(newGCPBackend(cfg, account)), nil
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
	_, err := g.tokens.Token(ctx)
	return err
}

// Refresh discards the token so the next lookup acquires a new one.
func (g *GCPBackend) Refresh(ctx context.Context) error {
	g.tokens.Reset()
	_, err := g.tokens.Token(ctx)
	return err
}

// Fetch reads a variable.
func (g *GCPBackend) Fetch(ctx context.Context, name string) (string, bool, error) {
	token, err := g.tokens.Token(ctx)
	if err != nil {
		return "", false, err
	}

	secret := g.cfg.Prefix + name
	resp, err := cloudauth.Do(ctx, cloudauth.Request{
		Method: "GET",
		URL: g.cfg.Endpoint + "/v1/projects/" + g.cfg.Project +
			"/secrets/" + url.PathEscape(secret) + "/versions/" + g.cfg.Version + ":access",
		Headers: map[string]string{"Authorization": "Bearer " + token, "Accept": "application/json"},
	})
	if err != nil {
		return "", false, fmt.Errorf("cannot be reached: %s", err)
	}

	how := g.tokens.How()

	switch {
	case resp.Status == 200:
	case resp.Status == 404:
		return "", false, nil
	case resp.Rejected():
		return "", false, wrap(ErrRejected, "Secret Manager answered %d %s, using %s",
			resp.Status, cloudauth.GCPErrorStatus(resp.Body), how)
	default:
		return "", false, fmt.Errorf("Secret Manager answered %d %s",
			resp.Status, cloudauth.GCPErrorStatus(resp.Body))
	}

	var payload struct {
		Payload struct {
			Data string `json:"data"`
		} `json:"payload"`
	}
	if err := resp.Decode(&payload); err != nil {
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
