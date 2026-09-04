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
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	// mintSeq, when set, is handed out one per exchange, so a test can make the
	// second exchange return something different from the first. The last entry
	// is repeated once the sequence runs out.
	mintSeq []string
	// acceptOnly, when set, is the one credential /v1/events accepts. Anything
	// else gets a 401, which is how an expiry looks from the engine: there is no
	// other notice.
	acceptOnly string
	// refuse, when set, is the status the exchange answers with instead.
	refuse int
	// refuseFrom, when non zero, is the exchange number from which the control
	// plane starts refusing. 2 means the first exchange succeeds and every
	// renewal after it fails, which is what a credential outliving its binding
	// looks like.
	refuseFrom int
	// retryAfter, when set, is the Retry-After the refusal carries, in seconds.
	retryAfter int

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
		issued, refuse, retryAfter := h.issued, h.refuse, h.retryAfter
		if h.refuseFrom > 0 {
			if h.exchanges >= h.refuseFrom {
				refuse = http.StatusForbidden
			} else {
				refuse = 0
			}
		}
		if len(h.mintSeq) > 0 {
			issued = h.mintSeq[min(h.exchanges-1, len(h.mintSeq)-1)]
		}
		h.mu.Unlock()

		if refuse != 0 {
			if retryAfter > 0 {
				w.Header().Set("Retry-After", fmt.Sprint(retryAfter))
			}
			w.WriteHeader(refuse)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "this job is not connected here"})
			return
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"token": issued, "expires_in": 3600})

	case "/v1/events":
		presented := req.Header.Get("authorization")
		h.mu.Lock()
		h.ingestAuths = append(h.ingestAuths, presented)
		acceptOnly := h.acceptOnly
		h.mu.Unlock()

		// What an expired credential looks like from the engine. The control
		// plane does not tell it in advance and there is nothing to poll.
		if acceptOnly != "" && presented != "Bearer "+acceptOnly {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "This token is not valid."})
			return
		}

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
	// spooled is how many batches were still owed to the control plane when the
	// command ended, read before Close tears the spool down.
	spooled int
}

// runOne attaches telemetry with the given environment, emits one event, and
// closes, which is the whole lifecycle a command has.
func runOne(t *testing.T, r *runner, h *hosted, env map[string]string) *mintingRun {
	t.Helper()
	return runWith(t, r, h, env, true, "")
}

// runWithoutAddress is the same, for a repository that names no control plane.
func runWithoutAddress(t *testing.T, r *runner, h *hosted, env map[string]string) *mintingRun {
	t.Helper()
	return runWith(t, r, h, env, false, "")
}

// runSignedIn is the same, for a machine somebody has run af login on. The CLI
// hands the stored credential over rather than telemetry reading it, so a test
// hands it over the same way.
func runSignedIn(t *testing.T, r *runner, h *hosted, env map[string]string, stored string) *mintingRun {
	t.Helper()
	return runWith(t, r, h, env, true, stored)
}

func runWith(
	t *testing.T, r *runner, h *hosted, env map[string]string, addressed bool, stored string,
) *mintingRun {
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

	address := planeSrv.URL
	if !addressed {
		address = ""
	}

	out := &mintingRun{runner: r, plane: h, dir: dir}
	tel, err := Attach(t.Context(), bus, Options{
		StateDir:          dir,
		EnvID:             "shop-main-a1b2",
		Redactor:          redact.New(),
		Clock:             fake,
		State:             db,
		ControlPlaneURL:   address,
		ControlPlaneToken: stored,
		Getenv:            func(k string) string { return full[k] },
		OnWarning:         func(s string) { out.warned = append(out.warned, s) },
	})
	require.NoError(t, err)

	bus.Info("shop-main-a1b2", events.EnvReady, "the environment is ready")
	require.NoError(t, tel.Close(t.Context()))

	// Read the way the NEXT command reads it: a fresh spool over the same
	// directory. What is still owed after this command ended is exactly what a
	// later one would pick up and send.
	if later, err := NewSpool(SpoolOptions{
		Dir: filepath.Join(dir, SpoolDir), Redactor: redact.New(),
	}); err == nil {
		out.spooled = later.Pending()
	}

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

// The credential is short lived by design, so a run outlives it.
//
// This is the failure that would otherwise be invisible: the sink acquires a
// credential when the environment comes up, reports happily for fifteen
// minutes, and then silently stops, leaving a dashboard showing a run that
// started and never finished with nothing anywhere saying why. A 401 is the
// only notice an expiry gives, so a 401 has to be the thing that triggers a
// fresh exchange.
func TestAnExpiredCredentialIsRenewedAndTheEventStillArrives(t *testing.T) {
	r := &runner{value: "signed.workflow.identity"}
	h := &hosted{
		mintSeq: []string{"first-credential", "second-credential"},
		// The first credential is already dead by the time an event is sent,
		// which is the case a long job hits.
		acceptOnly: "second-credential",
	}

	run := runOne(t, r, h, map[string]string{
		identityURLEnv:   "present",
		identityTokenEnv: "the-runners-request-token",
	})

	require.Equal(t, 2, run.plane.exchangeCount(),
		"the refusal did not produce a second exchange")
	require.Equal(t, []string{run.log[0].ID}, run.plane.ingested(),
		"the event was lost to an expiry that could have been recovered from")
	require.Equal(t,
		[]string{"Bearer first-credential", "Bearer second-credential"},
		run.plane.bearers(),
		"the retry has to present the new credential, not the dead one")
}

// The second ordering of the renewal, and the one that would lose events.
//
// A credential dies, the batch carrying that discovery is refused, and then the
// re-exchange ITSELF fails: the binding was revoked, GitHub's key set could not
// be fetched, or the exchange is rate limited. Two of those three are
// transient, so the batch must survive to be sent by a later command rather
// than being dropped on the floor.
//
// It is worth its own test because it is quiet. Renewal that replays the batch
// on success and drops it on failure loses exactly one batch per credential
// lifetime, which at fifteen minutes reads as sporadic event loss rather than
// as a retry bug, and only on long runs.
func TestWhenTheReExchangeItselfFailsTheBatchIsKeptNotDropped(t *testing.T) {
	r := &runner{value: "signed.workflow.identity"}
	h := &hosted{
		issued: "the-first-credential",
		// The first exchange works, so a sink exists and events flow. Every
		// renewal after it is refused.
		refuseFrom: 2,
		// And the credential is dead by the time anything is sent, so the
		// renewal is actually reached.
		acceptOnly: "a-credential-this-control-plane-will-never-issue",
	}

	run := runOne(t, r, h, map[string]string{
		identityURLEnv:   "present",
		identityTokenEnv: "the-runners-request-token",
	})

	require.Equal(t, 2, run.plane.exchangeCount(),
		"the refusal should have prompted exactly one renewal attempt")
	require.Empty(t, run.plane.ingested(), "nothing could have been accepted")
	// The point of the test. The events are on disk waiting for the next
	// command, not gone.
	require.NotZero(t, run.spooled,
		"the batch was dropped when the re-exchange failed, rather than kept for a later command")
	require.Len(t, run.log, 1, "and the run itself is unaffected")
}

// A refusal renewing cannot fix costs the run nothing.
//
// This deliberately does NOT claim to prove the renewal floor. One flush makes
// one request, so a bounded renewal and an unbounded one both exchange twice
// here and the count tells them apart not at all: setting RenewFloor to zero
// leaves this test green, which is how that was established. The floor is
// proved where it can be, at the client, by
// TestARefusedBatchRenewsAtMostOncePerFloor in the controlplane package. What
// this one proves is the part that belongs here: a control plane refusing
// everything does not fail the run, does not lose the local log, and does not
// hang the close.
func TestARefusalRenewingCannotFixIsNotAnExchangeLoop(t *testing.T) {
	r := &runner{value: "signed.workflow.identity"}
	h := &hosted{
		mintSeq: []string{"first", "second", "third", "fourth"},
		// Nothing is ever accepted, so renewing can never help.
		acceptOnly: "a-credential-this-control-plane-will-never-issue",
	}

	run := runOne(t, r, h, map[string]string{
		identityURLEnv:   "present",
		identityTokenEnv: "the-runners-request-token",
	})

	require.Empty(t, run.plane.ingested(), "nothing could have been accepted")
	require.Len(t, run.log, 1, "and the run is unaffected either way")
}

// A token the user set is never renewed over their head.
//
// Re-minting on top of a credential somebody deliberately configured would
// ignore their choice on the first refusal, and the refusal they need to see is
// that their token is wrong rather than that a fresh one also did not work.
func TestAnEnvironmentTokenIsNeverRenewedOverTheUsersHead(t *testing.T) {
	r := &runner{value: "signed.workflow.identity"}
	h := &hosted{
		issued:     "minted-for-this-job",
		acceptOnly: "a-credential-this-control-plane-will-never-issue",
	}

	run := runOne(t, r, h, map[string]string{
		"AF_CONTROL_PLANE_TOKEN": "from-the-environment",
		identityURLEnv:           "present",
		identityTokenEnv:         "the-runners-request-token",
	})

	require.Equal(t, 0, run.plane.exchangeCount(),
		"a configured token was quietly replaced with a minted one")
	require.Equal(t, 0, run.runner.calls())
	for _, presented := range run.plane.bearers() {
		require.Equal(t, "Bearer from-the-environment", presented)
	}
}

// A control plane older than the engine says so, rather than saying "refused".
//
// The difference matters at three in the morning: a 404 reported as a refusal
// sends somebody looking for a permission problem in a repository that does not
// have one, when what they need is to upgrade the server.
func TestAControlPlaneWithoutTheExchangeSaysToUpgradeIt(t *testing.T) {
	r := &runner{value: "signed.workflow.identity"}
	// refuse with 404, which is what a control plane that predates this route
	// answers: the route is simply not there.
	h := &hosted{refuse: http.StatusNotFound}

	run := runOne(t, r, h, map[string]string{
		identityURLEnv:   "present",
		identityTokenEnv: "the-runners-request-token",
	})

	require.Empty(t, run.plane.ingested())
	require.Len(t, run.log, 1, "the run is unaffected")
	require.True(t, warnedAbout(run.warned, "does not offer the identity exchange"),
		"a 404 has to name the cause rather than read as a refusal: %v", run.warned)
	require.True(t, warnedAbout(run.warned, "AF_CONTROL_PLANE_TOKEN"),
		"and name the way out: %v", run.warned)
}

// A repository the control plane does not yet associate with an organization
// is a setup step nobody has done, not a credential that failed.
//
// The identity verified perfectly. What is missing is the control plane knowing
// that this repository reports for this organization, and telling somebody
// their identity was refused sends them to check a permission that is already
// correct. The control plane writes the sentence; the engine's job is not to
// wrap it in the wrong noun.
func TestARepositoryTheControlPlaneDoesNotKnowReadsAsSetupNotAuthFailure(t *testing.T) {
	r := &runner{value: "signed.workflow.identity"}
	h := &hosted{refuse: http.StatusForbidden}

	run := runOne(t, r, h, map[string]string{
		identityURLEnv:   "present",
		identityTokenEnv: "the-runners-request-token",
	})

	require.Len(t, run.warned, 1, "expected exactly one warning: %v", run.warned)
	require.NotContains(t, run.warned[0], "refused",
		"a 403 must not read as a refused identity: %v", run.warned)
	require.Contains(t, run.warned[0], "not connected here",
		"and it has to carry the control plane's own sentence: %v", run.warned)
	require.Len(t, run.log, 1, "the run is unaffected")
}

// A rate limit says how long, because a limit with no number leaves a reader
// guessing whether the problem is theirs.
func TestARateLimitedExchangeSaysHowLongToWait(t *testing.T) {
	r := &runner{value: "signed.workflow.identity"}
	h := &hosted{refuse: http.StatusTooManyRequests, retryAfter: 90}

	run := runOne(t, r, h, map[string]string{
		identityURLEnv:   "present",
		identityTokenEnv: "the-runners-request-token",
	})

	require.Len(t, run.warned, 1, "expected exactly one warning: %v", run.warned)
	require.Contains(t, run.warned[0], "rate limited")
	require.Contains(t, run.warned[0], "1m30s",
		"the wait the control plane asked for has to appear: %v", run.warned)
	require.NotContains(t, run.warned[0], "refused")
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

// The commonest way to use this tool is without a control plane at all, and
// that path must stay silent.
//
// A runner will vouch for any job that asks, so running in GitHub Actions says
// nothing about whether this repository has a control plane. Without an address
// to report to, every such run would trade an identity against the hosted
// instance, be refused for a repository that is not connected to it, and print
// a warning about a service the user has never heard of.
func TestWithNoControlPlaneAddressNoIdentityIsSpent(t *testing.T) {
	r := &runner{value: "signed.workflow.identity"}
	h := &hosted{issued: "minted-for-this-job"}

	run := runWithoutAddress(t, r, h, map[string]string{
		identityURLEnv:   "present",
		identityTokenEnv: "the-runners-request-token",
	})

	require.Equal(t, 0, run.runner.calls(), "the runner was asked for an identity anyway")
	require.Equal(t, 0, run.plane.exchangeCount())
	require.Empty(t, run.warned, "and nothing was said, because nothing is wrong: %v", run.warned)
	require.Len(t, run.log, 1, "the run is unaffected")
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
