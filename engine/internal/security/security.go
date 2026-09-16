// Package security is the contract every dynamic security check family plugs
// into, and nothing more.
//
// The security suite fans out into families (authz, injection, canary_leak,
// side_effect, db_security, headers, supply_chain and more) that each run
// against the sanitized twin at exactly the targets a diff touched. This
// package is the seam they share: one Family interface, one Registry, and the
// policy-key and exit-code contract that keeps a security finding an ordinary
// report.Finding in a new namespace. It carries NO family logic. The registry
// ships empty and wired: with nothing registered, no security check is added
// to any plan and no security finding is produced, which is exactly the state
// the spine leaves the product in until a family lands.
//
// It imports nothing under ee. Edition gating is a probe-time decision a family
// makes by calling engine/pkg/edition.Permits at its own call site; filtering a
// family out of the registry would make an unlicensed family and an unrun one
// indistinguishable, and a family that vanished from the plan would report
// success having checked nothing. So the registry stays complete in both
// editions and the boundary is enforced where the family runs, never here.
package security

import (
	"context"
	"fmt"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
)

// Family is one dynamic security check family. authz, injection, canary_leak,
// side_effect, db_security, headers, supply_chain and the rest each implement
// exactly this and nothing else, so the spine can route, gate, report and
// license every one of them the same way.
type Family interface {
	// Name is the stable family id AND the namespace of every policy key and
	// finding rule it emits: the "authz" family owns keys "security.authz.*"
	// and emits findings whose Rule is "security.authz.*". It is what a reader
	// greps for and what the manifest configures. A duplicate name panics at
	// registration, the same discipline the MCP server holds tool names to.
	Name() string

	// Surfaces are the change surfaces this family consumes. The router runs
	// the family only when the diff selected one of these. Empty is illegal and
	// panics at registration: a family that consumes no surface is a family
	// that never runs, which is dead code wearing a working feature's clothes.
	Surfaces() []change.Surface

	// Checks are the change.Check ids this family contributes to the plan and
	// the report. Registering the family is what makes these routable; an
	// unregistered family names no runnable check, which is why the reserved
	// check constants in engine/internal/change are absent from change.Checks.
	Checks() []change.Check

	// Keys declares every policy key this family reads, with its default level,
	// a title and a docs slug. The manifest validator learns the legal keys
	// from here, so a key read but never declared can be made to fail the build
	// and a key declared but never read is visible as dead config. This is the
	// anti-drift seam between the families and the manifest.
	Keys() []KeySpec

	// Licensed returns the edition feature name that gates this family, or the
	// empty string for a family every edition gets. The registry stays complete
	// in both editions and consults nothing; the spine calls edition.Permits
	// with this feature at the Probe call site and, when it is refused, records
	// a blocked outcome that names the feature rather than dropping the family
	// from the plan. Absent and refused must never look the same.
	Licensed() string

	// Probe runs the family against the live sanitized twin, at the routed
	// targets only, and returns findings. CONTRACT: a returned Finding never
	// carries a response body, a database row, a log line or a screenshot in
	// any field. Detail is bounded and neutralized; Where is a location (a
	// route, a table, a header name), never a value. Level MUST come from
	// in.Policy.Level(key) and is never hard-coded, so the manifest stays the
	// one place a finding's severity is decided. An error is a blocked probe, a
	// fact about our tooling, never a security verdict about the change.
	Probe(ctx context.Context, in Input) ([]report.Finding, error)
}

// KeySpec is one policy key a family owns.
type KeySpec struct {
	// Key is the manifest key and the finding Rule at once, "security.<family>.<rule>".
	Key report.PolicyKey
	// Default is the level a finding on this key carries when the manifest does
	// not override it: report.LevelFail, LevelWarn or LevelIgnore.
	Default report.Level
	// Title is a one line description, for the generated policy documentation.
	Title string
	// Docs is the slug under the documentation site that explains the key.
	Docs string
	// Exit is the process exit code when a finding on this key is the worst
	// fail: report.ExitVerification for a proven vulnerability the family
	// exercised, or report.ExitPolicyDenial for a change refused on policy or
	// configuration grounds without a runtime exploit. It MUST equal
	// ExitFor(Key); a test in the family's own package proves it, so the
	// declared exit and the exit the gate produces cannot drift apart.
	Exit int
}

// Input is everything a family needs and nothing it must not have.
type Input struct {
	// Targets are the routed units this family was selected for, and the only
	// units it may exercise. A family that reaches past its targets is running
	// across the whole application, which is the cost the router exists to
	// avoid.
	Targets []change.Target
	// Env is the live sanitized twin: the base URL a family drives, and the
	// driver a later family adds beside it. It is never a real database.
	Env Environment
	// Golden is a read-only view of the golden's planted facts, so a leak
	// family can recognise a token in a response. The token values live in the
	// golden, a copy of production, and never in a finding.
	Golden GoldenView
	// Policy is the resolved levels for this family's keys. A family reads a
	// level with Policy.Level(key) and never decides one itself.
	Policy report.Policy
	// Clock is the time source, so a family that measures a duration or stamps
	// an observation stays testable and deterministic.
	Clock clock.Clock

	// The per-run reader artifacts a reader family consults, populated by the
	// security router after the run captured them and before Probe. They are
	// unexported and reached through the accessors in input.go so that an
	// absent artifact and an empty one are told apart: Baseline reports
	// ok=false when no base twin was measured, and Routes returns nil when no
	// observed route was sourced, neither of which a reader may read as "zero".
	// An Input built by an older constructor, as every existing one is, leaves
	// these zero and the accessors report absent, which is why the enrichment
	// is additive and the merged contract keeps compiling.
	//
	// These are the CANDIDATE run's artifacts, flat. The base twin's artifacts,
	// when one was built, arrive together in baseline, so a reader diffs a
	// candidate against a base through Baseline() and never confuses the two.
	decisions       []local.Decision
	messages        []local.Message
	observations    []RawObservation
	routes          []Route
	evidence        Evidence
	baseline        *Baseline
	dependencyFiles []DependencyFile
}

// Environment is the sanitized twin a family drives.
//
// Minimal on purpose: the spine gives a family the address of the twin and the
// families add the driver they need beside it. It is never a handle to a real
// database, and there is no field here that could become one.
type Environment struct {
	// BaseURL is where the twin answers, for a family that drives it over HTTP.
	BaseURL string
}

// GoldenView is a read-only view of the tokens planted in the golden.
//
// A leak family plants a known fake value per tenant in the golden and then
// watches for it in a response it should never appear in. The value is read
// here, against the twin, and is what the family matches on; it never reaches a
// finding, which carries only the location. Empty until a family plants tokens,
// which is the spine's state.
type GoldenView struct {
	canaries []Canary
}

// Canary is one planted token: the tenant it belongs to, a category, and the
// value a family matches on. The value stays inside the engine, against the
// twin; a finding that recognises it reports the location and never the value.
type Canary struct {
	Tenant string
	Kind   string
	Value  string
}

// NewGoldenView builds a view over a set of planted tokens.
func NewGoldenView(canaries []Canary) GoldenView {
	return GoldenView{canaries: append([]Canary(nil), canaries...)}
}

// Canaries returns the planted tokens, so a family can match a response against
// them. The caller must not mutate the result.
func (g GoldenView) Canaries() []Canary { return g.canaries }

// Registry is the set of registered families, in registration order.
//
// It mirrors the MCP server's panic-on-duplicate discipline: two families
// answering to one name is a programming error, not a runtime condition, so it
// stops the process at startup rather than serving two meanings for one
// namespace.
type Registry struct {
	byName map[string]Family
	order  []string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byName: map[string]Family{}}
}

// Register publishes a family. A duplicate name panics, and so does a family
// that declares no surface, because a family that consumes no surface never
// runs and would sit in the plan looking like a working check.
func (r *Registry) Register(f Family) {
	name := f.Name()
	if name == "" {
		panic("security: a family registered with an empty name")
	}
	if _, exists := r.byName[name]; exists {
		panic(fmt.Sprintf("security: the family %q is registered twice", name))
	}
	if len(f.Surfaces()) == 0 {
		panic(fmt.Sprintf("security: the family %q declares no surface, so it would never run", name))
	}
	r.byName[name] = f
	r.order = append(r.order, name)
}

// Families returns every registered family, in registration order.
func (r *Registry) Families() []Family {
	out := make([]Family, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.byName[name])
	}
	return out
}

// ForSurface returns the families that consume a surface, in registration
// order. It is how the router turns a touched surface into the families that
// run against it, so the surface-to-family routing is derived from the registry
// rather than written a second time in the change package.
func (r *Registry) ForSurface(s change.Surface) []Family {
	var out []Family
	for _, name := range r.order {
		for _, surface := range r.byName[name].Surfaces() {
			if surface == s {
				out = append(out, r.byName[name])
				break
			}
		}
	}
	return out
}

// Keys returns every policy key every registered family owns, in registration
// order, for the manifest validator and the generated documentation. Empty
// while the registry is empty.
func (r *Registry) Keys() []KeySpec {
	var out []KeySpec
	for _, name := range r.order {
		out = append(out, r.byName[name].Keys()...)
	}
	return out
}

// Default wires the built-in families once and returns them as a registry.
//
// Empty today, and that is the whole of the spine's promise: the contract is
// in place and nothing is registered against it, so the product plans and
// reports exactly as it did before a security family existed. Each family lands
// by adding one Register call here, in its own pull request, from new main.
func Default() *Registry {
	r := NewRegistry()
	// Families register here, one line each, as they land. None yet.
	return r
}

// ExitFor is the process exit code a finding on a security key produces when it
// is the worst fail: report.ExitVerification for a family that PROVED the
// running application is insecure by exercising it, or report.ExitPolicyDenial
// for a family that REFUSED a change on policy or configuration grounds without
// exercising a runtime vulnerability.
//
// This is the canonical mapping the whole namespace agrees on. A family's
// KeySpec.Exit must equal ExitFor(that key), which a test in the family's own
// package proves, so the exit a family declares and the exit the gate produces
// are the same number by construction rather than by coincidence. The gate
// reads it here rather than hard-coding a switch, so a new key inherits the
// right exit from its shape the moment it is named.
//
// The deny-it set is small and explicit: the headers and supply_chain families
// refuse a configuration or a dependency rather than exercising a hole, and two
// otherwise prove-it families carry one deny-it key each. Everything else is a
// verification failure, which is the safe default for a security namespace: a
// finding whose family is not yet recognised is treated as having proven
// something, not as a mere policy note.
func ExitFor(rule report.PolicyKey) int {
	key := string(rule)
	if !strings.HasPrefix(key, prefix) {
		return report.ExitVerification
	}
	family, _, _ := strings.Cut(strings.TrimPrefix(key, prefix), ".")
	switch {
	case family == "headers", family == "supply_chain",
		key == "security.side_effect.external_call",
		key == "security.db_security.broad_grant":
		return report.ExitPolicyDenial
	default:
		return report.ExitVerification
	}
}

// prefix is the one namespace every security key and finding rule lives under.
// It is what the gate keys its single security case on, and what ExitFor and
// the MCP projection recognise a security finding by.
const prefix = "security."

// Prefix returns the namespace every security policy key and finding rule
// shares, so a caller outside this package recognises a security finding
// without spelling the string a second time.
func Prefix() string { return prefix }

// FamilyOf returns the family name a security rule belongs to, or the empty
// string when the rule is not in the security namespace. "security.authz.idor"
// belongs to "authz". It is the projection the MCP read tool groups by and the
// gate names a finding with.
func FamilyOf(rule string) string {
	if !strings.HasPrefix(rule, prefix) {
		return ""
	}
	family, _, _ := strings.Cut(strings.TrimPrefix(rule, prefix), ".")
	return family
}
