// Package ssrf reads the egress decision log for requests the application was
// coaxed into making against an address it should never reach.
//
// It is a security framed reader over the SAME decisions the egress firewall
// already produces, not a second firewall. The transport facts stay where they
// are owned: the proxy refuses loopback, link local, private, carrier grade,
// the metadata endpoint and DNS rebinds, and every outbound request leaves a
// decision behind. This family adds the security reading of those decisions: a
// refused request whose destination is an INTERNAL or METADATA target is the
// firewall catching a server side request forgery, and the refusal is the
// proof. A change that made an endpoint fetch a user supplied URL which reached
// 169.254.169.254 shows up here as a refused internal decision, attributed to
// the surface the diff touched.
//
// The refusal is what makes this behavioral rather than a guess: the finding
// fires because the firewall actually blocked a request to an internal address,
// not because a URL parameter looked suspicious. An outbound request that was
// ALLOWED to an internal address is the twin talking to its own services and is
// not a finding; only a REFUSED internal reach is.
//
// The distinction from egress_surprise, which lane owns the transport layer:
// egress_surprise is a NEW host nothing declared. This family is an INTERNAL
// address reached, which is a different fact and is tuned separately. A refused
// internal decision that a webhook or callback outbound produced is reported as
// callback drift rather than a bare internal fetch, because the fix is
// different: the callback destination is user influenced.
package ssrf

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/internal/security"
)

// The two policy keys this family owns, both verification failures (exit 7):
// each is proven by the firewall actually refusing a request the application
// made, so the run exercised the hole rather than reasoning about it.
const (
	// RuleInternalHost fires when a non callback outbound reached an internal or
	// metadata target and was refused.
	RuleInternalHost = report.PolicyKey("security.ssrf.internal_host")
	// RuleCallbackDrift fires when a webhook or callback outbound reached an
	// internal or metadata target and was refused: the callback destination was
	// influenced into pointing somewhere it must not.
	RuleCallbackDrift = report.PolicyKey("security.ssrf.callback_drift")
)

// isInternalTarget reports whether a destination host is an internal or
// metadata address that the outside world must never reach through the
// application. It mirrors the proxy's own destination guard rather than reading
// a field off the decision, because the decision log carries no such predicate:
// the classification lives on the proxy side and is reproduced here over the
// host string the log does carry.
func isInternalTarget(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return false
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback() ||
			ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
			ip.IsPrivate() || ip.IsUnspecified() ||
			isCarrierGrade(ip)
	}
	// A name rather than a literal. localhost, a single label name with no dot,
	// and the internal cluster suffixes are all names that resolve inside the
	// perimeter.
	if h == "localhost" || !strings.Contains(h, ".") {
		return true
	}
	for _, suffix := range []string{
		".localhost", ".internal", ".local", ".cluster.local", ".svc",
	} {
		if strings.HasSuffix(h, suffix) {
			return true
		}
	}
	return false
}

// isCarrierGrade reports whether an address is in the 100.64.0.0/10 shared
// address space, which Go's IsPrivate does not cover and which the proxy
// refuses for the same reason it refuses the private ranges.
func isCarrierGrade(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	return ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127
}

// refused reports whether a decision was denied by the firewall. The Allowed
// field is authoritative: a decision the firewall let through is Allowed, and a
// refused internal reach is exactly the one it did not.
func refused(d local.Decision) bool {
	return !d.Allowed
}

// isCallback reports whether a decision looks like a webhook or callback
// delivery, so a refused internal reach it produced is reported as callback
// drift rather than a bare internal fetch. It is a path heuristic, the same
// shape the capture log uses to recognise a webhook, and it is deliberately
// conservative: a decision it does not recognise as a callback is reported
// under the plain internal fetch rule, never dropped.
func isCallback(d local.Decision) bool {
	p := strings.ToLower(d.Path)
	return strings.Contains(p, "webhook") || strings.Contains(p, "callback")
}

// sink is one internal destination the firewall refused, and how many times.
type sink struct {
	rule  report.PolicyKey
	host  string
	count int
}

// Detect turns the run's egress decisions and the endpoints the diff touched
// into findings. It fires only on a REFUSED internal or metadata reach, which
// is the firewall proving the application was coaxed into a request it should
// not make. Findings are grouped by destination host so many requests to one
// internal address are one finding with a count.
//
// The endpoints the change touched are named in the location, which is the
// security value over the raw transport refusal: it ties the internal reach to
// the surface a reviewer changed. A precise request to endpoint correlation
// needs an ingress trace id the proxy does not yet expose; until it does, the
// attribution is to the changed surface as a whole, stated plainly rather than
// overclaimed.
func Detect(decisions []local.Decision, targets []change.Target, pol report.Policy) []report.Finding {
	internalLevel := pol.Level(RuleInternalHost)
	callbackLevel := pol.Level(RuleCallbackDrift)

	// Group refused internal reaches by rule and host.
	index := map[string]*sink{}
	var order []string
	for _, d := range decisions {
		if d.Host == "" || !refused(d) || !isInternalTarget(d.Host) {
			continue
		}
		rule := RuleInternalHost
		if isCallback(d) {
			rule = RuleCallbackDrift
		}
		if pol.Level(rule) == report.LevelIgnore {
			continue
		}
		key := string(rule) + "\x00" + strings.ToLower(d.Host)
		s, ok := index[key]
		if !ok {
			s = &sink{rule: rule, host: strings.ToLower(d.Host)}
			index[key] = s
			order = append(order, key)
		}
		s.count++
	}
	if len(order) == 0 {
		return nil
	}
	sort.Strings(order)

	where := endpointWhere(targets)
	out := make([]report.Finding, 0, len(order))
	for _, key := range order {
		s := index[key]
		level := internalLevel
		title := "the application was coaxed into reaching an internal address"
		fix := "Validate and allow list the URL the endpoint fetches. A user supplied destination must never resolve to a loopback, link local, private, or metadata address."
		if s.rule == RuleCallbackDrift {
			level = callbackLevel
			title = "a webhook or callback was directed at an internal address"
			fix = "Pin the callback destination to a declared target rather than a caller supplied value, and reject a destination that resolves to an internal or metadata address."
		}
		out = append(out, report.Finding{
			Rule:  string(s.rule),
			Level: level,
			Title: title,
			Detail: fmt.Sprintf("the firewall refused %s to %s, an internal target, during the workflows",
				plural(s.count, "outbound request", "outbound requests"), internalLabel(s.host)),
			Fix:   fix,
			Count: s.count,
			Where: where,
		})
	}
	return out
}

// internalLabel names the class of internal destination without echoing a value
// that could be mistaken for a live target. A literal metadata address is named
// as such; everything else is described by its class.
func internalLabel(host string) string {
	if ip := net.ParseIP(host); ip != nil {
		switch {
		case ip.IsLinkLocalUnicast():
			return "the link local metadata range"
		case ip.IsLoopback():
			return "loopback"
		case isCarrierGrade(ip):
			return "the carrier grade shared range"
		case ip.IsPrivate():
			return "a private range address"
		case ip.IsUnspecified():
			return "the unspecified address"
		}
		return "an internal address"
	}
	if host == "localhost" {
		return "localhost"
	}
	return "an internal name"
}

// endpointWhere names the endpoints the diff touched, for the finding's
// location. It is the changed surface, not a guessed URL, matching the target
// contract that a ref is a location a reviewer can open.
func endpointWhere(targets []change.Target) string {
	var refs []string
	seen := map[string]bool{}
	for _, t := range targets {
		if t.Kind != change.TargetEndpoint && t.Kind != change.TargetScreen {
			continue
		}
		if seen[t.Ref] {
			continue
		}
		seen[t.Ref] = true
		refs = append(refs, t.Ref)
	}
	if len(refs) == 0 {
		return "the changed outbound path"
	}
	sort.Strings(refs)
	if len(refs) > 3 {
		refs = append(refs[:3], fmt.Sprintf("and %d more", len(refs)-3))
	}
	return strings.Join(refs, ", ")
}

// plural renders a count with the right noun.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// keys declares the two policy keys this family owns. Both are verification
// failures because both are proven by the firewall's refusal of a real request,
// and a test proves each declared exit equals security.ExitFor(key).
func keys() []security.KeySpec {
	return []security.KeySpec{
		{
			Key:     RuleInternalHost,
			Default: report.LevelFail,
			Title:   "the application reached an internal or metadata address",
			Docs:    "concepts/security",
			Exit:    report.ExitVerification,
		},
		{
			Key:     RuleCallbackDrift,
			Default: report.LevelFail,
			Title:   "a webhook or callback destination reached an internal address",
			Docs:    "concepts/security",
			Exit:    report.ExitVerification,
		},
	}
}

var (
	familySurfaces = []change.Surface{change.SurfaceCode, change.SurfaceService, change.SurfaceEgress}
	familyChecks   = []change.Check{change.CheckSSRF}
)

var _ security.Family = (*family)(nil)

// family is the SSRF reader as a registered security family.
type family struct{}

// New builds the SSRF family. The router registers it with a bare New and
// supplies the per run egress decisions through security.Input.
func New() security.Family {
	return &family{}
}

func (f *family) Name() string               { return "ssrf" }
func (f *family) Surfaces() []change.Surface { return familySurfaces }
func (f *family) Checks() []change.Check     { return familyChecks }
func (f *family) Keys() []security.KeySpec   { return keys() }
func (f *family) Licensed() string           { return "" }

// Probe reads the run's egress decisions and returns the internal reach
// findings. A nil decision log is a blocked probe, never a pass: the router
// distinguishes an unread log (nil, not measured) from a run that made no
// outbound call (an empty, non-nil log), and only the second is a clean look.
func (f *family) Probe(_ context.Context, in security.Input) ([]report.Finding, error) {
	decisions := in.Decisions()
	if decisions == nil {
		return nil, fmt.Errorf(
			"the SSRF reader could not read this run's egress decisions, so it observed no outbound request")
	}
	return Detect(decisions, in.Targets, in.Policy), nil
}
