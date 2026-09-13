package explore

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// Steering is what a call adds to a goal at the moment it runs.
//
// The manifest's goal is the default and every field here is an override. That
// order matters and it is the whole reason this type exists: `af explore
// --only` could select among the goals a manifest declared and could not add
// to them, so an evaluator who wanted to explore onboarding as a new owner on
// a phone had to edit antifailure.yaml first. The manifest's single goal was
// the entire reach of the instrument. Nothing in a call can weaken a safety
// control, because none of these fields is one: a persona is checked against
// the manifest, a start path is a path on the environment that is already
// running, and a viewport is the size of a window.
type Steering struct {
	// Persona names a declared persona to explore as. Empty keeps the goal's.
	Persona string
	// StartPath is where the exploration begins, as a path. Empty keeps the
	// goal's.
	StartPath string
	// Viewport is phone, tablet, desktop or WIDTHxHEIGHT. Empty keeps the
	// runner's default window.
	Viewport string
	// Budget is a step count such as 8, or a duration such as 5m. Empty keeps
	// the goal's budget.
	Budget string
	// Focus is a sentence about what to attend to. Its words steer which
	// controls are pressed first. It never changes what counts as the goal
	// being reached, because the goal is what the run is judged against and a
	// call that could move the goalposts is a call that could pass anything.
	Focus string
}

// Viewport is the window an exploration runs in.
type Viewport struct {
	// Name is phone, tablet, desktop or custom.
	Name   string `json:"name"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	// Mobile is true for the phone, which is when the browser also presents a
	// mobile user agent and a touch screen. A narrow desktop window is not a
	// phone: the layout reflows, and nothing else about the device changes.
	Mobile bool `json:"mobile"`
}

// String renders the viewport the way the report prints it.
func (v Viewport) String() string {
	if v.Width == 0 || v.Height == 0 {
		return ""
	}
	if v.Name == "" || v.Name == "custom" {
		return fmt.Sprintf("%dx%d", v.Width, v.Height)
	}
	return fmt.Sprintf("%s %dx%d", v.Name, v.Width, v.Height)
}

// The three named viewports and their fixed sizes, stated in the help and in
// the tool's schema, because a name that maps to a size nobody can look up is
// a name that gets argued about.
var namedViewports = map[string]Viewport{
	"phone":   {Name: "phone", Width: 390, Height: 844, Mobile: true},
	"tablet":  {Name: "tablet", Width: 768, Height: 1024},
	"desktop": {Name: "desktop", Width: 1440, Height: 900},
}

// Bounds on a custom size. Below 320 no page lays out at all, and above these
// a screenshot is a file nobody opens.
const (
	minViewportSide = 320
	maxViewportSide = 3840
)

// ParseViewport reads phone, tablet, desktop or WIDTHxHEIGHT.
func ParseViewport(s string) (Viewport, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Viewport{}, nil
	}
	if v, ok := namedViewports[strings.ToLower(s)]; ok {
		return v, nil
	}
	w, h, found := strings.Cut(strings.ToLower(s), "x")
	width, werr := strconv.Atoi(w)
	height, herr := strconv.Atoi(h)
	if !found || werr != nil || herr != nil {
		return Viewport{}, aferrors.Coded(aferrors.AFAGT023, "detail",
			fmt.Sprintf("%q is not a viewport. Use phone (390x844), tablet (768x1024), "+
				"desktop (1440x900), or a size such as 1280x720.", s))
	}
	for _, side := range []int{width, height} {
		if side < minViewportSide || side > maxViewportSide {
			return Viewport{}, aferrors.Coded(aferrors.AFAGT023, "detail",
				fmt.Sprintf("the viewport %s is outside %d to %d pixels a side.",
					s, minViewportSide, maxViewportSide))
		}
	}
	return Viewport{Name: "custom", Width: width, Height: height}, nil
}

// Budget is how much an exploration may spend, as steps or as time.
type Budget struct {
	Steps    int
	Duration time.Duration
}

// maxBudgetSteps is the most steps a call may ask for. The runner's own
// default is 40, and a thousand is already a run nobody reads.
const maxBudgetSteps = 1000

// ParseBudget reads a step count such as 8 or a duration such as 5m.
//
// A bare number is steps and not minutes, because a step is what the manifest
// counts, what every finding is indexed by, and what the reproduction prints.
// Minutes need their unit.
func ParseBudget(s string) (Budget, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Budget{}, nil
	}
	if n, err := strconv.Atoi(s); err == nil {
		if n < 1 || n > maxBudgetSteps {
			return Budget{}, aferrors.Coded(aferrors.AFAGT023, "detail",
				fmt.Sprintf("a budget of %d steps is outside 1 to %d.", n, maxBudgetSteps))
		}
		return Budget{Steps: n}, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return Budget{}, aferrors.Coded(aferrors.AFAGT023, "detail",
			fmt.Sprintf("%q is not a budget. Use a step count such as 8, or a duration "+
				"such as 5m or 90s.", s))
	}
	if d > 6*time.Hour {
		return Budget{}, aferrors.Coded(aferrors.AFAGT023, "detail",
			fmt.Sprintf("a budget of %s is longer than the six hours an environment lives.", s))
	}
	return Budget{Duration: d}, nil
}

// ParseStartPath checks that a start path is a path on the environment.
//
// Absolute, so it cannot be a URL to somewhere else: the environment under
// test is the only place an exploration may go, and a path that carried a
// scheme or a host would point the browser at whatever the caller named. A
// query string is allowed, because a page often needs one to open in the
// state worth exploring.
func ParseStartPath(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if !strings.HasPrefix(s, "/") || strings.HasPrefix(s, "//") ||
		strings.ContainsAny(s, " \t\r\n#") {
		return "", aferrors.Coded(aferrors.AFAGT023, "detail",
			fmt.Sprintf("%q is not a path. A start path begins with a single / and names a "+
				"page on the environment, such as /settings/billing.", s))
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "", aferrors.Coded(aferrors.AFAGT023, "detail",
			fmt.Sprintf("%q is not a path. A start path begins with a single / and names a "+
				"page on the environment, such as /settings/billing.", s))
	}
	return s, nil
}

// Resolved is a Steering after every field has been read and checked.
type Resolved struct {
	Persona   string
	StartPath string
	Viewport  Viewport
	Budget    Budget
	Focus     string
}

// maxFocusBytes bounds the focus sentence. It travels into the runner's goal
// words and into the report, and a paragraph there is a script, which is what
// a declared workflow is for.
const maxFocusBytes = 500

// Resolve checks every field of a steering against the personas the manifest
// declares and returns the parsed form.
//
// One error at a time and the first one wins, in field order, because a call
// with two mistakes is fixed by reading one message and running again, and a
// list of coded errors has no single exit code.
func (s Steering) Resolve(personas []string) (Resolved, error) {
	var out Resolved
	if p := strings.TrimSpace(s.Persona); p != "" {
		found := false
		for _, name := range personas {
			if name == p {
				found = true
				break
			}
		}
		if !found {
			declared := append([]string(nil), personas...)
			sort.Strings(declared)
			list := strings.Join(declared, ", ")
			if list == "" {
				list = "none"
			}
			return out, aferrors.Coded(aferrors.AFAGT022, "persona", p, "personas", list)
		}
		out.Persona = p
	}
	path, err := ParseStartPath(s.StartPath)
	if err != nil {
		return out, err
	}
	out.StartPath = path
	viewport, err := ParseViewport(s.Viewport)
	if err != nil {
		return out, err
	}
	out.Viewport = viewport
	budget, err := ParseBudget(s.Budget)
	if err != nil {
		return out, err
	}
	out.Budget = budget
	focus := strings.Join(strings.Fields(s.Focus), " ")
	if len(focus) > maxFocusBytes {
		return out, aferrors.Coded(aferrors.AFAGT023, "detail",
			fmt.Sprintf("the focus is %d bytes and the most a call may steer with is %d. "+
				"A focus is a sentence; a script is a workflow.", len(focus), maxFocusBytes))
	}
	out.Focus = focus
	return out, nil
}

// Flags renders the steering as the command line flags that reproduce it.
//
// Every reproduction line a report prints comes through here, so that a
// finding made on a phone as the owner replays on a phone as the owner rather
// than on the manifest's defaults, which would walk a different path and
// report the original as unreproducible. Rendered from the checked values
// rather than from what was typed, so a replay line never carries the stray
// whitespace or capitals the parsing forgave, and every value is quoted for a
// POSIX shell, because a start path with a query string holds an ampersand
// and a focus is a sentence.
func (r Resolved) Flags() string {
	var parts []string
	if r.Persona != "" {
		parts = append(parts, "--persona "+shellQuote(r.Persona))
	}
	if r.StartPath != "" {
		parts = append(parts, "--start "+shellQuote(r.StartPath))
	}
	switch {
	case r.Viewport.Name != "" && r.Viewport.Name != "custom":
		parts = append(parts, "--viewport "+r.Viewport.Name)
	case r.Viewport.Width > 0:
		parts = append(parts, fmt.Sprintf("--viewport %dx%d", r.Viewport.Width, r.Viewport.Height))
	}
	if r.Budget.Steps > 0 {
		parts = append(parts, fmt.Sprintf("--budget %d", r.Budget.Steps))
	}
	if r.Budget.Duration > 0 {
		parts = append(parts, "--budget "+r.Budget.Duration.String())
	}
	if r.Focus != "" {
		parts = append(parts, "--focus "+shellQuote(r.Focus))
	}
	return strings.Join(parts, " ")
}

// shellQuote leaves a word made only of characters no shell treats specially
// alone, and wraps anything else in single quotes, which is the one quoting in
// which nothing but the quote itself is special.
func shellQuote(s string) string {
	plain := s != ""
	for _, r := range s {
		letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		digit := r >= '0' && r <= '9'
		if !letter && !digit && !strings.ContainsRune("/_.:-=", r) {
			plain = false
			break
		}
	}
	if plain {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
