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
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/controlplane"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/redact"
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
		_, _ = w.Write([]byte(`{"held":true,"cancelRequested":false}`))
	}))
	cancelRequested, err := c.Heartbeat(context.Background(), claimedRunID)
	if err != nil {
		t.Fatal(err)
	}
	if sawPath != "/v1/workloads/runs/"+claimedRunID+"/heartbeat" {
		t.Fatalf("heartbeat posted to %q", sawPath)
	}
	if cancelRequested == nil || *cancelRequested {
		t.Fatal("the control plane said cancelRequested false and the engine did not read it")
	}
}

// The cancel rides the beat.
//
// This is the whole reason there is no command client in this package. The
// alternative was a poll of /v1/commands/claim beside every heartbeat, which
// cost a minute of latency on top of the beat and took a lease on every
// unrelated command it happened to return.
func TestHeartbeatCarriesTheCancelBack(t *testing.T) {
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"held":true,"cancelRequested":true}`))
	}))
	cancelRequested, err := c.Heartbeat(context.Background(), claimedRunID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelRequested == nil || !*cancelRequested {
		t.Fatal("a cancel was waiting and the beat did not carry it back")
	}
}

// An older control plane answers `{held: true}` and the answer is UNKNOWN.
//
// Not false, and the distinction is the whole test. A control plane that
// predates the cancel riding the beat cannot say anything about a cancel, and
// flattening that to "no cancel" is a cancel button in the console that
// silently does nothing: no error, no log line, no symptom except a control
// somebody pressed having no effect.
//
// Only the engine can tell absent from false, and only here. The control plane
// cannot make an older control plane send a field. So the distinguishable
// outcome is asserted rather than the tolerated one: this proves the engine
// knows that it does not know.
func TestAHeartbeatWithNoCancelFieldIsUnknownRatherThanNoCancel(t *testing.T) {
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"held":true}`))
	}))
	cancelRequested, err := c.Heartbeat(context.Background(), claimedRunID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelRequested != nil {
		t.Fatalf("an absent cancelRequested decoded to %v; absent and false have to stay "+
			"distinguishable, because only one of them means the console's cancel works",
			*cancelRequested)
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
	_, err := c.Heartbeat(context.Background(), claimedRunID)
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
	_, err := c.Heartbeat(context.Background(), claimedRunID)
	if err == nil {
		t.Fatal("a 502 was reported as a successful heartbeat")
	}
	var lost *controlplane.LeaseLost
	if errors.As(err, &lost) {
		t.Fatal("a 502 was reported as a lost lease, which would stop a healthy run")
	}
}

// What the control plane says about an event it stored and did not apply.
//
// The sentence is the only account there is of a report that landed and
// changed nothing, which is the difference between "the console has my
// numbers" and "the console says nobody reported". It was written at one end
// and discarded at the other: the wire type had no `note` field, and the only
// caller of Send threw the whole result away.
func TestTheControlPlanesOwnExplanationReachesTheEngine(t *testing.T) {
	var said []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`{"accepted":2,"duplicates":1,"rejected":1,"unprojected":1,"outcomes":[
		  {"id":"a","status":"accepted","note":"Stored, and the run was already abandoned, so nothing was applied."},
		  {"id":"b","status":"duplicate"},
		  {"id":"c","status":"rejected","reason":"the event has no id"},
		  {"id":"d","status":"accepted"}
		]}`))
	}))
	t.Cleanup(srv.Close)
	c, err := controlplane.New(controlplane.Options{
		BaseURL: srv.URL, Token: "aft_" + strings.Repeat("a", 40),
		HTTP: srv.Client(), Redactor: redact.New(),
	})
	if err != nil {
		t.Fatal(err)
	}

	sink := controlplane.NewSink(controlplane.SinkOptions{
		Client: c, Clock: clock.NewFake(time.Now()),
		OnError: func(err error) { said = append(said, err.Error()) },
	})
	if err := sink.Deliver(context.Background(), events.Event{
		ID: "a", Type: events.WorkloadFinished, TS: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	if len(said) == 0 {
		t.Fatal("the control plane explained why two events changed nothing and the engine said nothing")
	}
	joined := strings.Join(said, "\n")
	if !strings.Contains(joined, "already abandoned") {
		t.Errorf("the note was decoded and lost; got %q", joined)
	}
	if !strings.Contains(joined, "the event has no id") {
		t.Errorf("the rejection reason did not reach the engine; got %q", joined)
	}
	// A duplicate is the ordinary result of a resend and of the idempotency key
	// working. Reporting it would make every spool drain look like a fault.
	if strings.Contains(joined, "duplicate") {
		t.Errorf("a duplicate was reported as something changing nothing; got %q", joined)
	}
	if !strings.Contains(joined, "2 of 4") {
		t.Errorf("the count should be exact and over the whole batch; got %q", joined)
	}
}
