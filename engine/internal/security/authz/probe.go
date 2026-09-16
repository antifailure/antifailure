package authz

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/security"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
)

// Doer issues one HTTP request. *http.Client satisfies it, and so does a test's
// own transport, so the probe logic is exercised the same way in a test as
// against a real twin.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// defaultDoer is the transport New uses. It goes through the egress guard, never
// a raw client: an air gapped installation must not be able to reach the network
// through a security probe, and the guard is the one seam in the product that
// enforces that. SiteOracle is the engine driving the sanitized twin, which is
// exactly and only what this probe does: it reaches the configured twin BaseURL,
// the same deployments the oracle compares, and never an external host, so it
// carries no new outbound destination an air gapped buyer was not already told
// about.
//
// It does NOT follow redirects, because a 302 to a login page is a refusal this
// family must read as a refusal rather than chase into a 200 login screen and
// misread as a leak. The timeout bounds a hung endpoint so one unreachable
// target cannot stall the fleet.
func defaultDoer() Doer {
	c := airgap.Client(airgap.SiteOracle, 15*time.Second)
	c.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return c
}

// maxBodyBytes bounds how much of a response the family reads to decide content
// presence. A planted marker is short and near the top of a JSON body; reading
// the whole of a large response would let one endpoint's payload dominate the
// run's memory for no gain.
const maxBodyBytes = 1 << 20

// Probe runs the family against the live sanitized twin at the routed targets
// and returns findings. An error is a BLOCKED probe, a fact about the tooling,
// never a security verdict: a twin that will not answer is inconclusive, and
// reporting it as a pass with no findings is the single most damaging answer
// this family could give.
//
// The Go-driven live path exercises the unauthenticated reach, which is the one
// authorization question a Go probe can ask soundly without a session: no
// credential is derived, no login is driven, the request simply carries no
// identity. It fires only when a planted marker from the golden comes back to an
// anonymous caller, so a 200 that returns public content never flags. The
// horizontal, vertical and escalation classes need an authenticated identity,
// which is established by the runner's browser session and not reachable from Go
// here; those are decided by Assess over the structured per-persona observations
// the runner emits and a caller supplies through in.Observations(). The seam is
// the same: Assess is the brain, and the transport is what changes. This func is
// that caller: the authenticated differential is wired below over BuildSnapshot,
// so the observation contract has a live reader rather than an accessor nothing
// calls.
func (f *family) Probe(ctx context.Context, in security.Input) ([]report.Finding, error) {
	snap, err := f.collect(ctx, in.Env.BaseURL, in.Targets, markersOf(in.Golden))
	if err != nil {
		return nil, err
	}
	// The unauthenticated reach, judged against the absolute expectation: an added
	// endpoint that returns planted content to anon is a regression whether or not
	// there was ever a base reading, because there is nothing on the baseline for
	// a newly unprotected endpoint to differ from. This path is Go-driven and
	// works with no observations at all, so it is never regressed by the
	// authenticated path below.
	findings := Assess(nil, snap, in.Policy)

	// The authenticated differential: idor, cross_tenant and privilege_escalation,
	// decided over the runner's structured observations. BuildSnapshot returns nil
	// when in.Observations() is nil, which is the NOT MEASURED state: the runner
	// emitted nothing, so the authenticated classes are not exercised and this
	// appends no finding rather than a clean pass it did not earn. It never blocks
	// the whole family on the missing observations, because that would silence the
	// unauthenticated reach above, which needs none; the honest signal for "the
	// runner did not emit observations" is the absence of an authenticated
	// finding, not a red build over a working anonymous probe.
	//
	// When observations ARE present the differential runs: a cross-owner reach
	// that returned the victim's content fires on its class's key, and a reach the
	// boundary refused with a live arm produces nothing. A base twin, when one was
	// built, suppresses a pre-existing reach the base already allowed; without one
	// every candidate leak is judged against the absolute expectation, which still
	// fires, so the feature delivers value before the base-twin lane lands.
	if cand := BuildSnapshot(in.Observations(), in.Golden); cand != nil {
		var base *Snapshot
		if b, ok := in.Baseline(); ok {
			base = BuildSnapshot(b.Observations, in.Golden)
		}
		findings = append(findings, Assess(base, cand, in.Policy)...)
	}
	return findings, nil
}

// collect drives the twin and builds the candidate snapshot. It proves its own
// detector before trusting any reading, records reachability, and exercises each
// derivable route as an anonymous caller.
func (f *family) collect(ctx context.Context, baseURL string, targets []change.Target, markers []string) (*Snapshot, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		return nil, fmt.Errorf("authz: no twin URL to probe")
	}

	snap := &Snapshot{Observations: map[string]Observation{}}

	// Reachability: any HTTP answer counts, even a 404. Only a transport error
	// means the twin is not there, and that blocks the run rather than passing
	// it.
	if reachable, err := f.reachable(ctx, baseURL); err != nil {
		return nil, fmt.Errorf("authz: the twin at the given URL could not be reached, so this run says nothing about the change: %w", err)
	} else {
		snap.Reachable = reachable
	}

	// Detector control: prove the content matcher recognises a planted marker
	// and rejects an absent one, over the run's actual markers. Without markers
	// there is nothing to recognise, so the unauthenticated reach cannot tell a
	// protected body from a public one and the family stays silent rather than
	// guess: DetectorLive is false and Assess returns nothing.
	snap.DetectorLive = detectorLive(markers)

	for _, t := range targets {
		if t.Kind != change.TargetEndpoint && t.Kind != change.TargetScreen {
			continue
		}
		route, ok := routeOf(t)
		if !ok {
			// The target names a file whose served route cannot be derived. It
			// is not exercised, which is reported as absence, never as "no
			// endpoint found": a target the family could not probe is a gap in
			// the family, not a clean bill of health for the endpoint.
			continue
		}
		p := Probe{
			ID:          "anon " + route,
			Class:       ClassUnauthenticated,
			Where:       route,
			Method:      http.MethodGet,
			ObjectClass: "content the endpoint should require authentication to return",
			Because:     append([]string(nil), t.Because...),
			Public:      publicRoute(route),
		}
		outcome := f.reach(ctx, baseURL, route, markers)
		snap.Probes = append(snap.Probes, p)
		snap.Observations[p.ID] = Observation{
			ProbeID: p.ID,
			// An unauthenticated reach needs no liveness arm: the pass condition
			// is a refusal, but a LEAK is decided by planted content coming back,
			// which a dead control cannot fake, so the allowed reading is
			// self-proving. The refusal reading is the safe one and produces no
			// finding regardless.
			Arm: Arm{Actor: outcome, Liveness: OutcomeAllowed, LivenessArmed: true},
		}
	}
	return snap, nil
}

// reachable reports whether the twin answered at all.
func (f *family) reachable(ctx context.Context, baseURL string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/", nil)
	if err != nil {
		return false, err
	}
	resp, err := f.doer.Do(req)
	if err != nil {
		return false, err
	}
	drain(resp)
	return true, nil
}

// reach issues one unauthenticated request and classifies the outcome by content
// presence. A transport error is an error outcome, not a denial: a request that
// never completed says nothing about authorization.
func (f *family) reach(ctx context.Context, baseURL, route string, markers []string) Outcome {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+route, nil)
	if err != nil {
		return OutcomeError
	}
	resp, err := f.doer.Do(req)
	if err != nil {
		return OutcomeError
	}
	body := readBody(resp)
	return StatusOutcome(resp.StatusCode, contentPresent(body, markers))
}

// detectorLive proves the content matcher over the actual markers.
func detectorLive(markers []string) bool {
	live := false
	for _, m := range markers {
		if m == "" {
			continue
		}
		// Recognises the marker inside a body, and does not recognise it in an
		// empty one. Both must hold, or the matcher is not proven.
		if contentPresent("prefix "+m+" suffix", markers) && !contentPresent("", markers) {
			live = true
		}
	}
	return live
}

// markersOf reads the golden's canary values, which are the planted markers the
// content matcher looks for. The values live here, against the twin; a finding
// that recognises one reports the location and never the value.
func markersOf(g security.GoldenView) []string {
	var out []string
	for _, c := range g.Canaries() {
		if c.Value != "" {
			out = append(out, c.Value)
		}
	}
	return out
}

// routeOf derives a probeable route from a target, or reports that it could not.
//
// The change router deliberately names a target by its FILE, not by a guessed
// URL, so this derivation is best effort and says no rather than guess wrong. It
// handles the two cases it can be sure of: a ref that is already a route (it
// starts with a slash), and a framework route file whose directory names the
// route. A dynamic segment is dropped to the collection prefix, because an
// unauthenticated probe has no id to fill it with and a collection that returns
// planted content to anon is the leak either way. Everything else is not
// derivable, and the target is left unexercised rather than probed at a made up
// path.
func routeOf(t change.Target) (string, bool) {
	ref := strings.TrimSpace(t.Ref)
	if ref == "" {
		return "", false
	}
	if strings.HasPrefix(ref, "/") {
		return collectionOf(ref), true
	}
	base := strings.ToLower(path.Base(ref))
	isRouteFile := base == "route.ts" || base == "route.js" ||
		base == "route.tsx" || base == "route.go"
	if !isRouteFile {
		return "", false
	}
	dir := path.Dir(ref)
	// The route is the directory path under the last "app" (or "pages")
	// segment. Find it, and take everything after it.
	segs := strings.Split(dir, "/")
	start := -1
	for i, s := range segs {
		if s == "app" || s == "pages" {
			start = i + 1
		}
	}
	if start < 0 || start > len(segs) {
		return "", false
	}
	var route []string
	for _, s := range segs[start:] {
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
			// A route group is a folder that does not appear in the URL.
			continue
		}
		if isDynamicSegment(s) {
			// Drop the dynamic segment and everything after it: probe the
			// collection prefix.
			break
		}
		route = append(route, s)
	}
	if len(route) == 0 {
		return "/", true
	}
	return "/" + strings.Join(route, "/"), true
}

// isDynamicSegment reports whether a path segment is a route parameter in any of
// the common spellings: [id], :id, {id}.
func isDynamicSegment(s string) bool {
	return (strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]")) ||
		strings.HasPrefix(s, ":") ||
		(strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}"))
}

// collectionOf trims a trailing dynamic segment from a route so an
// unauthenticated probe hits the collection rather than a parameter it cannot
// fill.
func collectionOf(route string) string {
	segs := strings.Split(route, "/")
	var kept []string
	for _, s := range segs {
		if s == "" {
			continue
		}
		if isDynamicSegment(s) {
			break
		}
		kept = append(kept, s)
	}
	if len(kept) == 0 {
		return "/"
	}
	return "/" + strings.Join(kept, "/")
}

// publicRoutePrefixes are the conventionally unauthenticated surfaces: health,
// the sign-in and sign-up flows, webhooks, and served assets. An anonymous
// reach of one of these is expected and never flags. It is a conservative
// built-in set: the authoritative declaration is the manifest's public surface,
// which reaches the family through the router rather than the merged input, so
// this set plus the router's baseline agreement is what keeps a genuinely public
// route from false-reding until that declaration is threaded through.
var publicRoutePrefixes = []string{
	"/health", "/healthz", "/livez", "/readyz", "/ping", "/status",
	"/login", "/signin", "/sign-in", "/logout", "/signout", "/sign-out",
	"/signup", "/sign-up", "/register", "/auth", "/oauth", "/sso",
	"/webhook", "/webhooks", "/callback",
	"/static", "/assets", "/public", "/_next", "/favicon", "/robots.txt",
}

// publicRoute reports whether a route is conventionally public.
func publicRoute(route string) bool {
	r := strings.ToLower(route)
	for _, p := range publicRoutePrefixes {
		if r == p || strings.HasPrefix(r, p+"/") {
			return true
		}
	}
	return false
}

// readBody reads a bounded prefix of the response body and closes it. The value
// stays here; only whether a planted marker is in it leaves this function.
func readBody(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	return string(b)
}

// drain closes a response whose body is not read, so the connection can be
// reused.
func drain(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))
	_ = resp.Body.Close()
}
