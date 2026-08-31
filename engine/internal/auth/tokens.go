package auth

// `af token`, client side.
//
// Beside the provider calls rather than in the control plane client, for the
// same reason they are: these are made with the PERSONAL token af login stored,
// not with an engine token. That distinction is the whole point of this file.
// An engine token has no identity and cannot mint another one, so a machine
// that leaks its credential cannot use it to make a fresh credential; making
// one is an act by a person who is an owner or an admin right now.
//
// The mirror of the rule next door. A provider key travels up and never comes
// back; an engine token travels DOWN exactly once and never again, because only
// its hash is stored. So there is a Create that returns a token and there is no
// call that reads one back, and there never will be for the same reason.

import (
	"context"
	"net/http"
	"net/url"
)

// EngineToken is what a listing may know about a token. There is no field for
// the token itself: after the mint, nothing can read it.
type EngineToken struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	CreatedAt  string `json:"createdAt"`
	LastUsedAt string `json:"lastUsedAt"`
	RevokedAt  string `json:"revokedAt"`
}

// CreatedToken is the one response in the product that carries a credential.
type CreatedToken struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Prefix string `json:"prefix"`
	Token  string `json:"token"`
}

// CreateEngineToken mints a token. Needs tokens.manage.
func (c *Client) CreateEngineToken(ctx context.Context, token, name string) (CreatedToken, error) {
	var out CreatedToken
	err := c.do(ctx, http.MethodPost, "/v1/tokens", token, map[string]any{"name": name}, &out)
	return out, err
}

// ListEngineTokens reads what exists. Needs tokens.manage.
func (c *Client) ListEngineTokens(ctx context.Context, token string) ([]EngineToken, error) {
	var out struct {
		Tokens []EngineToken `json:"tokens"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/tokens", token, nil, &out)
	return out.Tokens, err
}

// RevokeEngineToken revokes one by its id or its prefix.
//
// AlreadyRevoked is reported rather than turned into an error, because the
// second run of the same command during an incident has to read as "it is
// still revoked" and not as a new problem.
func (c *Client) RevokeEngineToken(ctx context.Context, token, idOrPrefix string) (name string, alreadyRevoked bool, err error) {
	var out struct {
		Name           string `json:"name"`
		AlreadyRevoked bool   `json:"alreadyRevoked"`
	}
	err = c.do(ctx, http.MethodDelete, "/v1/tokens/"+url.PathEscape(idOrPrefix), token, nil, &out)
	return out.Name, out.AlreadyRevoked, err
}
