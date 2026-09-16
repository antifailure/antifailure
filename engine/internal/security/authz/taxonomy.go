// Package authz is the authorization security check family: it decides whether
// a change let an identity reach a thing that only another identity should
// reach, by exercising the sanitized twin and reading the outcome.
//
// It plugs into engine/internal/security as one Family. It owns the
// "security.authz.*" policy keys and emits findings under them, and nothing
// else in the security namespace. The spine routes it, the manifest levels it,
// and the gate turns its findings into an exit code; this package only decides
// what an authorization outcome was and whether the change made it worse.
//
// The load bearing rule of the whole family, stated once here because every
// probe depends on it: an authorization outcome is decided by whether the
// VICTIM'S CONTENT is present in the response, not by the status code. Row
// level security refuses by dropping the row, so a forbidden object comes back
// as an empty 200 or a 404, never a 403. A family that keyed "denied" on
// "status was 403" would pass every correctly isolated target and fail every
// one that enforces the boundary the modern way. So denial here includes the
// empty body and the 404, and the leak signal is "the victim's content came
// back," full stop.
package authz

import "strings"

// Outcome is the normalised result of one authorization probe. It is what the
// differential engine compares across the baseline and the candidate, and it
// is deliberately NOT the status code: two targets that both refuse an object,
// one with a 403 and one with an empty 200, must read as the same outcome or
// the family false-reds the second the moment somebody touches it.
type Outcome string

const (
	// OutcomeAllowed is a 2xx whose body carries the victim's content. This is
	// the only outcome that is a leak, and it is decided by content presence,
	// never by the status alone.
	OutcomeAllowed Outcome = "allowed"
	// OutcomeDenied is a refusal: a 401 or 403, a 404 for an object that exists
	// for another owner, or a 2xx whose body does NOT carry the victim's
	// content, which is how row level security refuses. All three are the same
	// fact to this family: the boundary held.
	OutcomeDenied Outcome = "denied"
	// OutcomeNotFound is a 404 for an object that exists nowhere, distinct from
	// a 404 that hides another owner's object, because the first says the probe
	// was aimed at nothing and the second says the boundary held.
	OutcomeNotFound Outcome = "not_found"
	// OutcomeError is a 5xx, a refusal page the app itself served in error, or
	// an unreachable endpoint. It is never a verdict about authorization: a
	// candidate build that broke the endpoint is a different check's finding,
	// not an authorization pass and not a fail.
	OutcomeError Outcome = "error"
	// OutcomeRateLimited is a 429, and it is its OWN outcome rather than a
	// denial. A probe fleet is bursty by construction and trips per-token and
	// per-address limits under load, so 429 arrives on the large runs and not
	// on the small ones a developer tests with, and it looks exactly like a
	// control holding firm. Normalising it to denied would report the family's
	// most confident greens exactly when the target was too busy to enforce
	// anything. It triggers backoff, and if it persists the probe is
	// inconclusive with the limiter named.
	OutcomeRateLimited Outcome = "rate_limited"
)

// isRefusal reports whether an outcome means the boundary held. NotFound counts
// because a 404 for another owner's object is a refusal; a caller that needs to
// separate the two reads the Outcome directly.
func (o Outcome) isRefusal() bool { return o == OutcomeDenied || o == OutcomeNotFound }

// Class is the shape of the authorization question a probe asks. It selects the
// policy key a finding lands on and, with it, the severity a reviewer reads,
// because a cross-tenant leak and a same-tenant object reference are not the
// same incident even when the mechanism is identical.
type Class string

const (
	// ClassIDOR is a horizontal, cross-USER reach inside one tenant: actor and
	// victim share an org, and the actor reached the victim's object.
	ClassIDOR Class = "idor"
	// ClassCrossTenant is a horizontal, cross-TENANT reach: actor and victim
	// are in different orgs. It is the highest severity horizontal break and
	// includes a list endpoint returning even one row from another tenant.
	ClassCrossTenant Class = "cross_tenant"
	// ClassMissingAuthorization is a vertical break: a lower-privileged
	// identity reached a surface only a higher-privileged one should, or a
	// control that should have refused was absent.
	ClassMissingAuthorization Class = "missing_authorization"
	// ClassUnauthenticated is an unauthenticated identity reaching an endpoint
	// the change added, when that endpoint is not declared public. It is the
	// single most common real regression and the reason the anonymous identity
	// is a first class one.
	ClassUnauthenticated Class = "unauthenticated_access"
	// ClassPrivilegeEscalation is a multi step sequence that opened a higher
	// capability to an identity that started lower.
	ClassPrivilegeEscalation Class = "privilege_escalation"
	// ClassPolicyBypass is the meta escalation: a single permission that lets
	// its holder edit the authorization model was used to widen the holder's
	// own capabilities. One grant becomes every grant, so it is the highest
	// severity of all and is probed as a single step rather than a sequence.
	ClassPolicyBypass Class = "policy_bypass"
)

// ruleFor maps a class to the full security policy key, which is also the
// finding rule. It is the one place the mapping lives, so a class and its key
// cannot drift.
func (c Class) ruleFor() string { return prefix + string(c) }

// passIsDenied reports whether a class's PASS condition is "denied," which is
// true for every class this family probes: the boundary is supposed to hold,
// so the safe reading is a refusal. That is exactly why every one of them needs
// a liveness arm (an identity that SHOULD be allowed, reading allowed), because
// a broken, absent, unreachable or unreached control also reads denied and is
// indistinguishable from the control working by the response alone.
func (c Class) passIsDenied() bool { return true }

// prefix is the family's namespace. Kept next to ruleFor so the two never
// separate.
const prefix = "security.authz."

// StatusOutcome classifies one HTTP response into an outcome, given whether the
// victim's content was found in the body. It is the seam every transport funnels
// through, so the content-presence rule is enforced in exactly one place rather
// than re-derived per probe.
//
// contentPresent is the only thing that can turn a 2xx into a leak. A 2xx with
// the content absent is a refusal, which is how RLS drops a row, and that is why
// the boolean is a separate input rather than inferred from the status.
func StatusOutcome(status int, contentPresent bool) Outcome {
	switch {
	case status == 429:
		return OutcomeRateLimited
	case status >= 500, status == 0:
		return OutcomeError
	case status == 401, status == 403:
		return OutcomeDenied
	case status == 404:
		// The caller decides whether this 404 hides another owner's object or
		// names nothing; at the status level a 404 is a refusal by default, and
		// the probe upgrades it to not_found when it knows the object exists
		// nowhere.
		return OutcomeDenied
	case status >= 200 && status < 300:
		if contentPresent {
			return OutcomeAllowed
		}
		return OutcomeDenied
	default:
		// 3xx and every other status is not a decision this family reads; it is
		// treated as a refusal rather than a leak, because the safe default in a
		// security namespace is that the boundary held unless the content proves
		// otherwise.
		return OutcomeDenied
	}
}

// contentPresent reports whether any of the victim's planted markers appears in
// the response body. The markers are the golden's canaries: a known fake value
// per tenant, planted in the copy of production, that must not appear in a
// response it does not belong in. The VALUE is matched here, inside the engine,
// against the twin; it never reaches a finding, which carries only the location.
func contentPresent(body string, markers []string) bool {
	for _, m := range markers {
		if m == "" {
			continue
		}
		if strings.Contains(body, m) {
			return true
		}
	}
	return false
}
