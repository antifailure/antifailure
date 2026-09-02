package controlplane_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/controlplane"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/redact"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// newClient points a client at a test server. httptest serves plain HTTP on
// 127.0.0.1, which the client permits precisely because it is the local
// machine; anywhere else it refuses to send a token in the clear, and there is
// a test for that below.
func newClient(t *testing.T, h http.Handler) (*controlplane.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := controlplane.New(controlplane.Options{
		BaseURL:  srv.URL,
		Token:    "aft_" + strings.Repeat("a", 40),
		HTTP:     srv.Client(),
		Redactor: redact.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return c, srv
}

func TestNew_RefusesToSendATokenInTheClear(t *testing.T) {
	_, err := controlplane.New(controlplane.Options{
		BaseURL: "http://control.example.com",
		Token:   "aft_secret",
	})
	if err == nil {
		t.Fatal("a token was allowed over plain HTTP to a remote host")
	}
	if !strings.Contains(err.Error(), "not https") {
		t.Fatalf("the error should say why: %v", err)
	}
	// And it must not quote the token while explaining.
	if strings.Contains(err.Error(), "aft_secret") {
		t.Fatal("the error message contains the token")
	}
}

func TestNew_AllowsPlainHTTPToTheLocalMachine(t *testing.T) {
	// Local development runs the control plane on localhost without a
	// certificate. Refusing that would mean nobody can try it.
	for _, host := range []string{"http://localhost:8080", "http://127.0.0.1:8080"} {
		if _, err := controlplane.New(controlplane.Options{
			BaseURL: host, Token: "aft_x", Redactor: redact.New(),
		}); err != nil {
			t.Errorf("%s: %v", host, err)
		}
	}
}

func TestNew_SaysWhenNoTokenIsConfigured(t *testing.T) {
	_, err := controlplane.New(controlplane.Options{BaseURL: "https://app.test"})
	if !errors.Is(err, controlplane.ErrNotConfigured) {
		t.Fatalf("want ErrNotConfigured, got %v", err)
	}
}

func TestSend_CarriesTheTokenAndTheBatch(t *testing.T) {
	var gotAuth string
	var gotBody struct {
		Events []controlplane.Event `json:"events"`
	}
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"accepted": len(gotBody.Events)})
	}))

	res, err := c.Send(t.Context(), []controlplane.Event{
		{ID: "a", Type: "environment.ready", EnvID: "env-1", Sequence: 3, OccurredAt: time.Unix(0, 0).UTC()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Accepted != 1 {
		t.Fatalf("accepted = %d", res.Accepted)
	}
	if !strings.HasPrefix(gotAuth, "Bearer aft_") {
		t.Fatalf("authorization header = %q", gotAuth)
	}
	if len(gotBody.Events) != 1 || gotBody.Events[0].Sequence != 3 {
		t.Fatalf("body did not round trip: %+v", gotBody.Events)
	}
}

func TestSend_TreatsAPartlyRejectedBatchAsAnAnswerRatherThanAFailure(t *testing.T) {
	// 207 means some events were rejected. A caller that treated it as an error
	// would resend the accepted ones forever.
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`{"accepted":1,"rejected":1,"outcomes":[
			{"id":"a","status":"accepted"},
			{"id":"b","status":"rejected","reason":"the event has no type"}]}`))
	}))

	res, err := c.Send(t.Context(), []controlplane.Event{
		{ID: "a", Type: "environment.ready", OccurredAt: time.Now()},
		{ID: "b", OccurredAt: time.Now()},
	})
	if err != nil {
		t.Fatalf("207 was reported as an error: %v", err)
	}
	if res.Accepted != 1 || res.Rejected != 1 {
		t.Fatalf("res = %+v", res)
	}
	if res.Outcomes[1].Reason == "" {
		t.Fatal("the rejection reason was dropped, so nobody can fix the event")
	}
}

func TestSend_ReportsAThrottleWithItsDelay(t *testing.T) {
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("retry-after", "42")
		w.WriteHeader(http.StatusTooManyRequests)
	}))

	_, err := c.Send(t.Context(), []controlplane.Event{{ID: "a", Type: "x", OccurredAt: time.Now()}})
	var throttled *controlplane.Throttled
	if !errors.As(err, &throttled) {
		t.Fatalf("want a Throttled error, got %v", err)
	}
	if throttled.RetryAfter != 42*time.Second {
		t.Fatalf("RetryAfter = %s", throttled.RetryAfter)
	}
}

func TestSend_UsesAFallbackDelayWhenTheHeaderIsMissingOrJunk(t *testing.T) {
	// A 429 with no usable Retry-After must still produce a delay. Zero would
	// mean retry immediately, which is what turns a busy service into a
	// dead one.
	for _, header := range []string{"", "soon", "-1"} {
		t.Run(fmt.Sprintf("header=%q", header), func(t *testing.T) {
			c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if header != "" {
					w.Header().Set("retry-after", header)
				}
				w.WriteHeader(http.StatusTooManyRequests)
			}))
			_, err := c.Send(t.Context(), []controlplane.Event{{ID: "a", Type: "x", OccurredAt: time.Now()}})
			var throttled *controlplane.Throttled
			if !errors.As(err, &throttled) {
				t.Fatalf("want Throttled, got %v", err)
			}
			if throttled.RetryAfter <= 0 {
				t.Fatalf("RetryAfter = %s, which means retry at once", throttled.RetryAfter)
			}
		})
	}
}

func TestSend_RefusesABatchLargerThanTheLimitWithoutSendingIt(t *testing.T) {
	var called bool
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	}))

	oversized := make([]controlplane.Event, controlplane.MaxBatch+1)
	for i := range oversized {
		oversized[i] = controlplane.Event{ID: fmt.Sprint(i), Type: "x", OccurredAt: time.Now()}
	}
	if _, err := c.Send(t.Context(), oversized); err == nil {
		t.Fatal("an oversized batch was accepted")
	}
	if called {
		t.Fatal("an oversized batch was sent anyway, which wastes a round trip to be refused")
	}
}

func TestSend_ExplainsARefusedToken(t *testing.T) {
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"This token is not valid."}`))
	}))

	_, err := c.Send(t.Context(), []controlplane.Event{{ID: "a", Type: "x", OccurredAt: time.Now()}})
	if err == nil {
		t.Fatal("want an error")
	}
	// The next step has to be in the message. "401" sends somebody looking at
	// permissions; the actual fix is a new token.
	if !strings.Contains(err.Error(), "AF_CONTROL_PLANE_TOKEN") {
		t.Fatalf("the error does not say what to do: %v", err)
	}
}

func TestErrors_DoNotQuoteTheToken(t *testing.T) {
	// A control plane that echoes the request in its error body would otherwise
	// put the token in a log line.
	secret := "aft_" + strings.Repeat("s", 40)
	r := redact.New()
	r.Register(secret)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintf(w, "failed to handle request with authorization: %s", r.Header.Get("authorization"))
	}))
	t.Cleanup(srv.Close)

	c, err := controlplane.New(controlplane.Options{
		BaseURL: srv.URL, Token: secret, HTTP: srv.Client(), Redactor: r,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.Send(t.Context(), []controlplane.Event{{ID: "a", Type: "x", OccurredAt: time.Now()}})
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("the token was echoed into an error message: %v", err)
	}
}

func TestPull_ReadsTheEngineEndpointRatherThanTheApplicationAPI(t *testing.T) {
	// The application API is authenticated by a session cookie, and an engine on
	// a CI runner has no browser to get one from. An earlier version called it
	// with a bearer token and was refused every time: code that compiled, looked
	// finished, and could never have worked. This asserts the path.
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/environments/env-1" {
			t.Errorf("asked for %s, want the engine endpoint", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{
			"env_id":"env-1","repository":"acme/app","branch":"main","state":"running",
			"preview_url":"http://preview.test","golden_version":"2026-01-01T00-00-00Z",
			"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`))
	}))

	env, err := c.Pull(t.Context(), "env-1")
	if err != nil {
		t.Fatal(err)
	}
	if env.Repository != "acme/app" || env.State != "running" {
		t.Fatalf("env = %+v", env)
	}
}

func TestPull_ReportsAnEnvironmentThatIsNotThere(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"a not found status": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
		"an empty body": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{}`))
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := newClient(t, handler)
			_, err := c.Pull(t.Context(), "env-9")
			var missing *controlplane.NotFound
			if !errors.As(err, &missing) {
				t.Fatalf("want NotFound, got %v", err)
			}
		})
	}
}

func TestPull_EscapesTheEnvironmentIdentifierIntoThePath(t *testing.T) {
	// An identifier arrives from a user and goes into a URL path. Without
	// escaping, one containing a slash reaches a different endpoint entirely.
	var gotPath string
	c, _ := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusNotFound)
	}))
	_, _ = c.Pull(t.Context(), "env/../../v1/events")
	if strings.Contains(gotPath, "/v1/events") {
		t.Fatalf("the identifier escaped its path segment: %s", gotPath)
	}
}

func TestTokenFromEnvironment_PrefersTheDocumentedNameAndIgnoresBlanks(t *testing.T) {
	env := map[string]string{"AF_CONTROL_PLANE_TOKEN": "  ", "ANTIFAILURE_TOKEN": "aft_second"}
	got := controlplane.TokenFromEnvironment(func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	})
	if got != "aft_second" {
		t.Fatalf("got %q; a whitespace-only value must not count as configured", got)
	}
}

// ---------------------------------------------------------------------------
// The sink
// ---------------------------------------------------------------------------

type recorder struct {
	mu       sync.Mutex
	batches  [][]controlplane.Event
	failures int
	status   int
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failures > 0 {
		r.failures--
		if r.status != 0 {
			w.WriteHeader(r.status)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	var body struct {
		Events []controlplane.Event `json:"events"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)
	r.batches = append(r.batches, body.Events)
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte(`{"accepted":0}`))
}

func (r *recorder) sent() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, b := range r.batches {
		n += len(b)
	}
	return n
}

func newSink(t *testing.T, rec *recorder, capacity int) (*controlplane.Sink, *clock.Fake) {
	t.Helper()
	c, _ := newClient(t, rec)
	fake := clock.NewFake(time.Unix(1700000000, 0).UTC())
	s := controlplane.NewSink(controlplane.SinkOptions{
		Client: c, Clock: fake, Capacity: capacity, BatchSize: 10,
		FlushEvery: time.Hour, // Flushed by hand, so the test is not timing dependent.
	})
	t.Cleanup(func() { _ = s.Close() })
	return s, fake
}

// event builds one engine event.
//
// The type is the events package's own constant and not a string literal, and
// that is deliberate rather than tidy. This helper used to say
// Type: "env.up.ready", which is not an event the engine can emit; typeMap was
// keyed by the same invented name, so the test and the code agreed with each
// other and both disagreed with the engine. Nine of the map's nine keys were
// wrong that way, and every test over the translation passed. A literal here is
// how that happens, so there is not one left in this file.
func event(id string, seq uint64) events.Event {
	return events.Event{
		ID: id, Env: "env-1", Seq: seq, Type: events.EnvReady,
		Level: "info", TS: time.Unix(1700000000, 0).UTC(),
	}
}

func TestSink_BatchesAndSends(t *testing.T) {
	rec := &recorder{}
	s, _ := newSink(t, rec, 100)

	for i := range 25 {
		if err := s.Deliver(t.Context(), event(fmt.Sprint(i), uint64(i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if rec.sent() != 25 {
		t.Fatalf("sent %d of 25", rec.sent())
	}
	if len(rec.batches) != 3 {
		t.Fatalf("25 events with a batch size of 10 should be 3 requests, got %d", len(rec.batches))
	}
}

func TestSink_TranslatesTheEngineVocabulary(t *testing.T) {
	rec := &recorder{}
	s, _ := newSink(t, rec, 10)

	_ = s.Deliver(t.Context(), event("a", 1))
	if err := s.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := rec.batches[0][0]
	if got.Type != "environment.ready" {
		t.Fatalf("type = %q, want the control plane's name for it", got.Type)
	}
	if got.ID != "a" || got.Sequence != 1 || got.EnvID != "env-1" {
		t.Fatalf("event did not carry through: %+v", got)
	}
	// The engine's own identifier is the idempotency key, so a resend after a
	// timeout is dropped by the control plane rather than duplicated.
	if got.ID != "a" {
		t.Fatal("the idempotency key is not the engine's event identifier")
	}
}

func TestSink_PassesThroughATypeItDoesNotKnow(t *testing.T) {
	// An older engine and a newer control plane, or the reverse. Neither should
	// need the other to be upgraded first.
	rec := &recorder{}
	s, _ := newSink(t, rec, 10)

	e := event("a", 1)
	e.Type = "something.invented.later"
	_ = s.Deliver(t.Context(), e)
	if err := s.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if rec.batches[0][0].Type != "something.invented.later" {
		t.Fatalf("type = %q", rec.batches[0][0].Type)
	}
}

func TestSink_KeepsEventsWhenAFlushFails(t *testing.T) {
	rec := &recorder{failures: 1}
	s, _ := newSink(t, rec, 100)

	for i := range 5 {
		_ = s.Deliver(t.Context(), event(fmt.Sprint(i), uint64(i)))
	}
	if err := s.Flush(t.Context()); err == nil {
		t.Fatal("a failed flush was reported as success")
	}
	if s.Pending() != 5 {
		t.Fatalf("pending = %d; a failed flush must not discard the events", s.Pending())
	}

	// The control plane comes back.
	if err := s.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if rec.sent() != 5 {
		t.Fatalf("sent %d of 5 after recovery", rec.sent())
	}
}

func TestSink_DropsTheOldestWhenFullAndSaysSo(t *testing.T) {
	rec := &recorder{}
	s, _ := newSink(t, rec, 5)

	for i := range 12 {
		_ = s.Deliver(t.Context(), event(fmt.Sprint(i), uint64(i)))
	}
	if s.Dropped() != 7 {
		t.Fatalf("dropped = %d, want 7", s.Dropped())
	}
	if err := s.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}

	// The newest survive. When something has gone wrong the recent events are
	// the ones that explain it, so a buffer that keeps the first five and
	// discards the rest keeps the least useful events it could.
	kept := rec.batches[0]
	if len(kept) != 5 || kept[0].ID != "7" || kept[4].ID != "11" {
		ids := make([]string, len(kept))
		for i, e := range kept {
			ids[i] = e.ID
		}
		t.Fatalf("kept %v, want the last five", ids)
	}
}

func TestSink_ObeysAThrottleInsteadOfRetrying(t *testing.T) {
	rec := &recorder{failures: 1, status: http.StatusTooManyRequests}
	s, fake := newSink(t, rec, 100)

	for i := range 3 {
		_ = s.Deliver(t.Context(), event(fmt.Sprint(i), uint64(i)))
	}
	if err := s.Flush(t.Context()); err == nil {
		t.Fatal("a throttle was reported as success")
	}

	// Still buffered, and deliberately not retried: retrying straight after a
	// 429 is how a busy control plane becomes an unreachable one.
	if err := s.Flush(t.Context()); err != nil {
		t.Fatalf("the second flush should be a quiet no-op while throttled: %v", err)
	}
	if rec.sent() != 0 {
		t.Fatalf("sent %d events while throttled", rec.sent())
	}
	if s.Pending() != 3 {
		t.Fatalf("pending = %d; throttled events must not be discarded", s.Pending())
	}

	// After the delay it goes.
	fake.Advance(31 * time.Second)
	if err := s.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if rec.sent() != 3 {
		t.Fatalf("sent %d of 3 after the pause", rec.sent())
	}
}

func TestSink_DeliverNeverBlocksOrFails(t *testing.T) {
	// The property the engine depends on: a control plane that is down must not
	// stall a build. The server here never answers, and Deliver still returns.
	blocked := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-blocked
	}))
	c, err := controlplane.New(controlplane.Options{
		BaseURL: srv.URL, Token: "aft_" + strings.Repeat("a", 40), HTTP: srv.Client(),
		Redactor: redact.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	s := controlplane.NewSink(controlplane.SinkOptions{
		Client: c, Clock: clock.NewFake(time.Unix(0, 0)), Capacity: 10,
		FlushEvery: time.Hour,
	})
	// Ordered by hand rather than with t.Cleanup, because the ordering is the
	// point: httptest.Server.Close waits for its handlers to return, so the
	// handler has to be released before anything closes the server. Getting it
	// wrong deadlocks the whole package rather than failing one test.
	defer func() {
		close(blocked)
		_ = s.Close()
		srv.Close()
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 1000 {
			_ = s.Deliver(context.Background(), event(fmt.Sprint(i), uint64(i)))
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Deliver blocked while the control plane was unreachable")
	}
	if s.Dropped() == 0 {
		t.Fatal("1000 events into a buffer of 10 should have dropped some")
	}
}

func TestSink_ReportsDroppedEventsAtClose(t *testing.T) {
	rec := &recorder{}
	c, _ := newClient(t, rec)
	s := controlplane.NewSink(controlplane.SinkOptions{
		Client: c, Clock: clock.NewFake(time.Unix(0, 0)), Capacity: 2, FlushEvery: time.Hour,
	})
	for i := range 5 {
		_ = s.Deliver(t.Context(), event(fmt.Sprint(i), uint64(i)))
	}
	err := s.Close()
	if err == nil {
		t.Fatal("dropped events were not reported at close")
	}
	if !strings.Contains(err.Error(), "dropped") {
		t.Fatalf("the message should say what happened: %v", err)
	}
}

func TestSink_CloseIsIdempotent(t *testing.T) {
	rec := &recorder{}
	s, _ := newSink(t, rec, 10)
	_ = s.Close()
	// A second close must not panic on an already closed channel, because
	// teardown paths call Close more than once when something else has failed.
	_ = s.Close()
}

func TestSink_SatisfiesTheBusSinkInterface(t *testing.T) {
	// Compile-time proof that the sink can actually be attached to the bus.
	// A sink that does not fit is a feature that exists and is never called.
	rec := &recorder{}
	s, _ := newSink(t, rec, 10)
	var _ events.Sink = s
}

// A client that could not redact is refused at construction.
//
// The other four writers in the engine already work this way: the spool and
// the telemetry attachment both refuse rather than accept a nil redactor. This
// one did not, and it is the only writer whose output leaves the machine.
func TestNew_RefusesAClientThatCouldNotRedact(t *testing.T) {
	_, err := controlplane.New(controlplane.Options{
		BaseURL: "https://app.test", Token: "aft_" + strings.Repeat("a", 40),
	})
	if err == nil {
		t.Fatal("a client with no redactor was allowed to exist")
	}
	if !strings.Contains(err.Error(), "redactor") {
		t.Fatalf("the error does not say what is missing: %v", err)
	}
	if errors.Is(err, controlplane.ErrNotConfigured) {
		t.Fatal("a missing redactor is a programming fault, not a missing token, " +
			"and reporting it as ErrNotConfigured would send an operator to create one")
	}
}

// Every string in a payload is scrubbed on its way to the wire, however deeply
// it is nested, and the caller's own batch is left alone.
//
// The nesting matters because a payload is map[string]any: an event carrying a
// structured detail puts a map or a slice in there, and a redaction that only
// looked at the top level would scrub the fields somebody thought of and post
// the rest.
func TestSend_ScrubsThePayloadOnItsWayToTheWire(t *testing.T) {
	// Assembled rather than written down, so this file holds no secret shaped
	// string for a scanner to have to forgive.
	password := "wire" + "-secret-" + "b73c01"
	r := redact.New()
	r.Register(password)

	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ = io.ReadAll(req.Body)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":1,"duplicates":0}`))
	}))
	defer srv.Close()

	c, err := controlplane.New(controlplane.Options{
		BaseURL: srv.URL, Token: "aft_" + strings.Repeat("a", 40),
		HTTP: srv.Client(), Redactor: r,
	})
	if err != nil {
		t.Fatal(err)
	}

	url := "postgres://app:" + password + "@db:5432/app"
	batch := []controlplane.Event{{
		ID: "ev_1", Type: "environment.ready", EnvID: "env-1", Sequence: 1,
		OccurredAt: time.Unix(1700000000, 0).UTC(),
		Payload: map[string]any{
			"top":    url,
			"nested": map[string]any{"deeper": map[string]any{"url": url}},
			"list":   []any{"fine", url, map[string]any{"url": url}},
			"number": 7,
			"flag":   false,
		},
	}}

	if _, err := c.Send(t.Context(), batch); err != nil {
		t.Fatal(err)
	}

	sent := string(body)
	if sent == "" {
		t.Fatal("nothing was sent, so this test proved nothing")
	}
	if strings.Contains(sent, password) {
		t.Fatalf("the password reached the wire: %s", sent)
	}
	if !strings.Contains(sent, "db:5432") {
		t.Fatalf("the host did not survive, so the event explains nothing: %s", sent)
	}
	// The numbers and booleans have to come through untouched, or redaction
	// has quietly changed the data rather than only the secrets in it.
	if !strings.Contains(sent, `"number":7`) || !strings.Contains(sent, `"flag":false`) {
		t.Fatalf("a non-string value was altered: %s", sent)
	}

	// The caller still owns its batch. A failed send is retried from the
	// sink's buffer and from the spool, and scrubbing in place would mean the
	// retry sent something different from what was queued.
	if got := batch[0].Payload["top"]; got != url {
		t.Fatalf("the caller's batch was modified: %v", got)
	}
	inner := batch[0].Payload["nested"].(map[string]any)["deeper"].(map[string]any)
	if inner["url"] != url {
		t.Fatalf("the caller's nested payload was modified: %v", inner["url"])
	}
}

// The renewal floor, proved at the client rather than end to end.
//
// The end to end test cannot show this: one flush makes one request, so a
// bounded renewal and an unbounded one both exchange once and the assertion
// passes either way. Two sends inside the same minute is the smallest thing
// that tells them apart, and this test fails when RenewFloor is set to zero,
// which is how it was checked.
func TestARefusedBatchRenewsAtMostOncePerFloor(t *testing.T) {
	var mu sync.Mutex
	renewals := 0

	// Refuses everything, which is a control plane that renewing cannot help:
	// a revoked organization, or a repository that was disconnected mid run.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	fake := clock.NewFake(time.Unix(1700000000, 0).UTC())
	client, err := controlplane.New(controlplane.Options{
		BaseURL: srv.URL, Token: "the-first-credential", Clock: fake, Redactor: redact.New(),
		Renew: func(context.Context) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			renewals++
			return fmt.Sprintf("credential-%d", renewals), nil
		},
	})
	if err != nil {
		t.Fatalf("building the client: %v", err)
	}

	batch := []controlplane.Event{{
		ID: "ev-1", Type: "environment.ready", EnvID: "env-1", Sequence: 1,
		OccurredAt: time.Unix(1700000000, 0).UTC(),
	}}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return renewals
	}

	// Two refused batches inside one minute.
	_, _ = client.Send(context.Background(), batch)
	_, _ = client.Send(context.Background(), batch)
	if got := count(); got != 1 {
		t.Fatalf("two refusals inside the floor caused %d exchanges, want 1", got)
	}

	// Past the floor it is willing to try again, because an expiry that happens
	// during a long run has to be recoverable rather than permanent.
	fake.Advance(controlplane.RenewFloor + time.Second)
	_, _ = client.Send(context.Background(), batch)
	if got := count(); got != 2 {
		t.Fatalf("a refusal past the floor caused %d exchanges in total, want 2", got)
	}
}
