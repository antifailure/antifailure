package explore

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The most valuable thing an exploration produces is not the report.
//
// It is that a run which found something can be turned into a declared
// workflow that reproduces it, so the discovery stops being a one time
// observation and becomes a check that runs on every pull request. Compile is
// that step: it takes the path an exploration actually walked and writes the
// manifest block a person pastes into antifailure.yaml.
//
// Two limits are stated rather than hidden, because a compiler that pretended
// otherwise would produce workflows people delete.
//
// The expectation is the goal sentence. An exploration knows what it was
// looking for and it does not know what a passing run should say, so the
// compiled workflow asserts the goal and the notes say to check it reads on
// the page. A workflow whose expectation cannot be read is reported as
// unverified, which is the honest answer and not a pass.
//
// A friction finding is not an expectation. "Pressing Upgrade plan changes
// nothing" is a defect to fix, not an outcome to assert, and a workflow that
// asserted it would go green the day somebody broke it differently and red the
// day somebody fixed it. Those findings are listed in the notes so whoever
// pastes the block knows what the exploration saw and the workflow will not.

// maxJourneyLines caps the enumerated path in a description. The schema allows
// 4000 characters and a forty step exploration would spend most of them on a
// list nobody reads past the tenth line.
const maxJourneyLines = 20

// Compile turns an exploration into the workflow block that replays it.
//
// The notes are returned rather than written into the YAML as comments,
// because a comment in a generated block gets pasted into somebody's manifest
// and stays there forever. They belong on the terminal, once.
func Compile(e Exploration, persona string) (schema.Workflow, []string) {
	w := schema.Workflow{
		Name:        e.Name,
		Description: description(e),
		Persona:     persona,
		StartPath:   startPath(e),
		Expect:      []string{sentence(e.Goal)},
		Budget: &schema.Budget{
			// Room above what the exploration spent, because a declared
			// workflow that starts at the same place takes a shorter route
			// and a budget with no slack turns one extra redirect into a
			// blocked run.
			Steps: len(e.Journey) + 10,
		},
		Tags: []string{"discovered"},
	}
	return w, notes(e)
}

// description says what to do, in sentences, and then names the route.
//
// The route is in it on purpose, and it does not contradict the guidance that
// a description should not be a script. That guidance is about selectors: a
// description saying "click #signup-btn" breaks when the markup changes. These
// are accessible names, which are the thing that survives a redesign and
// disappears when somebody removes the label a screen reader depends on, which
// is the failure a workflow should have.
func description(e Exploration) string {
	var b strings.Builder
	b.WriteString(sentence(e.Goal))
	b.WriteString(" Found by an exploration from seed ")
	b.WriteString(e.Seed)
	if len(e.Journey) == 0 {
		b.WriteString(", which did not get anywhere.")
		return b.String()
	}
	b.WriteString(", which got there by: ")
	shown := e.Journey
	extra := 0
	if len(shown) > maxJourneyLines {
		extra = len(shown) - maxJourneyLines
		shown = shown[:maxJourneyLines]
	}
	parts := make([]string, 0, len(shown))
	for _, m := range shown {
		parts = append(parts, lowerFirst(m.Sentence()))
	}
	b.WriteString(strings.Join(parts, ", "))
	if extra > 0 {
		fmt.Fprintf(&b, ", and %d more steps", extra)
	}
	b.WriteString(".")
	return b.String()
}

// startPath is where the exploration began, as a path.
//
// Taken from the journey's first move rather than from the goal, because the
// journey carries the URL the browser was actually on, and a goal whose
// start_path redirected somewhere else would compile a workflow that starts in
// the wrong place.
func startPath(e Exploration) string {
	for _, m := range e.Journey {
		if m.Kind != "goto" {
			continue
		}
		return pathOf(m.URL)
	}
	return "/"
}

func pathOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		// A URL the standard library refuses is not worth guessing at, and "/"
		// is where a workflow with no start_path begins anyway.
		return "/"
	}
	if u.RawQuery != "" {
		return u.Path + "?" + u.RawQuery
	}
	return u.Path
}

func notes(e Exploration) []string {
	out := []string{
		"The expectation is the goal, because an exploration knows what it was looking for and " +
			"not what a passing page should say. Check its words appear on the page this run " +
			"ended on, or rewrite it: a workflow whose expectation cannot be read is reported as " +
			"unverified rather than as a pass.",
	}
	if !e.Reached {
		out = append(out,
			"This exploration never reached the goal, so the workflow asserts something nobody has "+
				"seen happen. Expect it to be unverified until the path exists.")
	}
	for _, f := range e.Findings {
		if f.Kind == KindGoalUnreached {
			// Already said above, and saying it twice makes the list read as
			// though two things went wrong.
			continue
		}
		where := f.URL
		if f.Control != "" {
			where = fmt.Sprintf("%q on %s", f.Control, f.URL)
		}
		out = append(out, fmt.Sprintf(
			"The exploration found %s at %s, which this workflow will not assert. A friction "+
				"finding is something to fix, not an outcome to expect.", f.Kind.Title(), where))
	}
	if len(e.Missing) > 0 {
		out = append(out, fmt.Sprintf(
			"%s of the application was left unexplored, listed on the exploration. The compiled "+
				"workflow covers only the route that was walked.",
			plural(len(e.Missing), "part", "parts")))
	}
	return out
}

// sentence makes sure a goal ends like one, so a description built from it
// does not run into the next clause.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?") {
		return s
	}
	return s + "."
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
