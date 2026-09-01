package controlplane_test

// The claim and the heartbeat, against the shapes the control plane really
// sends.
//
// Every body in this file was copied from the endpoints in
// web/apps/api/src/server.ts rather than written from a description of them.
// That matters most for the two timestamps: they are formatted in SQL, as
// `to_char(... 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`, precisely because Postgres's own
// text form is not RFC 3339 and a strict parser on this side rejects it. A test
// that invented its own timestamp format would prove that a decoder can read a
// document nobody sends.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/antifailure/antifailure/engine/internal/controlplane"
)

// claimBody is what POST /v1/workloads/claim answers with a run waiting.
const claimBody = `{"run":{
  "runId":"5e4a1c8e-1f0b-4a35-9a1b-0c6d2f8e7a91",
  "workload":"checkout-mix",
  "kind":"observed_load",
  "version":3,
  "body":{"durationSeconds":60,"scale":1},
  "attempt":1,
  "deadlineAt":"2026-09-01T08:40:00Z",
  "leaseExpiresAt":"2026-09-01T06:55:00Z"
}}`

const claimedRunID = "5e4a1c8e-1f0b-4a35-9a1b-0c6d2f8e7a91"

func TestClaimReadsTheRunTheControlPlaneActuallySends(t *testing.T) {
	var sawPath, sawEnv, sawAuth string
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		sawAuth = r.Header.Get("authorization")
		var body struct {
			EnvID string `json:"envId"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sawEnv = body.EnvID
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(claimBody))
	}))

	run, err := c.ClaimWorkload(context.Background(), "pr-42")
	if err != nil {
		t.Fatal(err)
	}
	if sawPath != "/v1/workloads/claim" {
		t.Fatalf("posted to %q", sawPath)
	}
	if sawEnv != "pr-42" {
		t.Fatalf("the environment was sent as %q", sawEnv)
	}
	if !strings.HasPrefix(sawAuth, "Bearer ") {
		t.Fatalf("the claim went out without a bearer token: %q", sawAuth)
	}
	if run == nil {
		t.Fatal("a run was waiting and none was decoded")
	}
	if run.RunID != claimedRunID {
		t.Fatalf("run id %q", run.RunID)
	}
	if run.Workload != "checkout-mix" {
		// The identifier throughout Studio is the slug, not the row id, and a
		// decoder reading the wrong field here would put a uuid in front of
		// somebody.
		t.Fatalf("workload %q, which should be the slug", run.Workload)
	}
	if run.Kind != "observed_load" || run.Version != 3 || run.Attempt != 1 {
		t.Fatalf("kind %q version %d attempt %d", run.Kind, run.Version, run.Attempt)
	}
	if run.Body["scale"] != float64(1) {
		t.Fatalf("the version body did not decode as an object: %#v", run.Body)
	}
	// The two timestamps, which is what this test exists for.
	if !run.DeadlineAt.Equal(time.Date(2026, 9, 1, 8, 40, 0, 0, time.UTC)) {
		t.Fatalf("deadline %v", run.DeadlineAt)
	}
	if !run.LeaseExpiresAt.Equal(time.Date(2026, 9, 1, 6, 55, 0, 0, time.UTC)) {
		t.Fatalf("lease expiry %v", run.LeaseExpiresAt)
	}
}

// Nothing waiting is 200 with a null run, and it is the ordinary answer.
//
// The endpoint chose 200 over 204 so that a poller reading a status code and
// not a body cannot mistake "nothing waiting" for "something went wrong with
// the shape". This is the test on this side of that decision.
func TestClaimWithNothingWaitingIsNotAnError(t *testing.T) {
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"run":null}`))
	}))
	run, err := c.ClaimWorkload(context.Background(), "pr-42")
	if err != nil {
		t.Fatalf("nothing waiting was reported as an error: %v", err)
	}
	if run != nil {
		t.Fatalf("a run was decoded out of a null: %#v", run)
	}
}

func TestClaimOfAnUnknownEnvironmentIsANotFound(t *testing.T) {
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"No environment named pr-42 in this organization."}`))
	}))
	_, err := c.ClaimWorkload(context.Background(), "pr-42")
	var missing *controlplane.NotFound
	if !errors.As(err, &missing) {
		t.Fatalf("a 404 should be a NotFound, got %v", err)
	}
}

// A run with no identifier is refused here rather than carried.
//
// It cannot happen against the endpoint as written. It is refused anyway
// because the failure it would cause is silent and remote: an event with no
// workload_run_id is stored, projected against nothing, and answered with a
// sentence in a batch response the engine prints to a log nobody reads, while
// the run it was meant to be about abandons two hours later.
func TestAClaimedRunWithNoIdentifierIsRefused(t *testing.T) {
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"run":{"runId":"","kind":"observed_load"}}`))
	}))
	_, err := c.ClaimWorkload(context.Background(), "pr-42")
	if err == nil {
		t.Fatal("a run with no identifier was accepted")
	}
}

func TestHeartbeatNamesTheRunInThePath(t *testing.T) {
	var sawPath string
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		_, _ = w.Write([]byte(`{"held":true}`))
	}))
	if err := c.Heartbeat(context.Background(), claimedRunID); err != nil {
		t.Fatal(err)
	}
	if sawPath != "/v1/workloads/runs/"+claimedRunID+"/heartbeat" {
		t.Fatalf("heartbeat posted to %q", sawPath)
	}
}

// THE ORDERING NOBODY HAD TESTED: the lease runs out while the work is running.
//
// The control plane answers 409 with a sentence saying the run may have
// finished, been cancelled, or had its lease taken after it expired. It is a
// typed error here rather than a status code because the right response is not
// "retry": another engine may already be running this, and a caller that keeps
// working and reports at the end is reporting over somebody else's run.
func TestALostLeaseIsATypedErrorCarryingTheReason(t *testing.T) {
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"Run ` + claimedRunID +
			` is not held by this token. It may have finished, been cancelled, or had its ` +
			`lease taken after it expired. Stop and claim again."}`))
	}))
	err := c.Heartbeat(context.Background(), claimedRunID)
	var lost *controlplane.LeaseLost
	if !errors.As(err, &lost) {
		t.Fatalf("a 409 should be a LeaseLost, got %v", err)
	}
	if !strings.Contains(lost.Error(), "Stop and claim again") {
		t.Fatalf("the control plane's own sentence was lost: %q", lost.Error())
	}
}

// A heartbeat that fails for any other reason is NOT a lost lease.
//
// The distinction is the whole value of the type. A network error means the
// control plane could not be reached, which says nothing about whether this
// engine still holds the run, and treating it as a lost lease would stop a
// healthy run because a dashboard was briefly down.
func TestAnUnreachableControlPlaneIsNotALostLease(t *testing.T) {
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	err := c.Heartbeat(context.Background(), claimedRunID)
	if err == nil {
		t.Fatal("a 502 was reported as a successful heartbeat")
	}
	var lost *controlplane.LeaseLost
	if errors.As(err, &lost) {
		t.Fatal("a 502 was reported as a lost lease, which would stop a healthy run")
	}
}

func TestClaimCommandsReadsTheCancelForARun(t *testing.T) {
	var sawLimit float64
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		sawLimit, _ = body["limit"].(float64)
		_, _ = w.Write([]byte(`{"commands":[
		  {"id":"c1","kind":"workload.cancel","envId":"pr-42",
		   "workloadRunId":"` + claimedRunID + `","payload":{},"attempts":1}
		]}`))
	}))
	commands, err := c.ClaimCommands(context.Background(), "pr-42", 5)
	if err != nil {
		t.Fatal(err)
	}
	if sawLimit != 5 {
		t.Fatalf("the limit was sent as %v", sawLimit)
	}
	if len(commands) != 1 {
		t.Fatalf("decoded %d commands", len(commands))
	}
	if commands[0].Kind != controlplane.CommandCancelWorkload {
		t.Fatalf("kind %q", commands[0].Kind)
	}
	if commands[0].WorkloadRunID != claimedRunID {
		t.Fatalf("the run this cancel is about decoded as %q", commands[0].WorkloadRunID)
	}
}

// An empty list and an absent field are the same answer and both mean nothing
// is waiting. Never nil, so a caller can range over the answer without asking
// whether the question was understood.
func TestClaimCommandsWithNothingWaitingIsAnEmptyList(t *testing.T) {
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	commands, err := c.ClaimCommands(context.Background(), "pr-42", 5)
	if err != nil {
		t.Fatal(err)
	}
	if commands == nil {
		t.Fatal("an absent commands field decoded as nil rather than as an empty list")
	}
	if len(commands) != 0 {
		t.Fatalf("decoded %d commands out of nothing", len(commands))
	}
}
