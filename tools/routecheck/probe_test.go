package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func inertRoute(method, path string) Route {
	return Route{Name: "t", Method: method, Path: path, CalledFrom: "x.tsx", WhenMissing: "the form breaks", ProbeEffect: "inert", ProbeReason: "a guard refuses first"}
}

// A server that behaves the way the control plane does: it mints an
// x-request-id on every reply, including its 404s.
func controlPlaneLike(h http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-request-id", "11111111-2222-3333-4444-555555555555")
		h(w, r)
	}))
}

func probe(t *testing.T, srv *httptest.Server, routes []Route, allowWrites bool) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := probeAll(srv.URL, routes, allowWrites, 5*time.Second, 2, &out)
	return out.String(), err
}

// THE FAILURE ITSELF. A route the site calls that the deployed control plane
// answers 404 to has to fail, and has to name the route.
func TestAbsentRouteFails(t *testing.T) {
	srv := controlPlaneLike(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/leads" {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"Tell us your name."}`))
			return
		}
		w.WriteHeader(404)
		w.Write([]byte(`{"error":"No endpoint at this path."}`))
	})
	defer srv.Close()

	_, err := probe(t, srv, []Route{inertRoute("POST", "/v1/applications"), inertRoute("POST", "/v1/leads")}, false)
	if err == nil {
		t.Fatal("a route the origin 404s passed")
	}
	if !strings.Contains(err.Error(), "/v1/applications") {
		t.Errorf("the failure did not name the missing route: %v", err)
	}
	if strings.Contains(err.Error(), "/v1/leads") {
		t.Errorf("the failure named a route that answered 400, which is present: %v", err)
	}
}

// THE FALSE POSITIVE THAT WOULD MAKE IT USELESS. A route that exists and
// refuses the probe is PRESENT. On production today /v1/leads answers 400 and
// /v1/site/events answers 403 to exactly this request.
func TestARouteThatRejectsTheProbeIsPresent(t *testing.T) {
	for _, status := range []int{400, 401, 403, 405, 413, 415, 422, 429, 200, 201, 204, 302} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := controlPlaneLike(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) })
			defer srv.Close()
			if _, err := probe(t, srv, []Route{inertRoute("POST", "/v1/leads")}, false); err != nil {
				t.Fatalf("HTTP %d read as anything but present: %v", status, err)
			}
		})
	}
}

// FAIL CLOSED, ONE. An origin that cannot be reached is not a pass.
func TestAnUnreachableOriginFails(t *testing.T) {
	var out bytes.Buffer
	// Port 1 on the loopback address refuses immediately, so this is a
	// transport failure rather than a slow test.
	err := probeAll("http://127.0.0.1:1", []Route{inertRoute("POST", "/v1/applications")}, false, 2*time.Second, 2, &out)
	if err == nil {
		t.Fatal("an unreachable origin passed")
	}
	if !strings.Contains(err.Error(), "not a pass") {
		t.Errorf("did not report it as undetermined: %v", err)
	}
	if strings.Contains(out.String(), string(Absent)) {
		t.Error("an unreachable origin was reported as the route being absent, which would blame the wrong thing")
	}
}

// FAIL CLOSED, TWO. An origin that answers 500 to everything knows nothing
// about what it serves, and must not read as either answer.
func TestAnOriginThatFailsEverythingIsNotAPass(t *testing.T) {
	srv := controlPlaneLike(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	defer srv.Close()
	out, err := probe(t, srv, []Route{inertRoute("POST", "/v1/applications")}, false)
	if err == nil {
		t.Fatal("an origin answering 500 to everything passed")
	}
	if strings.Contains(out, string(Absent)) {
		t.Error("a 500 was read as the route being absent")
	}
}

// FAIL CLOSED, THREE. Something in front of the application answering instead
// of it. A WAF's own 404 page has no x-request-id, and reading it as "the route
// is gone" would blame the application for a proxy.
func TestSomethingInFrontOfTheAppIsNotAnAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte("<html>Request blocked</html>"))
	}))
	defer srv.Close()
	out, err := probe(t, srv, []Route{inertRoute("POST", "/v1/applications")}, false)
	if err == nil {
		t.Fatal("a reply that did not come from the control plane passed")
	}
	if strings.Contains(out, string(Absent)) {
		t.Error("a proxy's 404 was read as the control plane not serving the route")
	}
	if !strings.Contains(err.Error(), "not a pass") {
		t.Errorf("did not report it as undetermined: %v", err)
	}
}

// A ROUTE IT CANNOT PROBE INERTLY IS REFUSED, NOT SKIPPED.
func TestAWritingProbeIsNotSentUnlessAllowed(t *testing.T) {
	var asked int
	srv := controlPlaneLike(func(w http.ResponseWriter, r *http.Request) {
		asked++
		w.WriteHeader(302)
	})
	defer srv.Close()

	writes := Route{Name: "auth.github", Method: "GET", Path: "/auth/github", CalledFrom: "AuthScreen.tsx", WhenMissing: "sign in 404s", ProbeEffect: "writes", ProbeReason: "beginSignIn inserts a row. It writes."}

	out, err := probe(t, srv, []Route{writes}, false)
	if err == nil {
		t.Fatal("a route that was never checked passed")
	}
	if !strings.Contains(err.Error(), "NOT CHECKED") {
		t.Errorf("did not say the route went unchecked: %v", err)
	}
	if asked != 0 {
		t.Errorf("sent %d request(s) the run was not authorised to send", asked)
	}
	if !strings.Contains(out, string(NotProbed)) {
		t.Errorf("the report did not show it as not probed: %s", out)
	}

	if _, err := probe(t, srv, []Route{writes}, true); err != nil {
		t.Fatalf("with -allow-write-probes it should have been sent and passed: %v", err)
	}
	if asked == 0 {
		t.Error("-allow-write-probes did not actually send the probe")
	}
}

// The probe must ask with the method the site uses. A route registered for POST
// answers 404 to a GET, so a probe that used the wrong method would report a
// present route as missing.
func TestTheProbeUsesTheMethodTheSiteUses(t *testing.T) {
	var seen []string
	srv := controlPlaneLike(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.Method == "POST" {
			w.WriteHeader(400)
			return
		}
		w.WriteHeader(404)
	})
	defer srv.Close()

	if _, err := probe(t, srv, []Route{inertRoute("POST", "/v1/leads")}, false); err != nil {
		t.Fatalf("a POST route probed with POST failed: %v", err)
	}
	if len(seen) != 1 || seen[0] != "POST /v1/leads" {
		t.Fatalf("asked %v", seen)
	}
}

// A redirect is an answer, not something to follow. /auth/github says it is
// there with a 302 to github.com, and following it would ask GitHub a question
// about our control plane.
func TestARedirectIsTheAnswerAndIsNotFollowed(t *testing.T) {
	var hits int
	srv := controlPlaneLike(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, "https://github.example/login", http.StatusFound)
	})
	defer srv.Close()

	route := inertRoute("GET", "/auth/github")
	if _, err := probe(t, srv, []Route{route}, false); err != nil {
		t.Fatalf("a 302 did not read as present: %v", err)
	}
	if hits != 1 {
		t.Errorf("made %d requests for one route, so it followed the redirect", hits)
	}
}

// The probe must not send an Origin header. Three of the four routes are only
// inert because an origin guard refuses them first, and an Origin that matched
// would put the probe through to the handler.
func TestTheProbeSendsNoOriginAndNoRequestId(t *testing.T) {
	var origin, requestID string
	srv := controlPlaneLike(func(w http.ResponseWriter, r *http.Request) {
		origin = r.Header.Get("origin")
		requestID = r.Header.Get("x-request-id")
		w.WriteHeader(403)
	})
	defer srv.Close()

	if _, err := probe(t, srv, []Route{inertRoute("POST", "/v1/applications")}, false); err != nil {
		t.Fatal(err)
	}
	if origin != "" {
		t.Errorf("sent Origin %q, which would defeat the guard that makes the probe safe", origin)
	}
	if requestID != "" {
		t.Errorf("sent x-request-id %q, which the control plane echoes, destroying the evidence that it answered", requestID)
	}
}

// A transport failure that clears on a retry is a pass. The gate runs against
// the internet and must not turn one dropped connection into a blocked deploy.
func TestATransientFailureIsRetried(t *testing.T) {
	var n int
	srv := controlPlaneLike(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(400)
	})
	defer srv.Close()

	if _, err := probe(t, srv, []Route{inertRoute("POST", "/v1/leads")}, false); err != nil {
		t.Fatalf("a route that answered on the second attempt failed: %v", err)
	}
	if n != 2 {
		t.Errorf("made %d attempts", n)
	}
}
