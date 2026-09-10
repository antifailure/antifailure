// Package policyenforce turns organization policy into a refusal.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// The community edition already refuses to publish a golden that fails
// verification and already refuses egress to a host the manifest does not name.
// What a security team asks for on top is a guarantee rather than a default: a
// repository must not be able to opt out, and a policy set once must hold
// across every repository in the organization including the ones added next
// year by somebody who never read the policy.
//
// So this is deliberately one-directional. Every rule here can only make an
// environment refuse to start. There is no rule that permits something the
// manifest does not, and the extension point it plugs into has no return value
// that could express one. That is not a limitation to be worked around later:
// a control plane setting that could widen an egress rule would be a way to
// change what a preview environment can reach without changing the repository
// and without review, which is the exact thing the product exists to prevent.
//
// The conflict rule is that the stricter wins, and it is a property test rather
// than a paragraph: adding a policy never permits more than the set without it.
package policyenforce

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/ee/engine/feature"
	"github.com/antifailure/antifailure/ee/engine/license"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

func init() {
	// Recorded so that a feature which is sold and never checked shows up as
	// such. See ee/engine/feature.
	feature.Declare(license.FeaturePolicy, "policyenforce/policyenforce.go:Hook.Check")
}

// Policy is what an organization requires of every repository.
//
// Every field is a restriction. There is no field that grants anything, which
// is what makes "the stricter wins" true by construction rather than by care.
//
// The YAML tags are how an administrator writes one down; see configure.go for
// where the document is read from and why an unreadable one stops the process.
type Policy struct {
	// RequiredMaskedColumns are patterns that must be covered by a masking
	// rule. A pattern is table.column with * allowed in either part, so
	// "*.email" covers every email column in the database.
	RequiredMaskedColumns []string `yaml:"required_masked_columns"`
	// DeniedHosts are hosts no repository may reach in any mode other than
	// block, whatever its manifest says.
	DeniedHosts []string `yaml:"denied_hosts"`
	// AllowedModes limits which egress modes may be used at all. Empty means
	// every mode. Naming any mode excludes the rest.
	AllowedModes []string `yaml:"allowed_modes"`
	// SynthRequiresApproval refuses synth mode outright unless the environment
	// carries an approval. Synth invents responses from a model, so an
	// environment using it proves less than it appears to.
	SynthRequiresApproval bool `yaml:"synth_requires_approval"`
	// AllowedProviders limits which database providers may be used, for
	// residency. Empty means any.
	AllowedProviders []string `yaml:"allowed_providers"`
	// AllowedRegions limits where an environment may run. Empty means anywhere.
	AllowedRegions []string `yaml:"allowed_regions"`
	// MaxLifetimeHours bounds how long an environment may exist. Zero means no
	// bound. Enforced by the reaper rather than at creation, and carried here
	// so that one policy object describes the whole rule.
	//
	// There is no reaper yet, which is why FromEnvironment refuses a document
	// that sets this rather than accepting it. A field that reads as a bound
	// and enforces nothing is worse than an error.
	MaxLifetimeHours int `yaml:"max_lifetime_hours"`
}

// Approval is what a run carries when a human has agreed to something.
type Approval struct {
	// Synth permits synth mode for this environment.
	Synth bool
	// By names the approver, for the refusal message when it is missing.
	By string
}

// Hook enforces a policy at environment creation.
type Hook struct {
	policy Policy
	// approval is looked up per environment, so that an approved run and an
	// unapproved one of the same repository decide differently.
	approval func(envID string) Approval
}

// NewHook builds an enforcement hook.
//
// The approval lookup may be nil, which means nothing is ever approved. That is
// the safe direction: a control plane that cannot be reached must not be able
// to approve anything by being absent.
func NewHook(p Policy, approval func(envID string) Approval) *Hook {
	if approval == nil {
		approval = func(string) Approval { return Approval{} }
	}
	return &Hook{policy: p, approval: approval}
}

// Name identifies the hook in a refusal.
func (h *Hook) Name() string { return "organization-policy" }

// Refusal is a policy violation, with the code the catalog documents.
type Refusal struct {
	// Policy names which rule refused, so somebody knows what to ask for.
	Policy string
	// Detail is what specifically violated it.
	Detail string
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("AF-EE-010: organization policy %s refuses this environment: %s",
		r.Policy, r.Detail)
}

// Check refuses an environment that violates the policy.
//
// Rules are evaluated in a fixed order and the first violation is returned, so
// that a repository violating three policies is told about one of them and the
// same one every time. Reporting all three would read as a wall and would still
// have to be fixed one at a time.
func (h *Hook) Check(ctx context.Context, req extension.EnvironmentRequest) error {
	// The licence gate is here rather than at registration, because a licence
	// can expire while the process is running. A hook registered under a valid
	// licence must stop enforcing when that licence lapses, or an expired
	// customer keeps a feature they are no longer paying for and, worse, cannot
	// turn it off without a restart.
	if !feature.Enabled(ctx, license.FeaturePolicy) {
		return nil
	}

	if violation := h.checkEgress(req); violation != nil {
		return violation
	}
	if violation := h.checkSynth(req); violation != nil {
		return violation
	}
	if violation := h.checkPlacement(req); violation != nil {
		return violation
	}
	return nil
}

func (h *Hook) checkEgress(req extension.EnvironmentRequest) error {
	denied := make(map[string]bool, len(h.policy.DeniedHosts))
	for _, host := range h.policy.DeniedHosts {
		denied[strings.ToLower(strings.TrimSpace(host))] = true
	}

	// Sorted, so that a repository violating the policy for two hosts is told
	// about the same one every time rather than whichever the map reached first.
	hosts := append([]string(nil), req.EgressHosts...)
	sort.Strings(hosts)

	for _, host := range hosts {
		normalized := strings.ToLower(strings.TrimSpace(host))
		mode := strings.ToLower(req.EgressModes[host])

		// A denied host may still appear in a manifest, as long as it is
		// blocked. Refusing the rule outright would mean a repository cannot
		// document that it deliberately blocks something, which is exactly the
		// thing a policy wants to encourage.
		if mode != "block" && matchesAny(normalized, denied) {
			return &Refusal{
				Policy: "egress deny list",
				Detail: fmt.Sprintf(
					"%s is on the organization's deny list and this manifest names it in %s mode. "+
						"Change the rule to block, or ask an administrator to remove it from the list.",
					host, mode),
			}
		}

		if len(h.policy.AllowedModes) > 0 && mode != "" && !contains(h.policy.AllowedModes, mode) {
			return &Refusal{
				Policy: "allowed egress modes",
				Detail: fmt.Sprintf(
					"the rule for %s uses %s mode, and this organization permits only %s",
					host, mode, strings.Join(h.policy.AllowedModes, ", ")),
			}
		}
	}
	return nil
}

func (h *Hook) checkSynth(req extension.EnvironmentRequest) error {
	if !h.policy.SynthRequiresApproval {
		return nil
	}
	hosts := make([]string, 0, len(req.EgressModes))
	for host, mode := range req.EgressModes {
		if strings.EqualFold(mode, "synth") {
			hosts = append(hosts, host)
		}
	}
	if len(hosts) == 0 {
		return nil
	}
	sort.Strings(hosts)

	if h.approval(req.EnvID).Synth {
		return nil
	}
	return &Refusal{
		Policy: "synth requires approval",
		Detail: fmt.Sprintf(
			"the rules for %s use synth mode, which invents responses from a model. "+
				"A workflow that touches one reports unverified rather than passed, so this "+
				"organization requires an approver before an environment may use it.",
			strings.Join(hosts, ", ")),
	}
}

// CheckMasking refuses a masking plan that leaves a required column readable.
//
// A separate entry point from Check, and the separation is the point rather
// than a tidying. Check is asked before an environment is created, from a
// manifest, and a manifest names no columns: the required_masked_columns rule
// used to be evaluated there against a field the engine never filled, so it
// read every repository's plan as masking nothing, and the only reason it did
// not refuse everything is that a policy naming no columns returns early.
//
// This is asked during a golden refresh, from a catalogue the engine has read
// and a plan assigned against it, before the first row is rewritten. A refusal
// here means the golden is never published and never branched.
func (h *Hook) CheckMasking(ctx context.Context, req extension.MaskingRequest) error {
	// The same licence gate as Check, for the same reason: a licence that
	// lapses mid-process must stop the enforcement it paid for, and a second
	// entry point that skipped the check would be a way to keep the feature
	// after the licence went.
	if !feature.Enabled(ctx, license.FeaturePolicy) {
		return nil
	}
	if len(h.policy.RequiredMaskedColumns) == 0 {
		return nil
	}

	masked := normalizeColumns(req.MaskedColumns)
	catalog := normalizeColumns(req.CatalogColumns)

	for _, pattern := range h.policy.RequiredMaskedColumns {
		normalized := strings.ToLower(strings.TrimSpace(pattern))
		matches := matching(normalized, catalog)
		if len(matches) == 0 {
			return &Refusal{
				Policy: "required masking",
				Detail: fmt.Sprintf(
					"this organization requires %s to be masked and this database has no column it names. "+
						"A policy that quietly passes when the thing it protects is absent stops protecting "+
						"the moment somebody renames a table, so this is refused rather than skipped. "+
						"Ask an administrator to narrow the rule, or add the column back.",
					pattern),
			}
		}
		unmasked := notIn(matches, masked)
		if len(unmasked) == 0 {
			continue
		}
		return &Refusal{
			Policy: "required masking",
			Detail: fmt.Sprintf(
				"this organization requires %s to be masked and no rule in this repository covers %s. "+
					"Add a masking rule and commit it, then refresh the golden.",
				pattern, strings.Join(unmasked, ", ")),
		}
	}
	return nil
}

// normalizeColumns lowercases and trims a column list, dropping empties.
//
// Sorted on the way out, so that a plan violating the policy for two columns
// names them in the same order every time rather than in whichever order the
// catalogue was read.
func normalizeColumns(columns []string) []string {
	out := make([]string, 0, len(columns))
	for _, column := range columns {
		normalized := strings.ToLower(strings.TrimSpace(column))
		if normalized == "" {
			continue
		}
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out
}

// matching returns every column a required pattern names.
//
// A pattern is matched against the schema qualified name and against the name
// without its schema, which is the same rule masking.Rule already uses for its
// table patterns. An administrator writes users.email and means the users
// table wherever it lives; one who writes public.users.email means that one.
// Accepting only the long form would refuse every policy anybody has written,
// and accepting only the short form would make a policy unable to distinguish
// two schemas.
func matching(pattern string, catalog []string) []string {
	var out []string
	for _, column := range catalog {
		if matchesColumn(pattern, column) {
			out = append(out, column)
		}
	}
	return out
}

func matchesColumn(pattern, column string) bool {
	if pattern == column {
		return true
	}
	if ok, err := path.Match(pattern, column); err == nil && ok {
		return true
	}
	// The unqualified form. Cutting at the FIRST dot is what strips the
	// schema: a column is schema.table.column, so what follows the first dot
	// is table.column. Cutting at the last would leave the column name alone
	// and make every pattern with a table in it fail.
	if _, rest, found := strings.Cut(column, "."); found {
		if pattern == rest {
			return true
		}
		if ok, err := path.Match(pattern, rest); err == nil && ok {
			return true
		}
	}
	return false
}

// notIn returns the columns of want that are absent from have.
func notIn(want, have []string) []string {
	present := make(map[string]bool, len(have))
	for _, column := range have {
		present[column] = true
	}
	var out []string
	for _, column := range want {
		if !present[column] {
			out = append(out, column)
		}
	}
	return out
}

func (h *Hook) checkPlacement(req extension.EnvironmentRequest) error {
	if len(h.policy.AllowedProviders) > 0 && req.Provider != "" &&
		!contains(h.policy.AllowedProviders, strings.ToLower(req.Provider)) {
		return &Refusal{
			Policy: "allowed database providers",
			Detail: fmt.Sprintf(
				"this repository uses the %s provider and this organization permits only %s",
				req.Provider, strings.Join(h.policy.AllowedProviders, ", ")),
		}
	}
	if len(h.policy.AllowedRegions) > 0 && req.Region != "" &&
		!contains(h.policy.AllowedRegions, strings.ToLower(req.Region)) {
		return &Refusal{
			Policy: "data residency",
			Detail: fmt.Sprintf(
				"this environment would run in %s and this organization permits only %s",
				req.Region, strings.Join(h.policy.AllowedRegions, ", ")),
		}
	}
	return nil
}

// matchesAny reports whether a host matches a deny entry, treating a wildcard
// the way the egress policy does.
//
// Both wildcard shapes the policy engine compiles are handled here, and that
// is not tidiness. A deny entry the list understands less well than the
// manifest does is a deny list that silently permits: an organization writing
// s3.*.amazonaws.com would have had it compared as a literal string, matched
// nothing, and refused nothing, while reading in the console as though it
// covered every region.
func matchesAny(host string, denied map[string]bool) bool {
	if denied[host] {
		return true
	}
	for entry := range denied {
		switch {
		case strings.HasPrefix(entry, "*."):
			suffix := entry[1:]
			if strings.HasSuffix(host, suffix) && len(host) > len(suffix) {
				return true
			}
		case strings.Contains(entry, "*"):
			star := strings.IndexByte(entry, '*')
			head, tail := entry[:star], entry[star+1:]
			if !strings.HasSuffix(head, ".") || !strings.HasPrefix(tail, ".") {
				continue
			}
			if !strings.HasPrefix(host, head) || !strings.HasSuffix(host, tail) {
				continue
			}
			middle := len(host) - len(head) - len(tail)
			if middle < 1 {
				continue
			}
			if !strings.Contains(host[len(head):len(head)+middle], ".") {
				return true
			}
		}
	}
	return false
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), want) {
			return true
		}
	}
	return false
}

// Stricter reports whether b permits nothing that a does not.
//
// Used by the property test that adding a rule never widens the policy, and by
// the explain view, which shows which scope's rule won and why.
func Stricter(a, b Policy) bool {
	return len(b.RequiredMaskedColumns) >= len(a.RequiredMaskedColumns) &&
		len(b.DeniedHosts) >= len(a.DeniedHosts)
}
