// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package feature

import (
	"sort"

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
		Because: "extension.AuditSink and Registry.Audit exist in the community engine and NOTHING " +
			"registers a sink or calls Audit outside extension_test.go. The socket was built and " +
			"nothing was ever plugged into it, which is the gap ee/engine/cmd/af/main.go warns " +
			"about in its own header. L0.2 measured the same thing from the other side: nine " +
			"sockets implementable from outside, six consulted by the engine, and AuditSink is " +
			"one of the three that are not. Wave 7's L7.1 builds the sinks.",
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
		Because: "Roles and permissions are real, are enforced on every request by orgProcedure, " +
			"and are enforced for every organization on every plan including free. Nothing " +
			"consults the licence, so rbac is sold and given away. Making it gated is a " +
			"COMMERCIAL decision and not a defect to be fixed quietly: switching it on would " +
			"refuse permission checks for organizations that have them today, which is a change " +
			"to what people already paid for rather than a missing check.",
	},
	{
		Feature: license.FeatureSCIM,
		Summary: "Directory provisioning, so joiners and leavers arrive from the identity provider.",
		State:   StateAbsent,
		Because: "scim_tokens has existed since migration 0014 and nothing under " +
			"web/apps/api/src reads it, so a SCIM token authenticates nothing. admin/platform.ts " +
			"records the same finding and deliberately leaves those tokens off the credential " +
			"list rather than showing a row that cannot be revoked from. There is no " +
			"provisioning behaviour to refuse.",
	},
	{
		Feature: license.FeatureSSO,
		Summary: "Single sign on against the organization's own identity provider.",
		State:   StateAbsent,
		Because: "Migration 0014 built the whole schema, and no route configures a connection and " +
			"no sign in path consults one. writers.test.ts records it under UNWIRED as a table " +
			"every screen reads and nothing writes: every account still signs in through GitHub " +
			"or an email link, sso_assertions_seen and sso_break_glass_codes are equally " +
			"untouched, and no customer can turn single sign on on. There is nothing to refuse.",
	},
	{
		Feature:    license.FeatureSecrets,
		Summary:    "Declared variables resolved from Vault or a cloud secret manager.",
		EnforcedAt: "secrets/source.go:Source.Available",
		State:      StateGated,
	},
	{
		Feature: license.FeatureSupportAccess,
		Summary: "A supported way for the vendor to see what a customer sees.",
		State:   StateAbsent,
		Because: "support_access has no reference outside the constant and tools/licensegen's " +
			"copy of the name list. The nearest implemented thing is operator impersonation, " +
			"which is an OPERATOR capability guarded by admin permissions and by an audit entry " +
			"that has to exist before the session does, and it is not something a customer's " +
			"licence turns on. Nothing consults this feature, so nothing changes when it is " +
			"absent.",
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
