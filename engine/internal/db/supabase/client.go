// Package supabase implements the database provider backed by Supabase.
//
// Three things about Supabase decide the shape of everything here, and all
// three were established by running against the real Management API rather
// than by reading its specification.
//
// A branch is a whole separate project, not a copy on write clone of another
// branch, and it is created EMPTY on purpose: "New branches do not start with
// any data from your main project". So a branch cannot be a child of a golden
// the way Neon's is. The golden's rows are copied in by this provider, which
// makes branches independent databases, isolation free, and branching bounded
// by how long a copy takes rather than by how large the database is.
//
// A branch has no annotations. Neon carries identity in a string map that
// survives renames; Supabase gives a listing nothing but the name. So identity
// lives in the name, which accepts underscores and at least eighty characters,
// and therefore carries a golden version verbatim and reversibly. Publishing is
// still a rename, for the same reason it is on Neon: the attestation does not
// exist until the candidate has been masked and scanned, and a rename is the
// one atomic thing available at that moment.
//
// A persistent branch cannot be deleted. DELETE answers 422 "Cannot delete
// persistent branch" until the branch has been made non persistent, and
// branches must be created persistent because an ephemeral one is auto paused
// on inactivity and auto deleted when its pull request closes, neither of which
// an environment has. So every delete here is two calls, and a provider that
// sends one and reads the 422 as "already gone" leaks a branch at $0.01344 an
// hour, forever.
package supabase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// DefaultBaseURL is Supabase's Management API root.
const DefaultBaseURL = "https://api.supabase.com/v1"

// Client talks to the Supabase Management API.
type Client struct {
	BaseURL string
	// Key is a secrets.Value so that it renders as redacted anywhere a client
	// is printed, including in a test failure that dumps a struct.
	//
	// A Supabase personal access token is account wide: there is no per project
	// Management API credential. The containment is here rather than in the
	// credential, so every call this client makes names ProjectRef, and every
	// resource it will act on is filtered by the prefixes in supabase.go.
	Key        secrets.Value
	ProjectRef string
	HTTP       *http.Client
	// Sleep is how the client waits between polls. Injected so a test does not
	// spend real seconds proving that polling works.
	Sleep func(context.Context, time.Duration) error
	// PollInterval and PollTimeout bound waiting for a branch to come up.
	PollInterval time.Duration
	PollTimeout  time.Duration
	// Retries bounds attempts at an idempotent request. Zero uses four.
	Retries int
}

// Branch is the subset of Supabase's branch object this provider uses.
//
// It is what a listing returns. The credentials are not here: they come from
// Detail, a different endpoint, which is the first surprise for anybody porting
// a provider from Neon.
type Branch struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	ProjectRef       string    `json:"project_ref"`
	ParentProjectRef string    `json:"parent_project_ref"`
	IsDefault        bool      `json:"is_default"`
	Persistent       bool      `json:"persistent"`
	WithData         bool      `json:"with_data"`
	CreatedAt        time.Time `json:"created_at"`
	// GitBranch is Supabase's field for the git branch a preview belongs to,
	// and this provider's only durable per branch metadata. See
	// annotationPrefix in supabase.go for why it is used and how it is kept
	// from colliding with a real one.
	GitBranch string `json:"git_branch"`
	// Status is the branch's deployment workflow: CREATING_PROJECT through
	// MIGRATIONS_PASSED to FUNCTIONS_DEPLOYED. Supabase marks it deprecated in
	// favour of listing action runs, and it is read here anyway because it is
	// the only signal a plain listing carries about whether the branch's
	// migrations have finished. Waiting is done on PreviewStatus and on a real
	// query; this is used to say why something is not ready yet.
	Status string `json:"status"`
	// PreviewStatus is the branch project's own lifecycle: COMING_UP,
	// ACTIVE_HEALTHY, PAUSING and so on.
	PreviewStatus string `json:"preview_project_status"`
}

// IsOurs reports whether a branch is one this provider created.
//
// The check is the name prefix, and the default branch is excluded twice over.
// The first branch anybody ever creates on a project also registers a `main`
// row whose project_ref is the PRODUCTION project, and it appears in every
// listing from then on. A sweep that iterates branches and deletes would delete
// production's row. This is the function that stops that, so it is deliberately
// the only place either rule is written.
func (b Branch) IsOurs() bool {
	if b.IsDefault {
		return false
	}
	return strings.HasPrefix(b.Name, PrefixGolden) ||
		strings.HasPrefix(b.Name, PrefixCandidate) ||
		strings.HasPrefix(b.Name, PrefixEnv)
}

// Detail is a branch's own project reference and its credentials.
//
// DBPass is a secrets.Value, which is load bearing rather than decorative: this
// struct is the one place a branch password exists in this process, and a test
// failure that prints a Detail prints the redaction marker.
type Detail struct {
	Ref             string        `json:"ref"`
	PostgresVersion string        `json:"postgres_version"`
	PostgresEngine  string        `json:"postgres_engine"`
	Status          string        `json:"status"`
	DBHost          string        `json:"db_host"`
	DBPort          int           `json:"db_port"`
	DBUser          string        `json:"db_user"`
	DBPass          secrets.Value `json:"db_pass"`
}

// Pooler is one entry of a branch's pooler configuration.
type Pooler struct {
	Identifier   string `json:"identifier"`
	DatabaseType string `json:"database_type"`
	DBUser       string `json:"db_user"`
	DBHost       string `json:"db_host"`
	DBPort       int    `json:"db_port"`
	DBName       string `json:"db_name"`
	PoolMode     string `json:"pool_mode"`
	// ConnectionString is read for its shape and never handed out. Supabase
	// returns it with the literal text [YOUR-PASSWORD] where the password
	// belongs, so a provider that passed it through would declare pooled
	// endpoints and hand out a string that cannot connect. The usable string is
	// assembled from the fields above plus the password from Detail.
	ConnectionString string `json:"connection_string"`
}

// APIError is a non-2xx response, carrying enough to tell a missing resource
// apart from a rejected one without matching on prose.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("supabase: %d: %s", e.Status, e.Message)
}

// NotFound reports whether an error is Supabase saying the thing is not there.
// Used by every destroy, because destroying something already gone has to
// succeed: teardown retries.
func NotFound(err error) bool {
	var api *APIError
	return errors.As(err, &api) && api.Status == http.StatusNotFound
}

// Conflict reports whether an error is Supabase refusing to create a second
// branch with a name that is taken.
//
// It is the outcome of the race this provider's idempotency check cannot close:
// two callers look, both see nothing, both create. Supabase answers the loser
// with 409, which is the right answer and the reason a second branch is never
// made.
func Conflict(err error) bool {
	var api *APIError
	return errors.As(err, &api) && api.Status == http.StatusConflict
}

// Unauthorized reports whether Supabase rejected the token itself.
func Unauthorized(err error) bool {
	var api *APIError
	if !errors.As(err, &api) {
		return false
	}
	return api.Status == http.StatusUnauthorized || api.Status == http.StatusForbidden
}

// persistentRefusal reports whether a delete was refused because the branch is
// still marked persistent.
//
// Matched on the status and the prose together. The status alone is too broad,
// since 422 is Supabase's general refusal, and the prose alone would break the
// moment somebody rewords a message. Getting this wrong in the safe direction
// costs one wasted PATCH; getting it wrong in the unsafe direction leaks a
// branch that bills by the hour.
func persistentRefusal(err error) bool {
	var api *APIError
	if !errors.As(err, &api) || api.Status != http.StatusUnprocessableEntity {
		return false
	}
	return strings.Contains(strings.ToLower(api.Message), "persistent")
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return DefaultBaseURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) pollInterval() time.Duration {
	if c.PollInterval > 0 {
		return c.PollInterval
	}
	return time.Second
}

func (c *Client) pollTimeout() time.Duration {
	if c.PollTimeout > 0 {
		return c.PollTimeout
	}
	return 5 * time.Minute
}

const defaultRetries = 4

func (c *Client) retries() int {
	if c.Retries > 0 {
		return c.Retries
	}
	return defaultRetries
}

// do performs a request, retrying the ones that are safe to retry.
//
// GET, DELETE and PATCH are retried; POST is not. That asymmetry is the whole
// point. A create that timed out may have reached Supabase, and sending it
// again would make a second branch, which is exactly the orphan this provider
// exists to avoid. Idempotency for creates lives in the caller instead, which
// looks for an existing branch by name before making one.
//
// PATCH is on the retried side because every PATCH this client sends assigns a
// fixed value to a field: a name, or persistent false. Sending it twice reaches
// the same state as sending it once. That is a property of these calls and not
// of the verb, so it is asserted here rather than assumed.
//
// What counts as worth retrying: a transport failure, because a DNS blip or a
// reset connection says nothing about the request; 429, because Supabase is
// asking; and 5xx, because it is Supabase's side.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	attempts := 1
	switch method {
	case http.MethodGet, http.MethodDelete, http.MethodPatch:
		attempts = c.retries()
	}

	var last error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			backoff := c.pollInterval() << (attempt - 1)
			if err := c.sleep(ctx, backoff); err != nil {
				return err
			}
		}
		err := c.attempt(ctx, method, path, body, out)
		if err == nil {
			return nil
		}
		if !worthRetrying(err) {
			return err
		}
		last = err
	}
	return last
}

// worthRetrying reports whether an error says nothing about whether the request
// would fail again.
func worthRetrying(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var api *APIError
	if errors.As(err, &api) {
		return api.Status == http.StatusTooManyRequests || api.Status >= 500
	}
	// Anything that is not an answer from Supabase is a transport failure: a
	// name that did not resolve, a connection that was reset, a TLS handshake
	// that timed out. None of those is a verdict on the request.
	var syntax *json.SyntaxError
	return !errors.As(err, &syntax)
}

func (c *Client) attempt(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("supabase: encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, reader)
	if err != nil {
		return fmt.Errorf("supabase: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Key.Reveal())
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		// The URL is in the error and the token is not, because the token is in
		// a header. Worth keeping that way.
		return fmt.Errorf("supabase: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("supabase: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		api := &APIError{Status: resp.StatusCode, Message: strings.TrimSpace(string(payload))}
		var structured struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(payload, &structured) == nil && structured.Message != "" {
			api.Message = structured.Message
		}
		return api
	}

	if out == nil {
		return nil
	}
	// A 2xx with no body at all is a success, and decoding it as JSON is how
	// "destroying something already gone succeeds" stops being true. This bit
	// the Neon provider first; it is here because the same shape of API makes
	// the same shape of mistake available.
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("supabase: decode %s %s: %w", method, path, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Branches
// ---------------------------------------------------------------------------

// CreateBranchRequest describes a branch to create.
type CreateBranchRequest struct {
	Name string
	// Region and InstanceSize default to the parent project's when empty.
	Region       string
	InstanceSize string
	// PostgresEngine is the major version, as Supabase spells it: "15" or "17".
	PostgresEngine string
	// Annotation is the metadata to attach, carried in git_branch.
	Annotation annotation
}

type createBranchBody struct {
	BranchName     string `json:"branch_name"`
	Region         string `json:"region,omitempty"`
	InstanceSize   string `json:"desired_instance_size,omitempty"`
	PostgresEngine string `json:"postgres_engine,omitempty"`
	GitBranch      string `json:"git_branch,omitempty"`
	// Persistent is always true. An ephemeral preview branch is auto paused
	// after inactivity and auto deleted when its pull request closes; an
	// environment has no pull request, and a paused branch is an environment
	// whose database silently stops answering.
	Persistent bool `json:"persistent"`
	// WithData is always false, and this is a masking guarantee rather than a
	// default. The parent of a branch is the customer's PRODUCTION project, so
	// asking Supabase to carry its data across would put unmasked production
	// into an environment and walk straight around the verification this
	// product exists to enforce. Everything a branch holds arrives from a
	// golden that has been masked and scanned.
	WithData bool `json:"with_data"`
}

// CreateBranch creates a branch. It does not wait; WaitReady does that.
func (c *Client) CreateBranch(ctx context.Context, req CreateBranchRequest) (Branch, error) {
	body := createBranchBody{
		BranchName:     req.Name,
		Region:         req.Region,
		InstanceSize:   req.InstanceSize,
		PostgresEngine: req.PostgresEngine,
		GitBranch:      req.Annotation.String(),
		Persistent:     true,
		WithData:       false,
	}
	var out Branch
	if err := c.do(ctx, http.MethodPost, "/projects/"+c.ProjectRef+"/branches", body, &out); err != nil {
		return Branch{}, err
	}
	return out, nil
}

// ListBranches returns every branch of the project, including the default one
// that stands for production. Callers filter with Branch.IsOurs.
func (c *Client) ListBranches(ctx context.Context) ([]Branch, error) {
	var out []Branch
	if err := c.do(ctx, http.MethodGet, "/projects/"+c.ProjectRef+"/branches", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetDetail reads a branch's own project reference, status and credentials.
func (c *Client) GetDetail(ctx context.Context, id string) (Detail, error) {
	var out Detail
	if err := c.do(ctx, http.MethodGet, "/branches/"+url.PathEscape(id), nil, &out); err != nil {
		return Detail{}, err
	}
	return out, nil
}

// Rename gives a branch a new name, which is how a candidate becomes a golden.
func (c *Client) Rename(ctx context.Context, id, name string) error {
	body := map[string]string{"branch_name": name}
	return c.do(ctx, http.MethodPatch, "/branches/"+url.PathEscape(id), body, nil)
}

// SetPersistent marks a branch persistent or ephemeral.
func (c *Client) SetPersistent(ctx context.Context, id string, persistent bool) error {
	body := map[string]bool{"persistent": persistent}
	return c.do(ctx, http.MethodPatch, "/branches/"+url.PathEscape(id), body, nil)
}

// DeleteBranch removes a branch. Deleting one that is already gone succeeds.
//
// Two calls, because Supabase refuses to delete a persistent branch and every
// branch this provider makes is persistent. The refusal is a 422 with a message
// about persistence, so the clearing PATCH is sent only when that is what came
// back: sending it unconditionally would be one wasted round trip on every
// teardown, and reading every 422 as this one would turn a different refusal
// into a silent no-op.
func (c *Client) DeleteBranch(ctx context.Context, id string) error {
	err := c.do(ctx, http.MethodDelete, "/branches/"+url.PathEscape(id), nil, nil)
	if err == nil || NotFound(err) {
		return nil
	}
	if !persistentRefusal(err) {
		return err
	}
	if clearErr := c.SetPersistent(ctx, id, false); clearErr != nil {
		if NotFound(clearErr) {
			return nil
		}
		return fmt.Errorf("supabase: clear persistence before deleting branch %s: %w", id, clearErr)
	}
	err = c.do(ctx, http.MethodDelete, "/branches/"+url.PathEscape(id), nil, nil)
	if err == nil || NotFound(err) {
		return nil
	}
	return err
}

// WaitNamed waits until a branch with this name appears in the project's
// listing.
//
// Renaming a branch answers 200 with the new name and the LISTING can still
// carry the old one for a few seconds. That gap is not cosmetic: publishing a
// golden is a rename, and a caller that looks a moment later finds a candidate
// where a golden should be, which this provider reports as AF-MSK-001, "has no
// valid verification attestation". The golden was verified. The listing had not
// caught up, and the operator would have been told their masking failed.
//
// Bounded tightly and separately from PollTimeout, because this is a cache
// catching up rather than a project being provisioned. If it has not happened
// in a minute, something else is wrong and waiting five more will not fix it.
func (c *Client) WaitNamed(ctx context.Context, name string) error {
	deadline := time.Now().Add(namedVisibleTimeout)
	for {
		branches, err := c.ListBranches(ctx)
		if err != nil {
			return err
		}
		for _, b := range branches {
			if b.Name == name {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf(
				"supabase: branch %s did not appear in the project's branch listing within %s",
				name, namedVisibleTimeout)
		}
		if err := c.sleep(ctx, c.pollInterval()); err != nil {
			return err
		}
	}
}

// namedVisibleTimeout bounds waiting for the branch listing to catch up with a
// change already acknowledged.
const namedVisibleTimeout = 60 * time.Second

// PoolerConfig returns the pooler entries for a branch's own project.
func (c *Client) PoolerConfig(ctx context.Context, ref string) ([]Pooler, error) {
	var out []Pooler
	err := c.do(ctx, http.MethodGet, "/projects/"+url.PathEscape(ref)+"/config/database/pooler", nil, &out)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// WaitReady polls until a branch's project reports itself healthy.
//
// Reporting healthy is necessary and not sufficient: it says the project exists
// and is running, not that Postgres will answer a query. The caller follows this
// with a real query, which is what actually decides. Both are needed, because
// waiting only on the query would hide a branch that failed to provision behind
// a connection timeout.
func (c *Client) WaitReady(ctx context.Context, id string) (Detail, error) {
	deadline := time.Now().Add(c.pollTimeout())
	var last Detail
	for {
		detail, err := c.GetDetail(ctx, id)
		if err != nil {
			return Detail{}, err
		}
		last = detail
		switch detail.Status {
		case "ACTIVE_HEALTHY":
			return detail, nil
		case "INIT_FAILED", "RESTORE_FAILED", "PAUSE_FAILED", "REMOVED":
			return detail, fmt.Errorf(
				"supabase: branch %s reached %s and will not come up", id, detail.Status)
		}
		if time.Now().After(deadline) {
			return last, fmt.Errorf("supabase: branch %s was still %s after %s",
				id, last.Status, c.pollTimeout())
		}
		if err := c.sleep(ctx, c.pollInterval()); err != nil {
			return last, err
		}
	}
}
