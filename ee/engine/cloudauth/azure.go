package cloudauth

// Microsoft Entra tokens, two ways.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// A service principal, which is a tenant, a client id and a client secret
// exchanged for a bearer token, and is what a CI runner outside Azure has. Or
// the managed identity the host provides, read from the same link-local address
// every Azure VM and container app carries, which needs no secret at all.
//
// The authority is a field rather than a constant, and that is not a
// generalisation for its own sake. Azure Government obtains tokens from
// login.microsoftonline.us and the China cloud from
// login.partner.microsoftonline.cn, and data residency is one of the reasons a
// customer asks for any of this, so a host compiled in would refuse exactly the
// customers the feature exists for.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AzureKeyVaultResource is the resource a Key Vault token is minted for.
const AzureKeyVaultResource = "https://vault.azure.net"

// PublicAzureAuthority is where a token comes from in the public cloud.
const PublicAzureAuthority = "https://login.microsoftonline.com"

// AzureTokenSource hands out a bearer token and holds it until it expires.
type AzureTokenSource struct {
	// TenantID, ClientID and ClientSecret authenticate a service principal.
	// Leave ClientSecret empty to use the managed identity the host provides,
	// in which case ClientID still names which of several identities to use.
	TenantID     string
	ClientID     string
	ClientSecret string
	// Authority is where a token is obtained. Empty means the public cloud.
	Authority string
	// Resource is what the token is for, as https://vault.azure.net. The v2
	// scope a service principal asks for is this with "/.default" appended,
	// which is how Entra names the same thing on its two endpoints, so there is
	// one field rather than two that have to agree.
	Resource string

	mu      sync.Mutex
	token   string
	expires time.Time
	// how names where the token came from, for the refusal message.
	how string
}

// authority is the token host, defaulted here as well as by a caller so that a
// source built directly in a test is not pointed at nothing.
func (s *AzureTokenSource) authority() string {
	if s.Authority == "" {
		return PublicAzureAuthority
	}
	return strings.TrimRight(s.Authority, "/")
}

func (s *AzureTokenSource) resource() string {
	if s.Resource == "" {
		return AzureKeyVaultResource
	}
	return strings.TrimRight(s.Resource, "/")
}

// How names where the current token came from, for a refusal message.
func (s *AzureTokenSource) How() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.how
}

// Reset discards the token so the next call acquires a new one.
func (s *AzureTokenSource) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token, s.expires = "", time.Time{}
}

// Token returns a bearer token, acquiring one when the held token is missing or
// nearly expired.
func (s *AzureTokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	token, expires := s.token, s.expires
	s.mu.Unlock()
	// A minute early, so a token that expires between being read and being used
	// does not produce a rejection a renewal would have avoided.
	if token != "" && time.Now().Add(time.Minute).Before(expires) {
		return token, nil
	}

	got, lifetime, how, err := s.acquire(ctx)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.token, s.expires, s.how = got, time.Now().Add(lifetime), how
	s.mu.Unlock()
	return got, nil
}

func (s *AzureTokenSource) acquire(ctx context.Context) (token string, lifetime time.Duration, how string, err error) {
	if s.ClientSecret != "" {
		return s.fromServicePrincipal(ctx)
	}
	return s.fromManagedIdentity(ctx)
}

func (s *AzureTokenSource) fromServicePrincipal(ctx context.Context) (string, time.Duration, string, error) {
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {s.ClientID},
		"client_secret": {s.ClientSecret},
		"scope":         {s.resource() + "/.default"},
	}
	resp, err := Do(ctx, Request{
		Method: "POST",
		URL:    s.authority() + "/" + s.TenantID + "/oauth2/v2.0/token",
		Body:   []byte(form.Encode()),
		Headers: map[string]string{
			"Content-Type": "application/x-www-form-urlencoded",
			"Accept":       "application/json",
		},
	})
	if err != nil {
		return "", 0, "", fmt.Errorf("Microsoft Entra could not be reached: %s", err)
	}
	if resp.Status != 200 {
		// A wrong client secret and a wrong tenant both land here, and Entra's
		// own error code is the part that tells them apart, so it is passed
		// through. The description is not: it embeds the request and can run to
		// several lines.
		return "", 0, "", Wrap(ErrRejected,
			"Microsoft Entra refused the service principal with %d %s",
			resp.Status, AzureErrorCode(resp.Body))
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := resp.Decode(&payload); err != nil {
		return "", 0, "", err
	}
	if payload.AccessToken == "" {
		return "", 0, "", fmt.Errorf("Microsoft Entra answered 200 and returned no token")
	}
	return payload.AccessToken, time.Duration(payload.ExpiresIn) * time.Second,
		"the service principal " + s.ClientID, nil
}

func (s *AzureTokenSource) fromManagedIdentity(ctx context.Context) (string, time.Duration, string, error) {
	query := map[string]string{"api-version": "2018-02-01", "resource": s.resource()}
	if s.ClientID != "" {
		// A user-assigned identity has to be named, because a host may carry
		// several and the service will not guess between them.
		query["client_id"] = s.ClientID
	}
	resp, err := Do(ctx, Request{
		Method: "GET", URL: "http://169.254.169.254/metadata/identity/oauth2/token",
		Query: query, Headers: map[string]string{"Metadata": "true"},
		// The same second the AWS instance metadata gets, for the same reason:
		// this is a link-local address that answers immediately on a host that
		// has one and hangs on a laptop that does not.
		Timeout: time.Second,
	})
	if err != nil {
		return "", 0, "", Wrap(ErrNotConfigured,
			"no Azure credentials: AZURE_CLIENT_SECRET is unset and no managed identity "+
				"answered on this host. Set AZURE_TENANT_ID, AZURE_CLIENT_ID and "+
				"AZURE_CLIENT_SECRET, or run somewhere with an identity assigned")
	}
	if resp.Status != 200 {
		return "", 0, "", fmt.Errorf(
			"the managed identity endpoint answered %d; this host may have no identity assigned",
			resp.Status)
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		// Returned as a string of seconds by this endpoint, unlike Entra's,
		// which returns a number. Two shapes for one field on two endpoints of
		// one product, so it is decoded as text and converted.
		ExpiresIn string `json:"expires_in"`
	}
	if err := resp.Decode(&payload); err != nil {
		return "", 0, "", err
	}
	seconds, _ := strconv.Atoi(payload.ExpiresIn)
	if seconds <= 0 {
		seconds = 3600
	}
	return payload.AccessToken, time.Duration(seconds) * time.Second, "this host's managed identity", nil
}

// AzureErrorCode reads the code out of an Azure error document.
//
// The code and never the message. Azure's message embeds the request, and the
// request names the secret and the vault.
func AzureErrorCode(body []byte) string {
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
