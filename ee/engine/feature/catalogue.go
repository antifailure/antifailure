// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package feature

import (
	"sort"
	"strings"

	"github.com/antifailure/antifailure/ee/engine/license"
)

// The one answer to what a customer is entitled to.
//
// There were two entitlement systems and they did not know about each other.
// The engine has license.Feature, twelve names a signed licence may carry. The
// control plane has organizations.plan and web/apps/api/src/entitlements.ts,
// which is real, tested and about quotas rather than features. Nothing
// reconciled them, so "what does this customer get" had no single answer and a
// self hosted licence and a hosted plan could disagree in silence.
//
// This is that answer, and the shape is taken from two places in this
// repository that already solved the same problem for something smaller.
// entitlements.ts carries enforcedAt on every quota, naming the call that reads
// it or saying null out loud, and a test walks the catalogue and refuses an
// entry whose enforcedAt names nothing. admin/controls.ts carries enforcedBy on
// every operator switch, in the same path:symbol form, for the same reason: a
// bare function name proves only that SOME file declares one.
//
// The rule both of those state, and the one this file exists to apply to the
// licence:
//
//	AN ENTITLEMENT THAT IS REPORTED AND NOT ENFORCED IS ALLOWED HERE. AN
//	ENTITLEMENT THAT CLAIMS TO BE ENFORCED AND IS NOT IS A LIE THE TEST
//	REFUSES TO LET SHIP.
//
// That is why State exists and why it has four values rather than a boolean.
// Nine of the twelve features are not enforced, and they are not unenforced for
// the same reason: some are not built at all, one is built and deliberately
// free, and some are refused by the hosted control plane on the plan as a whole
// rather than on the feature. A boolean would flatten those into one word and
// the word would be wrong for most of them.
//
// WHAT A NEW FEATURE COSTS. An entry here, and the two tests are the gate. A
// license.Feature with no entry fails TestEveryLicensedFeatureIsInTheCatalogue.
// A Declare with no entry marked StateGated fails the reverse direction in
// ee/engine/cmd/af. So a feature cannot be sold without somebody writing down
// what happens when it is absent.

// State is what this installation actually does about one licensed feature.
//
// The question each value answers is the same one: a customer whose licence
// does NOT name this feature, what do they get.
type State string

const (
	// StateGated is a refusal keyed on this exact feature. Something calls
	// Enabled with this constant and does less when the answer is false.
	// EnforcedAt names where, and a test proves the file holds that call.
	//
	// This is the only value that counts toward the number this file is
	// measured by.
	StateGated State = "gated"

	// StatePlanWide is a capability the control plane does refuse, but on the
	// plan as a whole rather than on this feature.
	//
	// The distinction is the whole finding. On a hosted plane, trpc.ts refuses
	// every permission outside HOSTED_GATE_EXEMPT unless the organization is on
	// the enterprise plan. That is a real refusal and it is one boolean, while
	// the licence carries twelve, so a licence naming a feature and a plan that
	// does not are not reconcilable by anything. Counting these as enforced
	// would flatter the product: the same refusal covers a customer who bought
	// the feature and one who did not.
	StatePlanWide State = "plan wide"

	// StateFree is implemented, available to everyone, and deliberately so.
	// The licence sells it and nothing withholds it.
	StateFree State = "free"

	// StateAbsent is a capability that is not built, so there is nothing to
	// refuse. Because says what is actually there, which is usually a schema,
	// a socket or a pure function with no caller.
	StateAbsent State = "absent"

	// StateUnmounted is built, complete, tested, and loaded by no binary. So
	// nobody gets it, whether they paid for it or not.
	//
	// ADDED AFTER THIS CATALOGUE WAS WRONG, and the mistake is worth keeping
	// rather than tidying away, because it is the same mistake in a new place.
	// sso, scim and rbac sat in the absent column on the evidence that nothing
	// under web/apps/api/src reads the SSO tables, which is TRUE and is a
	// statement about one directory. ee/web holds four complete TypeScript
	// packages, sso with SAML and OIDC, scim, rbac and audit, and I never
	// looked there. The registry could not have seen them either, correctly:
	// they do not run in the engine process and no licence key is present in
	// one. So the measurement was right and its SCOPE was wrong, which is the
	// failure this file exists to catch, committed by this file.
	//
	// It is a separate state from absent because the two say different things
	// to a buyer and to whoever fixes it. Absent is a feature to build.
	// Unmounted is a feature already paid for that needs one import, and it is
	// the worse of the two: absent looks unfinished from every direction, while
	// unmounted looks finished from every direction except the running process.
	StateUnmounted State = "unmounted"
)

// Entitlement is one licensed feature and what this product does about it.
type Entitlement struct {
	// Feature is the name a licence carries.
	Feature license.Feature

	// Summary is one line, in the words that go on the licensing page.
	Summary string

	// EnforcedAt is where the engine refuses, as `path/from/ee/engine:symbol`,
	// or empty when the engine does not refuse.
	//
	// It is ALSO the string passed to Declare, so the registry and this
	// catalogue cannot drift into naming two different places. The test opens
	// this exact file and requires two things of it: the symbol, and a call to
	// Enabled naming this exact feature. The second is what a bare name grep
	// cannot do, and it is the one that catches a site copied from a
	// neighbouring feature.
	EnforcedAt string

	// ControlPlaneAt is where the control plane implements or refuses this, as
	// `path/from/web/apps/api/src:symbol`, or empty.
	//
	// Present for StatePlanWide and StateFree as well as StateGated, because
	// the useful answer to "where does this live" is the same either way and
	// State is what says whether the licence is consulted. A test in each build
	// system opens the file and looks for the symbol, so a TypeScript rename
	// fails the TypeScript suite rather than only the Go one that a control
	// plane developer never runs.
	ControlPlaneAt string

	// State is what a customer without this feature gets.
	State State

	// Because is why it is not gated, required for every state but StateGated.
	//
	// Required, because "reported but not enforced" is a legitimate state and
	// an undocumented one is indistinguishable from a bug. The same sentence
	// entitlements.ts writes above notEnforcedBecause, for the same reason.
	Because string
}

// catalogue is every licensed feature, in the order AllFeatures returns them.
//
// Every Because below names evidence that can be checked rather than an
// impression. Where the evidence is a test or a file in this repository that
// already recorded the same finding, it is cited by name, because two
// independent statements of one gap drift and the older one is the one people
// quote.
var catalogue = []Entitlement{
	{
		Feature: license.FeatureAirGapped,
		Summary: "An installation that reaches nothing outside the operator's own network.",
		State:   StateAbsent,
		Because: "air_gapped has no reference in any Go code outside the constant itself and " +
			"tools/licensegen's copy of the name list. There is no " +
			"offline verification path, no refusal at any call site, and nothing that would " +
			"change if a licence named it. Wave 7's L7.2 defines it and builds the lifecycle " +
			"test that fails on any attempted connection.",
	},
	{
		Feature:        license.FeatureAuditStream,
		Summary:        "Privileged actions forwarded to the organization's own SIEM.",
		ControlPlaneAt: "",
		State:          StateAbsent,
		Because: "Absent in the engine and unmounted in the control plane, which is why it is " +
			"filed under the worse of the two. extension.AuditSink and Registry.Audit exist in " +
			"the community engine and NOTHING registers a sink or calls Audit outside " +
			"extension_test.go, verified by grep on this tree: two hits, both in that test. " +
			"The socket was built and nothing was plugged into it, which is the gap " +
			"ee/engine/cmd/af/main.go warns about in its own header, and L0.2 measured it from " +
			"the other side as one of three sockets not consulted. ee/web/audit holds sink " +
			"implementations for the control plane and no file under web/apps/api/src imports " +
			"those either. L7.1 builds the engine sinks and L7.6 the entry point that would " +
			"load the others.",
	},
	{
		Feature:        license.FeatureBilling,
		Summary:        "Subscriptions, invoices and the plan an organization is on.",
		ControlPlaneAt: "routers/billing.ts:billingRouter",
		State:          StateFree,
		Because: "Every organization on every installation reaches billing, and that is deliberate " +
			"rather than an oversight. billing.manage is in HOSTED_GATE_EXEMPT, so even the " +
			"hosted plan gate refuses it under no condition: gating the path that RESOLVES a " +
			"refusal would leave a lapsed customer with no exit, which hosted.ts calls a legal " +
			"exposure and not a courtesy. Nothing here consults a licence and nothing should.",
	},
	{
		Feature:    license.FeatureCompliance,
		Summary:    "SOC 2 and ISO 27001 evidence gathered from the control plane's own records.",
		EnforcedAt: "compliance/command.go:Command",
		State:      StateGated,
	},
	{
		Feature:        license.FeatureDashboard,
		Summary:        "The console: environments, masking, egress, audit and workloads.",
		ControlPlaneAt: "trpc.ts:orgProcedure",
		State:          StatePlanWide,
		Because: "The console is the only interface this product has and a self hosted install " +
			"with no licence at all keeps every page of it. On a hosted plane orgProcedure " +
			"refuses environments.view, and every other permission outside HOSTED_GATE_EXEMPT, " +
			"unless the organization is on the enterprise plan. That refusal is real and it is " +
			"keyed on the plan, not on this feature, so a licence naming enterprise_dashboard " +
			"changes nothing about whether the console loads.",
	},
	{
		Feature:        license.FeatureMultiRuntime,
		Summary:        "Placing an environment across several runtimes at once, by requirement and by tag.",
		ControlPlaneAt: "routers/runtimes.ts:runtimesRouter",
		State:          StateAbsent,
		Because: "Three separate things have to be true before this can be gated and none of them " +
			"is. engine/internal/scheduler implements placement completely, including " +
			"requirements, tags, the unsatisfiable versus queued distinction and health aware " +
			"placement, and Plan has ZERO callers outside scheduler_test.go; " +
			"controlplane/sink.go already records that nothing emits environment.queued. It " +
			"lives under engine/internal, which is unimportable from this module, so a licence " +
			"check cannot be put there from here at all. And schema.Runtime carries no " +
			"placement requirement, so no manifest can ask for one. The control plane's runtime " +
			"registry is real and is refused on the plan when the plane is hosted, but " +
			"routers/runtimes.ts says in its own header that it deliberately never sends a " +
			"runtime name to the engine. Gating a pure function nobody calls would be a check " +
			"that cannot fire.",
	},
	{
		Feature:    license.FeaturePolicy,
		Summary:    "Organization policy that refuses an environment the manifest would have allowed.",
		EnforcedAt: "policyenforce/policyenforce.go:Hook.Check",
		State:      StateGated,
	},
	{
		Feature:        license.FeatureRBAC,
		Summary:        "Roles, and a permission on every route.",
		ControlPlaneAt: "permissions.ts:PERMISSIONS",
		State:          StateFree,
		Because: "Free rather than unmounted, and it is the only feature where BOTH are true " +
			"of different code. The roles and permissions in web/apps/api/src/permissions.ts " +
			"are real, are enforced on every request by orgProcedure, and are enforced for " +
			"every organization on every plan including free, so nothing consults the licence " +
			"and rbac is sold and given away. Making that gated is a COMMERCIAL decision and " +
			"not a defect to fix quietly: switching it on would refuse permission checks for " +
			"organizations that have them today. Separately, ee/web/rbac adds approvals and a " +
			"policy file on top, and nothing under web/apps/api/src imports it, so that half " +
			"is unmounted in the sense the state above describes. The entry is free because " +
			"what a customer gets today is the working ungated one.",
	},
	{
		Feature:        license.FeatureSCIM,
		Summary:        "Directory provisioning, so joiners and leavers arrive from the identity provider.",
		ControlPlaneAt: "",
		State:          StateUnmounted,
		Because: "ee/web/scim implements the protocol, including the filter grammar and PATCH, " +
			"and nothing under web/apps/api/src imports it, so not one of its routes is " +
			"served. admin/platform.ts independently records the other half: scim_tokens has " +
			"existed since migration 0014 and nothing reads it, so a SCIM token authenticates " +
			"nothing, which is why the operator portal deliberately leaves those tokens off " +
			"the credential list rather than showing a row that cannot be revoked from.",
	},
	{
		Feature:        license.FeatureSSO,
		Summary:        "Single sign on against the organization's own identity provider.",
		ControlPlaneAt: "",
		State:          StateUnmounted,
		Because: "ee/web/sso is a complete implementation, SAML and OIDC, domain binding, " +
			"assertion replay protection and break glass codes, and NOTHING LOADS IT. No file " +
			"under web/apps/api/src imports it, so its install() has no caller outside tests " +
			"and none of its routes is ever registered. Separately, migration 0014's tables " +
			"have no writer: writers.test.ts records sso_connections under UNWIRED, and the " +
			"only reads anywhere are two count queries in the operator portal. So a customer " +
			"who buys sso gets the same product as one who does not, and the reason is one " +
			"missing import rather than any missing work. L7.6 builds the entry point.",
	},
	{
		Feature:    license.FeatureSecrets,
		Summary:    "Declared variables resolved from Vault or a cloud secret manager.",
		EnforcedAt: "secrets/source.go:Source.Available",
		State:      StateGated,
	},
	{
		Feature:        license.FeatureSupportAccess,
		Summary:        "A supported way for the vendor to see what a customer sees.",
		ControlPlaneAt: "admin/customers.ts:registerImpersonationRoutes",
		State:          StateFree,
		Because: "Free rather than absent, and the correction is worth recording because the " +
			"two say different things to a buyer: absent says we did not build it, free says " +
			"we built it and do not charge for it. This entry read absent on the reasoning " +
			"that support_access has no reference outside the constant, which is true about " +
			"the NAME and false about the capability. Operator impersonation is built and is " +
			"the thing this feature names: POST /v1/admin/impersonation/start and /end, a " +
			"reason required at the edge and by a CHECK constraint, an audit entry written " +
			"before the session exists and structurally unable to be skipped because " +
			"sessions.impersonation_audit_seq is NOT NULL with a foreign key into the chain, a " +
			"copy of that entry in the CUSTOMER's own log, and a minutes long expiry enforced " +
			"on every request. What is missing is only that nothing asks the licence first. " +
			"Adding that ask would be the wrong direction anyway: it is guarded by an operator " +
			"admin permission rather than by the customer's plan, and refusing support access " +
			"to a customer whose licence has lapsed withholds help from exactly the person " +
			"most likely to need it.",
	},
}

func init() {
	sort.Slice(catalogue, func(i, j int) bool {
		return catalogue[i].Feature < catalogue[j].Feature
	})
}

// Catalogue returns every licensed feature and what this product does about it.
//
// A copy, because a caller that could edit this could change what an
// installation claims about itself after the tests had passed on it.
func Catalogue() []Entitlement {
	out := make([]Entitlement, len(catalogue))
	copy(out, catalogue)
	return out
}

// Of returns the catalogue entry for one feature.
func Of(f license.Feature) (Entitlement, bool) {
	for _, e := range catalogue {
		if e.Feature == f {
			return e, true
		}
	}
	return Entitlement{}, false
}

// SplitSite splits a site reference into the file it names and the symbol.
//
// The one definition of the format, exported so that the checks in
// ee/engine/feature and in ee/engine/cmd/af cannot come to disagree about what
// a site string IS while both believe they are validating it. They read
// different things, the catalogue and the registry, and they have to read them
// the same way.
//
// The symbol half may be `Command`, `Hook.Check` or `auditsink.permitted`. The
// receiver or package qualifier is there for a human reader, who needs to know
// WHICH Check is meant; what a scanner looks for in the file is the last
// component, because that is what appears after `func`.
func SplitSite(site string) (file, symbol string, ok bool) {
	i := strings.IndexByte(site, ':')
	if i <= 0 || i == len(site)-1 {
		return "", "", false
	}
	file, qualified := site[:i], site[i+1:]
	if j := strings.LastIndexByte(qualified, '.'); j >= 0 {
		symbol = qualified[j+1:]
	} else {
		symbol = qualified
	}
	if symbol == "" {
		return "", "", false
	}
	return file, symbol, true
}

// GatedFeatures is every feature a missing entitlement actually refuses.
//
// The number this lane is measured by is len(GatedFeatures()) over
// len(license.AllFeatures()), and it is deliberately the smaller of the two
// numbers that could be reported. See StatePlanWide for the other one and why
// it does not count.
func GatedFeatures() []license.Feature {
	out := []license.Feature{}
	for _, e := range catalogue {
		if e.State == StateGated {
			out = append(out, e.Feature)
		}
	}
	return out
}
