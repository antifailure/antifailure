// Package controlplane talks to a self-hosted or hosted control plane.
//
// It is optional. Everything the engine does works with no control plane at
// all, and that is deliberate: a preview environment must not stop working
// because a web application somewhere is down. So every call here fails soft,
// and the failure is reported rather than propagated.
//
// The client is deliberately small. It sends events and it pulls an
// environment's configuration, and it does nothing else. Anything that decides
// what an environment should look like happens in the engine, on the machine
// the environment is on, from a manifest that is in the repository. A control
// plane that could change what an environment does would be a control plane
// that could change what an environment masks, and there is no version of that
// which is acceptable.
package controlplane

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
	"sync"
	"time"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/redact"
)

// DefaultBaseURL is the hosted instance.
const DefaultBaseURL = "https://app.antifailure.dev"

// Client is a connection to one control plane.
type Client struct {
	baseURL *url.URL
	http    *http.Client
	clock   clock.Clock
	// redactor scrubs anything before it is written to a log line here. The
	// control plane's own error bodies are quoted in diagnostics, and a body
	// that echoes a request header would otherwise print a token.
	redactor *redact.Redactor

	// mu guards the credential, which is replaced in place when a minted one
	// expires. Flushes can overlap, so two goroutines can be reading it while a
	// third is renewing it.
	mu sync.Mutex
	// token is the credential currently presented.
	token string
	// renew obtains a fresh one, or is nil when the credential is static.
	renew func(context.Context) (string, error)
	// lastRenew bounds how often a refusal may trigger an exchange, because a
	// control plane that refuses everything must not be answered with one
	// exchange per batch.
	lastRenew time.Time
}

// RenewFloor is the least time between two credential exchanges.
//
// A refused batch is the signal to renew, and a control plane refusing for a
// reason renewing cannot fix, a revoked organization, say, would otherwise turn
// every flush into an identity exchange. One attempt a minute is enough to
// recover from an expiry promptly and slow enough not to become a loop.
const RenewFloor = time.Minute

// Options configures a client.
type Options struct {
	// BaseURL is the control plane's root. Empty means the hosted instance.
	BaseURL string
	// Token authenticates this engine. It is never logged and never written to
	// disk by this package.
	Token string
	// Renew obtains a fresh credential when the control plane refuses the
	// current one, and nil means the credential is static.
	//
	// This exists because a minted credential is deliberately short lived and a
	// run outlives it. A token acquired once when the environment came up and
	// then held would work for the first minutes of an `af ci` and quietly stop
	// reporting for the rest, which is the worst of both: the dashboard shows a
	// run that started and never finished, and nothing says why.
	Renew func(context.Context) (string, error)
	Clock clock.Clock
	// HTTP is the transport, for tests.
	HTTP *http.Client
	// Redactor scrubs strings before they appear in an error.
	Redactor *redact.Redactor
}

// ErrNotConfigured is returned when no token is available.
//
// It is a distinct error rather than a generic one because the right response
// is different: the user has to create a token, and telling them "unauthorized"
// would send them looking for a permissions problem that does not exist.
var ErrNotConfigured = errors.New("no control plane token is configured")

// New builds a client.
func New(opts Options) (*Client, error) {
	raw := strings.TrimSpace(opts.BaseURL)
	if raw == "" {
		raw = DefaultBaseURL
	}
	base, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("controlplane: %q is not a URL: %w", raw, err)
	}
	if base.Scheme != "https" && base.Hostname() != "localhost" && base.Hostname() != "127.0.0.1" {
		// A token sent over plain HTTP to anywhere but the local machine is a
		// token on the wire. Refused rather than warned about, because a
		// warning during a CI run is a warning nobody reads.
		return nil, fmt.Errorf(
			"controlplane: %s is not https, and a token must not be sent in the clear", raw)
	}
	if strings.TrimSpace(opts.Token) == "" {
		return nil, ErrNotConfigured
	}
	if opts.Redactor == nil {
		// Refused rather than defaulted. This client is the only thing in the
		// engine that sends an event off the machine, every payload it carries
		// was assembled somewhere else, and a redactor that may be nil is a
		// redaction that may be skipped. Defaulting to redact.New() here would
		// be worse than refusing: it would silently disagree with whatever
		// secrets the caller had registered, and scrub less than the caller
		// believed it was scrubbing.
		return nil, errors.New(
			"controlplane: a client needs a redactor, because everything it sends leaves the machine")
	}

	c := opts.Clock
	if c == nil {
		c = clock.New()
	}
	httpClient := opts.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		baseURL:  base,
		token:    opts.Token,
		renew:    opts.Renew,
		http:     httpClient,
		clock:    c,
		redactor: opts.Redactor,
	}, nil
}

// bearer reads the credential currently presented.
func (c *Client) bearer() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

// renewCredential exchanges for a fresh credential, at most once a minute.
//
// It reports whether the credential changed. False covers every reason not to
// have another go: a static token, a renewal that failed, one attempted too
// recently, and one that came back with the same value it replaced. The caller
// retries only on true, so a refusal that renewing cannot fix costs one
// exchange rather than one per batch forever.
func (c *Client) renewCredential(ctx context.Context) bool {
	c.mu.Lock()
	if c.renew == nil {
		c.mu.Unlock()
		return false
	}
	now := c.clock.Now()
	if !c.lastRenew.IsZero() && now.Sub(c.lastRenew) < RenewFloor {
		c.mu.Unlock()
		return false
	}
	c.lastRenew = now
	previous := c.token
	renew := c.renew
	c.mu.Unlock()

	// Outside the lock. The exchange is two network calls and holding the
	// credential's mutex across them would stall every flush behind it.
	fresh, err := renew(ctx)
	if err != nil || strings.TrimSpace(fresh) == "" || fresh == previous {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = fresh
	return true
}

// Event is one event in the wire form the control plane accepts.
//
// Deliberately not the engine's own Event type. The two are allowed to differ:
// the engine's carries a level and a message for display, and the control plane
// wants an idempotency key and a sequence. Sharing one struct would mean a
// field added for the dashboard changes the ingestion contract.
type Event struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	EnvID      string         `json:"envId,omitempty"`
	Sequence   uint64         `json:"sequence,omitempty"`
	OccurredAt time.Time      `json:"occurredAt"`
	Payload    map[string]any `json:"payload,omitempty"`
}

// SendResult reports what the control plane did with a batch.
type SendResult struct {
	Accepted   int `json:"accepted"`
	Duplicates int `json:"duplicates"`
	Rejected   int `json:"rejected"`
	// Unprojected counts events that were stored and changed nothing. It is
	// not a failure and it is not a success either: the event is on record and
	// whatever it was meant to advance did not advance.
	Unprojected int       `json:"unprojected"`
	Outcomes    []Outcome `json:"outcomes"`
}

// Outcome is what happened to one event.
//
// `reason` and `note` are two fields because they are two answers, and reading
// only one of them is how this type came to discard half of what it was told.
// A rejected event has a `reason` and was not stored. An accepted event with a
// `note` WAS stored and changed nothing, which is the more interesting of the
// two: it is the control plane saying it understood the event and could not
// apply it, and it is the only signal that distinguishes a report that landed
// from one that was refused by a projection.
//
// The engine decoded `reason` and not `note`. The control plane's own comment
// says the note "reaches the sender in the batch response", and it did not
// reach it: it was dropped by this struct and then, further along, the whole
// SendResult was discarded by the only caller. Two silent losses in one path,
// on the one channel that explains why a run said nothing.
type Outcome struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	Note   string `json:"note,omitempty"`
}

// Explanation is the sentence this outcome carries, or empty when it carries
// none. A duplicate carries neither and is not worth a word: it is the ordinary
// result of a resend and the idempotency key working.
func (o Outcome) Explanation() string {
	if o.Reason != "" {
		return o.Reason
	}
	return o.Note
}

// Throttled is returned when the control plane asks for a pause.
//
// It carries the delay so that a caller can obey it. A caller that retries
// immediately after a 429 turns a busy control plane into an unreachable one,
// which is why this is a typed error rather than a status code somebody has to
// remember to check.
type Throttled struct {
	RetryAfter time.Duration
}

func (t *Throttled) Error() string {
	return fmt.Sprintf("the control plane asked for a pause of %s before the same batch is sent again",
		t.RetryAfter)
}

// MaxBatch is the most events one request may carry. It matches the control
// plane's limit; sending more is refused there rather than truncated.
const MaxBatch = 500

// Send delivers a batch of events.
//
// Events are idempotent by ID, so a caller that is unsure whether a previous
// attempt landed should send the same batch again rather than trying to work
// out which half to resend.
func (c *Client) Send(ctx context.Context, events []Event) (SendResult, error) {
	var zero SendResult
	if len(events) == 0 {
		return zero, nil
	}
	if len(events) > MaxBatch {
		return zero, fmt.Errorf(
			"controlplane: a batch carries at most %d events and this one carries %d",
			MaxBatch, len(events))
	}

	body, err := json.Marshal(map[string]any{"events": scrub(c.redactor, events)})
	if err != nil {
		return zero, fmt.Errorf("controlplane: %w", err)
	}

	res, err := c.do(ctx, http.MethodPost, "/v1/events", bytes.NewReader(body))
	if err != nil {
		return zero, err
	}
	defer func() { _ = res.Body.Close() }()

	// A minted credential is short lived by design, and a run outlives it. The
	// control plane says so with a 401, which is the only signal there is: the
	// engine is not told when the credential dies and polling for it would be a
	// worse design than being told. So a refusal is one reason to try once more
	// with a fresh one, and exactly once, because the second 401 means renewing
	// is not the answer.
	//
	// The batch is re-read from the same bytes rather than re-marshalled, so
	// the retry sends the identical payload and the control plane's idempotency
	// on event ID does the rest if the first attempt was somehow applied.
	if res.StatusCode == http.StatusUnauthorized && c.renewCredential(ctx) {
		_ = res.Body.Close()
		res, err = c.do(ctx, http.MethodPost, "/v1/events", bytes.NewReader(body))
		if err != nil {
			return zero, err
		}
		defer func() { _ = res.Body.Close() }()
	}

	if res.StatusCode == http.StatusTooManyRequests {
		return zero, &Throttled{RetryAfter: retryAfter(res, 30*time.Second)}
	}
	// 202 for a clean batch, 207 when some events were rejected. Both are
	// answers rather than failures: the rejected ones are named in the body,
	// and a caller that treats 207 as an error would resend the accepted ones
	// forever.
	if res.StatusCode != http.StatusAccepted && res.StatusCode != http.StatusMultiStatus {
		return zero, c.statusError(res)
	}

	var out SendResult
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return zero, fmt.Errorf("controlplane: the response was not the expected shape: %w", err)
	}
	return out, nil
}

// Environment is what the control plane knows about one environment.
type Environment struct {
	EnvID         string    `json:"env_id"`
	Repository    string    `json:"repository"`
	Branch        string    `json:"branch"`
	PullRequest   *int      `json:"pull_request"`
	State         string    `json:"state"`
	PreviewURL    string    `json:"preview_url"`
	Runtime       string    `json:"runtime"`
	GoldenVersion string    `json:"golden_version"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// NotFound reports that the control plane has no such environment.
type NotFound struct{ EnvID string }

func (e *NotFound) Error() string {
	return fmt.Sprintf("the control plane has no environment called %q for this organization", e.EnvID)
}

// Pull fetches one environment's record.
func (c *Client) Pull(ctx context.Context, envID string) (Environment, error) {
	var zero Environment
	if strings.TrimSpace(envID) == "" {
		return zero, errors.New("controlplane: an environment identifier is required")
	}

	// The engine's own endpoint, not the one the web application uses. That one
	// is authenticated by a session cookie, and an engine on a CI runner has no
	// browser to get one from. An earlier version of this called the web API
	// with a bearer token and was refused every time, which is the failure mode
	// worth naming: the code existed, compiled, and could never have worked.
	res, err := c.do(ctx, http.MethodGet, "/v1/environments/"+url.PathEscape(envID), nil)
	if err != nil {
		return zero, err
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode == http.StatusNotFound {
		return zero, &NotFound{EnvID: envID}
	}
	if res.StatusCode != http.StatusOK {
		return zero, c.statusError(res)
	}

	var env Environment
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		return zero, fmt.Errorf("controlplane: the response was not the expected shape: %w", err)
	}
	if env.EnvID == "" {
		return zero, &NotFound{EnvID: envID}
	}
	return env, nil
}

// Ping checks that the control plane is reachable and the token works.
//
// Used by af doctor, which wants to distinguish three states that look alike
// from the outside: unreachable, reachable but unauthenticated, and working.
func (c *Client) Ping(ctx context.Context) error {
	res, err := c.do(ctx, http.MethodGet, "/health", nil)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return c.statusError(res)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	target := *c.baseURL
	if i := strings.IndexByte(path, '?'); i >= 0 {
		target.Path = strings.TrimRight(target.Path, "/") + path[:i]
		target.RawQuery = path[i+1:]
	} else {
		target.Path = strings.TrimRight(target.Path, "/") + path
	}

	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, fmt.Errorf("controlplane: %w", err)
	}
	req.Header.Set("authorization", "Bearer "+c.bearer())
	req.Header.Set("accept", "application/json")
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}

	res, err := c.http.Do(req)
	if err != nil {
		// The URL is included and the token is not. A transport error names the
		// host, which is what somebody needs to see, and nothing else about the
		// request.
		return nil, fmt.Errorf("controlplane: could not reach %s: %w", c.baseURL.Host, err)
	}
	return res, nil
}

func (c *Client) statusError(res *http.Response) error {
	// Bounded, because an error path must not read an unbounded body from a
	// server that may not be the one intended.
	snippet, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
	detail := c.scrub(strings.TrimSpace(string(snippet)))

	switch res.StatusCode {
	case http.StatusUnauthorized:
		// The command that fixes it, not the place it used to say to go. There
		// was no screen in the control plane that created an engine token when
		// this sentence was written, so it sent a reader looking for one.
		return fmt.Errorf(
			"controlplane: the token was refused. Run 'af token create ci' and set " +
				"AF_CONTROL_PLANE_TOKEN to what it prints, or run 'af login' again")
	case http.StatusForbidden:
		return fmt.Errorf("controlplane: this token does not have permission to do that")
	}
	if detail == "" {
		return fmt.Errorf("controlplane: %s answered %d", c.baseURL.Host, res.StatusCode)
	}
	return fmt.Errorf("controlplane: %s answered %d: %s", c.baseURL.Host, res.StatusCode, detail)
}

// scrub removes anything sensitive from text that is about to be shown.
func (c *Client) scrub(s string) string {
	if c.redactor == nil {
		return s
	}
	return c.redactor.String(s)
}

func retryAfter(res *http.Response, fallback time.Duration) time.Duration {
	raw := res.Header.Get("retry-after")
	if raw == "" {
		return fallback
	}
	if seconds, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(raw); err == nil {
		if d := time.Until(at); d > 0 {
			return d
		}
	}
	return fallback
}

// scrub redacts every payload string in a batch on its way to the wire.
//
// It is here, in the one function through which every event leaves the machine,
// rather than at the call sites that build events. Both paths to the network
// pass through Send: the live one from the sink's buffer and the drained one
// from the spool. A call site somebody forgot is how a secret reaches a log,
// and this is the writer.
//
// The local log, the spool, span attributes and the bytes sent to an OTLP
// collector each redact at their own writer for the same reason. This one was
// missing, and it was the only one of the five that leaves the machine.
//
// Identifiers are left alone deliberately. ID is the idempotency key the
// control plane deduplicates on, Type is a closed vocabulary, and EnvID is
// named by the operator; passing them through the redactor could only change a
// value that has to match on the other side.
func scrub(r *redact.Redactor, in []Event) []Event {
	if r == nil {
		return in
	}
	out := make([]Event, len(in))
	for i, e := range in {
		// A nil payload is left nil rather than turned into an empty object,
		// so redaction cannot change the shape of what is sent.
		if e.Payload != nil {
			if scrubbed, ok := scrubValue(r, e.Payload).(map[string]any); ok {
				e.Payload = scrubbed
			}
		}
		out[i] = e
	}
	return out
}

// scrubValue copies a payload value, redacting every string it contains.
//
// It copies rather than editing in place because the batch it is handed is
// still owned by the sink's buffer and by the spool, and a failed send is
// retried from those. Editing in place would work here and would silently
// corrupt anything that later wanted the original.
func scrubValue(r *redact.Redactor, v any) any {
	switch t := v.(type) {
	case string:
		return r.String(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, inner := range t {
			out[k] = scrubValue(r, inner)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, inner := range t {
			out[i] = scrubValue(r, inner)
		}
		return out
	default:
		return v
	}
}
