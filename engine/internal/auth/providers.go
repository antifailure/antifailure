package auth

// `af provider`, client side.
//
// These live beside the credential rather than in the control plane client,
// because they are the only calls the engine makes with the PERSONAL token --
// the one af login stored -- rather than with an engine token. An engine token
// belongs to a machine and can write events; it deliberately has no identity
// and no way to touch a secret. Putting these next to the thing that holds the
// personal token keeps that distinction visible instead of leaving two clients
// that look interchangeable and are not.
//
// One rule shapes the whole file: a key travels UP and never comes back. There
// is a Set and there is no Get, because the control plane has no route that
// returns a key and no scope that would grant one. If a Get appeared here it
// would be a client for an endpoint that does not exist, which is the shape of
// a feature somebody is about to ask for.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Scopes a terminal may ask for, mirroring GRANTABLE_SCOPES on the server.
//
// Listed here so that `af login --scope` can refuse a typo before somebody
// approves a login in a browser and finds out at the first command. The server
// is still the authority and intersects what it is sent; this is a courtesy
// that saves a round trip through a person.
var GrantableScopes = []string{
	"environments.view",
	"runs.view",
	"events.write",
	"providers.view",
	"providers.write",
	"tokens.manage",
}

// ProviderKey is what a screen or a terminal may know about a stored key.
//
// There is no field for the key. That is not an omission to be filled in later:
// the control plane does not send one.
type ProviderKey struct {
	Provider    string `json:"provider"`
	Last4       string `json:"last4"`
	Fingerprint string `json:"fingerprint"`
	CreatedAt   string `json:"createdAt"`
	RotatedAt   string `json:"rotatedAt"`
}

// ProviderBudget is one provider's cap for the current month.
type ProviderBudget struct {
	Provider     string  `json:"provider"`
	Period       string  `json:"period"`
	CapUSD       float64 `json:"capUsd"`
	SpentUSD     float64 `json:"spentUsd"`
	RemainingUSD float64 `json:"remainingUsd"`
}

// Providers is the whole answer to `af provider list`.
type Providers struct {
	// Sealing reports whether this control plane can store a key at all.
	// Carried so that an installation with no sealing secret explains itself
	// rather than looking merely empty.
	Sealing bool             `json:"sealing"`
	Keys    []ProviderKey    `json:"keys"`
	Budgets []ProviderBudget `json:"budgets"`
}

// SavedKey is what storing one reports back.
type SavedKey struct {
	Provider    string `json:"provider"`
	Last4       string `json:"last4"`
	Fingerprint string `json:"fingerprint"`
	Replaced    bool   `json:"replaced"`
	// SameAsBefore is true when the key stored is the one that was already
	// there. Surfaced rather than swallowed: pasting the old key is the mistake
	// somebody makes at the moment they believe they have rotated a leaked one.
	SameAsBefore bool `json:"sameAsBefore"`
}

// ErrScopeMissing is returned when the token is valid and does not carry the
// capability. Distinct from a plain refusal because the fix is a specific
// command rather than a permissions conversation.
var ErrScopeMissing = errors.New("this token does not carry the scope")

// ListProviders reads what is configured. Needs providers.view.
func (c *Client) ListProviders(ctx context.Context, token string) (Providers, error) {
	var out Providers
	err := c.do(ctx, http.MethodGet, "/v1/providers", token, nil, &out)
	return out, err
}

// SetProviderKey stores or rotates a key. Needs providers.write.
func (c *Client) SetProviderKey(ctx context.Context, token, provider, key string) (SavedKey, error) {
	var out SavedKey
	err := c.do(ctx, http.MethodPut, "/v1/providers/"+url.PathEscape(provider), token,
		map[string]any{"key": key}, &out)
	return out, err
}

// RemoveProviderKey revokes the stored key, if there is one.
func (c *Client) RemoveProviderKey(ctx context.Context, token, provider string) (bool, error) {
	var out struct {
		Revoked bool `json:"revoked"`
	}
	err := c.do(ctx, http.MethodDelete, "/v1/providers/"+url.PathEscape(provider), token, nil, &out)
	return out.Revoked, err
}

// SetProviderBudget sets the monthly cap.
func (c *Client) SetProviderBudget(ctx context.Context, token, provider string, capUSD float64) (ProviderBudget, error) {
	var out ProviderBudget
	err := c.do(ctx, http.MethodPut, "/v1/providers/"+url.PathEscape(provider)+"/budget", token,
		map[string]any{"capUsd": capUSD}, &out)
	return out, err
}

// do is the one place a request is built, so that a key placed in a body cannot
// end up in a query string by somebody adding a call in a hurry.
func (c *Client) do(ctx context.Context, method, path, token string, body any, out any) error {
	var reader *strings.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = strings.NewReader(string(encoded))
	}

	var req *http.Request
	var err error
	if reader != nil {
		req, err = http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, c.BaseURL+path, nil)
	}
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	req.Header.Set("authorization", "Bearer "+token)

	res, err := c.HTTP.Do(req)
	if err != nil {
		// The URL, never the body. A transport error that quoted the request
		// would put a key in whatever captured the terminal.
		return fmt.Errorf("reach %s: %w", c.BaseURL+path, err)
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		var payload struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(res.Body).Decode(&payload)
		switch {
		case res.StatusCode == http.StatusUnauthorized:
			return ErrNotSignedIn
		case res.StatusCode == http.StatusForbidden && strings.Contains(payload.Error, "--scope"):
			return fmt.Errorf("%w: %s", ErrScopeMissing, payload.Error)
		case payload.Error != "":
			// The server's own words. They are written to be readable and are
			// checked not to contain a key.
			return errors.New(payload.Error)
		default:
			return fmt.Errorf("%s answered %d", c.BaseURL+path, res.StatusCode)
		}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("read the answer from %s: %w", c.BaseURL+path, err)
	}
	return nil
}
