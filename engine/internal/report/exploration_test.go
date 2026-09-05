package report

import (
	"strings"
	"testing"

	"github.com/antifailure/antifailure/engine/internal/explore"
)

func exploredGoal() explore.Exploration {
	x := explore.Exploration{Name: "read-evidence", Visited: []string{"/runs"}}
	x.Outcome.Verdict = "pass"
	x.Evidence.Trace = "artifacts/trace.zip"
	return x
}

func TestConfiguredExplorationRequiresEvidence(t *testing.T) {
	for _, name := range []string{"complete", "absent", "blocked", "unknown", "no-page", "no-trace", "duplicate", "duplicate-declaration", "wrong-goal", "extra-goal", "unavailable", "empty-config"} {
		t.Run(name, func(t *testing.T) {
			x := exploredGoal()
			e := &Exploration{Declared: []string{x.Name}, Results: []explore.Exploration{x}}
			switch name {
			case "absent":
				e.Results = nil
			case "blocked":
				e.Results[0].Outcome.Verdict = "blocked"
			case "unknown":
				e.Results[0].Outcome.Verdict = "future"
			case "no-page":
				e.Results[0].Visited = nil
			case "no-trace":
				e.Results[0].Evidence.Trace = ""
			case "duplicate":
				e.Results = append(e.Results, x)
			case "duplicate-declaration":
				// Two goals of the same name. The counts match and every
				// declared name is present, so only the repeated result
				// itself says one browser run cannot stand in for two goals.
				e.Declared = []string{x.Name, x.Name}
				e.Results = append(e.Results, x)
			case "wrong-goal":
				e.Results[0].Name = "another"
			case "extra-goal":
				// Every declared goal is accounted for and every result is
				// complete, and there is still one result too many. Only the
				// count catches this, so without it the line is untested.
				extra := exploredGoal()
				extra.Name = "unasked"
				e.Results = append(e.Results, extra)
			case "unavailable":
				e.Unavailable = "browser refused"
			case "empty-config":
				e.Declared = nil
				e.Results = nil
			}
			r := Run{Workflows: []Workflow{{Name: "existing", Verdict: "pass"}}, Exploration: e}
			want := VerdictBlocked
			if name == "complete" {
				want = VerdictPass
			}
			if got := r.Verdict(); got != want {
				t.Fatalf("got %s, want %s", got, want)
			}
		})
	}
}

func TestExplorationObservationIsNotAnApplicationFailure(t *testing.T) {
	x := exploredGoal()
	x.Findings = []explore.Finding{{Kind: explore.KindNoEffect, URL: "/runs", Detail: "Read report did nothing", Step: 2}}
	r := Run{Workflows: []Workflow{{Verdict: "pass"}}, Exploration: &Exploration{Declared: []string{x.Name}, Results: []explore.Exploration{x}}}
	if got := r.Verdict(); got != VerdictPass {
		t.Fatalf("observation changed verdict: %s", got)
	}
}

func TestIncompleteExplorationCannotBeHiddenByAnAdvisory(t *testing.T) {
	for _, name := range []string{"flaky", "warning"} {
		t.Run(name, func(t *testing.T) {
			r := Run{Workflows: []Workflow{{Verdict: "flaky"}}, Exploration: &Exploration{Declared: []string{"goal"}}}
			if name == "warning" {
				r.Workflows[0].Verdict = "pass"
				r.Findings = []Finding{{Level: LevelWarn}}
			}
			if got := r.Verdict(); got != VerdictBlocked {
				t.Fatalf("incomplete exploration was hidden by %s", got)
			}
		})
	}
}

func TestExplorationReportRetainsObservedEvidence(t *testing.T) {
	x := exploredGoal()
	x.Findings = []explore.Finding{{Kind: explore.KindNoEffect, URL: "/runs", Detail: "Read report did nothing", Step: 2}}
	x.Missing = []string{"Delete refused"}
	x.Journey = []explore.Move{{Kind: "click", Control: "Read report"}}
	r := Run{Exploration: &Exploration{Declared: []string{x.Name}, Results: []explore.Exploration{x}}}
	for _, needle := range []string{"read-evidence", "1 page visited, 1 move, 1 observation", "Read report did nothing", "Delete refused", "artifacts/trace.zip"} {
		t.Run(needle, func(t *testing.T) {
			if !strings.Contains(r.Markdown(), needle) {
				t.Fatalf("missing %q", needle)
			}
		})
	}
}

func TestIncompleteExplorationNamesTheMissingExperiment(t *testing.T) {
	r := Run{Workflows: []Workflow{{Verdict: "pass"}}, Exploration: &Exploration{Declared: []string{"goal"}, Unavailable: "browser refused"}}
	for _, needle := range []string{"Configured exploration could not be completed", "browser refused", "not a clean exploration", "Goal `goal` produced no browser result"} {
		t.Run(needle, func(t *testing.T) {
			if !strings.Contains(r.Markdown(), needle) {
				t.Fatalf("missing %q", needle)
			}
		})
	}
}

func TestARealFailureStillOutranksAnIncompleteExploration(t *testing.T) {
	for _, name := range []string{"workflow", "invariant", "finding"} {
		t.Run(name, func(t *testing.T) {
			r := Run{Exploration: &Exploration{Declared: []string{"goal"}}}
			switch name {
			case "workflow":
				r.Workflows = []Workflow{{Verdict: VerdictFail}}
			case "invariant":
				r.Invariants = []Invariant{{Name: "one", Held: false}}
			case "finding":
				r.Findings = []Finding{{Level: LevelFail}}
			}
			if got := r.Verdict(); got != VerdictFail {
				t.Fatalf("an incomplete exploration hid a real failure as %s", got)
			}
		})
	}
}
