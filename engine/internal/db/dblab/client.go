// Package dblab implements the database provider backed by a Database Lab
// Engine, the self hosted thin cloning server from Postgres.ai.
//
// Three things about the Database Lab Engine decide the shape of everything
// here.
//
// It is self hosted, so it sits between the two providers that already exist.
// Like Neon it is an HTTP API that hands back connection strings to databases
// this process does not run. Like Docker it is something the developer stood
// up, on hardware they can see, with no account and no bill. That is why the
// instance is named in the manifest rather than discovered: there is no
// account to enumerate.
//
// It is asynchronous everywhere it matters. Creating a clone returns
// immediately with the status CREATING, resetting one returns RESETTING, and
// deleting one returns before the container is gone. A client that returns as
// soon as the HTTP call does hands back a connection string to a Postgres that
// is not listening yet, and reports a branch as destroyed while it is still in
// the inventory. So every mutating call waits for the state it asked for.
//
// It never gives a clone's password back. The engine stores the ephemeral
// user's name and database and deliberately does not store the password, so
// GET /clone answers with an empty one. A provider that remembers the password
// in memory would hand out a working connection string until the process
// restarted and a broken one afterwards. See derivedPassword in dblab.go for
// what this provider does instead.
package dblab

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

// DefaultBaseURL is where a Database Lab Engine listens when it was started
// from the project's own instructions.
const DefaultBaseURL = "http://127.0.0.1:2345"

// TokenHeader is the header the engine authenticates with. It is not a bearer
// token and sending it as one fails with a 401 that says nothing useful.
const TokenHeader = "Verification-Token"

// Client talks to a Database Lab Engine.
type Client struct {
	BaseURL string
	// Token is a secrets.Value so that it renders as redacted anywhere a
	// client is printed, including in a test failure that dumps a struct.
	Token secrets.Value
	HTTP  *http.Client
	// Sleep is how the client waits between polls. Injected so a test does not
	// spend real seconds proving that polling works.
	Sleep func(context.Context, time.Duration) error
	// PollInterval and PollTimeout bound waiting for a clone to settle.
	PollInterval time.Duration
	PollTimeout  time.Duration
	// Retries bounds attempts at an idempotent request. Zero uses four.
	Retries int
}

// Time is a timestamp as the Database Lab Engine writes one.
//
// It needs its own type because the engine's own LocalTime marshals a zero
// time as an empty string rather than omitting the field, and marshals a set
// one in the server's local zone. A plain time.Time would fail to parse the
// empty string, and because the snapshots and clones arrive as JSON arrays,
// that single failure would discard the whole list: one snapshot with no
// recorded creation time would make ListGoldens return nothing at all and look
// exactly like an engine holding no goldens. Being tolerant on the read
// boundary is what stops one odd element blanking the feature.
type Time struct{ time.Time }

// legacyTimeFormat is what older engines wrote, and what a clone session
// restored from an older state file still carries.
const legacyTimeFormat = "2006-01-02 15:04:05 UTC"

// UnmarshalJSON accepts every form the engine emits: an empty string, a JSON
// null, RFC 3339, and the legacy format.
func (t *Time) UnmarshalJSON(data []byte) error {
	raw := strings.Trim(strings.TrimSpace(string(data)), `"`)
	if raw == "" || raw == "null" {
		return nil
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		t.Time = parsed
		return nil
	}
	parsed, err := time.Parse(legacyTimeFormat, raw)
	if err != nil {
		// Still not an error. A timestamp this client cannot read is worth
		// less than the resource it is attached to, and refusing the whole
		// list because of one is the failure this type exists to prevent.
		return nil
	}
	t.Time = parsed
	return nil
}

// MarshalJSON writes RFC 3339, or an empty string for a zero time, which is
// what the engine itself does.
func (t Time) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte(`""`), nil
	}
	return []byte(`"` + t.UTC().Format(time.RFC3339) + `"`), nil
}

// Snapshot is a point in time in a pool. It is what a golden version is.
type Snapshot struct {
	ID           string   `json:"id"`
	CreatedAt    Time     `json:"createdAt"`
	DataStateAt  Time     `json:"dataStateAt"`
	PhysicalSize int64    `json:"physicalSize"`
	LogicalSize  int64    `json:"logicalSize"`
	Pool         string   `json:"pool"`
	NumClones    int      `json:"numClones"`
	Clones       []string `json:"clones"`
	Branch       string   `json:"branch"`
	// Message is a free text commit message the engine stores as a ZFS user
	// property on the snapshot and returns on every listing. It is where this
	// provider records what makes a snapshot a golden version. See meta.go.
	Message   string `json:"message"`
	Protected bool   `json:"protected"`
}

// Database is how a clone is reached.
//
// Password is present in the JSON and is always empty on a read: the engine
// records the ephemeral user's name, database and owner and deliberately does
// not record the password it was given. Kept here so the shape matches the
// wire and nobody has to rediscover that it is always empty.
type Database struct {
	ConnStr  string `json:"connStr"`
	Host     string `json:"host"`
	Port     string `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	DBName   string `json:"dbName"`
}

// Status is a clone's state.
type Status struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// The status codes a clone passes through.
const (
	StatusOK        = "OK"
	StatusCreating  = "CREATING"
	StatusResetting = "RESETTING"
	StatusDeleting  = "DELETING"
	StatusFatal     = "FATAL"
)

// Settled reports whether a clone has stopped changing on its own.
func (s Status) Settled() bool {
	switch s.Code {
	case StatusCreating, StatusResetting, StatusDeleting:
		return false
	}
	return true
}

// Clone is one thin clone. It is what a branch is.
type Clone struct {
	ID        string    `json:"id"`
	Snapshot  *Snapshot `json:"snapshot"`
	Branch    string    `json:"branch"`
	Protected bool      `json:"protected"`
	CreatedAt Time      `json:"createdAt"`
	Status    Status    `json:"status"`
	DB        Database  `json:"db"`
	Metadata  struct {
		CloneDiffSize int64   `json:"cloneDiffSize"`
		LogicalSize   int64   `json:"logicalSize"`
		CloningTime   float64 `json:"cloningTime"`
	} `json:"metadata"`
}

// SnapshotID returns the snapshot a clone came from, or the empty string.
//
// The field is a pointer on the wire and a clone that failed to create carries
// a nil one, so every read of it goes through here.
func (c Clone) SnapshotID() string {
	if c.Snapshot == nil {
		return ""
	}
	return c.Snapshot.ID
}

// CreateCloneRequest is the body of a clone creation.
type CreateCloneRequest struct {
	ID       string `json:"id"`
	Snapshot struct {
		ID string `json:"id"`
	} `json:"snapshot"`
	Branch string `json:"branch,omitempty"`
	DB     struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		Restricted bool   `json:"restricted"`
		DBName     string `json:"db_name,omitempty"`
	} `json:"db"`
}

// APIError is a non-2xx answer, carrying enough to tell a missing resource
// from a rejected one without matching on prose.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("dblab: %d %s: %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("dblab: %d: %s", e.Status, e.Message)
}

// NotFound reports whether an error is the engine saying the thing is not
// there. Every destroy asks, because destroying something already gone has to
// succeed: teardown retries.
//
// It matches on the message as well as the status because the engine answers a
// missing clone with 404 NOT_FOUND and a missing snapshot with 400 BAD_REQUEST
// whose message says the snapshot does not exist. Treating that 400 as a real
// failure made "destroying twice succeeds" false for goldens while it was true
// for branches, which is the kind of asymmetry nobody finds by reading.
func NotFound(err error) bool {
	var api *APIError
	if !errors.As(err, &api) {
		return false
	}
	if api.Status == http.StatusNotFound || api.Code == "NOT_FOUND" {
		return true
	}
	lower := strings.ToLower(api.Message)
	return strings.Contains(lower, "not found") ||
		strings.Contains(lower, "does not exist") ||
		strings.Contains(lower, "no such")
}

// HasDependents reports whether an error is the engine refusing to delete
// something because a clone or a child dataset still comes from it.
//
// Recognised so that the provider can answer with AF-DB-005 and the count the
// operator can act on, rather than passing through a message about datasets.
func HasDependents(err error) bool {
	var api *APIError
	if !errors.As(err, &api) {
		return false
	}
	lower := strings.ToLower(api.Message)
	return strings.Contains(lower, "dependent") ||
		strings.Contains(lower, "has clones") ||
		strings.Contains(lower, "cannot destroy") ||
		strings.Contains(lower, "filesystem has children")
}

// Unauthorized reports whether the engine rejected the verification token.
func Unauthorized(err error) bool {
	var api *APIError
	return errors.As(err, &api) &&
		(api.Status == http.StatusUnauthorized || api.Code == "UNAUTHORIZED")
}

// ---------------------------------------------------------------------------
// Calls
// ---------------------------------------------------------------------------

// ListSnapshots returns every snapshot the engine holds, newest first.
func (c *Client) ListSnapshots(ctx context.Context) ([]Snapshot, error) {
	var out []Snapshot
	if err := c.do(ctx, http.MethodGet, "/snapshots", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetSnapshot returns one snapshot.
func (c *Client) GetSnapshot(ctx context.Context, id string) (Snapshot, error) {
	var out Snapshot
	err := c.do(ctx, http.MethodGet, "/snapshot/"+escapeID(id), nil, &out)
	return out, err
}

// SnapshotClone freezes a clone's current state into a new snapshot.
//
// This is the publish step of a refresh, and the reason this provider can
// produce a masked golden at all: the engine's own retrieval brings production
// in unmasked, and the only way to get masked data into a snapshot is to put
// it into a clone first and then commit that clone.
func (c *Client) SnapshotClone(ctx context.Context, cloneID, message string) (string, error) {
	body := map[string]string{"cloneID": cloneID, "message": message}
	var out struct {
		SnapshotID string `json:"snapshotID"`
	}
	if err := c.do(ctx, http.MethodPost, "/branch/snapshot", body, &out); err != nil {
		return "", err
	}
	if out.SnapshotID == "" {
		return "", fmt.Errorf("dblab: the engine created a snapshot of clone %s and returned no identifier", cloneID)
	}
	return out.SnapshotID, nil
}

// DeleteSnapshot removes a snapshot. Force removes it along with anything
// that depends on it, and is never used by this provider: a golden with a live
// branch must be refused, not collected.
func (c *Client) DeleteSnapshot(ctx context.Context, id string, force bool) error {
	path := "/snapshot/" + escapeID(id)
	if force {
		path += "?force=true"
	}
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// ListClones returns every clone the engine holds.
func (c *Client) ListClones(ctx context.Context) ([]Clone, error) {
	// The engine answers with a bare array here and with an object elsewhere,
	// so this decodes into both shapes rather than assuming one. A version
	// that changes its mind about the envelope should not empty the inventory,
	// because an empty inventory is what the leak detector reads as "nothing
	// was left behind".
	var payload json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/clones", nil, &payload); err != nil {
		return nil, err
	}
	var list []Clone
	if err := json.Unmarshal(payload, &list); err == nil {
		return list, nil
	}
	var wrapped struct {
		Clones []Clone `json:"clones"`
	}
	if err := json.Unmarshal(payload, &wrapped); err != nil {
		return nil, fmt.Errorf("dblab: decode the clone listing: %w", err)
	}
	return wrapped.Clones, nil
}

// GetClone returns one clone.
func (c *Client) GetClone(ctx context.Context, id string) (Clone, error) {
	var out Clone
	err := c.do(ctx, http.MethodGet, "/clone/"+url.PathEscape(id), nil, &out)
	return out, err
}

// CreateClone starts a clone and returns as soon as the engine has accepted
// it. The clone is not usable yet; call AwaitClone.
func (c *Client) CreateClone(ctx context.Context, req CreateCloneRequest) (Clone, error) {
	var out Clone
	err := c.do(ctx, http.MethodPost, "/clone", req, &out)
	return out, err
}

// DeleteClone removes a clone and waits until it is gone.
//
// The wait is not politeness. The engine answers the delete before the
// container is stopped and the dataset destroyed, and the conformance suite
// reads the inventory immediately afterwards; returning early reports a branch
// as destroyed while it is still there, which is precisely the leak the
// journal exists to catch.
func (c *Client) DeleteClone(ctx context.Context, id string) error {
	if err := c.do(ctx, http.MethodDelete, "/clone/"+url.PathEscape(id), nil, nil); err != nil {
		if NotFound(err) {
			return nil
		}
		return err
	}
	return c.awaitGone(ctx, id)
}

// ResetClone returns a clone to a named snapshot and waits for it to come
// back.
//
// The snapshot is always named. The engine also accepts {"latest": true},
// which this client deliberately does not offer: the latest snapshot may be a
// golden published by a refresh that happened while an environment was up, and
// resetting to that would silently move the environment onto data it never
// branched from.
func (c *Client) ResetClone(ctx context.Context, id, snapshotID string) error {
	if snapshotID == "" {
		return fmt.Errorf("dblab: reset of clone %s needs a snapshot to reset to", id)
	}
	body := map[string]any{"snapshotID": snapshotID, "latest": false}
	if err := c.do(ctx, http.MethodPost, "/clone/"+url.PathEscape(id)+"/reset", body, nil); err != nil {
		return err
	}
	_, err := c.AwaitClone(ctx, id)
	return err
}

// AwaitClone polls until a clone has settled, and reports the state it settled
// into.
//
// A clone that reaches FATAL is an error here rather than a value the caller
// has to remember to check, because every caller wants the same thing: a clone
// it can connect to.
func (c *Client) AwaitClone(ctx context.Context, id string) (Clone, error) {
	deadline := time.Now().Add(c.pollTimeout())
	for {
		clone, err := c.GetClone(ctx, id)
		if err != nil {
			return Clone{}, err
		}
		if clone.Status.Code == StatusFatal {
			detail := clone.Status.Message
			if detail == "" {
				detail = "the engine reported no reason"
			}
			return clone, fmt.Errorf("dblab: clone %s failed: %s", id, detail)
		}
		if clone.Status.Settled() {
			return clone, nil
		}
		if time.Now().After(deadline) {
			return clone, fmt.Errorf("dblab: clone %s was still %s after %s",
				id, clone.Status.Code, c.pollTimeout())
		}
		if err := c.sleep(ctx, c.pollInterval()); err != nil {
			return Clone{}, err
		}
	}
}

// awaitGone polls until a clone no longer exists.
func (c *Client) awaitGone(ctx context.Context, id string) error {
	deadline := time.Now().Add(c.pollTimeout())
	for {
		_, err := c.GetClone(ctx, id)
		if err != nil {
			if NotFound(err) {
				return nil
			}
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("dblab: clone %s still existed %s after it was deleted",
				id, c.pollTimeout())
		}
		if err := c.sleep(ctx, c.pollInterval()); err != nil {
			return err
		}
	}
}

// ---------------------------------------------------------------------------
// Transport
// ---------------------------------------------------------------------------

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

// DefaultPollTimeout bounds how long a mutating call waits for the engine to
// finish what it started.
//
// Ten minutes rather than the three this began with, and the number is
// measured rather than chosen. A clone is a ZFS clone, a container start and a
// Postgres recovery, and the engine drives that last part by shelling out to
// docker and psql in a poll loop. One clone on an idle engine takes about
// ninety seconds; a second one, created while the first is still running, took
// longer than three minutes on the machine this was proved on, and the client
// gave up on a clone that was healthy a moment later. That failure is
// expensive rather than merely slow: the engine had already created the clone,
// so a caller that gives up leaves it behind.
//
// This is a backstop and not the real deadline. Every wait sleeps through the
// caller's context, so an af up with its own deadline still fails on time.
const DefaultPollTimeout = 10 * time.Minute

func (c *Client) pollTimeout() time.Duration {
	if c.PollTimeout > 0 {
		return c.PollTimeout
	}
	return DefaultPollTimeout
}

const defaultRetries = 4

func (c *Client) retries() int {
	if c.Retries > 0 {
		return c.Retries
	}
	return defaultRetries
}

// escapeID escapes a snapshot identifier for a path segment.
//
// Snapshot identifiers contain slashes and an at sign: the engine's own
// example is dblab_pool/dataset_2/main/20230224202652@20230224202652. The
// route is registered as {id:.*}, so the slashes must survive rather than be
// percent encoded, while everything else must be escaped. url.PathEscape would
// encode the slashes and route the request to nothing.
func escapeID(id string) string {
	parts := strings.Split(id, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

// do performs a request, retrying the ones that are safe to retry.
//
// GET is retried; POST and DELETE are not. The asymmetry matters. A clone
// creation that timed out may have reached the engine, and sending it again
// would either make a second clone or fail with "clone with such ID already
// exists"; idempotency for creates lives in the caller instead, which looks
// for an existing clone by its derived identifier before making one. DELETE is
// not retried here because DeleteClone already handles the only outcome a
// retry would fix, by treating a missing clone as success.
//
// What counts as worth retrying: a transport failure, because a connection
// that was refused while the engine restarted says nothing about the request;
// 429, because the engine is asking; and 5xx, because it is the engine's side.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	attempts := 1
	if method == http.MethodGet {
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

// worthRetrying reports whether an error says nothing about whether the
// request would fail again.
func worthRetrying(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var api *APIError
	if errors.As(err, &api) {
		return api.Status == http.StatusTooManyRequests || api.Status >= 500
	}
	// Anything that is not an answer from the engine is a transport failure: a
	// refused connection, a reset socket, a name that did not resolve. None of
	// those is a verdict on the request. A malformed body is, so it is not
	// retried.
	var syntax *json.SyntaxError
	var typ *json.UnmarshalTypeError
	return !errors.As(err, &syntax) && !errors.As(err, &typ)
}

func (c *Client) attempt(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("dblab: encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, reader)
	if err != nil {
		return fmt.Errorf("dblab: build request: %w", err)
	}
	// The token goes in a header, never in the URL, so that the URL is safe to
	// put in an error message and the token is not in any of them.
	req.Header.Set(TokenHeader, c.Token.Reveal())
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("dblab: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("dblab: read response: %w", err)
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
	// The engine answers a delete with 200 and an empty body. That is a
	// success, and decoding it as JSON is how "destroying something already
	// gone succeeds" stops being true. The same shape caught the Neon
	// provider, which is why it is checked here before anybody hits it.
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("dblab: decode %s %s: %w", method, path, err)
	}
	return nil
}
