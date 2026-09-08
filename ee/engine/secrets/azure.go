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
// Obtaining it, from a service principal or from the host's managed identity,
// lives in ee/engine/cloudauth, because an Azure Database for PostgreSQL
// provider authenticates through the identical exchange against a different
// resource.

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/antifailure/antifailure/ee/engine/cloudauth"
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
	// tokens holds the bearer token and knows whether it came from a service
	// principal or from the host's managed identity, which is what the refusal
	// message names.
	tokens *cloudauth.AzureTokenSource
}

// newAzureBackend wires a config to a token source for the vault resource.
func newAzureBackend(cfg AzureConfig) *AzureBackend {
	return &AzureBackend{cfg: cfg, tokens: &cloudauth.AzureTokenSource{
		TenantID:     cfg.TenantID,
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Authority:    cfg.Authority,
		Resource:     cloudauth.AzureKeyVaultResource,
	}}
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
		cfg.Authority = cloudauth.PublicAzureAuthority
	}
	cfg.Authority = strings.TrimRight(cfg.Authority, "/")
	cfg.VaultURL = strings.TrimRight(cfg.VaultURL, "/")
	return New(newAzureBackend(cfg)), nil
}

func (a *AzureBackend) Describe() string {
	return "Azure Key Vault at " + a.cfg.VaultURL + " (a variable's underscores become hyphens)"
}

// Reach acquires a token and then proves the vault itself answers.
//
// Both halves are needed and for a while this did only the first. Acquiring the
// token is what fails for a wrong tenant, an expired client secret, or a host
// with no managed identity, so it looked like the whole story. It is not:
// Microsoft Entra is a different host from the vault, so a vault behind a
// private endpoint, a firewall rule, a typo in the name, or a DNS failure still
// hands back a perfectly good token. Reach then reported the source usable,
// Available said nothing was wrong, and AF-SEC-001 listed Key Vault as a place
// the value could have come from while nothing there could be read.
//
// A local server standing in for Azure could never have caught it, because the
// fake is one process serving both the token endpoint and the vault, so
// pointing the test at a dead address broke them together. The real service
// separated them and the conformance suite said "a source pointed at nothing
// reports itself usable" on the first live run.
//
// ANY answer from the vault proves it is reachable, including a refusal, and
// that distinction is the whole point of the second call. 403 in particular is
// the normal state of a correctly configured source: Key Vault Secrets User
// grants get and not list, so the principal that is supposed to be here cannot
// list and must not be reported as unreachable for it. Whether a credential is
// refused is Fetch's question and it is answered per variable, because a
// principal may hold one secret and not another.
func (a *AzureBackend) Reach(ctx context.Context) error {
	if _, err := a.tokens.Token(ctx); err != nil {
		return err
	}
	if _, err := cloudauth.Do(ctx, cloudauth.Request{
		Method: "GET",
		URL:    a.cfg.VaultURL + "/secrets",
		Query:  map[string]string{"api-version": a.cfg.APIVersion, "maxresults": "1"},
	}); err != nil {
		return fmt.Errorf("cannot be reached: %s", err)
	}
	return nil
}

// Refresh discards the token so the next lookup acquires a new one.
func (a *AzureBackend) Refresh(ctx context.Context) error {
	a.tokens.Reset()
	_, err := a.tokens.Token(ctx)
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

	token, err := a.tokens.Token(ctx)
	if err != nil {
		return "", false, err
	}
	resp, err := cloudauth.Do(ctx, cloudauth.Request{
		Method:  "GET",
		URL:     a.cfg.VaultURL + "/secrets/" + secretName,
		Query:   map[string]string{"api-version": a.cfg.APIVersion},
		Headers: map[string]string{"Authorization": "Bearer " + token, "Accept": "application/json"},
	})
	if err != nil {
		return "", false, fmt.Errorf("cannot be reached: %s", err)
	}

	how := a.tokens.How()

	switch {
	case resp.Status == 200:
	case resp.Status == 404:
		return "", false, nil
	case resp.Rejected():
		return "", false, wrap(ErrRejected, "Key Vault answered %d %s, using %s",
			resp.Status, cloudauth.AzureErrorCode(resp.Body), how)
	default:
		return "", false, fmt.Errorf("Key Vault answered %d %s",
			resp.Status, cloudauth.AzureErrorCode(resp.Body))
	}

	var payload struct {
		Value string `json:"value"`
	}
	if err := resp.Decode(&payload); err != nil {
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
