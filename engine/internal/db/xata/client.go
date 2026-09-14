package xata

// The Xata control plane, as its own published OpenAPI document describes it.
//
// Every path, field name, enum and status code in this file was read from
// https://api.xata.tech/openapi.json on 2026-09-12, the document the reference
// pages under https://xata.io/docs/api-reference are generated from, rather
// than inferred from how a branching service usually looks. A fake control
// plane speaking endpoints somebody invented proves the provider agrees with
// its author's idea of the vendor, which this repository has already ruled is
// a fabrication that compiles.
//
// The six operations this provider needs:
//
//	POST   /organizations/{organizationID}/projects/{projectID}/branches
//	GET    /organizations/{organizationID}/projects/{projectID}/branches
//	GET    /organizations/{organizationID}/projects/{projectID}/branches/{branchID}
//	PATCH  /organizations/{organizationID}/projects/{projectID}/branches/{branchID}
//	DELETE /organizations/{organizationID}/projects/{projectID}/branches/{branchID}
//	GET    /organizations/{organizationID}/projects/{projectID}/branches/{branchID}/credentials
//
// Authentication is the document's apiKey scheme: "Bearer <api_key>" in the
// Authorization header. The key needs the branch:read, branch:write and
// credentials:read scopes, all three named in the same document.
//
// WHAT IS NOT MODELLED, and why the absence is deliberate. The create body is a
// oneOf discriminated by mode: "inherit" takes a parentID, "custom" takes a
// cluster configuration of region, storage, instance type, image and replicas.
// Only "inherit" is sent. This provider never needs an empty branch, because a
// golden is a copy on write branch of the project's own parent, and a cluster
// configuration this file could not check against a real account would be the
// guessing the paragraph above refuses.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
)

// DefaultBaseURL is Xata's control plane, the one server the document names.
const DefaultBaseURL = "https://api.xata.tech"

// Client talks to one Xata project.
type Client struct {
	// BaseURL overrides DefaultBaseURL, which is what the fake control plane
	// in the tests sets and the only thing it sets: every other line of this
	// client runs unchanged against the fake, so what the fake exercises is
	// the real request shapes rather than a test path.
	BaseURL string
	// Key is the bearer token.
	Key secrets.Value
	// OrgID and ProjectID address the project. Both are path segments, so both
	// are escaped rather than concatenated.
	OrgID     string
	ProjectID string
	// HTTP is the client to use. Nil means the air gap guarded default.
	HTTP *http.Client
	// Sleep is the clock's sleep, so a poll in a test does not really wait.
	Sleep func(ctx context.Context, d time.Duration) error
	// PollInterval and PollTimeout bound waiting for a branch to become ready.
	PollInterval time.Duration
	PollTimeout  time.Duration
	// Progress receives a line when a wait for a branch starts, while it lasts,
	// and when it ends. Nil reports nothing.
	Progress func(line string)
	// ProgressEvery is how often a wait that is still going says so. Zero means
	// thirty seconds.
	ProgressEvery time.Duration
}

// Branch is one Xata branch, in the fields this provider reads.
//
// The document returns three different shapes for a branch and this one type
// decodes all of them, which is only safe because every field it does not find
// is left at its zero value rather than refused. A create and a rename answer
// BranchShortMetadata, a listing answers BranchListMetadata, and only a get
// answers BranchMetadata, which is the one carrying status. So Status is empty
// everywhere except on a get, and nothing here reads it from anywhere else.
//
// ParentID is a POINTER because the document declares it nullable, and the root
// branch of a project has none. Decoding a nullable string into a string is the
// shape mismatch this repository keeps finding at boundaries: it would fail on
// exactly the one branch that has no parent, which every listing contains.
type Branch struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	CreatedAt   time.Time    `json:"createdAt"`
	UpdatedAt   time.Time    `json:"updatedAt"`
	ParentID    *string      `json:"parentID"`
	Region      string       `json:"region"`
	Status      BranchStatus `json:"status"`
}

// Parent is the parent branch identifier, or empty when there is none.
func (b Branch) Parent() string {
	if b.ParentID == nil {
		return ""
	}
	return *b.ParentID
}

// BranchStatus is the document's BranchStatus, in the two fields this reads.
//
// Both are read because they answer different questions. statusType is one of
// STATUS_TYPE_UNSPECIFIED, HEALTHY, TRANSIENT, FAULT and HIBERNATED.
// lifecycle.state is one of ready, creating, updating, upgrading and unknown.
// A branch that is ready and faulted is not one to hand an environment.
type BranchStatus struct {
	StatusType string `json:"statusType"`
	Lifecycle  struct {
		State string `json:"state"`
	} `json:"lifecycle"`
}

// Ready reports whether a branch can be connected to.
//
// HIBERNATED counts as ready because of scale to zero. Xata's branching page
// says a hibernated branch "wakes automatically when a new connection arrives",
// so treating it as not ready would make this provider wait out its own timeout
// on a branch that would have answered the first query.
//
// An absent statusType is tolerated rather than refused, because the lifecycle
// object is marked deprecated in the same document and a read boundary that
// failed on a field being dropped would turn a vendor's cleanup into an outage.
// UNSPECIFIED is not tolerated: it is the vendor saying it does not know.
func (s BranchStatus) Ready() bool {
	if s.Lifecycle.State != "" && s.Lifecycle.State != "ready" {
		return false
	}
	switch s.StatusType {
	case "", "STATUS_TYPE_HEALTHY", "STATUS_TYPE_HIBERNATED":
		return true
	default:
		return false
	}
}

// Credentials is the document's BranchCredentials.
type Credentials struct {
	Username         string `json:"username"`
	Password         string `json:"password"`
	Hostname         string `json:"hostname"`
	Port             int    `json:"port"`
	DBName           string `json:"dbname"`
	ConnectionString string `json:"connectionString"`
}

// APIError is a non 2xx answer from Xata, in the document's ErrorResponse
// shape: a message, a code, and an optional detail and hint.
type APIError struct {
	Status  int
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  string `json:"detail"`
	Hint    string `json:"hint"`
	// Body is what came back when it did not parse as the documented shape. A
	// gateway between the caller and Xata returns HTML, and an error that
	// swallowed it would leave an operator with a status code and nothing else.
	Body string
}

func (e *APIError) Error() string {
	detail := e.Message
	if detail == "" {
		detail = strings.TrimSpace(e.Body)
	}
	if e.Code != "" {
		detail = e.Code + ": " + detail
	}
	if e.Detail != "" {
		detail += " (" + e.Detail + ")"
	}
	if e.Hint != "" {
		detail += ". " + e.Hint
	}
	if detail == "" {
		detail = "no message"
	}
	return fmt.Sprintf("xata: HTTP %d: %s", e.Status, detail)
}

// NotFound reports whether an error is Xata saying the thing is not there.
//
// It exists so that destroying something already gone can succeed, which the
// provider interface requires of every destroy: the engine retries after
// timeouts, and a retry that failed on an absent resource would turn a
// successful teardown into a failed one.
func NotFound(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimSuffix(c.BaseURL, "/")
	}
	return DefaultBaseURL
}

// httpClient is the air gap guarded client, so an installation that has turned
// outbound traffic off refuses this site by name rather than reaching Xata.
func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return airgap.Client(airgap.SiteXata, 60*time.Second)
}

// branchesPath is the collection every call here hangs off.
//
// Both identifiers are path escaped. The document gives the organization id the
// pattern [a-zA-Z0-9_-~:]+ and the project id no pattern at all, and a provider
// that concatenated a caller supplied string into a URL would be one manifest
// away from a request to a path nobody wrote.
func (c *Client) branchesPath() string {
	return "/organizations/" + url.PathEscape(c.OrgID) +
		"/projects/" + url.PathEscape(c.ProjectID) + "/branches"
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("db.xata: encode the request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, reader)
	if err != nil {
		return fmt.Errorf("db.xata: build the request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Key.Reveal())
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("db.xata: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Bounded, because the body of an error is read into a message and an
	// unbounded read of somebody else's response is somebody else's decision
	// about this process's memory.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("db.xata: read the response to %s %s: %w", method, path, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		apiErr := &APIError{Status: resp.StatusCode, Body: string(raw)}
		// Decoded if it is the documented shape and kept as a body if it is
		// not. Tolerant on the read boundary: one unexpected response must not
		// turn a refusal Xata explained into an error that explains nothing.
		_ = json.Unmarshal(raw, apiErr)
		return apiErr
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("db.xata: decode the response to %s %s: %w", method, path, err)
	}
	return nil
}

// descriptionPattern is the document's pattern for a branch description, and
// descriptionMax its maxLength.
var descriptionPattern = regexp.MustCompile(`^([a-zA-Z0-9][a-zA-Z0-9\-_./: ]*)?$`)

const descriptionMax = 255

// CreateBranchRequest is a create, in the fields this provider sends.
type CreateBranchRequest struct {
	// Name is the branch name, which carries the kind and the identifier.
	Name string
	// ParentID is the branch this one is a copy on write branch of.
	ParentID string
	// Description carries the golden version, or the version a branch came
	// from. It is the branch object's only free text field.
	Description string
}

type createBranchBody struct {
	Name        string `json:"name"`
	Mode        string `json:"mode"`
	ParentID    string `json:"parentID"`
	Description string `json:"description,omitempty"`
}

// CreateBranch makes a copy on write branch of ParentID.
//
// The description is checked against the document's own pattern and length
// before anything is sent. Strict on the write boundary: a golden version
// identifier always fits, so a description that does not is a defect in this
// process, and refusing it here names that defect where a 400 from Xata would
// name nothing.
func (c *Client) CreateBranch(ctx context.Context, req CreateBranchRequest) (Branch, error) {
	if len(req.Description) > descriptionMax || !descriptionPattern.MatchString(req.Description) {
		return Branch{}, fmt.Errorf(
			"db.xata: the branch description %q is not one Xata accepts, which is at most %d "+
				"characters starting with a letter or a digit and then only letters, digits, "+
				"spaces, hyphens, underscores, dots, slashes and colons",
			req.Description, descriptionMax)
	}
	var out Branch
	body := createBranchBody{
		Name: req.Name,
		// Always inherit. See this file's header for why custom is not sent.
		Mode:        "inherit",
		ParentID:    req.ParentID,
		Description: req.Description,
	}
	if err := c.do(ctx, http.MethodPost, c.branchesPath(), body, &out); err != nil {
		return Branch{}, err
	}
	return out, nil
}

type listBranchesEnvelope struct {
	Branches []Branch `json:"branches"`
}

// ListBranches returns every branch in the project, without status.
func (c *Client) ListBranches(ctx context.Context) ([]Branch, error) {
	var env listBranchesEnvelope
	if err := c.do(ctx, http.MethodGet, c.branchesPath(), nil, &env); err != nil {
		return nil, err
	}
	return env.Branches, nil
}

// GetBranch reads one branch, which is the only call whose answer has a status.
func (c *Client) GetBranch(ctx context.Context, id string) (Branch, error) {
	var out Branch
	if err := c.do(ctx, http.MethodGet, c.branchesPath()+"/"+url.PathEscape(id), nil, &out); err != nil {
		return Branch{}, err
	}
	return out, nil
}

// RenameBranch is the publish, and it is the only atomic moment available.
//
// A golden becomes a golden when its name gains the golden prefix, exactly as
// in the Neon provider and for the same reason: the attestation does not exist
// until the candidate has been masked and scanned, which is after the branch
// was created, so there is no way to create a branch that is already published.
func (c *Client) RenameBranch(ctx context.Context, id, name string) error {
	body := map[string]string{"name": name}
	return c.do(ctx, http.MethodPatch, c.branchesPath()+"/"+url.PathEscape(id), body, nil)
}

// DeleteBranch removes a branch. One that is already gone is not an error.
func (c *Client) DeleteBranch(ctx context.Context, id string) error {
	err := c.do(ctx, http.MethodDelete, c.branchesPath()+"/"+url.PathEscape(id), nil, nil)
	if NotFound(err) {
		return nil
	}
	return err
}

// Credentials returns the connection details for a branch.
func (c *Client) Credentials(ctx context.Context, id string) (Credentials, error) {
	var out Credentials
	path := c.branchesPath() + "/" + url.PathEscape(id) + "/credentials"
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return Credentials{}, err
	}
	return out, nil
}

// ConnectionString is the credentials call reduced to the one value the engine
// hands an environment, as a secret.
//
// It returns a secrets.Value rather than a string, so it renders as redacted
// everywhere text is produced and cannot reach a log by accident. The document
// says the string "carries no sslmode parameter, clients choose their own TLS
// settings", so this adds none: a provider that appended one would be deciding
// somebody's transport security from a default.
func (c *Client) ConnectionString(ctx context.Context, id string) (secrets.Value, error) {
	creds, err := c.Credentials(ctx, id)
	if err != nil {
		return secrets.Value{}, err
	}
	if creds.ConnectionString == "" {
		return secrets.Value{}, fmt.Errorf(
			"db.xata: branch %s returned credentials with no connection string", id)
	}
	return secrets.NewFrom(creds.ConnectionString, "xata branch "+id), nil
}

func (c *Client) pollInterval() time.Duration {
	if c.PollInterval > 0 {
		return c.PollInterval
	}
	return 2 * time.Second
}

func (c *Client) pollTimeout() time.Duration {
	if c.PollTimeout > 0 {
		return c.PollTimeout
	}
	return 5 * time.Minute
}

func (c *Client) progressEvery() time.Duration {
	if c.ProgressEvery > 0 {
		return c.ProgressEvery
	}
	return 30 * time.Second
}

func (c *Client) report(line string) {
	if c.Progress != nil {
		c.Progress(line)
	}
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// AwaitReady polls a branch until its own status says it is ready.
//
// A 201 says the branch exists, and the document's lifecycle states include
// creating, so it does not say anything can connect yet. Polling the status
// rather than trying the connection is what makes the failure legible: a branch
// that never leaves creating is a different problem from one that is ready and
// refusing connections, and a provider that only tried to connect would report
// both as a timeout.
//
// A wait of up to five minutes is also one nobody watching af up can see, so it
// is REPORTED: a line when the branch is first found not ready, a line every
// ProgressEvery while it stays that way, and a line when it becomes ready. A
// branch ready on the first poll reports nothing, because there was no wait and
// a line about one would be noise on every branch. No line carries anything but
// the branch identifier, the elapsed time and the status Xata reported.
func (c *Client) AwaitReady(ctx context.Context, id string) (Branch, error) {
	start := time.Now()
	deadline := start.Add(c.pollTimeout())
	var last Branch
	waiting := false
	lastReport := start
	for {
		b, err := c.GetBranch(ctx, id)
		if err != nil {
			return Branch{}, err
		}
		last = b
		if b.Status.Ready() {
			if waiting {
				c.report(fmt.Sprintf("xata: branch %s is ready after %s",
					id, time.Since(start).Round(time.Second)))
			}
			return b, nil
		}
		switch now := time.Now(); {
		case !waiting:
			waiting = true
			lastReport = now
			c.report(fmt.Sprintf("xata: waiting for branch %s to become ready, Xata reports it %s",
				id, describeStatus(b.Status)))
		case now.Sub(lastReport) >= c.progressEvery():
			lastReport = now
			c.report(fmt.Sprintf("xata: still waiting for branch %s after %s, Xata reports it %s",
				id, now.Sub(start).Round(time.Second), describeStatus(b.Status)))
		}
		if time.Now().After(deadline) {
			return last, fmt.Errorf(
				"db.xata: branch %s was still %q after %s; a branch that does not become "+
					"ready is a different problem from one that is ready and refusing "+
					"connections, and this says which",
				id, describeStatus(b.Status), c.pollTimeout())
		}
		if err := c.sleep(ctx, c.pollInterval()); err != nil {
			return last, err
		}
	}
}

// describeStatus is what a timeout message says the branch was doing.
func describeStatus(s BranchStatus) string {
	switch {
	case s.Lifecycle.State != "" && s.StatusType != "":
		return s.Lifecycle.State + "/" + s.StatusType
	case s.Lifecycle.State != "":
		return s.Lifecycle.State
	case s.StatusType != "":
		return s.StatusType
	default:
		return "reporting no status at all"
	}
}
