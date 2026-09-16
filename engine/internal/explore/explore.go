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
	"strings"
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
	Name string `json:"name"`
	Goal string `json:"goal"`
	Seed string `json:"seed"`
	// Persona, StartPath and Viewport say what was explored, as the runner
	// actually did it rather than as the manifest declared it. A call can
	// steer all three, and evidence that did not say which persona stood on
	// which page in which window would be evidence about a run nobody can
	// name. Persona is the account signed in as, empty when nobody was.
	Persona   string   `json:"persona"`
	StartPath string   `json:"startPath"`
	Viewport  Viewport `json:"viewport"`
	// Focus is the sentence a call steered with, empty when it did not.
	Focus   string `json:"focus,omitempty"`
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
		// DOM and Responses are what the authz and canary_leak families read to
		// decide whether a persona reached content it should not have: the DOM
		// the page rendered and the bodies of the responses the browser
		// received. They are the runner's observations, decoded from the same
		// JSON as the rest of this struct, and empty is fine for now: the runner
		// populates them where it is cheap to, and a family that finds them
		// empty has simply not been handed that evidence rather than been told
		// there was none. They stay inside the run, against the sanitized twin;
		// a finding that reads them reports a location and never a body.
		DOM       []string `json:"dom,omitempty"`
		Responses []string `json:"responses,omitempty"`
	} `json:"evidence"`
	// Observations are the structured per-persona authorization readings the
	// runner made against the twin: which persona reached which object class at
	// which route, what the twin answered, and whether the object's planted
	// canary came back. The authz family reads them to decide access control by
	// content presence rather than by status code. Every field is a bounded
	// location, an identity comparison or a flag, and none is a raw value; the
	// runner decides content presence against the golden's canary inside the run
	// and emits only the flag. Empty until the runner has the ownership and canary
	// metadata to make a reading, which a reader fails closed on rather than
	// reading as "no violation".
	Observations []Observation `json:"observations,omitempty"`
	DurationMs   int64         `json:"durationMs"`
}

// Observation is one per-persona authorization reading the runner made against
// the twin, the wire form the authz family consumes. It carries a bounded
// location, the acting and owning identities so a boundary crossing can be
// decided, the status the reach returned, and the two flags a sound reading
// needs: whether the object's planted canary was present, and whether the object
// was seeded before the reach so a refusal proves a boundary held rather than
// that the id was invented. No field is a raw value: Route is a template,
// ObjectClass is a category label, and content presence is the golden canary's
// verdict, never the body. The field names and JSON tags mirror the runner's
// emission and the engine's security.RawObservation exactly, so the shape cannot
// drift across the boundary.
type Observation struct {
	Route                string `json:"route"`
	Method               string `json:"method"`
	Anonymous            bool   `json:"anonymous,omitempty"`
	ActorTenant          string `json:"actorTenant,omitempty"`
	ActorUser            string `json:"actorUser,omitempty"`
	ActorRole            string `json:"actorRole,omitempty"`
	ObjectClass          string `json:"objectClass,omitempty"`
	OwnerTenant          string `json:"ownerTenant,omitempty"`
	OwnerUser            string `json:"ownerUser,omitempty"`
	OwnerRole            string `json:"ownerRole,omitempty"`
	Status               int    `json:"status"`
	VictimContentPresent bool   `json:"victimContentPresent,omitempty"`
	SetupConfirmed       bool   `json:"setupConfirmed,omitempty"`
}

// Setting is the one line saying how an exploration was pointed.
//
// "as viewer from /environments on phone 390x844". Printed under every
// exploration and carried into the MCP result, because the first question
// about a finding on a phone is whether it happens on a desktop too, and that
// question cannot be asked of a report that never said which it was.
func (e Exploration) Setting() string {
	var parts []string
	if e.Persona != "" {
		parts = append(parts, "as "+e.Persona)
	} else {
		parts = append(parts, "signed out")
	}
	if e.StartPath != "" {
		parts = append(parts, "from "+e.StartPath)
	}
	if v := e.Viewport.String(); v != "" {
		parts = append(parts, "on "+v)
	}
	if e.Focus != "" {
		parts = append(parts, fmt.Sprintf("focused on %q", e.Focus))
	}
	return strings.Join(parts, " ")
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

// Headline is the one line summary, for a person with thirty seconds.
//
// It ends by saying the findings do not count against the change, and that
// sentence is the whole verdict decision made visible. An exploration wanders
// pages nobody wrote a workflow for, so nothing declared what should have
// happened there, and turning "people would hesitate at this control" into a
// red mark is how a check becomes one people mute. The enforcement lives in
// the runner, where the cause `explored` maps to `pass`; this is where a
// reader is told.
func (r Report) Headline() string {
	if len(r.Explorations) == 0 {
		return "No exploration ran."
	}
	b := r.Blocked()
	if b == len(r.Explorations) {
		return fmt.Sprintf("%s could not run, so nothing was explored.",
			plural(b, "exploration", "explorations"))
	}
	findings := r.Findings()
	observed := len(r.Explorations) - b
	suffix := ""
	if b > 0 {
		suffix = fmt.Sprintf(" Not explored: %s could not run.", plural(b, "exploration", "explorations"))
	}
	if len(findings) == 0 {
		return fmt.Sprintf("%s wandered the application and found nothing worth reporting.",
			plural(observed, "exploration", "explorations")) + suffix
	}
	return fmt.Sprintf("%s found %s. None of it counts against this change.",
		plural(observed, "exploration", "explorations"),
		plural(len(findings), "thing", "things")) + suffix
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
