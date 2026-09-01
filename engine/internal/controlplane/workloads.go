package controlplane

// The engine's half of a hosted workload run.
//
// WHY THE ENGINE ASKS RATHER THAN BEING TOLD.
//
// A hosted run starts as a `workflow_dispatch` against the customer's own
// repository, and a dispatch carries only the inputs that workflow DECLARES.
// GitHub reads the declaration from the repository's DEFAULT BRANCH and answers
// a dispatch carrying an undeclared input with a 422 that is indistinguishable
// from the file being missing, so the control plane cannot add the run
// identifier to a dispatch without breaking every copy of the workflow already
// in the wild. The control plane therefore sends what to run, and the engine
// asks this endpoint which recorded request it belongs to.
//
// That also makes the correlation survive the dispatch failing. A run whose
// dispatch was refused, because no App is installed or Actions are off, is
// still claimable by an engine somebody starts by hand.
//
// `af workload run --run-id` remains, and remains the only way to run a hosted
// definition without claiming: a person reproducing a run on a laptop must not
// take the next queued run away from CI.

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
)

// WorkloadRun is a run the control plane is waiting for an engine to carry out.
//
// The field names are the ones the endpoint sends, which are camel case because
// they are aliased in SQL rather than mapped in TypeScript. Timestamps arrive
// as RFC 3339 in UTC because the endpoint formats them in SQL rather than
// leaving them to the driver; the reason is recorded beside the query, and it
// is that Postgres's own text form is not RFC 3339 and every strict parser
// rejects it.
type WorkloadRun struct {
	// RunID is what every workload event has to carry back.
	RunID string `json:"runId"`
	// Workload is the definition's slug. The identifier throughout Studio is
	// the slug and not the row id.
	Workload string `json:"workload"`
	// Kind is the control plane's workload_kind, spelled the way the engine's
	// own workload.Kind spells it.
	Kind string `json:"kind"`
	// Version is the immutable definition version this run was requested at.
	Version int `json:"version"`
	// Body is the version's knobs. Read as an object rather than a typed shape
	// because the engine takes its knobs from the dispatch inputs; this is here
	// so a mismatch can be reported rather than guessed at.
	Body map[string]any `json:"body"`
	// Attempt counts from one. A second attempt is a retry the control plane
	// created, not a resend of this one.
	Attempt int `json:"attempt"`
	// DeadlineAt is when the control plane gives up and calls the run
	// abandoned. A heartbeat pushes it.
	DeadlineAt time.Time `json:"deadlineAt"`
	// LeaseExpiresAt is when another engine may take this run. A heartbeat
	// pushes it too, and the two are different numbers on purpose: the lease
	// bounds a dead process and the deadline bounds silence.
	LeaseExpiresAt time.Time `json:"leaseExpiresAt"`
}

// claimResponse is the endpoint's envelope.
//
// A run of null is the ordinary answer and not an error: most engines asking
// have nothing waiting for them. The endpoint answers 200 with a null run
// rather than 204 for exactly that reason, so this decodes a body rather than
// reading a status code.
type claimResponse struct {
	Run *WorkloadRun `json:"run"`
}

// ClaimWorkload takes the run waiting for an environment, if there is one.
//
// A nil run with a nil error means nothing is waiting. An unknown environment
// is a NotFound, which is the same answer the control plane gives whether the
// environment belongs to another organization or does not exist.
func (c *Client) ClaimWorkload(ctx context.Context, envID string) (*WorkloadRun, error) {
	if strings.TrimSpace(envID) == "" {
		return nil, errors.New("controlplane: an environment identifier is required to claim a run")
	}
	body, err := json.Marshal(map[string]string{"envId": envID})
	if err != nil {
		return nil, fmt.Errorf("controlplane: %w", err)
	}

	res, err := c.do(ctx, http.MethodPost, "/v1/workloads/claim", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode == http.StatusNotFound {
		return nil, &NotFound{EnvID: envID}
	}
	if res.StatusCode != http.StatusOK {
		return nil, c.statusError(res)
	}

	var out claimResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("controlplane: the response was not the expected shape: %w", err)
	}
	if out.Run == nil {
		return nil, nil
	}
	if strings.TrimSpace(out.Run.RunID) == "" {
		// A run with no identifier cannot be reported on, and reporting on the
		// wrong row is worse than not reporting at all. Refused here rather
		// than carried into a payload the projection would reject with a
		// sentence about a missing workload_run_id.
		return nil, errors.New("controlplane: a claimed run carried no run identifier")
	}
	return out.Run, nil
}

// LeaseLost reports that this engine no longer holds the run it is working on.
//
// A typed error rather than a status code because the right response is
// specific and is not "retry": the run has finished, been cancelled, or had its
// lease taken after it expired, and another engine may already be running it.
// Whatever this process reports about it from here is at best a duplicate.
type LeaseLost struct {
	RunID  string
	Detail string
}

func (e *LeaseLost) Error() string {
	if e.Detail != "" {
		return e.Detail
	}
	return fmt.Sprintf("the control plane no longer holds run %s for this engine", e.RunID)
}

// Heartbeat says this engine is still working on a run.
//
// It pushes the lease and the deadline together, so a run that keeps reporting
// is never abandoned for taking a long time, and a run whose engine died is
// abandoned a deadline after its last word rather than a deadline after it was
// asked for.
//
// A LeaseLost is the interesting failure and the one a caller must act on. Any
// other error is the network, and a caller should keep working: a heartbeat
// that could not be sent says nothing about whether the work is going well.
func (c *Client) Heartbeat(ctx context.Context, runID string) error {
	if strings.TrimSpace(runID) == "" {
		return errors.New("controlplane: a run identifier is required to heartbeat")
	}
	res, err := c.do(ctx, http.MethodPost,
		"/v1/workloads/runs/"+url.PathEscape(runID)+"/heartbeat", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode == http.StatusConflict {
		return &LeaseLost{RunID: runID, Detail: c.errorSentence(res)}
	}
	if res.StatusCode != http.StatusOK {
		return c.statusError(res)
	}
	return nil
}

// Command is one durable instruction the control plane is waiting to have
// carried out.
type Command struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// EnvID is the environment an engine knows, or empty when the command
	// names a run instead.
	EnvID string `json:"envId"`
	// WorkloadRunID is the run a workload.cancel is about.
	WorkloadRunID string         `json:"workloadRunId"`
	Payload       map[string]any `json:"payload"`
	Attempts      int            `json:"attempts"`
}

// CommandCancelWorkload is the kind that asks a run to stop.
const CommandCancelWorkload = "workload.cancel"

type commandsResponse struct {
	Commands []Command `json:"commands"`
}

// ClaimCommands takes the durable commands waiting for an environment.
//
// This is how a cancel pressed in a console reaches a run that is already
// going. Nothing else carries it: the claim endpoint refuses a run that has a
// cancel outstanding, and the heartbeat answers whether the lease is held
// rather than whether somebody asked for a stop, so an engine that never asks
// here runs a cancelled workload to completion.
//
// Deliberately reachable while an organization is suspended, on the control
// plane's side. A suspension stops new work and a cancel is the opposite of
// new work.
func (c *Client) ClaimCommands(ctx context.Context, envID string, limit int) ([]Command, error) {
	request := map[string]any{}
	if strings.TrimSpace(envID) != "" {
		request["envId"] = envID
	}
	if limit > 0 {
		request["limit"] = limit
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("controlplane: %w", err)
	}

	res, err := c.do(ctx, http.MethodPost, "/v1/commands/claim", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		return nil, c.statusError(res)
	}
	var out commandsResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("controlplane: the response was not the expected shape: %w", err)
	}
	// Never nil, so a caller can range over the answer without asking whether
	// the question was understood. An empty list and an absent field are the
	// same answer here and both mean nothing is waiting.
	if out.Commands == nil {
		return []Command{}, nil
	}
	return out.Commands, nil
}

// errorSentence pulls the control plane's own explanation out of a refusal.
//
// The sentence is worth carrying because these endpoints write one: the
// heartbeat's 409 says the lease may have been taken, which tells an engine to
// stop rather than to retry. Bounded and scrubbed for the same reasons
// statusError bounds and scrubs, and empty when the body is not what this
// expects, so a caller falls back to its own wording rather than printing a
// fragment of somebody else's HTML.
func (c *Client) errorSentence(res *http.Response) string {
	snippet, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(snippet, &body); err != nil {
		return ""
	}
	return c.scrub(strings.TrimSpace(body.Error))
}
