package secrets

// Azure Key Vault.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Two things about Key Vault shape this adapter and neither is obvious.
//
// A secret name may contain only letters, digits and hyphens. An environment
// variable name is conventionally SCREAMING_SNAKE_CASE, so DATABASE_URL is not
// a name Key Vault will accept and never was: a request for it comes back 400,
// and a 400 that fell through as a miss would make every underscored variable
// invisible with nothing said. The mapping is done here, once, and reported in
// the source's description so nobody has to discover it from a stack trace.
//
// The credential is a bearer token that expires, usually in an hour. That is
// the case the one-refresh rule exists for, and it is why the token is fetched
// with an expiry and renewed a minute early rather than on rejection alone.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AzureConfig is what a Key Vault source needs.
type AzureConfig struct {
	// VaultURL is the vault, as https://af-secrets.vault.azure.net.
	VaultURL string
	// TenantID, ClientID and ClientSecret authenticate a service principal.
	// Leave them empty to use the managed identity the host provides.
	TenantID     string
	ClientID     string
	ClientSecret string
	// APIVersion overrides the data plane version. Empty means 7.4.
	APIVersion string
	// Authority is where a token is obtained. Empty means the public cloud.
	//
	// Not the same host everywhere: Azure Government is
	// login.microsoftonline.us and the China cloud is
	// login.partner.microsoftonline.cn, and an organization in either of those
	// cannot use a source that has the public one compiled into it. Data
	// residency is one of the reasons a customer asks for this feature at all,
	// so hard-coding the host would refuse exactly the customers it is for.
	Authority string
	// Getenv is injected so a test does not have to mutate the process
	// environment.
	Getenv func(string) string
}

// AzureBackend reads from Key Vault.
type AzureBackend struct {
	cfg AzureConfig

	mu      sync.Mutex
	token   string
	expires time.Time
	// how names where the token came from, for the refusal message.
	how string
}

// NewAzureKeyVault builds a Key Vault source, or reports what it is missing.
func NewAzureKeyVault(cfg AzureConfig) (*Source, error) {
	if cfg.Getenv == nil {
		cfg.Getenv = os.Getenv
	}
	if strings.TrimSpace(cfg.VaultURL) == "" {
		cfg.VaultURL = cfg.Getenv("AZURE_KEY_VAULT_URL")
	}
	if strings.TrimSpace(cfg.VaultURL) == "" {
		return nil, wrap(ErrNotConfigured,
			"Azure Key Vault needs the vault's URL (AZURE_KEY_VAULT_URL), "+
				"as https://your-vault.vault.azure.net")
	}
	parsed, err := url.Parse(cfg.VaultURL)
	if err != nil || parsed.Host == "" {
		return nil, wrap(ErrNotConfigured, "the Key Vault URL is not a URL")
	}
	if cfg.TenantID == "" {
		cfg.TenantID = cfg.Getenv("AZURE_TENANT_ID")
	}
	if cfg.ClientID == "" {
		cfg.ClientID = cfg.Getenv("AZURE_CLIENT_ID")
	}
	if cfg.ClientSecret == "" {
		cfg.ClientSecret = cfg.Getenv("AZURE_CLIENT_SECRET")
	}
	if cfg.ClientSecret != "" && (cfg.TenantID == "" || cfg.ClientID == "") {
		return nil, wrap(ErrNotConfigured,
			"a Key Vault client secret was given without AZURE_TENANT_ID and AZURE_CLIENT_ID")
	}
	if cfg.APIVersion == "" {
		cfg.APIVersion = "7.4"
	}
	if cfg.Authority == "" {
		cfg.Authority = cfg.Getenv("AZURE_AUTHORITY_HOST")
	}
	if cfg.Authority == "" {
		cfg.Authority = "https://login.microsoftonline.com"
	}
	cfg.Authority = strings.TrimRight(cfg.Authority, "/")
	cfg.VaultURL = strings.TrimRight(cfg.VaultURL, "/")
	return New(&AzureBackend{cfg: cfg}), nil
}

// authority is the token host, defaulted here as well as in the constructor so
// that a backend built directly in a test is not pointed at nothing.
func (c AzureConfig) authority() string {
	if c.Authority == "" {
		return "https://login.microsoftonline.com"
	}
	return strings.TrimRight(c.Authority, "/")
}

func (a *AzureBackend) Describe() string {
	return "Azure Key Vault at " + a.cfg.VaultURL + " (a variable's underscores become hyphens)"
}

// Reach acquires a token.
//
// A real call, and the right one to make: acquiring the token is what actually
// fails, through a wrong tenant, an expired client secret, or a host with no
// managed identity assigned, and it costs nothing at the vault.
func (a *AzureBackend) Reach(ctx context.Context) error {
	_, err := a.bearer(ctx)
	return err
}

func (a *AzureBackend) bearer(ctx context.Context) (string, error) {
	a.mu.Lock()
	token, expires := a.token, a.expires
	a.mu.Unlock()
	// A minute early, so a token that expires between being read and being used
	// does not produce a rejection a renewal would have avoided.
	if token != "" && time.Now().Add(time.Minute).Before(expires) {
		return token, nil
	}

	got, lifetime, how, err := a.acquire(ctx)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	a.token, a.expires, a.how = got, time.Now().Add(lifetime), how
	a.mu.Unlock()
	return got, nil
}

const azureScope = "https://vault.azure.net/.default"

func (a *AzureBackend) acquire(ctx context.Context) (token string, lifetime time.Duration, how string, err error) {
	if a.cfg.ClientSecret != "" {
		return a.fromServicePrincipal(ctx)
	}
	return a.fromManagedIdentity(ctx)
}

func (a *AzureBackend) fromServicePrincipal(ctx context.Context) (string, time.Duration, string, error) {
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {a.cfg.ClientID},
		"client_secret": {a.cfg.ClientSecret},
		"scope":         {azureScope},
	}
	resp, err := do(ctx, request{
		method: "POST",
		url:    a.cfg.authority() + "/" + a.cfg.TenantID + "/oauth2/v2.0/token",
		body:   []byte(form.Encode()),
		headers: map[string]string{
			"Content-Type": "application/x-www-form-urlencoded",
			"Accept":       "application/json",
		},
	})
	if err != nil {
		return "", 0, "", fmt.Errorf("Microsoft Entra could not be reached: %s", err)
	}
	if resp.status != 200 {
		// A wrong client secret and a wrong tenant both land here, and Entra's
		// own error code is the part that tells them apart, so it is passed
		// through. The description is not: it embeds the request and can run to
		// several lines.
		return "", 0, "", wrap(ErrRejected,
			"Microsoft Entra refused the service principal with %d %s",
			resp.status, azureErrorCode(resp.body))
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := resp.decode(&payload); err != nil {
		return "", 0, "", err
	}
	if payload.AccessToken == "" {
		return "", 0, "", fmt.Errorf("Microsoft Entra answered 200 and returned no token")
	}
	return payload.AccessToken, time.Duration(payload.ExpiresIn) * time.Second,
		"the service principal " + a.cfg.ClientID, nil
}

func (a *AzureBackend) fromManagedIdentity(ctx context.Context) (string, time.Duration, string, error) {
	query := map[string]string{"api-version": "2018-02-01", "resource": "https://vault.azure.net"}
	if a.cfg.ClientID != "" {
		// A user-assigned identity has to be named, because a host may carry
		// several and the service will not guess between them.
		query["client_id"] = a.cfg.ClientID
	}
	resp, err := do(ctx, request{
		method: "GET", url: "http://169.254.169.254/metadata/identity/oauth2/token",
		query: query, headers: map[string]string{"Metadata": "true"},
		// The same second the AWS instance metadata gets, for the same reason:
		// this is a link-local address that answers immediately on a host that
		// has one and hangs on a laptop that does not.
		timeout: time.Second,
	})
	if err != nil {
		return "", 0, "", wrap(ErrNotConfigured,
			"no Azure credentials: AZURE_CLIENT_SECRET is unset and no managed identity "+
				"answered on this host. Set AZURE_TENANT_ID, AZURE_CLIENT_ID and "+
				"AZURE_CLIENT_SECRET, or run somewhere with an identity assigned")
	}
	if resp.status != 200 {
		return "", 0, "", fmt.Errorf(
			"the managed identity endpoint answered %d; this host may have no identity assigned",
			resp.status)
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		// Returned as a string of seconds by this endpoint, unlike Entra's,
		// which returns a number. Two shapes for one field on two endpoints of
		// one product, so it is decoded as text and converted.
		ExpiresIn string `json:"expires_in"`
	}
	if err := resp.decode(&payload); err != nil {
		return "", 0, "", err
	}
	seconds, _ := strconv.Atoi(payload.ExpiresIn)
	if seconds <= 0 {
		seconds = 3600
	}
	return payload.AccessToken, time.Duration(seconds) * time.Second, "this host's managed identity", nil
}

// Refresh discards the token so the next lookup acquires a new one.
func (a *AzureBackend) Refresh(ctx context.Context) error {
	a.mu.Lock()
	a.token, a.expires = "", time.Time{}
	a.mu.Unlock()
	_, err := a.bearer(ctx)
	return err
}

// Fetch reads a variable.
func (a *AzureBackend) Fetch(ctx context.Context, name string) (string, bool, error) {
	secretName := AzureSecretName(name)
	if secretName == "" {
		// Reported rather than looked up. A name Key Vault cannot hold produces
		// a 400, and a 400 treated as a miss would make the variable invisible
		// while the operator looks at a vault that plainly contains it.
		return "", false, fmt.Errorf(
			"%q cannot be a Key Vault secret name: names may hold only letters, "+
				"digits and hyphens", name)
	}

	token, err := a.bearer(ctx)
	if err != nil {
		return "", false, err
	}
	resp, err := do(ctx, request{
		method:  "GET",
		url:     a.cfg.VaultURL + "/secrets/" + secretName,
		query:   map[string]string{"api-version": a.cfg.APIVersion},
		headers: map[string]string{"Authorization": "Bearer " + token, "Accept": "application/json"},
	})
	if err != nil {
		return "", false, fmt.Errorf("cannot be reached: %s", err)
	}

	a.mu.Lock()
	how := a.how
	a.mu.Unlock()

	switch {
	case resp.status == 200:
	case resp.status == 404:
		return "", false, nil
	case resp.rejected():
		return "", false, wrap(ErrRejected, "Key Vault answered %d %s, using %s",
			resp.status, azureErrorCode(resp.body), how)
	default:
		return "", false, fmt.Errorf("Key Vault answered %d %s",
			resp.status, azureErrorCode(resp.body))
	}

	var payload struct {
		Value string `json:"value"`
	}
	if err := resp.decode(&payload); err != nil {
		return "", false, err
	}
	return payload.Value, true, nil
}

// AzureSecretName maps a variable name onto a name Key Vault will accept.
//
// Underscores become hyphens, which is the convention every Azure tutorial
// uses, and anything else outside letters, digits and hyphens makes the name
// unusable and is reported rather than silently stripped. Stripping would map
// two different variables onto one secret, which is a way to hand an
// application the wrong credential.
func AzureSecretName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == '_':
			b.WriteByte('-')
		default:
			return ""
		}
	}
	out := b.String()
	if out == "" || strings.HasPrefix(out, "-") {
		// Key Vault refuses a name that starts with a hyphen, and a variable
		// named _FOO would produce one.
		return ""
	}
	return out
}

// azureErrorCode reads the code out of an Azure error document.
//
// The code and never the message. Azure's message embeds the request, and the
// request names the secret and the vault.
func azureErrorCode(body []byte) string {
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Error.Code == "" {
		return "with no error code"
	}
	return payload.Error.Code
}
