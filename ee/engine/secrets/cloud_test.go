// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package secrets

// Azure Key Vault and Google Secret Manager, as far as they can honestly be
// taken without an account.
//
// Two different kinds of proof here and it is worth being precise about which
// is which, because calling both of them "tested" is how a row in STATUS.md
// stops meaning anything.
//
// The first kind is real. The service account assertion is signed with a real
// RSA key and verified with its public half, so the signing is proved rather
// than asserted. The Key Vault name mapping is a pure function over a real
// constraint. Neither needs a cloud account and neither is a stand-in.
//
// The second kind is not. The conformance runs below are against a local
// server speaking the documented wire format, which proves what the ADAPTER
// does with each response and proves nothing about whether the request is one
// the service accepts. That is a real gap and it is why these rows stay
// `written` in STATUS.md rather than `proven`. What it does catch is the class
// of mistake the contract exists to prevent: a 404 reported as a failure rather
// than a miss, a 403 reported as a miss rather than a refusal, a token renewed
// on every lookup. Those are adapter bugs and they are catchable here.
//
// The AWS adapter gets a third and better kind: its signature is checked
// against the example AWS publishes, which is the service's own answer. There
// is no equivalent published vector for the other two.

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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Azure Key Vault
// ---------------------------------------------------------------------------

func TestAzureMapsAVariableNameOntoANameKeyVaultAccepts(t *testing.T) {
	// The trap this adapter exists to not fall into. A Key Vault secret name
	// may hold only letters, digits and hyphens, and an environment variable is
	// conventionally SCREAMING_SNAKE_CASE, so DATABASE_URL is not a name the
	// service will accept and a request for it comes back 400.
	require.Equal(t, "DATABASE-URL", AzureSecretName("DATABASE_URL"))
	require.Equal(t, "STRIPE-SECRET-KEY", AzureSecretName("STRIPE_SECRET_KEY"))
	require.Equal(t, "already-fine", AzureSecretName("already-fine"))

	// Anything else is refused rather than stripped. Stripping would map two
	// different variables onto one secret, which is a way to hand an
	// application a credential that belongs to something else.
	require.Empty(t, AzureSecretName("HAS SPACE"))
	require.Empty(t, AzureSecretName("HAS.DOT"))
	require.Empty(t, AzureSecretName("HAS/SLASH"))
	require.Empty(t, AzureSecretName(""))
	// A leading hyphen is refused by the service, and _FOO would produce one.
	require.Empty(t, AzureSecretName("_LEADING"))
}

func TestAzureSaysSoRatherThanLookingUpANameItCannotHold(t *testing.T) {
	// A 400 treated as a miss would make the variable invisible while the
	// operator looks at a vault that plainly contains something like it.
	backend := &AzureBackend{cfg: AzureConfig{VaultURL: "https://v.vault.azure.net"}}
	_, found, err := backend.Fetch(t.Context(), "HAS SPACE")
	require.False(t, found)
	require.Error(t, err)
	require.Contains(t, err.Error(), "letters, digits and hyphens")
}

func TestAzureNamesTheMappingInItsDescription(t *testing.T) {
	// The description is what AF-SEC-001 prints. Somebody looking at that list
	// after storing DATABASE_URL and being told it was not found needs to be
	// able to see, without reading the source, that the vault is holding
	// DATABASE-URL.
	source, err := NewAzureKeyVault(AzureConfig{
		VaultURL: "https://af-secrets.vault.azure.net",
		Getenv:   func(string) string { return "" },
	})
	require.NoError(t, err)
	require.Contains(t, source.Name(), "af-secrets.vault.azure.net")
	require.Contains(t, source.Name(), "underscores become hyphens")
}

func TestAzureUsesTheAuthorityItIsGiven(t *testing.T) {
	// Azure Government and the China cloud obtain tokens from different hosts,
	// and data residency is one of the reasons a customer asks for this feature
	// at all. A source with the public host compiled into it would refuse
	// exactly the customers it exists for.
	source, err := NewAzureKeyVault(AzureConfig{
		VaultURL: "https://v.vault.usgovcloudapi.net",
		Getenv: func(name string) string {
			if name == "AZURE_AUTHORITY_HOST" {
				return "https://login.microsoftonline.us"
			}
			return ""
		},
	})
	require.NoError(t, err)
	require.Equal(t, "https://login.microsoftonline.us",
		source.backend.(*AzureBackend).cfg.authority())
}

func TestAzureRefusesToBeBuiltWithoutWhatItNeeds(t *testing.T) {
	_, err := NewAzureKeyVault(AzureConfig{Getenv: func(string) string { return "" }})
	require.ErrorIs(t, err, ErrNotConfigured)
	require.Contains(t, err.Error(), "AZURE_KEY_VAULT_URL")

	// A client secret with no tenant is a half-finished configuration, and
	// falling through to the managed identity path would report "no identity on
	// this host" for somebody who plainly configured a service principal.
	_, err = NewAzureKeyVault(AzureConfig{
		VaultURL: "https://v.vault.azure.net", ClientSecret: "assembled-at-run-time",
		Getenv: func(string) string { return "" },
	})
	require.ErrorIs(t, err, ErrNotConfigured)
	require.Contains(t, err.Error(), "AZURE_TENANT_ID")
}

func TestAzureConformanceAgainstTheDocumentedWireFormat(t *testing.T) {
	// Not a proof that Key Vault accepts these requests. A proof that the
	// adapter treats each documented response the way the contract requires.
	server := fakeKeyVault(t)

	working := &AzureBackend{cfg: AzureConfig{
		VaultURL: server.URL + "/ok", Authority: server.URL, TenantID: "t", ClientID: "c",
		ClientSecret: "assembled-at-run-time", APIVersion: "7.4",
	}}
	rejecting := &countingAzure{AzureBackend: &AzureBackend{cfg: AzureConfig{
		VaultURL: server.URL + "/denied", Authority: server.URL, TenantID: "t", ClientID: "c",
		ClientSecret: "assembled-at-run-time", APIVersion: "7.4",
	}}}
	// No client secret, so this one takes the managed identity path, and there
	// is no managed identity on the machine running the tests. That is the
	// realistic shape of an unreachable Azure source: not a vault that refuses
	// a connection, but a host that cannot obtain a token at all.
	unreachable := &AzureBackend{cfg: AzureConfig{
		VaultURL: "http://127.0.0.1:1", APIVersion: "7.4",
	}}

	result := Run(t.Context(), t, Harness{
		Name:         "Azure Key Vault (documented wire format, not a live vault)",
		Working:      New(working),
		Present:      "DATABASE_URL",
		PresentValue: "postgres://azure",
		Empty:        "BLANK",
		Absent:       "NOT_IN_THE_VAULT",
		Rejecting:    New(rejecting),
		Refreshes:    rejecting.count,
		Unreachable:  New(unreachable),
	})
	require.Empty(t, result.Failed)
	require.Empty(t, result.Skipped)
}

type countingAzure struct {
	*AzureBackend
	renewals int
}

func (c *countingAzure) Refresh(ctx context.Context) error {
	c.renewals++
	return c.AzureBackend.Refresh(ctx)
}
func (c *countingAzure) count() int { return c.renewals }

// fakeKeyVault answers the way the Key Vault and Entra documentation say the
// real ones do. It stands in for the service and does not stand in for the
// adapter.
func fakeKeyVault(t *testing.T) *httptest.Server {
	t.Helper()
	values := map[string]string{"DATABASE-URL": "postgres://azure", "BLANK": ""}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/oauth2/v2.0/token"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"a-token","expires_in":3600}`))

		case strings.HasPrefix(r.URL.Path, "/denied/secrets/"):
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"Forbidden","message":"..."}}`))

		case strings.HasPrefix(r.URL.Path, "/ok/secrets/"):
			name := strings.TrimPrefix(r.URL.Path, "/ok/secrets/")
			value, ok := values[name]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"code":"SecretNotFound","message":"..."}}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"value": value})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// ---------------------------------------------------------------------------
// Google Secret Manager
// ---------------------------------------------------------------------------

func TestGCPSignsAnAssertionThatVerifies(t *testing.T) {
	// The real proof in this file. The assertion is signed with a real RSA key
	// and checked with its public half, so RS256, the base64url encoding of
	// each segment, and the exact bytes that are signed are all verified rather
	// than assumed. A signature Google refuses looks identical to a wrong
	// service account from the outside.
	key, keyJSON := serviceAccountKey(t)
	source, err := NewGCPSecretManager(GCPConfig{
		Project: "af-test", CredentialsJSON: keyJSON, Getenv: func(string) string { return "" },
	})
	require.NoError(t, err)
	backend := source.backend.(*GCPBackend)
	require.NotNil(t, backend.account, "the key was not parsed")

	assertion, err := backend.signAssertion(time.Now())
	require.NoError(t, err)

	parts := strings.Split(assertion, ".")
	require.Len(t, parts, 3, "a JWT is three segments")

	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	require.NoError(t, err)
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	require.NoError(t,
		rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature),
		"the assertion does not verify against its own key, so Google would refuse it")

	var header struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	requireSegment(t, parts[0], &header)
	require.Equal(t, "RS256", header.Alg)
	require.Equal(t, "JWT", header.Typ)

	var claims struct {
		Iss   string `json:"iss"`
		Aud   string `json:"aud"`
		Scope string `json:"scope"`
		Exp   int64  `json:"exp"`
		Iat   int64  `json:"iat"`
	}
	requireSegment(t, parts[1], &claims)
	require.Equal(t, "af-test@example.iam.gserviceaccount.com", claims.Iss)
	// The audience is the token endpoint, which is what stops an assertion
	// minted for one service being replayed against another.
	require.Equal(t, "https://oauth2.googleapis.com/token", claims.Aud)
	require.Equal(t, gcpScope, claims.Scope)
	require.Equal(t, int64(3600), claims.Exp-claims.Iat)
}

func TestGCPDropsThePrivateKeyPEMOnceItIsParsed(t *testing.T) {
	// The plaintext of a private key should not sit in a struct for the life of
	// the process when the parsed form is what is used.
	_, keyJSON := serviceAccountKey(t)
	source, err := NewGCPSecretManager(GCPConfig{
		Project: "af-test", CredentialsJSON: keyJSON, Getenv: func(string) string { return "" },
	})
	require.NoError(t, err)
	require.Empty(t, source.backend.(*GCPBackend).account.PrivateKey)
}

func TestGCPRefusesAKeyItCannotUse(t *testing.T) {
	for _, tc := range []struct{ name, json, want string }{
		{"not JSON", `not json at all`, "not JSON"},
		{"a user key", `{"type":"authorized_user"}`, "authorized_user"},
		{"no private key", `{"type":"service_account","client_email":"a@b"}`, "no private_key"},
		{"a private key that is not PEM", `{"type":"service_account","client_email":"a@b","private_key":"nope"}`, "not PEM"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewGCPSecretManager(GCPConfig{
				Project: "p", CredentialsJSON: []byte(tc.json),
				Getenv: func(string) string { return "" },
			})
			require.ErrorIs(t, err, ErrNotConfigured)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestGCPRefusesToBeBuiltWithoutAProject(t *testing.T) {
	_, err := NewGCPSecretManager(GCPConfig{Getenv: func(string) string { return "" }})
	require.ErrorIs(t, err, ErrNotConfigured)
	require.Contains(t, err.Error(), "GOOGLE_CLOUD_PROJECT")
}

func TestGCPConformanceAgainstTheDocumentedWireFormat(t *testing.T) {
	// As with Key Vault: this proves what the adapter does with each documented
	// response and proves nothing about whether Google accepts the request.
	// What it does prove is the payload decoding, which is the mistake most
	// worth catching here: Secret Manager returns the value base64 encoded, and
	// handing the application the encoded form produces a credential that looks
	// plausible and authenticates against nothing.
	server := fakeSecretManager(t)

	working := &GCPBackend{
		cfg:     GCPConfig{Project: "ok", Version: "latest", Endpoint: server.URL},
		account: tokenEndpoint(t, server.URL)}
	rejecting := &countingGCP{GCPBackend: &GCPBackend{
		cfg:     GCPConfig{Project: "denied", Version: "latest", Endpoint: server.URL},
		account: tokenEndpoint(t, server.URL)}}
	unreachable := &GCPBackend{
		cfg:     GCPConfig{Project: "ok", Version: "latest", Endpoint: "http://127.0.0.1:1"},
		account: tokenEndpoint(t, "http://127.0.0.1:1")}

	result := Run(t.Context(), t, Harness{
		Name:         "Google Secret Manager (documented wire format, not a live project)",
		Working:      New(working),
		Present:      "DATABASE_URL",
		PresentValue: "postgres://google",
		Empty:        "BLANK",
		Absent:       "NOT_IN_THE_PROJECT",
		Rejecting:    New(rejecting),
		Refreshes:    rejecting.count,
		Unreachable:  New(unreachable),
	})
	require.Empty(t, result.Failed)
	require.Empty(t, result.Skipped)
}

type countingGCP struct {
	*GCPBackend
	renewals int
}

func (c *countingGCP) Refresh(ctx context.Context) error {
	c.renewals++
	return c.GCPBackend.Refresh(ctx)
}
func (c *countingGCP) count() int { return c.renewals }

// serviceAccountKey builds a real key at run time.
//
// Generated rather than written into the repository. A private key in a test
// fixture is a private key in a repository, and the rule about that has no
// exception for keys that guard nothing: a scanner cannot tell, and a person
// skimming a diff cannot either.
func serviceAccountKey(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()
	// 2048 rather than 4096, because this is generated on every run and the
	// difference is a second of test time for no additional proof.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	encoded := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	document, err := json.Marshal(map[string]string{
		"type":         "service_account",
		"project_id":   "af-test",
		"client_email": "af-test@example.iam.gserviceaccount.com",
		"private_key":  string(encoded),
		"token_uri":    "https://oauth2.googleapis.com/token",
	})
	require.NoError(t, err)
	return key, document
}

// tokenEndpoint builds an account whose token endpoint is the local server, so
// the adapter takes the service account path rather than trying to reach
// Google's metadata server, which does not answer here and should not be asked.
func tokenEndpoint(t *testing.T, base string) *gcpServiceAccount {
	t.Helper()
	_, keyJSON := serviceAccountKey(t)
	account, err := parseServiceAccount(keyJSON)
	require.NoError(t, err)
	account.TokenURI = base + "/token"
	return account
}

func fakeSecretManager(t *testing.T) *httptest.Server {
	t.Helper()
	values := map[string]string{"DATABASE_URL": "postgres://google", "BLANK": ""}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"a-token","expires_in":3600}`))
			return
		}

		// The path the adapter built, checked rather than ignored. A test that
		// answered any path would pass with the resource name assembled wrongly,
		// which is the one thing this can still catch about the request.
		project, secret, version, ok := parseAccessPath(r.URL.Path)
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if version != "latest" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if project == "denied" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"status":"PERMISSION_DENIED","message":"..."}}`))
			return
		}
		value, found := values[secret]
		if !found {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"status":"NOT_FOUND","message":"..."}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// Base64, as the API documents. An adapter that skipped the decode
		// would hand the application this string.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"payload": map[string]string{"data": base64.StdEncoding.EncodeToString([]byte(value))},
		})
	}))
	t.Cleanup(server.Close)
	return server
}

// parseAccessPath reads /v1/projects/{p}/secrets/{s}/versions/{v}:access.
func parseAccessPath(path string) (project, secret, version string, ok bool) {
	rest, found := strings.CutPrefix(path, "/v1/projects/")
	if !found {
		return "", "", "", false
	}
	project, rest, found = strings.Cut(rest, "/secrets/")
	if !found {
		return "", "", "", false
	}
	secret, rest, found = strings.Cut(rest, "/versions/")
	if !found {
		return "", "", "", false
	}
	version, found = strings.CutSuffix(rest, ":access")
	return project, secret, version, found
}

func requireSegment(t *testing.T, segment string, into any) {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, into))
}
