package personas

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Provisioning through a hosted identity provider, for the applications that
// do not own their users at all.
//
// Clerk, Auth0 and WorkOS will not accept a row written into a table, because
// the table is not in your database. Supabase will, and its admin API is still
// the better path: it hashes the password the way its own signup does and it
// writes the identity row, both of which the SQL adapter has to reproduce by
// hand.
//
// The important thing about this file is what it does when it cannot work.
// A hosted provider needs somewhere to put a persona that is not production,
// which means a sandbox tenant, a development instance or a staging tenant
// depending on whose product it is. Without one there is no correct answer,
// and inventing one would mean creating a user in the real tenant. So it
// refuses, with AF-DB-020, naming the provider and what to configure.

// Hosted is one hosted identity provider's admin API.
//
// Small on purpose, for the same reason Adapter is: the differences between
// these four are entirely in the request bodies and the field names, and the
// shape of "look for the address, create or update" is the same for all of
// them.
type Hosted interface {
	// Name is the provider's name, as it appears in AF-DB-020.
	Name() string
	// Base is the API root. Never carries the token.
	Base() string
	// FindRequest asks for the account with an address.
	FindRequest(base, email string) (*http.Request, error)
	// ParseFind returns the provider's id for the address, or "" when there
	// is no such account.
	ParseFind(body []byte) (string, error)
	// CreateRequest asks for a new account.
	CreateRequest(base string, p schema.Persona, want Credentials) (*http.Request, error)
	// UpdateRequest reconciles an existing one.
	UpdateRequest(base, id string, p schema.Persona, want Credentials) (*http.Request, error)
	// ParseAccount returns the provider's id from a create or update reply.
	ParseAccount(body []byte) (string, error)
	// Authorize puts the token on a request. A method rather than a header
	// name because WorkOS and Supabase disagree about how.
	Authorize(r *http.Request, token secrets.Value)
}

// APIAdapter provisions personas through a hosted provider.
type APIAdapter struct {
	hosted Hosted
	http   *http.Client
	token  secrets.Value
	// sandbox records that this adapter is pointed at a tenant that is not
	// production. Without it the adapter refuses rather than guessing.
	sandbox bool
}

// APIOptions configure a hosted adapter.
type APIOptions struct {
	// Token is the admin credential. Held as a Value so it cannot reach a log
	// through a format verb.
	Token secrets.Value
	// Sandbox says the configured tenant is a sandbox, development or staging
	// tenant rather than the production one.
	Sandbox bool
	// HTTP overrides the client, for tests and for a proxy.
	HTTP *http.Client
}

// NewAPIAdapter returns an adapter for a hosted provider.
func NewAPIAdapter(h Hosted, opts APIOptions) *APIAdapter {
	client := opts.HTTP
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &APIAdapter{hosted: h, http: client, token: opts.Token, sandbox: opts.Sandbox}
}

// Name identifies the adapter.
func (a *APIAdapter) Name() string { return a.hosted.Name() }

// Provision creates or reconciles the persona through the provider's API.
func (a *APIAdapter) Provision(
	ctx context.Context, p schema.Persona, want Credentials,
) (*Account, error) {
	if !a.sandbox {
		// The refusal the spec asks for, and the reason it is a refusal
		// rather than a default: the only tenant this adapter could fall back
		// to is the production one, and creating a persona there means
		// creating a user in the real product.
		return nil, aferrors.Coded(aferrors.AFDB020, "provider", a.hosted.Name())
	}
	if a.token.Reveal() == "" {
		return nil, fmt.Errorf(
			"the %s adapter has no admin token, so it cannot create a persona",
			a.hosted.Name())
	}

	base := a.hosted.Base()
	account := &Account{
		Name: p.Name, Email: p.Email, Phone: p.Phone, Role: p.Role,
		Login: p.Login, Adapter: a.hosted.Name(), Password: want.Password,
	}
	if account.Login == "" {
		account.Login = schema.LoginPassword
	}
	if !needsPassword(account.Login) {
		account.Password = secrets.Value{}
	}
	if p.MFA || account.Login == schema.LoginTOTP {
		account.TOTPSecret = want.TOTPSecret
	}

	find, err := a.hosted.FindRequest(base, p.Email)
	if err != nil {
		return nil, err
	}
	body, status, err := a.do(ctx, find)
	if err != nil {
		return nil, err
	}
	existing := ""
	if status != http.StatusNotFound {
		existing, err = a.hosted.ParseFind(body)
		if err != nil {
			return nil, fmt.Errorf("reading %s's answer about %s: %w",
				a.hosted.Name(), p.Email, err)
		}
	}

	var req *http.Request
	if existing != "" {
		req, err = a.hosted.UpdateRequest(base, existing, p, want)
		account.Reconciled = true
	} else {
		req, err = a.hosted.CreateRequest(base, p, want)
	}
	if err != nil {
		return nil, err
	}

	body, _, err = a.do(ctx, req)
	if err != nil {
		return nil, err
	}
	id, err := a.hosted.ParseAccount(body)
	if err != nil {
		return nil, fmt.Errorf("reading the account %s returned for %q: %w",
			a.hosted.Name(), p.Name, err)
	}
	if id == "" {
		id = existing
	}
	account.Subject = id
	return account, nil
}

// do sends a request and returns its body.
//
// Two behaviours here are deliberate and both come from a trap this project
// has already been caught by. A 2xx with an empty body is a success, not a
// decode error: several of these APIs answer a successful update with 204 and
// nothing, and treating that as malformed turns a working call into a
// failure. And Retry-After is honoured rather than ignored, because a
// provisioning run that hammers through a rate limit gets the whole token
// throttled and takes the rest of the run with it.
func (a *APIAdapter) do(ctx context.Context, req *http.Request) ([]byte, int, error) {
	const attempts = 4
	var lastErr error

	for attempt := 0; attempt < attempts; attempt++ {
		r := req.Clone(ctx)
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, 0, err
			}
			r.Body = body
		}
		a.hosted.Authorize(r, a.token)
		r.Header.Set("Accept", "application/json")

		resp, err := a.http.Do(r)
		if err != nil {
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}

		switch {
		case resp.StatusCode == http.StatusTooManyRequests,
			resp.StatusCode >= 500:
			wait := retryAfter(resp.Header.Get("Retry-After"))
			lastErr = fmt.Errorf("%s answered %d", a.hosted.Name(), resp.StatusCode)
			if attempt == attempts-1 {
				break
			}
			select {
			case <-ctx.Done():
				return nil, resp.StatusCode, ctx.Err()
			case <-time.After(wait):
			}
			continue
		case resp.StatusCode == http.StatusUnauthorized,
			resp.StatusCode == http.StatusForbidden:
			return nil, resp.StatusCode, aferrors.Coded(aferrors.AFDB021,
				"provider", a.hosted.Name())
		case resp.StatusCode == http.StatusNotFound:
			// Not an error here: it is how three of these four answer "no
			// account with that address", which is the common case on a
			// first run.
			return body, resp.StatusCode, nil
		case resp.StatusCode >= 400:
			return nil, resp.StatusCode, fmt.Errorf("%s answered %d: %s",
				a.hosted.Name(), resp.StatusCode, summarise(body))
		}
		return body, resp.StatusCode, nil
	}
	return nil, 0, fmt.Errorf("%s did not answer: %w", a.hosted.Name(), lastErr)
}

// retryAfter reads the header, in both the forms the RFC allows.
func retryAfter(header string) time.Duration {
	if header == "" {
		return 2 * time.Second
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(header)); err == nil {
		if secs < 0 {
			return 2 * time.Second
		}
		if secs > 60 {
			secs = 60
		}
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(header); err == nil {
		if d := time.Until(when); d > 0 && d < time.Minute {
			return d
		}
	}
	return 2 * time.Second
}

// summarise shortens an error body for a message, so a provider that returns
// a page of HTML does not become a page of error.
func summarise(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	if s == "" {
		return "(no body)"
	}
	return s
}

// jsonRequest builds a request with a JSON body that can be replayed on retry.
func jsonRequest(method, endpoint string, payload any) (*http.Request, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// Set explicitly so a retry sends the body again rather than an empty
	// one, which is the silent way a retried POST becomes a no-op.
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(encoded)), nil
	}
	return req, nil
}

// decodeJSON reads a body, treating an empty one as an empty object.
func decodeJSON(body []byte, into any) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	return json.Unmarshal(body, into)
}

// ---------------------------------------------------------------------------
// Supabase Auth, through the admin API rather than the tables.

// SupabaseHosted provisions through a project's auth admin API.
//
// Preferred over the SQL adapter when the project's URL and service role key
// are available, because it hashes the password the way Supabase's own signup
// does and writes the identity row itself.
type SupabaseHosted struct{ URL string }

func (s SupabaseHosted) Name() string { return "supabase-api" }
func (s SupabaseHosted) Base() string { return strings.TrimRight(s.URL, "/") }

func (s SupabaseHosted) Authorize(r *http.Request, token secrets.Value) {
	// Both headers, which is what GoTrue expects: apikey identifies the
	// project and Authorization carries the role.
	r.Header.Set("apikey", token.Reveal())
	r.Header.Set("Authorization", "Bearer "+token.Reveal())
}

func (s SupabaseHosted) FindRequest(base, email string) (*http.Request, error) {
	// The filter goes in the query and the token never does, which is the
	// rule the Neon provider already follows: a key in a URL is a key in
	// every proxy log between here and there.
	q := url.Values{}
	q.Set("filter", email)
	q.Set("per_page", "1")
	return http.NewRequest(http.MethodGet, base+"/auth/v1/admin/users?"+q.Encode(), nil)
}

func (s SupabaseHosted) ParseFind(body []byte) (string, error) {
	var out struct {
		Users []struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"users"`
	}
	if err := decodeJSON(body, &out); err != nil {
		return "", err
	}
	if len(out.Users) == 0 {
		return "", nil
	}
	return out.Users[0].ID, nil
}

func (s SupabaseHosted) CreateRequest(
	base string, p schema.Persona, want Credentials,
) (*http.Request, error) {
	payload := map[string]any{
		"email": p.Email,
		// Confirmed on creation. A project with email confirmation on refuses
		// a sign in for an unconfirmed address, and the persona would be
		// waiting for a mail nobody is going to send.
		"email_confirm":  true,
		"user_metadata":  metadataFor(p),
		"app_metadata":   map[string]any{"provider": "email", "providers": []string{"email"}},
		"password":       want.Password.Reveal(),
		"ban_duration":   "none",
		"role":           p.Role,
		"phone_confirm":  false,
		"email_verified": true,
	}
	if p.Role == "" {
		delete(payload, "role")
	}
	return jsonRequest(http.MethodPost, base+"/auth/v1/admin/users", payload)
}

func (s SupabaseHosted) UpdateRequest(
	base, id string, p schema.Persona, want Credentials,
) (*http.Request, error) {
	payload := map[string]any{
		"email":         p.Email,
		"email_confirm": true,
		"password":      want.Password.Reveal(),
		"user_metadata": metadataFor(p),
	}
	if p.Role != "" {
		payload["role"] = p.Role
	}
	return jsonRequest(http.MethodPut, base+"/auth/v1/admin/users/"+url.PathEscape(id), payload)
}

func (s SupabaseHosted) ParseAccount(body []byte) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	return out.ID, decodeJSON(body, &out)
}

// ---------------------------------------------------------------------------
// Clerk.

// ClerkHosted provisions through Clerk's backend API.
//
// Clerk's development instances are the sandbox: they are separate from
// production, free, and the same API, which is why the third party catalog
// already marks Clerk as sandbox mode for egress.
type ClerkHosted struct{}

func (ClerkHosted) Name() string { return "clerk" }
func (ClerkHosted) Base() string { return "https://api.clerk.com/v1" }

func (ClerkHosted) Authorize(r *http.Request, token secrets.Value) {
	r.Header.Set("Authorization", "Bearer "+token.Reveal())
}

func (ClerkHosted) FindRequest(base, email string) (*http.Request, error) {
	q := url.Values{}
	q.Set("email_address", email)
	q.Set("limit", "1")
	return http.NewRequest(http.MethodGet, base+"/users?"+q.Encode(), nil)
}

func (ClerkHosted) ParseFind(body []byte) (string, error) {
	// Clerk answers a list query with a bare array rather than an object,
	// which is the sort of difference that only shows up against the real API.
	var out []struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(body, &out); err != nil {
		return "", err
	}
	if len(out) == 0 {
		return "", nil
	}
	return out[0].ID, nil
}

func (ClerkHosted) CreateRequest(
	base string, p schema.Persona, want Credentials,
) (*http.Request, error) {
	payload := map[string]any{
		"email_address":        []string{p.Email},
		"password":             want.Password.Reveal(),
		"skip_password_checks": true,
		"public_metadata":      metadataFor(p),
	}
	if p.MFA || p.Login == schema.LoginTOTP {
		payload["totp_secret"] = want.TOTPSecret.Reveal()
	}
	return jsonRequest(http.MethodPost, base+"/users", payload)
}

func (ClerkHosted) UpdateRequest(
	base, id string, p schema.Persona, want Credentials,
) (*http.Request, error) {
	payload := map[string]any{
		"password":             want.Password.Reveal(),
		"skip_password_checks": true,
		"public_metadata":      metadataFor(p),
	}
	return jsonRequest(http.MethodPatch, base+"/users/"+url.PathEscape(id), payload)
}

func (ClerkHosted) ParseAccount(body []byte) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	return out.ID, decodeJSON(body, &out)
}

// ---------------------------------------------------------------------------
// Auth0.

// Auth0Hosted provisions through the Auth0 Management API.
//
// The tenant is the sandbox here: Auth0 tenants are free to create and a
// development tenant is separate from production, which is what the third
// party catalog already assumes for egress.
type Auth0Hosted struct {
	// Domain is the tenant, for example dev-abc123.us.auth0.com.
	Domain string
	// Connection is the database connection users are created in. Auth0's
	// default name, unless the tenant renamed it.
	Connection string
}

func (a Auth0Hosted) Name() string { return "auth0" }
func (a Auth0Hosted) Base() string {
	return "https://" + strings.TrimPrefix(strings.TrimRight(a.Domain, "/"), "https://") + "/api/v2"
}

func (Auth0Hosted) Authorize(r *http.Request, token secrets.Value) {
	r.Header.Set("Authorization", "Bearer "+token.Reveal())
}

func (Auth0Hosted) FindRequest(base, email string) (*http.Request, error) {
	q := url.Values{}
	q.Set("email", strings.ToLower(email))
	return http.NewRequest(http.MethodGet, base+"/users-by-email?"+q.Encode(), nil)
}

func (Auth0Hosted) ParseFind(body []byte) (string, error) {
	var out []struct {
		UserID string `json:"user_id"`
	}
	if err := decodeJSON(body, &out); err != nil {
		return "", err
	}
	if len(out) == 0 {
		return "", nil
	}
	return out[0].UserID, nil
}

func (a Auth0Hosted) connection() string {
	if a.Connection != "" {
		return a.Connection
	}
	return "Username-Password-Authentication"
}

func (a Auth0Hosted) CreateRequest(
	base string, p schema.Persona, want Credentials,
) (*http.Request, error) {
	payload := map[string]any{
		"email":          p.Email,
		"password":       want.Password.Reveal(),
		"connection":     a.connection(),
		"email_verified": true,
		"verify_email":   false,
		"user_metadata":  metadataFor(p),
	}
	return jsonRequest(http.MethodPost, base+"/users", payload)
}

func (a Auth0Hosted) UpdateRequest(
	base, id string, p schema.Persona, want Credentials,
) (*http.Request, error) {
	// Auth0 refuses a PATCH that changes the password and other fields in one
	// call, so the password goes on its own with the connection named.
	payload := map[string]any{
		"password":   want.Password.Reveal(),
		"connection": a.connection(),
	}
	return jsonRequest(http.MethodPatch, base+"/users/"+url.PathEscape(id), payload)
}

func (Auth0Hosted) ParseAccount(body []byte) (string, error) {
	var out struct {
		UserID string `json:"user_id"`
	}
	return out.UserID, decodeJSON(body, &out)
}

// ---------------------------------------------------------------------------
// WorkOS.

// WorkOSHosted provisions through WorkOS User Management.
//
// The staging environment is the sandbox: WorkOS gives every account a
// staging environment with its own API key, and it is separate from
// production.
type WorkOSHosted struct{}

func (WorkOSHosted) Name() string { return "workos" }
func (WorkOSHosted) Base() string { return "https://api.workos.com/user_management" }

func (WorkOSHosted) Authorize(r *http.Request, token secrets.Value) {
	r.Header.Set("Authorization", "Bearer "+token.Reveal())
}

func (WorkOSHosted) FindRequest(base, email string) (*http.Request, error) {
	q := url.Values{}
	q.Set("email", email)
	q.Set("limit", "1")
	return http.NewRequest(http.MethodGet, base+"/users?"+q.Encode(), nil)
}

func (WorkOSHosted) ParseFind(body []byte) (string, error) {
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := decodeJSON(body, &out); err != nil {
		return "", err
	}
	if len(out.Data) == 0 {
		return "", nil
	}
	return out.Data[0].ID, nil
}

func (WorkOSHosted) CreateRequest(
	base string, p schema.Persona, want Credentials,
) (*http.Request, error) {
	payload := map[string]any{
		"email":          p.Email,
		"password":       want.Password.Reveal(),
		"email_verified": true,
	}
	if name := p.Attributes["first_name"]; name != "" {
		payload["first_name"] = name
	}
	if name := p.Attributes["last_name"]; name != "" {
		payload["last_name"] = name
	}
	return jsonRequest(http.MethodPost, base+"/users", payload)
}

func (WorkOSHosted) UpdateRequest(
	base, id string, p schema.Persona, want Credentials,
) (*http.Request, error) {
	payload := map[string]any{
		"password":       want.Password.Reveal(),
		"email_verified": true,
	}
	return jsonRequest(http.MethodPut, base+"/users/"+url.PathEscape(id), payload)
}

func (WorkOSHosted) ParseAccount(body []byte) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	return out.ID, decodeJSON(body, &out)
}

// metadataFor renders a persona's attributes and role as provider metadata.
func metadataFor(p schema.Persona) map[string]any {
	out := map[string]any{}
	for _, k := range SortedAttributes(p.Attributes) {
		out[k] = p.Attributes[k]
	}
	if p.Role != "" {
		out["role"] = p.Role
	}
	out["antifailure_persona"] = p.Name
	return out
}
