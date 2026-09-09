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

	// THERE WAS A StatePlanWide HERE AND ITS REMOVAL IS THE POINT, so the
	// reasoning is kept rather than deleted with it. It meant "the control
	// plane refuses this, but on the plan as a whole rather than on this
	// feature", and enterprise_dashboard was its only member for exactly the
	// wrong reason: somebody reached for a state meaning "refused on the plan"
	// and put OUR OWN funnel in it. A plan wide refusal is us enforcing our own
	// pricing tiers, not a licence granting a capability, so it does not
	// describe anything sellable and has no business being one of the states a
	// catalogue of sellable things can take. Leaving the slot in place would
	// leave the same invitation for the next author, on the page that says what
	// customers buy. The distinction it recorded is real and now lives in
	// enterprise_dashboard's own Because, which is where a reader meets it in
	// context instead of as an available category.

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

	// StateControlPlaneGated is refused, by name, by the control plane, on a
	// licence check written in TypeScript that this package cannot see.
	//
	// ADDED BECAUSE THIS CATALOGUE DESCRIBED A TWO LANGUAGE PRODUCT BY LOOKING
	// IN ONE LANGUAGE, and that is a worse error than either of the two rows it
	// got wrong. Enforcement here lives in Go and in TypeScript. This file
	// measured `feature.Enabled` call sites, which only the engine has, and
	// read a zero as "nothing refuses this". A zero in that count means NOT
	// ENFORCED BY THE ENGINE, which is a different fact from not enforced, and
	// collapsing the two is how sso and scim came to be published as features
	// nobody has, paid or not, while the enterprise control plane was refusing
	// them by name with a 402.
	//
	// It is separate from StateGated because the two are checked by different
	// instruments and neither can see the other's evidence. A gated entry names
	// an engine file and a Go test opens it and requires an Enabled call for
	// that exact feature. A control plane gated entry names a site the
	// TypeScript registry declared, and only the TypeScript suite can confirm
	// it, because the declaration is made at module scope by the package that
	// enforces it and a package nothing imports declares nothing.
	//
	// So the honest count is two numbers rather than one. See GatedFeatures for
	// the engine's and ControlPlaneGatedFeatures for this one.
	StateControlPlaneGated State = "control_plane_gated"

	// StateEditionGated is refused by the COMMUNITY engine, through
	// edition.Permits, with the licence crossing the module boundary as
	// strings rather than the code crossing it as packages.
	//
	// The sixth state exists because "not enforced by the engine" is a
	// different fact from "not enforced". This is that sentence a third time
	// with a different subject: not enforced by feature.Enabled IN ee/engine is
	// a different fact from not enforced, and reading the first as the second
	// is what would file multi_runtime as absent while
	// engine/internal/env/env.go refuses a second placement target.
	//
	// It is not StateGated, and widening StateGated to swallow it would erase
	// the distinction the sixth state was created to draw. A gated entry is
	// confirmed by opening an ee/engine file and requiring a feature.Enabled
	// call for that exact feature; this one is confirmed by opening an engine/
	// file and requiring an edition.Permits call. A test that accepted either
	// mechanism to stay green would have stopped testing both.
	//
	// Why the enforcement is there rather than here is ee/engine/cmd/af/
	// placement.go's argument and it is a good one: the manifest's targets, the
	// scheduler, the runtime constructors and every command that agrees on
	// where an environment went would all have to move, and a second copy of
	// that in the enterprise module would be a second answer to where an
	// environment is.
	StateEditionGated State = "edition_gated"
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
	// Present for StateFree as well as StateGated and StateControlPlaneGated,
	// because
	// the useful answer to "where does this live" is the same either way and
	// State is what says whether the licence is consulted. A test in each build
	// system opens the file and looks for the symbol, so a TypeScript rename
	// fails the TypeScript suite rather than only the Go one that a control
	// plane developer never runs.
	//
	// ONE PATH CONVENTION: from the REPOSITORY ROOT, always.
	//
	// It was briefly two, and the bug that produced is the reason this comment
	// is emphatic. These paths used to be relative to web/apps/api/src, which
	// is fine while every entry lives there, and the enterprise packages do
	// not: a control plane gated entry has to name the site the TypeScript
	// registry declared, byte for byte, and those are repository relative. So a
	// second convention was introduced and DOCUMENTED HERE AND TAUGHT TO
	// NOTHING, and both readers promptly tried to open
	// web/apps/api/src/ee/web/scim/src/routes.ts. A comment describing a rule
	// that no code implements is the same defect as a licensed feature nothing
	// enforces, which is the subject of this entire file.
	//
	// One convention costs three longer strings and removes the branch from
	// both readers, and there are two: the Go test in this package and
	// web/apps/api/test/licensed-features.test.ts, which is deliberately in the
	// other build system so a control plane developer who renames a symbol and
	// never runs the Go suite still sees it fail.
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
		Because: "The only row where the question the others turn on does not arise, and it is " +
			"worth saying so rather than leaving the silence to be read as an oversight. " +
			"There is no code serving ANY subject here: air_gapped has no reference in any Go " +
			"code outside the constant itself and tools/licensegen's copy of the name list, " +
			"no offline verification path, and no refusal at any call site. billing and " +
			"enterprise_dashboard were classified wrongly because real code existed and " +
			"nobody asked who it served; here there is nothing to attribute to anybody, so " +
			"absent is the whole answer. Wave 7's L7.2 would define it and build the " +
			"lifecycle test that fails on any attempted connection, and that is a statement " +
			"about a lane which has NOT landed as of 88627d7f rather than about this tree.",
	},
	{
		Feature: license.FeatureAuditStream,
		Summary: "Privileged actions forwarded to the organization's own SIEM.",
		// The literal rather than auditsink.AuditStreamSite, because auditsink
		// imports THIS package to ask the licence, so importing it back is a
		// cycle. The constant exists precisely so the two cannot drift, and the
		// import that would have enforced that is the one the dependency
		// forbids, so a test in ee/engine/cmd/af asserts they are equal from a
		// binary that links both. See TestTheAuditSiteIsTheOneAuditsinkDeclares.
		EnforcedAt: "auditsink/auditsink.go:auditsink.permitted",
		State:      StateGated,
	},
	{
		Feature: license.FeatureBilling,
		Summary: "Subscriptions, invoices and the plan an organization is on.",
		State:   StateAbsent,
		Because: "REAL CODE FOR THE WRONG SUBJECT, and this entry read free on the strength of " +
			"it. billingRouter exists at routers/billing.ts:132 and is mounted at " +
			"index.ts:1390, so a reader who greps for billing finds a working, ungated " +
			"feature and concludes we built it and do not charge for it. That code bills the " +
			"customer FOR ANTIFAILURE. The licensed feature of this name would meter, rate " +
			"and charge on the CUSTOMER'S OWN behalf, and nothing in the engine or the " +
			"control plane does that; there is no billing hook in engine/pkg/extension for an " +
			"enterprise implementation to plug into either. license.go's notShipped map is " +
			"the authority and says exactly this, which is why billing cannot be sold at all. " +
			"ControlPlaneAt is deliberately EMPTY: naming billingRouter here is what caused " +
			"the mistake, because that field means where the control plane implements or " +
			"refuses THIS, and billingRouter does neither.",
	},
	{
		Feature:    license.FeatureCompliance,
		Summary:    "SOC 2 and ISO 27001 evidence gathered from the control plane's own records.",
		EnforcedAt: "compliance/command.go:Command",
		State:      StateGated,
	},
	{
		Feature: license.FeatureDashboard,
		Summary: "The console: environments, masking, egress, audit and workloads.",
		State:   StateAbsent,
		Because: "THE SAME MISTAKE AS billing, and it is worth having both written down " +
			"because seeing the pair is what teaches the question. This entry read plan wide " +
			"on the strength of orgProcedure refusing environments.view to an organization " +
			"that is not on the enterprise plan. That refusal is real, and it is OUR OWN " +
			"funnel enforcing OUR OWN plans, keyed on organizations.plan and not on this " +
			"licence name, which the previous wording of this entry already admitted without " +
			"drawing the conclusion. There is no enterprise dashboard to sell: the name " +
			"appears in this catalogue, in licensegen's copy, in the feature name lists and " +
			"in the documentation, and in no implementation anywhere, which is what " +
			"notShipped records and why it cannot be sold. The Summary maps the name onto " +
			"the console, and the console is the whole product rather than a licensed part " +
			"of it. ControlPlaneAt is EMPTY for the reason billing's is.",
	},
	{
		Feature:    license.FeatureMultiRuntime,
		Summary:    "Placing an environment across several runtimes at once, by requirement and by tag.",
		EnforcedAt: "engine/internal/env/env.go:Orchestrator.placement",
		State:      StateEditionGated,
		Because: "Refused by the COMMUNITY engine rather than by this module, which is why a " +
			"count of feature.Enabled call sites in ee/engine reports zero for it. " +
			"engine/internal/env/env.go:898 refuses a second placement target unless " +
			"edition.Permits says otherwise, and scheduler.Plan is called from env.go:924. " +
			"THIS ENTRY READ absent, on a measurement that was true when it was taken and " +
			"named its own expiry: the scheduler had no caller, no manifest could express a " +
			"placement requirement, and gating a pure function nobody calls would have been a " +
			"check that cannot fire. All three of those changed under it. The licence crosses " +
			"the module boundary as strings and the engine reads them, rather than the code " +
			"crossing as packages, and ee/engine/cmd/af/placement.go argues for that " +
			"arrangement: moving the manifest's targets, the scheduler and the runtime " +
			"constructors into this module would be a second answer to where an environment " +
			"is. The control plane's runtime registry is a different subject again, real and " +
			"refused on the hosted plan, and routers/runtimes.ts deliberately never sends a " +
			"runtime name to the engine.",
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
		ControlPlaneAt: "web/apps/api/src/permissions.ts:PERMISSIONS",
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
		ControlPlaneAt: "ee/web/scim/src/routes.ts:guard",
		State:          StateControlPlaneGated,
		Because: "Refused by the control plane rather than by the engine, which is why the " +
			"engine's own count of Enabled call sites reports zero for it. ee/web/scim " +
			"implements the protocol, including the filter grammar and PATCH, its guard asks " +
			"the licence at routes.ts:198, and ee/web/server/src/register.ts mounts it, so an " +
			"unlicensed installation is answered 402 naming scim rather than 404. " +
			"THIS ENTRY READ absent AND THEN unmounted, and both were measured by asking " +
			"whether anything under web/apps/api/src reads scim_tokens. That is true and it " +
			"is a statement about the hosted tree, not about the product: the enterprise " +
			"control plane is a different entry point. The token half of the finding still " +
			"stands, and admin/platform.ts records it, so the operator portal leaves those " +
			"tokens off the credential list rather than showing a row nothing can revoke.",
	},
	{
		Feature:        license.FeatureSSO,
		Summary:        "Single sign on against the organization's own identity provider.",
		ControlPlaneAt: "ee/web/sso/src/store.ts:connectionByHandle",
		State:          StateControlPlaneGated,
		Because: "Refused by the control plane rather than by the engine, which is why the " +
			"engine's own count of Enabled call sites reports zero for it. ee/web/sso is a " +
			"complete implementation, SAML and OIDC, domain binding, assertion replay " +
			"protection and break glass codes; it asks the licence in three places, " +
			"enforce.ts:167 and store.ts:133 and :202; and ee/web/server/src/register.ts " +
			"mounts it, so an unlicensed installation is answered 402 naming sso. " +
			"THIS ENTRY READ absent AND THEN unmounted, and the second was written the day " +
			"before the entry point it said did not exist was already on main. Both readings " +
			"came from asking what web/apps/api/src imports, which is the hosted tree and not " +
			"the only one: the enterprise image runs ee/web/server. The schema half of the " +
			"older finding still stands, since writers.test.ts records sso_connections under " +
			"UNWIRED, and that is a gap in the HOSTED plane rather than in this feature.",
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
		ControlPlaneAt: "web/apps/api/src/admin/customers.ts:registerImpersonationRoutes",
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

// ControlPlaneGatedFeatures is every feature the CONTROL PLANE refuses by name.
//
// A second list rather than more entries in the first, because the two are
// confirmed by different suites and a caller that merges them silently loses
// which instrument is standing behind each name. GatedFeatures is what the
// engine's own entry point test exercises twice, once with the entitlement and
// once without; these cannot be exercised from Go at all, and the TypeScript
// catalogue test is what holds them to the registry.
func ControlPlaneGatedFeatures() []license.Feature {
	out := []license.Feature{}
	for _, e := range catalogue {
		if e.State == StateControlPlaneGated {
			out = append(out, e.Feature)
		}
	}
	return out
}

// EditionGatedFeatures is every feature the COMMUNITY engine refuses by name.
//
// A third list for the reason there is a second: the three are confirmed by
// three different instruments, and a caller that merged them would lose which
// one is standing behind each name.
func EditionGatedFeatures() []license.Feature {
	out := []license.Feature{}
	for _, e := range catalogue {
		if e.State == StateEditionGated {
			out = append(out, e.Feature)
		}
	}
	return out
}

// GatedFeatures is every feature a missing entitlement actually refuses.
//
// The number this lane is measured by is len(GatedFeatures()) over
// len(license.AllFeatures()), and it is deliberately the smaller of the two
// numbers that could be reported. The larger one would count a refusal made on
// the hosted plan as a whole, which covers a customer who bought the feature
// and one who did not identically and therefore says nothing about the licence.
func GatedFeatures() []license.Feature {
	out := []license.Feature{}
	for _, e := range catalogue {
		if e.State == StateGated {
			out = append(out, e.Feature)
		}
	}
	return out
}
