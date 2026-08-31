// Package fidelity answers one question about an environment: which parts of
// production does this copy actually reproduce, and which parts does it not.
//
// The temptation here is a single percentage, and it is worth saying why this
// package refuses one as its only output. A number that is not derived from
// something measurable is an invented statistic, and a number that averages a
// certainty together with an unknown is neither. So two rules shape everything
// below.
//
// Every input is something the product already knew. Which services the
// manifest declares and which containers are running comes from the runtime.
// Which golden the branch came from, whether it was verified and whether its
// attestation still checks out comes from the database provider and from
// internal/verify. Which hosts the policy covers and in what mode comes from
// the manifest, and whether a mock pack answers for one comes from
// internal/mockpack. Nothing here estimates, and nothing here is a constant
// somebody typed because the page needed a number.
//
// What could not be measured is a result, not a gap in the output. It is
// carried as its own state, excluded from the score, and named with the reason
// it could not be measured. The same discipline internal/insights applies with
// its Missing field, for the same reason: a report that silently omits a check
// reads exactly like a check that found nothing, and a clean bill of health
// nobody earned is worse than no report.
//
// Where each part lives:
//
//   - This file: the vocabulary, the inventory, and the score.
//   - build.go: turning an observation into components, one dimension at a
//     time. It is a pure function of its input, which is what makes the
//     inventory reproducible and what lets the whole thing be tested without a
//     database or a daemon.
//   - explain.go: rendering it for somebody with thirty seconds.
package fidelity

import (
	"sort"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// State is what became of one component in the copy.
//
// Ordered from worst to best by rank below, because a dimension's verdict is
// the weakest state in it: a reader deciding whether to trust a run needs the
// one component that was not reproduced, not the average of the ones that
// were.
type State string

const (
	// Unmeasured is a component whose state could not be determined. It is
	// never counted as a pass or as a failure, and the component says why.
	Unmeasured State = "unmeasured"
	// Absent is a component the manifest asked for and the environment does
	// not have.
	Absent State = "absent"
	// Refused is a component the policy deliberately does not reproduce. A
	// host in block mode is refused rather than absent: the environment is
	// doing what it was told, and it still does not reproduce that host.
	Refused State = "refused"
	// Substituted is something that stands in and behaves, and is not the
	// thing itself. A stateful mock pack and a captured message are both
	// substitutions, and both are worth more than a refusal.
	Substituted State = "substituted"
	// Reproduced is the real thing, present and answering.
	Reproduced State = "reproduced"
)

// rank orders states from worst to best, so a dimension can report the
// weakest one it holds.
func rank(s State) int {
	switch s {
	case Unmeasured:
		return 0
	case Absent:
		return 1
	case Refused:
		return 2
	case Substituted:
		return 3
	case Reproduced:
		return 4
	default:
		return 0
	}
}

// Measured reports whether a state says anything about the environment.
//
// Only Unmeasured does not, and the distinction is the whole point: an
// unmeasured component is excluded from the score and named, rather than
// quietly counted as either answer.
func (s State) Measured() bool { return s != Unmeasured }

// Component is one named thing the environment was asked to reproduce.
type Component struct {
	Name  string `json:"name"`
	State State  `json:"state"`
	// Detail is what was found, in one line, in the words of whatever knew
	// it. For an unmeasured component it is the reason it could not be
	// measured, which is the only thing that makes an unmeasured result
	// useful.
	Detail string `json:"detail,omitempty"`
}

// Dimension is one part of the environment, with a verdict of its own.
//
// Reported separately rather than folded into one number because the single
// dimension that matters to a particular change is exactly what an average
// hides. A change to billing cares about the third party dimension and not
// about traffic; a migration cares about the database and not about either.
type Dimension struct {
	Name       schema.FidelityDimension `json:"name"`
	Components []Component              `json:"components,omitempty"`
	// NotApplicable says why this dimension has nothing to measure, and is
	// empty when it has something. A dimension the manifest never asked for
	// is excluded from the score rather than counted as fully reproduced,
	// because an environment that reproduces no traffic at all has not
	// reproduced traffic perfectly.
	NotApplicable string `json:"not_applicable,omitempty"`
}

// Verdict is the one word answer for a dimension.
//
// The weakest measured state in it, or Unmeasured when nothing in it could be
// measured. A dimension with nothing to measure has no verdict and reports
// Unmeasured with NotApplicable saying why.
func (d Dimension) Verdict() State {
	worst := Reproduced
	measured := false
	for _, c := range d.Components {
		if !c.State.Measured() {
			continue
		}
		measured = true
		if rank(c.State) < rank(worst) {
			worst = c.State
		}
	}
	if !measured {
		return Unmeasured
	}
	return worst
}

// Counts returns how many components are in each state, for a summary line.
func (d Dimension) Counts() map[State]int {
	out := map[State]int{}
	for _, c := range d.Components {
		out[c.State]++
	}
	return out
}

// Inventory is what the environment reproduces, one component at a time.
type Inventory struct {
	// EnvID is the environment this describes.
	EnvID string `json:"env_id"`
	// Dimensions are in schema.AllFidelityDimensions order, always all of
	// them, so that two runs of the same environment produce byte identical
	// output and a dimension that measured nothing is visibly present rather
	// than missing from the document.
	Dimensions []Dimension `json:"dimensions"`
}

// Exclusion is one thing the score does not count, and why.
type Exclusion struct {
	Dimension schema.FidelityDimension `json:"dimension"`
	// Component is the component excluded, and is empty when the whole
	// dimension was.
	Component string `json:"component,omitempty"`
	Because   string `json:"because"`
}

// Score is the headline, defined exactly.
//
// Reproduced over Counted, where Counted is every component whose state could
// be determined and Reproduced is the subset of those that are the real thing.
// A substitution, a refusal and an absence are all in the denominator and none
// of them is in the numerator, which is what makes the number mean "how much of
// this is production" rather than "how much of this went to plan".
//
// Nothing unmeasured is in either. Every exclusion is listed in Excluded with
// the reason, and a caller that renders the number without the exclusions has
// published a figure it cannot defend.
type Score struct {
	Reproduced int         `json:"reproduced"`
	Counted    int         `json:"counted"`
	Excluded   []Exclusion `json:"excluded,omitempty"`
}

// Percent is the score as a whole number, and false when nothing was counted.
//
// Integer arithmetic with a half up round, so the same inventory produces the
// same percentage on every machine. A float here would be reproducible in
// practice and the point of this package is that its output is reproducible by
// construction.
//
// The false result is not zero percent and must not be rendered as one. An
// environment where nothing could be measured has not been shown to reproduce
// nothing; it has not been measured.
func (s Score) Percent() (int, bool) {
	if s.Counted <= 0 {
		return 0, false
	}
	return (200*s.Reproduced + s.Counted) / (2 * s.Counted), true
}

// Score computes the headline over the whole inventory.
func (i Inventory) Score() Score {
	var s Score
	for _, d := range i.Dimensions {
		if d.NotApplicable != "" {
			s.Excluded = append(s.Excluded, Exclusion{
				Dimension: d.Name, Because: d.NotApplicable,
			})
			continue
		}
		for _, c := range d.Components {
			if !c.State.Measured() {
				s.Excluded = append(s.Excluded, Exclusion{
					Dimension: d.Name, Component: c.Name, Because: c.Detail,
				})
				continue
			}
			s.Counted++
			if c.State == Reproduced {
				s.Reproduced++
			}
		}
	}
	return s
}

// Dimension returns one dimension by name.
func (i Inventory) Dimension(name schema.FidelityDimension) (Dimension, bool) {
	for _, d := range i.Dimensions {
		if d.Name == name {
			return d, true
		}
	}
	return Dimension{}, false
}

// Unmeasured lists every component whose state could not be determined,
// dimension first.
//
// Exported because the requirement check needs it and because a caller
// rendering the score has to be able to name what the score left out.
func (i Inventory) Unmeasured() []Exclusion {
	var out []Exclusion
	for _, d := range i.Dimensions {
		for _, c := range d.Components {
			if !c.State.Measured() {
				out = append(out, Exclusion{
					Dimension: d.Name, Component: c.Name, Because: c.Detail,
				})
			}
		}
	}
	return out
}

// Requirement is the outcome of one entry in fidelity.require.
type Requirement struct {
	Dimension schema.FidelityDimension `json:"dimension"`
	// Met is true only when every component of the dimension was measured and
	// reproduced.
	Met bool `json:"met"`
	// Measurable is false when the dimension has nothing to measure or when
	// something in it could not be measured. A requirement that could not be
	// evaluated is neither met nor broken, and the caller must not report it
	// as either.
	Measurable bool `json:"measurable"`
	// Because explains an unmet or unmeasurable requirement, and is empty
	// when the requirement was met.
	Because string `json:"because,omitempty"`
}

// Check evaluates the manifest's fidelity.require against the inventory.
//
// The three outcomes are deliberately distinct rather than a bool. A dimension
// that could not be measured must not fail the way a dimension that was
// measured and found absent fails, because the first is a gap in what we can
// see and the second is a fact about the environment, and telling somebody the
// first is the second is how a report stops being believed.
func (i Inventory) Check(require []schema.FidelityDimension) []Requirement {
	out := make([]Requirement, 0, len(require))
	for _, name := range require {
		d, found := i.Dimension(name)
		switch {
		case !found:
			out = append(out, Requirement{Dimension: name, Because: "this build does not measure that dimension"})
			continue
		case d.NotApplicable != "":
			out = append(out, Requirement{Dimension: name, Because: d.NotApplicable})
			continue
		}

		var unmeasured, notReproduced []string
		for _, c := range d.Components {
			switch {
			case !c.State.Measured():
				unmeasured = append(unmeasured, c.Name)
			case c.State != Reproduced:
				notReproduced = append(notReproduced, c.Name+" is "+string(c.State))
			}
		}
		sort.Strings(unmeasured)
		sort.Strings(notReproduced)

		switch {
		case len(unmeasured) > 0:
			// Names the components without repeating the verdict. Both callers
			// that render this already say the dimension could not be
			// measured, and a reason that says it a second time reads as a
			// stutter in the one message somebody has to act on.
			out = append(out, Requirement{
				Dimension: name,
				Because:   "no state could be read for " + join(unmeasured),
			})
		case len(notReproduced) > 0:
			out = append(out, Requirement{
				Dimension: name, Measurable: true,
				Because: join(notReproduced),
			})
		default:
			out = append(out, Requirement{Dimension: name, Met: true, Measurable: true})
		}
	}
	return out
}

// join renders a list for one line of prose.
func join(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	out := items[0]
	for _, s := range items[1 : len(items)-1] {
		out += ", " + s
	}
	return out + " and " + items[len(items)-1]
}
