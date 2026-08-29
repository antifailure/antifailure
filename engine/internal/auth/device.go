// The terminal half of the device authorization grant.
//
// Three calls: ask for a pair of codes, show the short one, poll with the long
// one. The server's half is web/apps/api/src/auth/device.ts, and the error
// codes below are RFC 8628's because that is what it answers with.
//
// The polling loop is the only part with any subtlety, and it is all in one
// place: the server can tell a client to slow down, and a client that ignores
// that is a client that gets rate limited and then reports a confusing failure.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// The RFC 8628 error codes this client acts on. The first two mean keep going;
// everything else means stop, and the distinction is the whole reason the
// server answers with a code rather than a message.
const (
	errAuthorizationPending = "authorization_pending"
	errSlowDown             = "slow_down"
	errAccessDenied         = "access_denied"
	errExpiredToken         = "expired_token"
)

// ErrDeclined is returned when a person said no.
var ErrDeclined = errors.New("the login was declined")

// ErrLoginExpired is returned when nobody approved it in time.
var ErrLoginExpired = errors.New("the login request expired")

// Start is what the terminal shows the person.
type Start struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// Token is what it receives.
type Token struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
}

// Identity is the answer to `af whoami`.
type Identity struct {
	Login        string   `json:"login"`
	Name         string   `json:"name"`
	Organization string   `json:"organization"`
	Role         string   `json:"role"`
	Scopes       []string `json:"scopes"`
	TokenPrefix  string   `json:"tokenPrefix"`
	ExpiresAt    string   `json:"expiresAt"`
}

// Client talks to one control plane.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// NewClient returns a client with a timeout that is short enough to fail
// rather than hang. A login that appears to do nothing is worse than one that
// says the server did not answer.
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimSuffix(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 20 * time.Second},
	}
}

// Begin asks for a pair of codes.
func (c *Client) Begin(ctx context.Context, clientLabel string, scopes []string) (Start, error) {
	body := map[string]any{"clientLabel": clientLabel}
	if len(scopes) > 0 {
		body["scopes"] = scopes
	}
	var out Start
	if err := c.post(ctx, "/auth/device/code", "", body, &out); err != nil {
		return Start{}, err
	}
	if out.DeviceCode == "" || out.UserCode == "" {
		return Start{}, errors.New("the control plane did not return a login code")
	}
	if out.Interval <= 0 {
		out.Interval = 5
	}
	return out, nil
}

// Poll waits for somebody to approve, and returns the token when they do.
//
// onWait is called before each sleep so a caller can show progress. It is
// allowed to be nil.
func (c *Client) Poll(ctx context.Context, start Start, sleep func(time.Duration), onWait func()) (Token, error) {
	if sleep == nil {
		sleep = func(d time.Duration) { time.Sleep(d) }
	}
	interval := time.Duration(start.Interval) * time.Second
	deadline := time.Now().Add(time.Duration(max(start.ExpiresIn, 60)) * time.Second)

	for {
		if ctx.Err() != nil {
			return Token{}, ctx.Err()
		}
		if time.Now().After(deadline) {
			return Token{}, ErrLoginExpired
		}

		var tok Token
		err := c.post(ctx, "/auth/device/token", "", map[string]any{"device_code": start.DeviceCode}, &tok)
		if err == nil {
			if tok.AccessToken == "" {
				return Token{}, errors.New("the control plane approved the login and returned no token")
			}
			return tok, nil
		}

		var oa *oauthError
		if !errors.As(err, &oa) {
			return Token{}, err
		}
		switch oa.Code {
		case errAuthorizationPending:
			// Keep waiting at the interval we were given.
		case errSlowDown:
			// RFC 8628: add five seconds and keep going. Ignoring this is what
			// turns a working login into a rate limited one, and the failure
			// then reads as the server being broken.
			interval += 5 * time.Second
		case errAccessDenied:
			return Token{}, ErrDeclined
		case errExpiredToken:
			return Token{}, ErrLoginExpired
		default:
			return Token{}, err
		}

		if onWait != nil {
			onWait()
		}
		sleep(interval)
	}
}

// Whoami asks who a token belongs to.
func (c *Client) Whoami(ctx context.Context, token string) (Identity, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/whoami", nil)
	if err != nil {
		return Identity{}, err
	}
	req.Header.Set("authorization", "Bearer "+token)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("ask %s who this token belongs to: %w", c.BaseURL, err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return Identity{}, ErrNotSignedIn
	}
	if res.StatusCode != http.StatusOK {
		return Identity{}, fmt.Errorf("%s answered %d", c.BaseURL+"/v1/whoami", res.StatusCode)
	}
	var id Identity
	if err := json.NewDecoder(res.Body).Decode(&id); err != nil {
		return Identity{}, fmt.Errorf("read the answer from %s: %w", c.BaseURL, err)
	}
	return id, nil
}

// Revoke tells the control plane the token is finished with.
//
// Deliberately tolerant: a logout whose network call fails must still remove
// the local copy, so the caller treats an error here as something to report
// rather than something to stop for. A credential left on a laptop because the
// server was unreachable is the worse outcome.
func (c *Client) Revoke(ctx context.Context, token string) error {
	return c.post(ctx, "/v1/logout", token, map[string]any{}, nil)
}

// oauthError carries the RFC 8628 code so Poll can switch on it.
type oauthError struct {
	Code        string
	Description string
	Status      int
}

func (e *oauthError) Error() string {
	if e.Description != "" {
		return e.Description
	}
	return e.Code
}

func (c *Client) post(ctx context.Context, path, bearer string, body any, out any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	if bearer != "" {
		req.Header.Set("authorization", "Bearer "+bearer)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("reach %s: %w", c.BaseURL+path, err)
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		var payload struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.NewDecoder(res.Body).Decode(&payload)
		if payload.Error != "" {
			return &oauthError{Code: payload.Error, Description: payload.Description, Status: res.StatusCode}
		}
		return fmt.Errorf("%s answered %d", c.BaseURL+path, res.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("read the answer from %s: %w", c.BaseURL+path, err)
	}
	return nil
}
