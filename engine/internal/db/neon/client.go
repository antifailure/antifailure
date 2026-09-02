// Package neon implements the database provider backed by Neon.
//
// Two things about Neon's API decide the shape of everything here.
//
// It is asynchronous. Creating a branch returns immediately with a list of
// operations that are still scheduling, and the branch is not usable until they
// finish. A client that returns as soon as the HTTP call does hands back a
// connection string to a compute that is not running yet, and the failure lands
// somewhere else entirely, usually as a timeout inside somebody's migration.
// So every mutating call waits for its own operations.
//
// It has no idempotency keys. The engine retries after a timeout, and a retry
// that creates a second branch is how an orphan is made. So identity lives in
// the branch's annotation, which survives a rename, and every create looks
// first.
package neon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// DefaultBaseURL is Neon's API root.
const DefaultBaseURL = "https://console.neon.tech/api/v2"

// Annotation keys. Neon annotations are a string map that survives renames and
// comes back on a list, which makes them the right place for the facts the
// engine needs to recognise its own resources.
const (
	AnnKind        = "antifailure-kind"    // "golden" or "branch"
	AnnEnvID       = "antifailure-env"     // the environment a branch belongs to
	AnnVersion     = "antifailure-version" // the golden version identifier
	AnnFrom        = "antifailure-from"    // the golden a branch came from
	AnnRulesHash   = "antifailure-rules"   // the masking rules that produced a golden
	AnnProvenance  = "antifailure-project" // the project a golden was made for, and out of what
	AnnVerified    = "antifailure-verified"
	AnnAttestation = "antifailure-attestation"
	AnnCreatedAt   = "antifailure-created"
)

// Client talks to the Neon API.
type Client struct {
	BaseURL string
	// Key is a secrets.Value so that it renders as redacted anywhere a client
	// is printed, including in a test failure that dumps a struct.
	Key       secrets.Value
	ProjectID string
	HTTP      *http.Client
	// Sleep is how the client waits between polls. Injected so a test does not
	// spend real seconds proving that polling works.
	Sleep func(context.Context, time.Duration) error
	// PollInterval and PollTimeout bound waiting for operations.
	PollInterval time.Duration
	PollTimeout  time.Duration
	// Retries bounds attempts at an idempotent request. Zero uses four.
	Retries int
}

// Branch is the subset of Neon's branch object this provider uses.
type Branch struct {
	ID           string    `json:"id"`
	ProjectID    string    `json:"project_id"`
	ParentID     string    `json:"parent_id"`
	Name         string    `json:"name"`
	CurrentState string    `json:"current_state"`
	Default      bool      `json:"default"`
	Primary      bool      `json:"primary"`
	LogicalSize  int64     `json:"logical_size"`
	CreatedAt    time.Time `json:"created_at"`

	// Annotation is filled in by the list and get calls below. Neon returns it
	// alongside the branch rather than inside it.
	Annotation map[string]string `json:"-"`
}

// Endpoint is a compute attached to a branch.
type Endpoint struct {
	ID       string `json:"id"`
	BranchID string `json:"branch_id"`
	Host     string `json:"host"`
	Type     string `json:"type"`
	Hosts    struct {
		ReadWrite       string `json:"read_write_host"`
		ReadWritePooled string `json:"read_write_pooled_host"`
	} `json:"hosts"`
	CurrentState string `json:"current_state"`
}

// Operation is one asynchronous step Neon is performing.
type Operation struct {
	ID            string `json:"id"`
	Action        string `json:"action"`
	Status        string `json:"status"`
	Error         string `json:"error"`
	FailuresCount int    `json:"failures_count"`
}

// Done reports whether an operation has reached a terminal state.
func (o Operation) Done() bool {
	switch o.Status {
	case "finished", "failed", "error", "cancelled", "skipped":
		return true
	}
	return false
}

// OK reports whether a finished operation finished well. "skipped" counts,
// because Neon skips work that is already in the state it was asked for, and
// treating that as a failure would break every retry.
func (o Operation) OK() bool { return o.Status == "finished" || o.Status == "skipped" }

// APIError is a non-2xx response, carrying enough to tell apart a missing
// resource from a rejected one without matching on prose.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("neon: %d %s: %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("neon: %d: %s", e.Status, e.Message)
}

// LimitExceeded reports whether an error is Neon refusing because the plan's
// branch ceiling is reached.
//
// Recognised by code rather than by prose, and separate from any limit this
// provider was configured with: the plan's limit is the real one, and a
// provider that only knows its own declared number would surface Neon's refusal
// as an unexplained 422.
func LimitExceeded(err error) bool {
	var api *APIError
	if !errors.As(err, &api) {
		return false
	}
	return api.Code == "BRANCHES_LIMIT_EXCEEDED" ||
		strings.Contains(strings.ToLower(api.Message), "branches limit exceeded")
}

// NotFound reports whether an error is Neon saying the thing is not there.
// Used by every destroy, because destroying something already gone has to
// succeed: teardown retries.
func NotFound(err error) bool {
	var api *APIError
	return errors.As(err, &api) && api.Status == http.StatusNotFound
}

// ErrOperationFailed is returned when Neon reports an operation as failed. It
// wraps rather than replaces, so a caller can still see which action it was.
type ErrOperationFailed struct{ Op Operation }

func (e *ErrOperationFailed) Error() string {
	detail := e.Op.Error
	if detail == "" {
		detail = e.Op.Status
	}
	return fmt.Sprintf("neon: operation %s (%s) did not succeed: %s", e.Op.Action, e.Op.ID, detail)
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
	return 500 * time.Millisecond
}

func (c *Client) pollTimeout() time.Duration {
	if c.PollTimeout > 0 {
		return c.PollTimeout
	}
	return 3 * time.Minute
}

// Retries bounds how many times an idempotent request is attempted.
const defaultRetries = 4

func (c *Client) retries() int {
	if c.Retries > 0 {
		return c.Retries
	}
	return defaultRetries
}

// do performs a request, retrying the ones that are safe to retry.
//
// GET and DELETE are retried; POST and PATCH are not. That asymmetry is the
// whole point. A create that timed out may have reached Neon, and sending it
// again would make a second branch, which is exactly the orphan this provider
// exists to avoid. Idempotency for creates lives in the caller instead, which
// looks for an existing branch by annotation before making one.
//
// What counts as worth retrying: a transport failure, because a DNS blip or a
// reset connection says nothing about the request; 429, because Neon is asking;
// and 5xx, because it is Neon's side.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	idempotent := method == http.MethodGet || method == http.MethodDelete
	attempts := 1
	if idempotent {
		attempts = c.retries()
	}

	var last error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			// Doubling, from the poll interval. Bounded by the attempt count
			// rather than by a deadline, because the context already carries
			// the deadline that matters.
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
	// Anything that is not an answer from Neon is a transport failure: a name
	// that did not resolve, a connection that was reset, a TLS handshake that
	// timed out. None of those are a verdict on the request.
	var syntax *json.SyntaxError
	return !errors.As(err, &syntax)
}

func (c *Client) attempt(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("neon: encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, reader)
	if err != nil {
		return fmt.Errorf("neon: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Key.Reveal())
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		// The URL is in the error and the key is not, because the key is in a
		// header. Worth keeping that way.
		return fmt.Errorf("neon: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("neon: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		api := &APIError{Status: resp.StatusCode, Message: strings.TrimSpace(string(payload))}
		var structured struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(payload, &structured) == nil && structured.Message != "" {
			api.Code, api.Message = structured.Code, structured.Message
		}
		return api
	}

	if out == nil {
		return nil
	}
	// Neon answers some deletes with 200 and no body at all. That is a
	// success, and decoding it as JSON is how "destroying something already
	// gone succeeds" stopped being true.
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("neon: decode %s %s: %w", method, path, err)
	}
	return nil
}

// operationsEnvelope is the part of a mutating response this client acts on.
// Every create, delete, and restore returns one.
type operationsEnvelope struct {
	Operations []Operation `json:"operations"`
}

// Await waits for every operation to reach a terminal state.
//
// Polled by identifier rather than by listing the project's operations,
// because a busy project has other work in flight and waiting for all of it
// would make one environment's branch wait on another's teardown.
func (c *Client) Await(ctx context.Context, ops []Operation) error {
	deadline := time.Now().Add(c.pollTimeout())
	for _, op := range ops {
		current := op
		for !current.Done() {
			if time.Now().After(deadline) {
				return fmt.Errorf("neon: operation %s (%s) was still %s after %s",
					current.Action, current.ID, current.Status, c.pollTimeout())
			}
			if err := c.sleep(ctx, c.pollInterval()); err != nil {
				return err
			}
			var got struct {
				Operation Operation `json:"operation"`
			}
			err := c.do(ctx, http.MethodGet,
				fmt.Sprintf("/projects/%s/operations/%s", c.ProjectID, current.ID), nil, &got)
			if err != nil {
				// An operation Neon has forgotten is one that finished long
				// enough ago to be pruned. Treating that as a failure would
				// make a slow caller fail for having been slow.
				if NotFound(err) {
					current.Status = "finished"
					break
				}
				return err
			}
			current = got.Operation
		}
		if !current.OK() {
			return &ErrOperationFailed{Op: current}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Branches
// ---------------------------------------------------------------------------

// CreateBranchRequest describes a branch to create.
type CreateBranchRequest struct {
	Name       string
	ParentID   string
	Annotation map[string]string
	// WithEndpoint attaches a read_write compute. A branch with no compute
	// cannot be connected to, so this is set for everything the engine hands
	// out a connection string for.
	WithEndpoint bool
}

type createBranchBody struct {
	Branch struct {
		Name     string `json:"name,omitempty"`
		ParentID string `json:"parent_id,omitempty"`
	} `json:"branch"`
	Endpoints       []map[string]string `json:"endpoints,omitempty"`
	AnnotationValue map[string]string   `json:"annotation_value,omitempty"`
}

type branchEnvelope struct {
	Branch     Branch      `json:"branch"`
	Endpoints  []Endpoint  `json:"endpoints"`
	Operations []Operation `json:"operations"`
}

// CreateBranch creates a branch and waits for it to be usable.
func (c *Client) CreateBranch(ctx context.Context, req CreateBranchRequest) (Branch, []Endpoint, error) {
	var body createBranchBody
	body.Branch.Name = req.Name
	body.Branch.ParentID = req.ParentID
	body.AnnotationValue = req.Annotation
	if req.WithEndpoint {
		body.Endpoints = []map[string]string{{"type": "read_write"}}
	}

	var env branchEnvelope
	if err := c.do(ctx, http.MethodPost, "/projects/"+c.ProjectID+"/branches", body, &env); err != nil {
		return Branch{}, nil, err
	}
	if err := c.Await(ctx, env.Operations); err != nil {
		// The branch may exist despite the failure. Left in place rather than
		// cleaned up here, because Inventory reports it and the leak detector
		// can see it; deleting it would hide the evidence of what went wrong.
		return env.Branch, env.Endpoints, err
	}
	env.Branch.Annotation = req.Annotation
	return env.Branch, env.Endpoints, nil
}

type listBranchesEnvelope struct {
	Branches    []Branch `json:"branches"`
	Annotations map[string]struct {
		Value map[string]string `json:"value"`
	} `json:"annotations"`
}

// ListBranches returns every branch in the project, with its annotation.
func (c *Client) ListBranches(ctx context.Context) ([]Branch, error) {
	var env listBranchesEnvelope
	if err := c.do(ctx, http.MethodGet, "/projects/"+c.ProjectID+"/branches", nil, &env); err != nil {
		return nil, err
	}
	for i := range env.Branches {
		if ann, ok := env.Annotations[env.Branches[i].ID]; ok {
			env.Branches[i].Annotation = ann.Value
		}
	}
	return env.Branches, nil
}

type getBranchEnvelope struct {
	Branch     Branch `json:"branch"`
	Annotation *struct {
		Value map[string]string `json:"value"`
	} `json:"annotation"`
}

// GetBranch reads one branch and its annotation.
func (c *Client) GetBranch(ctx context.Context, id string) (Branch, error) {
	var env getBranchEnvelope
	if err := c.do(ctx, http.MethodGet, "/projects/"+c.ProjectID+"/branches/"+id, nil, &env); err != nil {
		return Branch{}, err
	}
	if env.Annotation != nil {
		env.Branch.Annotation = env.Annotation.Value
	}
	return env.Branch, nil
}

// DeleteBranch removes a branch and waits for the removal to finish. Deleting
// one that is already gone succeeds, because teardown retries.
func (c *Client) DeleteBranch(ctx context.Context, id string) error {
	var env operationsEnvelope
	err := c.do(ctx, http.MethodDelete, "/projects/"+c.ProjectID+"/branches/"+id, nil, &env)
	if err != nil {
		if NotFound(err) {
			return nil
		}
		return err
	}
	return c.Await(ctx, env.Operations)
}

// RestoreBranch returns a branch to the state of another one, which is how a
// reset is done: restore the environment's branch to its golden.
func (c *Client) RestoreBranch(ctx context.Context, id, sourceID string) error {
	body := map[string]string{"source_branch_id": sourceID}
	var env operationsEnvelope
	if err := c.do(ctx, http.MethodPost,
		"/projects/"+c.ProjectID+"/branches/"+id+"/restore", body, &env); err != nil {
		return err
	}
	return c.Await(ctx, env.Operations)
}

// ListEndpoints returns the computes in the project.
func (c *Client) ListEndpoints(ctx context.Context) ([]Endpoint, error) {
	var env struct {
		Endpoints []Endpoint `json:"endpoints"`
	}
	if err := c.do(ctx, http.MethodGet, "/projects/"+c.ProjectID+"/endpoints", nil, &env); err != nil {
		return nil, err
	}
	return env.Endpoints, nil
}

// ConnectionURI asks Neon for a ready to use connection string.
//
// Asked for rather than assembled, because the password is Neon's and building
// the string here would mean fetching and handling it separately. The result is
// wrapped in a secrets.Value immediately, before it can reach anything that
// prints.
func (c *Client) ConnectionURI(ctx context.Context, branchID, database, role string, pooled bool) (secrets.Value, error) {
	q := url.Values{}
	q.Set("branch_id", branchID)
	q.Set("database_name", database)
	q.Set("role_name", role)
	// Sent explicitly in both directions. Omitting it does not mean "direct":
	// Neon defaults to the pooled host, so leaving it out hands a pooled
	// connection to pg_restore, which needs session level features a
	// transaction pooler does not have. The failure would be a restore that
	// half works.
	q.Set("pooled", strconv.FormatBool(pooled))
	var env struct {
		URI string `json:"uri"`
	}
	if err := c.do(ctx, http.MethodGet,
		"/projects/"+c.ProjectID+"/connection_uri?"+q.Encode(), nil, &env); err != nil {
		return secrets.Value{}, err
	}
	if env.URI == "" {
		return secrets.Value{}, fmt.Errorf("neon: no connection string for branch %s", branchID)
	}
	return secrets.NewFrom(env.URI, "neon"), nil
}

// ListDatabases returns the databases on a branch.
func (c *Client) ListDatabases(ctx context.Context, branchID string) ([]string, error) {
	var env struct {
		Databases []struct {
			Name string `json:"name"`
		} `json:"databases"`
	}
	if err := c.do(ctx, http.MethodGet,
		"/projects/"+c.ProjectID+"/branches/"+branchID+"/databases", nil, &env); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(env.Databases))
	for _, d := range env.Databases {
		names = append(names, d.Name)
	}
	return names, nil
}

// ListRoles returns the roles on a branch.
func (c *Client) ListRoles(ctx context.Context, branchID string) ([]string, error) {
	var env struct {
		Roles []struct {
			Name string `json:"name"`
		} `json:"roles"`
	}
	if err := c.do(ctx, http.MethodGet,
		"/projects/"+c.ProjectID+"/branches/"+branchID+"/roles", nil, &env); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(env.Roles))
	for _, r := range env.Roles {
		names = append(names, r.Name)
	}
	return names, nil
}

// DefaultBranch returns the project's default branch, which is what a golden
// candidate is created from.
func (c *Client) DefaultBranch(ctx context.Context) (Branch, error) {
	branches, err := c.ListBranches(ctx)
	if err != nil {
		return Branch{}, err
	}
	for _, b := range branches {
		if b.Default {
			return b, nil
		}
	}
	for _, b := range branches {
		if b.ParentID == "" {
			return b, nil
		}
	}
	return Branch{}, fmt.Errorf("neon: project %s has no default branch", c.ProjectID)
}
