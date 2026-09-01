package telemetry

// What these prove, and why they are here rather than in the controlplane
// package.
//
// The controlplane package can be tested into believing it mints a token, and
// that would prove nothing about whether a run reports anything. The gap this
// lane closes was never in the minting: it was that attachControlPlane read an
// environment variable nothing in any shipped workflow ever set, took the empty
// string, and returned before building a client, a spool or a sink. So these
// tests attach a real bus to a real Telemetry, emit a real event, and assert it
// arrived at an HTTP server that checked the bearer. Defined, wired, effective,
// with the last one being the assertion.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/controlplane"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/state"
)

// runner stands in for the identity service the GitHub Actions runner exposes.
//
// The real one lives at ACTIONS_ID_TOKEN_REQUEST_URL and answers a job that
// presents ACTIONS_ID_TOKEN_REQUEST_TOKEN. Both are put in the environment by
// the runner itself for a job with id-token: write.
type runner struct {
	mu sync.Mutex
	// value is the identity to hand back. Empty is the fork case: GitHub
	// declines to mint, on purpose, and the exchange has to survive it.
	value string
	// status, when set, replaces the 200.
	status int

	requests  int
	audiences []string
	presented []string
}

func (r *runner) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.requests++
	r.audiences = append(r.audiences, req.URL.Query().Get("audience"))
	r.presented = append(r.presented, req.Header.Get("authorization"))
	value, status := r.value, r.status
	r.mu.Unlock()

	if status != 0 && status != http.StatusOK {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"count": 1, "value": value})
}

func (r *runner) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requests
}

func (r *runner) audienceAsked() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.audiences...)
}

// hosted stands in for the control plane, serving the two routes that matter
// here: the identity exchange and ingestion.
type hosted struct {
	mu sync.Mutex
	// issued is the engine token to hand back for a verified identity.
	issued string
	// refuse, when set, is the status the exchange answers with instead.
	refuse int

	exchanges   int
	identities  []string
	ingestAuths []string
	eventIDs    []string
}

func (h *hosted) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch req.URL.Path {
	case controlplane.EngineTokenPath:
		h.mu.Lock()
		h.exchanges++
		h.identities = append(h.identities, req.Header.Get("authorization"))
		issued, refuse := h.issued, h.refuse
		h.mu.Unlock()

		if refuse != 0 {
			w.WriteHeader(refuse)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "this job is not connected here"})
			return
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"token": issued, "expires_in": 3600})

	case "/v1/events":
		h.mu.Lock()
		h.ingestAuths = append(h.ingestAuths, req.Header.Get("authorization"))
		h.mu.Unlock()

		var body struct {
			Events []controlplane.Event `json:"events"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		h.mu.Lock()
		for _, e := range body.Events {
			h.eventIDs = append(h.eventIDs, e.ID)
		}
		h.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted": len(body.Events), "duplicates": 0,
		})

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (h *hosted) ingested() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.eventIDs...)
}

func (h *hosted) bearers() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.ingestAuths...)
}

func (h *hosted) exchangeCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.exchanges
}

// mintingRun is one `af` command on a runner, with somewhere to get an identity
// and somewhere to spend it.
type mintingRun struct {
	runner *runner
	plane  *hosted
	warned []string
	log    []events.Event
	dir    string
}

// runOne attaches telemetry with the given environment, emits one event, and
// closes, which is the whole lifecycle a command has.
func runOne(t *testing.T, r *runner, h *hosted, env map[string]string) *mintingRun {
	t.Helper()

	runnerSrv := httptest.NewServer(r)
	t.Cleanup(runnerSrv.Close)
	planeSrv := httptest.NewServer(h)
	t.Cleanup(planeSrv.Close)

	full := map[string]string{}
	for k, v := range env {
		full[k] = v
	}
	// The runner's own URL carries an api-version query, so the audience the
	// exchange appends has to be a second parameter rather than the first.
	if full[identityURLEnv] == "present" {
		full[identityURLEnv] = runnerSrv.URL + "/token?api-version=2.0"
	}

	dir := t.TempDir()
	db, err := state.Open(t.Context(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	fake := clock.NewFake(time.Unix(1700000000, 0).UTC())
	bus := events.NewBus(fake)

	out := &mintingRun{runner: r, plane: h, dir: dir}
	tel, err := Attach(t.Context(), bus, Options{
		StateDir:        dir,
		EnvID:           "shop-main-a1b2",
		Redactor:        redact.New(),
		Clock:           fake,
		State:           db,
		ControlPlaneURL: planeSrv.URL,
		Getenv:          func(k string) string { return full[k] },
		OnWarning:       func(s string) { out.warned = append(out.warned, s) },
	})
	require.NoError(t, err)

	bus.Info("shop-main-a1b2", events.EnvReady, "the environment is ready")
	require.NoError(t, tel.Close(t.Context()))

	out.log = readLog(t, dir)
	return out
}

// The environment variable name, spelled here so the test fails if the constant
// is renamed out from under the workflow that sets it.
const (
	identityURLEnv   = "ACTIONS_ID_TOKEN_REQUEST_URL"
	identityTokenEnv = "ACTIONS_ID_TOKEN_REQUEST_TOKEN"
)

// The one this lane exists for.
//
// No AF_CONTROL_PLANE_TOKEN anywhere, which is the state of every workflow this
// project ships: `grep -rn AF_CONTROL_PLANE_TOKEN .github/ examples/` found
// nothing. Before this change that meant no sink at all and a run that reported
// nothing. Now the job proves what it is and the event arrives.
func TestTheSinkMintsFromTheWorkflowIdentityWhenNoTokenIsSet(t *testing.T) {
	r := &runner{value: "signed.workflow.identity"}
	h := &hosted{issued: "minted-for-this-job"}

	run := runOne(t, r, h, map[string]string{
		identityURLEnv:   "present",
		identityTokenEnv: "the-runners-request-token",
	})

	// Effective, not merely wired: the event is at the control plane.
	require.Len(t, run.log, 1, "the local log still gets it too")
	require.Equal(t, []string{run.log[0].ID}, run.plane.ingested(),
		"the event the bus emitted reached the control plane")

	// And it got there with the minted credential, which is the thing that did
	// not exist before. Asserting the header rather than just the arrival,
	// because an ingestion endpoint that ignored the bearer would let a broken
	// mint pass this test.
	require.Equal(t, []string{"Bearer minted-for-this-job"}, run.plane.bearers())
	require.Equal(t, 1, run.plane.exchangeCount(), "the identity was exchanged exactly once")
	require.Equal(t, []string{"Bearer signed.workflow.identity"}, run.plane.identities,
		"and what was presented for it was the runner's identity")
}

// The audience is a security property rather than a detail.
//
// GitHub's default audience is the repository owner's URL, and a token minted
// for that is one every workflow in the organization can obtain, which makes it
// worthless as proof of anything in particular. The control plane checks this
// exact string in web/apps/api/src/github/oidc.ts, so asking for the wrong one
// fails in production and nowhere earlier.
func TestTheIdentityIsAskedForWithTheAudienceTheControlPlaneChecks(t *testing.T) {
	r := &runner{value: "signed.workflow.identity"}
	h := &hosted{issued: "minted-for-this-job"}

	run := runOne(t, r, h, map[string]string{
		identityURLEnv:   "present",
		identityTokenEnv: "the-runners-request-token",
	})

	require.Equal(t, []string{controlplane.WorkflowAudience}, run.runner.audienceAsked())
	require.Equal(t, "antifailure-control-plane", controlplane.WorkflowAudience,
		"the same string the example workflow and the control plane both name")
}

// The environment token still wins, which is what keeps self hosted and local
// use working. A user who has set one has said what they want, and a runner
// that would also vouch for the job does not get to overrule that.
func TestTheEnvironmentTokenWinsOverTheWorkflowIdentity(t *testing.T) {
	r := &runner{value: "signed.workflow.identity"}
	h := &hosted{issued: "minted-for-this-job"}

	run := runOne(t, r, h, map[string]string{
		"AF_CONTROL_PLANE_TOKEN": "from-the-environment",
		identityURLEnv:           "present",
		identityTokenEnv:         "the-runners-request-token",
	})

	require.Equal(t, []string{"Bearer from-the-environment"}, run.plane.bearers(),
		"the configured token is the one that authenticates")
	require.Equal(t, 0, run.runner.calls(),
		"and the runner is never even asked, so no identity is minted needlessly")
	require.Equal(t, 0, run.plane.exchangeCount())
}

// The second name is still honoured too, since it is documented and somebody is
// using it.
func TestTheOlderEnvironmentTokenNameStillWorks(t *testing.T) {
	r := &runner{value: "signed.workflow.identity"}
	h := &hosted{issued: "minted-for-this-job"}

	run := runOne(t, r, h, map[string]string{
		"ANTIFAILURE_TOKEN": "the-older-name",
		identityURLEnv:      "present",
		identityTokenEnv:    "the-runners-request-token",
	})

	require.Equal(t, []string{"Bearer the-older-name"}, run.plane.bearers())
	require.Equal(t, 0, run.runner.calls())
}

// A pull request from a fork, which is the commonest reason to reach the
// failure path and is not a failure.
//
// GitHub will not mint an identity for a fork's pull request job, which is
// exactly what stops a fork reporting as the upstream repository. The run has
// to carry on: no sink, no crash, and a line saying why rather than silence.
func TestAForkGetsNoIdentityAndTheRunCarriesOn(t *testing.T) {
	r := &runner{value: ""}
	h := &hosted{issued: "minted-for-this-job"}

	run := runOne(t, r, h, map[string]string{
		identityURLEnv:   "present",
		identityTokenEnv: "the-runners-request-token",
	})

	require.Equal(t, 1, run.runner.calls(), "it did try")
	require.Empty(t, run.plane.ingested(), "and reported nothing, having no credential")
	require.Equal(t, 0, run.plane.exchangeCount(), "nothing was presented for exchange")
	require.Len(t, run.log, 1, "but the run itself is unaffected and still logged locally")
	require.True(t, warnedAbout(run.warned, "fork"),
		"and the reason is said out loud rather than swallowed: %v", run.warned)
}

// A control plane that refuses the exchange is reported, not fatal. The
// repository may simply not be connected to this control plane.
func TestARefusedExchangeIsReportedAndTheRunCarriesOn(t *testing.T) {
	r := &runner{value: "signed.workflow.identity"}
	h := &hosted{refuse: http.StatusForbidden}

	run := runOne(t, r, h, map[string]string{
		identityURLEnv:   "present",
		identityTokenEnv: "the-runners-request-token",
	})

	require.Equal(t, 1, run.plane.exchangeCount())
	require.Empty(t, run.plane.ingested())
	require.Len(t, run.log, 1, "the run is unaffected")
	require.True(t, warnedAbout(run.warned, "not connected here"),
		"the control plane's own reason is carried through: %v", run.warned)
}

// With neither a token nor a runner, nothing is attempted and nothing is said.
// This is a laptop, and it is the ordinary case rather than a problem.
func TestWithNoTokenAndNoRunnerNothingIsAttached(t *testing.T) {
	r := &runner{value: "signed.workflow.identity"}
	h := &hosted{issued: "minted-for-this-job"}

	run := runOne(t, r, h, nil)

	require.Equal(t, 0, run.runner.calls())
	require.Equal(t, 0, run.plane.exchangeCount())
	require.Empty(t, run.plane.ingested())
	require.Empty(t, run.warned, "and no warning, because nothing is wrong")
	require.Len(t, run.log, 1)
}

// Half the runner variables means something is wrong, and guessing is worse
// than not trying: an exchange that cannot work would produce a warning on
// every local run that happened to have one of them set.
func TestHalfTheRunnerEnvironmentIsNotTreatedAsARunner(t *testing.T) {
	r := &runner{value: "signed.workflow.identity"}
	h := &hosted{issued: "minted-for-this-job"}

	run := runOne(t, r, h, map[string]string{identityURLEnv: "present"})

	require.Equal(t, 0, run.runner.calls())
	require.Empty(t, run.warned)
}

// No credential, minted or configured, is ever written to the event log. The
// log goes into support bundles.
func TestNoMintedTokenReachesTheEventLog(t *testing.T) {
	r := &runner{value: "signed.workflow.identity"}
	h := &hosted{issued: "minted-for-this-job"}

	run := runOne(t, r, h, map[string]string{
		identityURLEnv:   "present",
		identityTokenEnv: "the-runners-request-token",
	})

	raw, err := json.Marshal(run.log)
	require.NoError(t, err)
	for _, secret := range []string{
		"minted-for-this-job", "signed.workflow.identity", "the-runners-request-token",
	} {
		require.NotContains(t, string(raw), secret)
	}
	for _, w := range run.warned {
		for _, secret := range []string{
			"minted-for-this-job", "signed.workflow.identity", "the-runners-request-token",
		} {
			require.NotContains(t, w, secret, "a warning must not carry a credential")
		}
	}
}

func warnedAbout(warnings []string, substring string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substring) {
			return true
		}
	}
	return false
}
