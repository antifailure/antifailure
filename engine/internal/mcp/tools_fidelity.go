package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/fidelity"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// observeFidelity takes the inventory of what the environment reproduces.
//
// It returns the inventory and whether it could be taken at all. The second
// return carries the same weight it does for the decision log: an inventory
// nobody could take and an inventory in which nothing was reproduced look
// identical once they are reduced to a number, and reporting the first as the
// second would put a zero on a measurement that never happened.
type observeFidelity func(ctx context.Context) (inv fidelity.Inventory, available bool, err error)

// maxComponentsPerDimension bounds one dimension's component list.
//
// A dimension grows with the project rather than with the run: a monorepo
// declares dozens of services and a policy names dozens of hosts. Forty is
// past the point where a reader is still reading and far short of a context
// this result could crowd out.
const maxComponentsPerDimension = 40

// newFidelityTool builds assess_environment_fidelity.
//
// Synchronous and read only. Every line of the inventory comes from something
// the engine already knew, so there is nothing to run and nothing to poll.
func newFidelityTool(p *Project, observe observeFidelity) *Tool {
	return &Tool{
		Name:     "assess_environment_fidelity",
		Title:    "How close the environment is to production",
		ReadOnly: true,
		Description: "Answer how much of this environment is production's own thing and how " +
			"much is a stand in, component by component. Call this before trusting any " +
			"other result about this environment, because a verdict is only worth what " +
			"the copy it was measured on reproduces. It reports six dimensions " +
			"separately, which is the part to read: a change to billing depends on the " +
			"third party hosts and not on traffic, a migration depends on the database " +
			"and on neither, and one averaged number hides whichever of those is yours. " +
			"Anything that could not be measured is named and excluded from the score " +
			"rather than counted as either a pass or a failure. The verdict comes from " +
			"the manifest's fidelity.require, and a project that requires nothing cannot " +
			"fail here, which the summary says outright so a PASS is not read as a clean " +
			"bill of health. This measures the environment, not a change; to find out " +
			"which checks a diff needs, use plan_checks_for_change.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id"},
			Properties: map[string]*Schema{
				"project_id": projectIDSchema(),
				"dimension": {
					Type: "string", MaxLength: 32,
					Enum: dimensionNames(),
					Description: "Optional. Report one dimension instead of all six, when you " +
						"already know which part of the copy your change depends on. " +
						"services is the declared processes against the containers that are " +
						"running; database is which golden the branch came from and whether " +
						"its attestation still checks out; third_party is what answers for " +
						"each host the policy names; auth is whether each declared persona " +
						"exists in the branch; runtime is where the environment runs; " +
						"traffic is where the endpoint mix came from. The score and the " +
						"requirements are always reported over all six, because narrowing " +
						"the reading must not narrow the measurement.",
				},
			},
		},
		Handler: func(ctx context.Context, _ *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			return assessFidelity(ctx, p, observe, args)
		},
	}
}

// dimensionNames is the enum, taken from the schema rather than written out.
//
// Written out, a dimension added to the engine would be measurable and
// unnameable, and the enum would silently refuse the one value a caller read
// in the result it was just handed.
func dimensionNames() []string {
	all := schema.AllFidelityDimensions()
	out := make([]string, 0, len(all))
	for _, d := range all {
		out = append(out, string(d))
	}
	return out
}

// fidelityResult is what assess_environment_fidelity returns.
type fidelityResult struct {
	Kind    string  `json:"kind"`
	Verdict Verdict `json:"verdict"`
	Summary string  `json:"summary"`
	// Measured says whether the inventory could be taken at all. When it is
	// false there is no score and no dimension, rather than a score of zero.
	Measured    bool   `json:"measured"`
	Unavailable string `json:"unavailable,omitempty"`
	EnvID       string `json:"env_id,omitempty"`
	// Score is the headline, with its own definition beside it.
	Score        *fidelityScoreDoc `json:"score,omitempty"`
	Dimensions   []dimensionDoc    `json:"dimensions,omitempty"`
	Requirements []requirementDoc  `json:"requirements"`
	Excluded     []exclusionDoc    `json:"not_measured,omitempty"`
	Metrics      []Metric          `json:"metrics,omitempty"`
	EvidenceNote string            `json:"evidence_note,omitempty"`
}

// fidelityScoreDoc carries the definition with the number, every time.
//
// A percentage with no definition is the shape of an invented statistic even
// when it is not one, so the count that produced it travels beside it and the
// sentence that defines it is a field rather than a comment.
type fidelityScoreDoc struct {
	Reproduced int `json:"reproduced"`
	Counted    int `json:"counted"`
	// Percent is absent rather than zero when nothing could be counted.
	Percent      *int   `json:"percent,omitempty"`
	ExcludedFrom int    `json:"excluded_from_the_score"`
	Definition   string `json:"definition"`
}

type dimensionDoc struct {
	Name    string `json:"name"`
	Verdict string `json:"verdict"`
	// NotApplicable says why a dimension had nothing to measure. A dimension
	// the manifest never asked for is excluded from the score rather than
	// counted as fully reproduced.
	NotApplicable  string         `json:"not_applicable,omitempty"`
	Components     []componentDoc `json:"components"`
	ComponentsSeen int            `json:"components_total"`
	Shown          int            `json:"components_shown"`
	Truncated      bool           `json:"components_truncated"`
	Note           string         `json:"note,omitempty"`
}

type componentDoc struct {
	Name  string `json:"name"`
	State string `json:"state"`
	// Detail is what was found, in the words of whatever knew it, bounded.
	Detail string `json:"detail,omitempty"`
}

type requirementDoc struct {
	Dimension string `json:"dimension"`
	Met       bool   `json:"met"`
	// Measurable is false when the requirement could not be evaluated. Such a
	// requirement is neither met nor broken and must not be reported as
	// either.
	Measurable bool   `json:"measurable"`
	Because    string `json:"because,omitempty"`
}

type exclusionDoc struct {
	Dimension string `json:"dimension"`
	Component string `json:"component,omitempty"`
	Because   string `json:"because"`
}

func assessFidelity(
	ctx context.Context, p *Project, observe observeFidelity, args map[string]any,
) (any, *Fault) {
	out := fidelityResult{Kind: "fidelity_inventory", Requirements: []requirementDoc{}}

	// A manifest that turns the inventory off is not an environment that
	// reproduced nothing. It is an environment nobody looked at, which is the
	// one thing this result must never render as a score.
	if f := p.Manifest.Fidelity; f != nil && f.Enabled != nil && !*f.Enabled {
		out.Verdict = VerdictInconclusive
		out.Summary = "The inventory is turned off by fidelity.enabled in this project's " +
			"manifest, so nothing was measured. That is not the same as everything " +
			"having been reproduced."
		return out, nil
	}

	inv, available, obsErr := observe(ctx)
	out.Measured = available
	if !available {
		// Fail closed, for the same reason inspect_egress_firewall does. The
		// question is what this environment reproduces and nothing can answer
		// it right now.
		out.Verdict = VerdictInconclusive
		out.Unavailable = fidelityUnavailable(obsErr)
		out.Summary = "The inventory could not be taken, so this says nothing about what " +
			"the environment reproduces. " + out.Unavailable
		return out, nil
	}

	out.EnvID, _ = safeIdentifier(inv.EnvID)
	score := inv.Score()
	doc := fidelityScoreDoc{
		Reproduced: score.Reproduced, Counted: score.Counted,
		ExcludedFrom: len(score.Excluded),
		Definition: "Reproduced over counted, where counted is every component whose state " +
			"could be read and reproduced is the subset that is production's own thing. " +
			"A substitution, a refusal and an absence are all in the denominator and none " +
			"is in the numerator. Nothing unmeasured is in either, and every exclusion is " +
			"listed under not_measured.",
	}
	if pct, ok := score.Percent(); ok {
		doc.Percent = &pct
	}
	out.Score = &doc

	only, _ := args["dimension"].(string)
	for _, d := range inv.Dimensions {
		if only != "" && string(d.Name) != only {
			continue
		}
		out.Dimensions = append(out.Dimensions, describeDimension(d))
	}
	for _, e := range score.Excluded {
		out.Excluded = append(out.Excluded, exclusionDoc{
			Dimension: string(e.Dimension),
			Component: neutralize(e.Component, maxIdentifierBytes),
			Because:   neutralize(e.Because, 300),
		})
	}

	var require []fidelity.Requirement
	if p.Manifest.Fidelity != nil {
		require = inv.Check(p.Manifest.Fidelity.Require)
	}
	for _, r := range require {
		out.Requirements = append(out.Requirements, requirementDoc{
			Dimension: string(r.Dimension), Met: r.Met, Measurable: r.Measurable,
			Because: neutralize(r.Because, 400),
		})
	}

	out.Verdict = fidelityVerdict(require)
	out.Metrics = fidelityMetrics(score, require)
	out.Summary = fidelitySummary(inv, score, require, out.Verdict, only)
	out.EvidenceNote = "Run af fidelity for the same inventory as a table, and af fidelity " +
		"-o json for every component without the bounds this result applies."
	return out, nil
}

// fidelityVerdict applies the manifest's own requirements and nothing else.
//
// The three outcomes are kept apart deliberately. A dimension measured and
// found wanting is a fact about the environment; a dimension that could not be
// measured is a fact about what could be seen, and reporting the second as the
// first is how a report stops being believed. A project that requires nothing
// passes, and the summary says why so that the pass is not read as a finding.
func fidelityVerdict(require []fidelity.Requirement) Verdict {
	for _, r := range require {
		if !r.Met && !r.Measurable {
			return VerdictInconclusive
		}
	}
	for _, r := range require {
		if !r.Met {
			return VerdictFail
		}
	}
	return VerdictPass
}

func fidelityMetrics(score fidelity.Score, require []fidelity.Requirement) []Metric {
	out := []Metric{
		{Name: "components_reproduced", Value: float64(score.Reproduced), Unit: "components"},
		{Name: "components_counted", Value: float64(score.Counted), Unit: "components"},
		{Name: "components_not_measured", Value: float64(len(score.Excluded)), Unit: "components"},
	}
	if pct, ok := score.Percent(); ok {
		out = append(out, Metric{
			Name: "reproduced_percent", Value: float64(pct), Unit: "percent",
		})
	}
	// The required dimensions are reported against a threshold of zero unmet,
	// which is the manifest's own rule rather than one invented here.
	var unmet float64
	for _, r := range require {
		if !r.Met {
			unmet++
		}
	}
	zero := 0.0
	out = append(out, Metric{
		Name: "required_dimensions_unmet", Value: unmet, Unit: "dimensions",
		Threshold: &zero, Breached: unmet > 0,
	})
	return out
}

func describeDimension(d fidelity.Dimension) dimensionDoc {
	doc := dimensionDoc{
		Name: string(d.Name), Verdict: string(d.Verdict()),
		NotApplicable:  neutralize(d.NotApplicable, 300),
		ComponentsSeen: len(d.Components),
	}
	shown := d.Components
	if len(shown) > maxComponentsPerDimension {
		shown = shown[:maxComponentsPerDimension]
		doc.Truncated = true
		doc.Note = fmt.Sprintf(
			"This dimension has %d components and the first %d are shown. The verdict "+
				"above and the score cover all %d; run af fidelity for the rest.",
			len(d.Components), maxComponentsPerDimension, len(d.Components))
	}
	for _, c := range shown {
		doc.Components = append(doc.Components, componentDoc{
			// A component name is a service, a host or a persona from the
			// manifest, and a persona is legitimately called "alice smith", so
			// the identifier check would withhold a name somebody has to read.
			// Neutralised and bounded instead: no control characters, no line
			// breaks, one short line with no structure in it.
			Name:  neutralize(c.Name, maxIdentifierBytes),
			State: string(c.State),
			// The detail is the runtime's own words, or the reason a component
			// could not be measured. It carries a URL for a running service,
			// so it cannot go through safeProse, which refuses a destination.
			Detail: neutralize(c.Detail, 200),
		})
	}
	doc.Shown = len(doc.Components)
	if doc.Components == nil {
		doc.Components = []componentDoc{}
	}
	return doc
}

// fidelityUnavailable turns a failure to take the inventory into one sentence,
// without letting an engine error string reach the caller.
func fidelityUnavailable(err error) string {
	if err == nil {
		return "No environment is running for this branch, so there is nothing to take an " +
			"inventory of. Bring one up with af up."
	}
	return withCause("The environment could not be asked what it reproduces.", err)
}

func fidelitySummary(
	inv fidelity.Inventory, score fidelity.Score,
	require []fidelity.Requirement, verdict Verdict, only string,
) string {
	var b strings.Builder

	if pct, ok := score.Percent(); ok {
		fmt.Fprintf(&b, "%d of %d measured components are production's own, which is %d percent. ",
			score.Reproduced, score.Counted, pct)
	} else {
		b.WriteString("Nothing in this environment could be measured, so there is no score. ")
	}
	if n := len(score.Excluded); n > 0 {
		fmt.Fprintf(&b, "%d %s excluded from that number and named under not_measured. ",
			n, plural(n, "component or dimension is", "components and dimensions are"))
	}

	// The weakest dimension, named. Somebody who reads one sentence should
	// read the one that decides whether their own change can be trusted here.
	weakest, weakestName := "", ""
	for _, d := range inv.Dimensions {
		if d.NotApplicable != "" {
			continue
		}
		v := string(d.Verdict())
		if weakest == "" || fidelityRank(v) < fidelityRank(weakest) {
			weakest, weakestName = v, string(d.Name)
		}
	}
	if weakestName != "" {
		fmt.Fprintf(&b, "The weakest dimension is %s, which is %s. ", weakestName, weakest)
	}
	if only != "" {
		fmt.Fprintf(&b, "Only the %s dimension is listed, at your request; the score and "+
			"the requirements still cover all of them. ", only)
	}

	switch {
	case len(require) == 0:
		b.WriteString("This project's manifest requires no dimension to be fully " +
			"reproduced, so nothing here could have failed. The verdict is PASS because " +
			"there was no requirement to break, not because the copy is complete.")
	case verdict == VerdictInconclusive:
		b.WriteString("A required dimension could not be measured, so the requirement is " +
			"neither met nor broken and this is INCONCLUSIVE rather than a failure.")
	case verdict == VerdictFail:
		var broken []string
		for _, r := range require {
			if !r.Met {
				broken = append(broken, string(r.Dimension))
			}
		}
		fmt.Fprintf(&b, "The manifest requires %s to be fully reproduced and %s not.",
			joinNames(requiredNames(require)), joinNames(broken))
	default:
		fmt.Fprintf(&b, "Every dimension the manifest requires is fully reproduced: %s.",
			joinNames(requiredNames(require)))
	}
	return strings.TrimSpace(b.String())
}

func requiredNames(require []fidelity.Requirement) []string {
	out := make([]string, 0, len(require))
	for _, r := range require {
		out = append(out, string(r.Dimension))
	}
	return out
}

func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return "nothing"
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// fidelityRank orders the states worst first, mirroring the fidelity package's
// own ranking so that the two cannot disagree about which dimension is weakest.
func fidelityRank(state string) int {
	switch fidelity.State(state) {
	case fidelity.Unmeasured:
		return 0
	case fidelity.Absent:
		return 1
	case fidelity.Refused:
		return 2
	case fidelity.Substituted:
		return 3
	case fidelity.Reproduced:
		return 4
	default:
		return 0
	}
}

// fidelity takes the inventory through an orchestrator built for this call.
//
// Every failure is reported as unavailable rather than as an empty inventory,
// for the reason observeFidelity's own comment gives: the two look identical
// once they are reduced to a number and they mean opposite things.
func (f *orchestratorFactory) fidelity(ctx context.Context) (fidelity.Inventory, bool, error) {
	o, err := f.build()
	if err != nil {
		return fidelity.Inventory{}, false, err
	}
	inv, err := o.Fidelity(ctx)
	if err != nil {
		return fidelity.Inventory{}, false, err
	}
	return inv, true, nil
}
