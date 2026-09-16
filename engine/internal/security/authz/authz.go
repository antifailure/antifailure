package authz

import (
	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/security"
)

// New returns the authorization family. It is the one constructor the spine's
// registry registers, and it is what a router calls to obtain the family; the
// registration itself lives in engine/internal/security's Default, added by the
// router that collects findings, not here, so this package never edits a file
// that every security lane also edits.
//
// The family drives the sanitized twin over HTTP. The transport is the default
// client; a test hands it a twin's own address through the Input's BaseURL and
// drives the real path, and a caller that needs an egress-policed client passes
// one with NewWith.
func New() security.Family { return &family{doer: defaultDoer()} }

// NewWith returns the family with a specific HTTP transport, so a caller that
// must route the twin's traffic through an egress-policed client, and a test
// that must observe the requests, can supply their own. Both reach the same
// probe logic.
func NewWith(d Doer) security.Family { return &family{doer: d} }

// family is the authorization check family.
type family struct {
	doer Doer
}

// Name is the family id and the namespace of every key and rule it owns.
func (f *family) Name() string { return "authz" }

// Surfaces are the change surfaces this family runs on. Auth is the sharpest:
// a guard, a middleware, a policy map, an entitlement, a session or token seam.
// Code is included because a route handler that gains or loses a check is
// ordinary source until something reads it as a route, and a missing check is
// as often the regression as a wrong one. A database migration that redefines a
// role is NOT consumed here: that target is exercised by SQL, and this family
// drives HTTP; the vertical and broadening probes it implies reach the family
// through the endpoints the role change protects, which come in as code and
// auth surfaces.
func (f *family) Surfaces() []change.Surface {
	return []change.Surface{change.SurfaceAuth, change.SurfaceCode}
}

// Checks is the single change check this family contributes.
func (f *family) Checks() []change.Check { return []change.Check{change.CheckAuthz} }

// Licensed returns the empty string: the authorization family is one every
// edition gets. Broken access control is the first entry in the industry's own
// top ten, and gating it behind a licence would mean the product shipped a
// security suite that refused to check the most common vulnerability for most
// of its users. The registry stays complete in both editions regardless; this
// only says the probe never refuses on a licence.
func (f *family) Licensed() string { return "" }

// The policy keys this family owns, each the manifest key and the finding rule
// at once. Every one defaults to fail, because the default in a security
// namespace has to be the one that does not lie: a project with no authorization
// model yet sets these to warn in its manifest and records that choice, rather
// than getting a silent pass. Every one exits with the verification code,
// because each is a vulnerability the family PROVED by exercising the twin, not
// a configuration it refused; the KeySpec.Exit must equal security.ExitFor(key),
// which a test in this package proves so the declared exit and the gate's exit
// cannot drift.
const (
	// RuleMissingAuthorization is a surface reached without the authorization it
	// requires: a normal identity on an admin only endpoint, or a control that
	// should have refused and was absent.
	RuleMissingAuthorization = prefix + "missing_authorization"
	// RuleIDOR is a horizontal cross-user object reference inside one tenant.
	RuleIDOR = prefix + "idor"
	// RuleCrossTenant is a cross-tenant isolation break, including a list
	// endpoint that returned another tenant's row. It is the highest severity
	// horizontal break and carries its own key so a project can hold tenant
	// isolation ungovernable-down.
	RuleCrossTenant = prefix + "cross_tenant"
	// RuleUnauthenticatedAccess is an added endpoint reachable unauthenticated
	// and not declared public.
	RuleUnauthenticatedAccess = prefix + "unauthenticated_access"
	// RulePrivilegeEscalation is a sequence that opened a higher capability to
	// an identity that started lower.
	RulePrivilegeEscalation = prefix + "privilege_escalation"
	// RulePolicyBypass is the meta escalation: a permission over the
	// authorization model used to widen its own holder. Reserved by the spine's
	// exit mapping and owned here so it carries a default level, its title and
	// its docs slug like the rest of the family.
	RulePolicyBypass = prefix + "policy_bypass"
)

// Keys declares every policy key the family reads, with its default level.
func (f *family) Keys() []security.KeySpec {
	spec := func(rule, title string) security.KeySpec {
		return security.KeySpec{
			Key:     report.PolicyKey(rule),
			Default: report.LevelFail,
			Title:   title,
			Docs:    "concepts/security",
			Exit:    security.ExitFor(report.PolicyKey(rule)),
		}
	}
	return []security.KeySpec{
		spec(RuleMissingAuthorization, "a surface was reached without the authorization it requires"),
		spec(RuleIDOR, "an object was reached across a user boundary"),
		spec(RuleCrossTenant, "a response carried another tenant's content"),
		spec(RuleUnauthenticatedAccess, "an added endpoint answered an unauthenticated request with content"),
		spec(RulePrivilegeEscalation, "a low privilege identity reached a higher capability"),
		spec(RulePolicyBypass, "a permission over the authorization model widened its own holder"),
	}
}
