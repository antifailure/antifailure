package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// THE RECORDED FAILURE, REPLAYED.
//
// The other tests in this package assert one property each against a server
// written to exercise it. This one asserts the whole command against the thing
// that actually happened, so that the four routes, their real status codes and
// the verdict for each are pinned together rather than one at a time.
//
// Every status code and body below was OBSERVED, not composed. The production
// column was captured at 2026-09-05T09:27:44Z and again at 09:33:24Z from
// https://app.antifailure.dev while `/readyz` reported v1.1.1 / 59486b63, which
// is the deployment the careers form was posting into when somebody filled it
// in and was told the server could not be reached. The staging column was
// captured at 09:33:24Z from https://app.dev.antifailure.dev while `/readyz`
// reported v1.2.0 / b66ca628, which is the same tree with the route released.
//
// The two columns differ in exactly one cell, and that cell is the bug.
type recordedReply struct {
	status int
	body   string
}

// What production answered while it was serving v1.1.1.
var recordedProduction = map[string]recordedReply{
	"POST /v1/applications": {404, `{"error":"No endpoint at this path. GET /openapi.json lists every endpoint this control plane serves."}`},
	"POST /v1/leads":        {400, `{"error":"Tell us your name so a reply is addressed to somebody."}`},
	"POST /v1/site/events":  {403, `{"error":"This endpoint serves the marketing site only."}`},
	"GET /auth/github":      {302, ``},
}

// What staging answered while it was serving v1.2.0, the same tree with the
// route released. One cell differs: 403 from the origin guard instead of 404.
var recordedStaging = map[string]recordedReply{
	"POST /v1/applications": {403, `{"error":"Use the application form on the official website."}`},
	"POST /v1/leads":        {400, `{"error":"Tell us your name so a reply is addressed to somebody."}`},
	"POST /v1/site/events":  {403, `{"error":"This endpoint serves the marketing site only."}`},
	"GET /auth/github":      {302, ``},
}

// replay serves a recorded observation. It mints an x-request-id on every
// reply, including the 404, because the real control plane does: the middleware
// that mints it runs before routing. A replay that omitted it would be testing
// the "something in front of the app answered" branch by accident and would
// never reach the verdict this file is about.
func replay(t *testing.T, recorded map[string]recordedReply) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply, ok := recorded[r.Method+" "+r.URL.Path]
		if !ok {
			t.Errorf("the command asked for %s %s, which is not in the recording", r.Method, r.URL.Path)
			w.WriteHeader(599)
			return
		}
		w.Header().Set("x-request-id", "00000000-1111-2222-3333-444444444444")
		if reply.status == 302 {
			w.Header().Set("location", "https://github.com/login/oauth/authorize?client_id=x")
		}
		w.WriteHeader(reply.status)
		_, _ = w.Write([]byte(reply.body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The four routes as the inventory declares them, so the replay is driven by
// the same list the command reads in production rather than by a second copy.
func recordedRoutes() []Route {
	return []Route{
		{Name: "applications.create", Method: "POST", Path: "/v1/applications", CalledFrom: "components/pages/company/ApplicationForm.tsx", WhenMissing: "The careers form says 'Could not reach the server' and a job application is lost.", ProbeEffect: "inert", ProbeReason: "An origin guard answers 403 before the handler."},
		{Name: "leads.create", Method: "POST", Path: "/v1/leads", CalledFrom: "components/pages/company/EnterpriseForm.tsx", WhenMissing: "The enterprise contact form says 'Could not reach the server'.", ProbeEffect: "inert", ProbeReason: "validateLead is pure and refuses an empty name with 400."},
		{Name: "site.events", Method: "POST", Path: "/v1/site/events", CalledFrom: "lib/beacon.ts", WhenMissing: "The site beacon posts into nothing and no reader sees an error.", ProbeEffect: "inert", ProbeReason: "An origin guard answers 403 before the ingest."},
		{Name: "auth.github", Method: "GET", Path: "/auth/github", CalledFrom: "components/AuthScreen.tsx", WhenMissing: "'Continue with GitHub' lands on a 404 page.", ProbeEffect: "writes", ProbeReason: "beginSignIn inserts one oauth_states row unconditionally."},
	}
}

// ROW ONE. Against the control plane that was really serving antifailure.dev
// when the form broke, the command has to say NO, and it has to name the route
// rather than reporting that something somewhere is wrong.
func TestTheRecordedProductionIsRefusedAndTheRouteIsNamed(t *testing.T) {
	srv := replay(t, recordedProduction)

	var out bytes.Buffer
	err := probeAll(srv.URL, recordedRoutes(), true, 5*time.Second, 2, &out)
	if err == nil {
		t.Fatal("the deployment that answered the careers form with a 404 passed the gate")
	}
	if !strings.Contains(err.Error(), "POST /v1/applications") {
		t.Errorf("the refusal did not name the missing route:\n%v", err)
	}
	if !strings.Contains(err.Error(), "does not serve") {
		t.Errorf("the refusal did not say the route is missing, so a reader cannot tell it from a network failure:\n%v", err)
	}
	// It must name the ONE route that was missing. Naming all four would be a
	// gate that says "something is wrong" and sends a person to read four
	// handlers, which is how a check gets ignored.
	for _, present := range []string{"/v1/leads", "/v1/site/events", "/auth/github"} {
		if strings.Contains(err.Error(), present) {
			t.Errorf("blamed %s, which answered as a route that exists:\n%v", present, err)
		}
	}
	// And it has to say where the call comes from, so the person reading a red
	// deploy knows which file to open. That sentence belongs to the FAILURE,
	// not to the table: the table is the record of what was asked and answered,
	// and the failure is what somebody has to act on. This assertion was
	// written against out.String() first and CI caught it, which is the whole
	// argument for checking the stream a message actually goes to rather than
	// the one it feels like it should.
	if !strings.Contains(err.Error(), "ApplicationForm.tsx") {
		t.Errorf("the failure did not say where the call comes from:\n%v", err)
	}
	// The table still has to carry the four rows, on the other stream.
	if !strings.Contains(out.String(), "POST /v1/applications") {
		t.Errorf("the report did not list the route it asked about:\n%s", out.String())
	}
}

// ROW ONE, THE HALF THAT MAKES IT USEFUL. Three of the four routes REFUSED the
// probe on that same recorded production, with 400, 403 and a 302. A gate that
// read a refusal as an absence would have called every one of them missing and
// been wrong three times out of four while being right once by accident.
func TestTheRecordedRefusalsAreReadAsRoutesThatExist(t *testing.T) {
	srv := replay(t, recordedProduction)

	var out bytes.Buffer
	_ = probeAll(srv.URL, recordedRoutes(), true, 5*time.Second, 2, &out)
	report := out.String()

	for _, want := range []string{
		"POST /v1/leads          400  present",
		"POST /v1/site/events    403  present",
		"GET /auth/github        302  present",
		"POST /v1/applications   404  absent",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not contain %q:\n%s", want, report)
		}
	}
}

// ROW TWO. The same command, the same inventory, against the same tree with the
// route released. It has to PASS, or the gate is a wall rather than a check.
func TestTheRecordedStagingPasses(t *testing.T) {
	srv := replay(t, recordedStaging)

	var out bytes.Buffer
	if err := probeAll(srv.URL, recordedRoutes(), true, 5*time.Second, 2, &out); err != nil {
		t.Fatalf("a control plane serving all four routes was refused:\n%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "all 4 route(s) the site calls are served") {
		t.Errorf("passed without saying what it established:\n%s", out.String())
	}
}
