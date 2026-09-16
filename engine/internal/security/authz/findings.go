package authz

import "strings"

// The finding text. Every string here is bounded and neutralized: it names a
// class, a location, a method and an outcome, and never a request body, a
// response, a row or an object id. The object under test is named by its class
// ("another tenant's object"), because the id comes from the masked golden and
// must not leave the run. This is the data boundary a security buyer scrutinises,
// and it is enforced by construction rather than by a scrub after the fact.

// titleFor is the one line headline for a class.
func titleFor(c Class) string {
	switch c {
	case ClassIDOR:
		return "an object was reached across a user boundary"
	case ClassCrossTenant:
		return "a response carried another tenant's content"
	case ClassMissingAuthorization:
		return "a surface was reached without the authorization it requires"
	case ClassUnauthenticated:
		return "an added endpoint answered an unauthenticated request with content"
	case ClassPrivilegeEscalation:
		return "a low privilege identity reached a higher capability"
	case ClassPolicyBypass:
		return "a permission over the authorization model widened its own holder"
	default:
		return "an authorization boundary did not hold"
	}
}

// detailFor is the bounded description. It states what happened in terms of the
// location and the outcomes, so a reviewer can reproduce it by hand without the
// finding ever carrying the leaked value.
func detailFor(p Probe, baseOutcome, candOutcome Outcome, hasBase bool) string {
	var b strings.Builder
	b.WriteString(actorPhrase(p.Class))
	b.WriteString(" reached ")
	if p.Method != "" {
		b.WriteString(p.Method)
		b.WriteString(" ")
	}
	b.WriteString(locationPhrase(p))
	b.WriteString(" and the response carried ")
	b.WriteString(victimPhrase(p))
	b.WriteString(".")
	if hasBase {
		b.WriteString(" The base revision returned ")
		b.WriteString(outcomePhrase(baseOutcome))
		b.WriteString(" for the same request, so this change is what opened it.")
	} else {
		b.WriteString(" There is no reading on the base revision for this request, so it is judged against the absolute expectation that the boundary holds.")
	}
	return b.String()
}

// fixFor is what to write instead, in one sentence, per class.
func fixFor(c Class) string {
	switch c {
	case ClassIDOR:
		return "Scope the object lookup to the acting identity, so a request for an object the identity does not own returns nothing rather than the object."
	case ClassCrossTenant:
		return "Enforce the tenant boundary in the query itself, through row level security or an equivalent predicate, so another tenant's rows are dropped before the handler ever sees them."
	case ClassMissingAuthorization:
		return "Require the authorization the surface needs, and prove the check is reached rather than defined: a guard with no live call site refuses nobody."
	case ClassUnauthenticated:
		return "Put the new endpoint behind the authentication middleware, or declare it in the manifest's public surface if it is genuinely meant to be reached unauthenticated."
	case ClassPrivilegeEscalation:
		return "Close the step where the capability opened: an identity that started low must not gain a higher capability by any ordering of the sequence."
	case ClassPolicyBypass:
		return "Forbid a permission that edits the authorization model from widening its own holder, so one grant is not every grant."
	default:
		return "Refuse the request the boundary is supposed to refuse."
	}
}

// actorPhrase names the identity that should have been refused.
func actorPhrase(c Class) string {
	switch c {
	case ClassIDOR:
		return "a user in the same tenant"
	case ClassCrossTenant:
		return "an identity in another tenant"
	case ClassUnauthenticated:
		return "an unauthenticated identity"
	case ClassPrivilegeEscalation, ClassPolicyBypass:
		return "a low privilege identity"
	default:
		return "an identity without the required role"
	}
}

// victimPhrase names, by class and never by value, what came back that should
// not have.
func victimPhrase(p Probe) string {
	if p.ObjectClass != "" {
		return p.ObjectClass
	}
	switch p.Class {
	case ClassCrossTenant:
		return "another tenant's content"
	case ClassIDOR:
		return "another user's object"
	default:
		return "resource content it should not reach"
	}
}

// locationPhrase names the route, which is a location and safe to print.
func locationPhrase(p Probe) string {
	if p.Where == "" {
		return "the endpoint"
	}
	return p.Where
}

// outcomePhrase renders an outcome for the reproduction sentence.
func outcomePhrase(o Outcome) string {
	switch o {
	case OutcomeDenied:
		return "a refusal"
	case OutcomeNotFound:
		return "not found"
	case OutcomeAllowed:
		return "the content"
	case OutcomeError:
		return "an error"
	case OutcomeRateLimited:
		return "a rate limit"
	default:
		return "no reading"
	}
}
