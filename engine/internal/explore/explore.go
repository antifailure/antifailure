// Package explore holds what an exploratory run found.
//
// A workflow says what to do and what proves it happened. An exploration says
// only what somebody is trying to achieve, and then wanders: it reads each
// page, chooses somewhere to go, and writes down every place the application
// cost it effort. It answers the question a declared workflow cannot ask,
// which is "nothing broke, so why would somebody give up here".
//
// The engine does not decide any of that. The runner drives the browser and
// emits the findings; this package is the vocabulary both sides agree on, the
// roll up a person reads, and the compiler that turns a discovery back into a
// declared workflow. Keeping the vocabulary here rather than only in
// TypeScript is what lets a test prove the two halves have not drifted.
package explore

import (
	"fmt"
	"sort"
)

// Kind names one way an application costs somebody effort without failing.
//
// Six of them, and every one is decided from something the runner measured
// rather than from a judgement about what a user would feel. That constraint
// is why there is no "confusion" and no "frustration" here: the runner can see
// a control that did nothing and a page it came back to twice, and it cannot
// see a person's patience. A taxonomy naming things nothing observes produces
// findings nobody can check, which is worse than no taxonomy.
type Kind string

const (
	// KindNoEffect is a control that was activated and changed nothing.
	KindNoEffect Kind = "no_effect"
	// KindDeadEnd is a page that offered nothing not already tried.
	KindDeadEnd Kind = "dead_end"
	// KindRevisit is a path that came back to a page it had already left.
	KindRevisit Kind = "revisit"
	// KindUnnamedControl is an interactive element with no accessible name.
	KindUnnamedControl Kind = "unnamed_control"
	// KindSlowResponse is a step that took longer than the goal allows.
	KindSlowResponse Kind = "slow_response"
	// KindGoalUnreached is an exploration that never found what it sought.
	KindGoalUnreached Kind = "goal_unreached"
)

// AllKinds is every kind, in the order the documentation lists them.
//
// Kept so that three things cannot drift: this list, the same list in
// runner/src/explore.ts, and the reference page. A test walks all three.
func AllKinds() []Kind {
	return []Kind{
		KindNoEffect, KindDeadEnd, KindRevisit,
		KindUnnamedControl, KindSlowResponse, KindGoalUnreached,
	}
}

// Title is the kind in the words a person reads in a report.
func (k Kind) Title() string {
	switch k {
	case KindNoEffect:
		return "did nothing"
	case KindDeadEnd:
		return "dead end"
	case KindRevisit:
		return "loops back"
	case KindUnnamedControl:
		return "unnamed control"
	case KindSlowResponse:
		return "slow to answer"
	case KindGoalUnreached:
		return "goal not reached"
	default:
		return string(k)
	}
}

// Finding is one thing an exploration ran into.
//
// Every field exists so that somebody can go and look. URL and Control locate
// it the way a person would search for it, and Step indexes the journey, so
// the finding opens where it happened rather than somewhere in a trace. A
// finding that says "users hesitate here" and names neither is a complaint.
//
// There is deliberately no severity and no conversion estimate. A number with
// no measurement behind it reads as evidence and is not, and this product's
// own marketing refuses to invent one.
type Finding struct {
	Kind Kind   `json:"kind"`
	URL  string `json:"url"`
	// Control is the accessible name of the element, when one element is
	// responsible. Empty for a finding about a whole page or a whole run.
	Control string `json:"control,omitempty"`
	Step    int    `json:"step"`
	// Confidence is high when the runner measured it and medium when it
	// inferred it from the goal's words. Two values, because a third would be
	// a guess about a guess.
	Confidence string `json:"confidence"`
	// Detail says what happened. Fix says what to do about it.
	Detail string `json:"detail"`
	Fix    string `json:"fix"`
	// MeasuredMs is the duration behind a slow response. Zero elsewhere.
	MeasuredMs int64 `json:"measuredMs"`
}

// Move is one concrete thing an exploration did, in a form that replays.
type Move struct {
	Kind    string `json:"kind"`
	URL     string `json:"url,omitempty"`
	Field   string `json:"field,omitempty"`
	Value   string `json:"value,omitempty"`
	Control string `json:"control,omitempty"`
}

// Sentence is the move in the words a person would use.
func (m Move) Sentence() string {
	switch m.Kind {
	case "goto":
		return "Open " + m.URL
	case "fill":
		return "Fill " + m.Field
	case "click":
		return fmt.Sprintf("Press %q", m.Control)
	default:
		return m.Kind
	}
}

// Exploration is what one goal produced. The shape the runner writes.
type Exploration struct {
	Name    string `json:"name"`
	Goal    string `json:"goal"`
	Seed    string `json:"seed"`
	Outcome struct {
		Verdict      string   `json:"verdict"`
		Cause        string   `json:"cause"`
		Detail       string   `json:"detail"`
		Reproduction []string `json:"reproduction"`
	} `json:"outcome"`
	// Reached says whether the goal's own words ever appeared on a page.
	Reached  bool      `json:"reached"`
	Steps    []string  `json:"steps"`
	Journey  []Move    `json:"journey"`
	Findings []Finding `json:"findings"`
	Visited  []string  `json:"visited"`
	// Missing names what was not explored, and why. An exploration that
	// refused half the application must never read as a clean bill of health.
	Missing  []string `json:"missing"`
	Evidence struct {
		Video      string   `json:"video"`
		Trace      string   `json:"trace"`
		Screenshot string   `json:"screenshot"`
		Console    []string `json:"console"`
		Failed     []string `json:"failed"`
	} `json:"evidence"`
	DurationMs int64 `json:"durationMs"`
}

// Report is every exploration one run produced.
type Report struct {
	Explorations []Exploration `json:"explorations"`
}

// Findings is every finding across every exploration, worst first.
//
// Sorted by confidence and then by kind rather than by the order they were
// found, because somebody reading a pull request comment reads the top of the
// list and stops. A measured fact outranks an inference, always.
func (r Report) Findings() []Finding {
	order := map[Kind]int{}
	for i, k := range AllKinds() {
		order[k] = i
	}
	var out []Finding
	for _, e := range r.Explorations {
		out = append(out, e.Findings...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Confidence != out[j].Confidence {
			// "high" sorts before "medium" alphabetically, which is the order
			// wanted here, but relying on that would break the day a third
			// value arrives, so it is stated.
			return rank(out[i].Confidence) > rank(out[j].Confidence)
		}
		if order[out[i].Kind] != order[out[j].Kind] {
			return order[out[i].Kind] < order[out[j].Kind]
		}
		return out[i].Step < out[j].Step
	})
	return out
}

func rank(confidence string) int {
	switch confidence {
	case "high":
		return 2
	case "medium":
		return 1
	default:
		return 0
	}
}

// Blocked counts explorations that could not run.
//
// Separate from a count of findings for the same reason an invariant's Error
// is separate from its Held: an exploration that never opened a page has not
// found that the application is fine, and a report that showed it as zero
// findings would say the opposite of what happened.
func (r Report) Blocked() int {
	n := 0
	for _, e := range r.Explorations {
		if e.Outcome.Verdict == "blocked" {
			n++
		}
	}
	return n
}

// CountsAgainstTheApplication is false, always, and this function exists to
// say so where somebody would look for it.
//
// An exploration wanders pages nobody wrote a workflow for, so nothing
// declared what should have happened there. Turning "people would hesitate at
// this control" into a red mark on a pull request is how a check becomes one
// people mute, and report.go opens by saying a muted comment is worse than
// none. Findings go in the body; they never reach the exit code.
func (r Report) CountsAgainstTheApplication() bool { return false }

// Headline is the one line summary, for a person with thirty seconds.
func (r Report) Headline() string {
	if len(r.Explorations) == 0 {
		return "No exploration ran."
	}
	if b := r.Blocked(); b == len(r.Explorations) {
		return fmt.Sprintf("%s could not run, so nothing was explored.",
			plural(b, "exploration", "explorations"))
	}
	findings := r.Findings()
	if len(findings) == 0 {
		return fmt.Sprintf("%s wandered the application and found nothing worth reporting.",
			plural(len(r.Explorations), "exploration", "explorations"))
	}
	return fmt.Sprintf("%s found %s. None of it counts against this change.",
		plural(len(r.Explorations), "exploration", "explorations"),
		plural(len(findings), "thing", "things"))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
