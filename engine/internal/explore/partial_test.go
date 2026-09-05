package explore

import (
	"strings"
	"testing"
)

func TestPartialExplorationNeverCountsBlockedGoalsAsObserved(t *testing.T) {
	for _, name := range []string{"clean", "findings"} {
		t.Run(name, func(t *testing.T) {
			good, bad := Exploration{}, Exploration{}
			good.Outcome.Verdict = "pass"
			bad.Outcome.Verdict = "blocked"
			if name == "findings" {
				good.Findings = []Finding{{Kind: KindNoEffect}}
			}
			headline := (Report{Explorations: []Exploration{good, bad}}).Headline()
			t.Run("observed", func(t *testing.T) {
				if !strings.HasPrefix(headline, "1 exploration ") {
					t.Fatalf("miscounted execution: %s", headline)
				}
			})
			t.Run("blocked", func(t *testing.T) {
				if !strings.Contains(headline, "1 exploration could not run") {
					t.Fatalf("missing blocked goals: %s", headline)
				}
			})
		})
	}
}
