// Package dbsecurity is the security check family for database-security
// regressions in a migration: row level security disabled, a policy made
// permissive, a grant broadened to everyone, a tenant scoping column dropped,
// or a role handed an attribute that bypasses every policy.
//
// It sits on the security spine like any other family, so its policy keys, its
// documentation and its exit codes are handled the same way as every other
// family's. It is different from the live-probe families in ONE way, stated
// plainly here because the difference is easy to mistake for a gap: its
// findings are not produced by Probe. They are produced deterministically on
// the CheckMigration path, by the migration lint in engine/internal/insights
// reasoning about the migration DIFF. A migration is analysed from its text and
// the branch's captured schema, neither of which the security spine's Input
// carries (Input.Env is a base URL to the running twin, not a database handle),
// so a Probe here could see nothing to analyse. Probe is therefore an
// intentional no-op, and the capability it looks like it should provide is
// instead provided, and proven end to end, by gate.MigrationFindings.
//
// What the family object is FOR, given Probe does nothing, is its Keys. The
// registry's Keys are the one catalogue the manifest validator, the generated
// policy documentation and the policy-default overlay all read. Registering the
// family is what puts security.db_security.* into that catalogue, so a manifest
// may configure those keys, the docs list them, and the deterministic gate can
// resolve a finding's level from the SAME KeySpec default it declares here
// rather than from a second copy of the default that could drift.
//
// It imports nothing under ee. This family is available in every edition, so
// Licensed is the empty string and no edition gate is consulted.
package dbsecurity

import (
	"context"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/security"
)

// Name is the family id and the namespace of every key and finding rule it
// owns: the keys are "security.db_security.<rule>".
const Name = "db_security"

// keyPrefix is the namespace this family's policy keys and finding rules share.
// It is "security." + Name + ".", the shape security.FamilyOf reads the family
// out of, so a finding rule built from it groups under this family in the MCP
// read tool without spelling the family a second time.
var keyPrefix = security.Prefix() + Name + "."

// KeyFor is the policy key a database-security lint rule's finding carries. It
// is the single spelling of the security.db_security.<rule> shape, used both by
// this family to declare its keys and by the gate to resolve a finding's level,
// so the two cannot disagree about a rule's key.
func KeyFor(rule insights.Rule) report.PolicyKey {
	return report.PolicyKey(keyPrefix + string(rule))
}

// family is the security.Family implementation. It holds no state: the rule set
// is a constant of the insights package and the defaults are a constant here.
type family struct{}

// New returns the database-security family, for the registry to register and
// for the gate to read its key defaults from.
func New() security.Family { return family{} }

// Name identifies the family.
func (family) Name() string { return Name }

// Surfaces is the schema surface: a migration is a schema change, and the
// router runs this family when the diff touches one. It is non-empty, which the
// registry requires, so the family cannot be registered as one that never runs.
func (family) Surfaces() []change.Surface { return []change.Surface{change.SurfaceSchema} }

// Checks is the database-security check the family contributes to the plan and
// the report. The findings themselves ride the migration path; this names the
// check the report groups them under.
func (family) Checks() []change.Check { return []change.Check{change.CheckDBSecurity} }

// Licensed is empty: every edition gets database-security analysis.
func (family) Licensed() string { return "" }

// keyTitles is the one-line description of each key for the generated policy
// documentation. It is beside the defaults so a key added to one and not the
// other is visible.
var keyTitles = map[insights.Rule]string{
	insights.RuleRLSDisabled:         "A migration disabled row level security on a table.",
	insights.RuleRLSPolicyPermissive: "A migration made a row level policy admit every row.",
	insights.RuleBroadGrant:          "A migration granted a table privilege to PUBLIC, anon or authenticated.",
	insights.RuleTenantColumnRemoved: "A migration dropped the column a table is scoped by across tenants.",
	insights.RuleDBRolePrivBroadened: "A migration gave a role SUPERUSER, BYPASSRLS or CREATEROLE.",
}

// Keys declares one policy key per database-security rule. Every key defaults to
// fail: a migration that turns off row level security, opens a policy, or
// broadens a grant is a real regression, and a project that wants one of them
// as a warning can say so in its manifest. The default lives ONLY here, and the
// gate reads it back through KeySpecFor, so there is one source of the default
// rather than two that could drift.
//
// The exit each key produces is read from the spine's ExitFor rather than
// chosen here, so a family cannot declare an exit the gate would not honour.
// broad_grant is the deny-it key, refusing the change on policy grounds (exit
// 6); the rest are verification failures (exit 7). TestKeySpecExitMatchesSpine
// proves the two agree.
func (family) Keys() []security.KeySpec {
	specs := make([]security.KeySpec, 0, len(insights.SecurityRules()))
	for _, rule := range insights.SecurityRules() {
		key := KeyFor(rule)
		specs = append(specs, security.KeySpec{
			Key:     key,
			Default: report.LevelFail,
			Title:   keyTitles[rule],
			Docs:    "reference/lint-findings",
			Exit:    security.ExitFor(key),
		})
	}
	return specs
}

// KeySpecFor returns the KeySpec for a key, so the gate resolves a finding's
// level from the family's declared default when the manifest is silent. The
// gate builds the key from an insights.Rule with KeyFor and looks it up here;
// an unknown key returns false, which the gate treats as not-a-security-rule.
func KeySpecFor(key report.PolicyKey) (security.KeySpec, bool) {
	for _, spec := range (family{}).Keys() {
		if spec.Key == key {
			return spec, true
		}
	}
	return security.KeySpec{}, false
}

// Probe is an intentional no-op. See the package comment: database-security
// findings are produced deterministically on the CheckMigration path from the
// migration text and the branch's captured schema, which the security Input
// does not and should not carry. Returning no findings here is not a gap; the
// findings emerge, and are tested end to end, through gate.MigrationFindings.
// The router calls this when the schema surface is routed, and it returns
// nothing rather than duplicating what the migration path already reported.
func (family) Probe(context.Context, security.Input) ([]report.Finding, error) {
	return nil, nil
}
