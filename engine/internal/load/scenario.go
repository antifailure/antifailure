package load

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// A scenario is a journey, and the middle this package was missing.
//
// There were two things a person could ask for and nothing between them.
// Replay production's mix, which is a weighted set of independent requests and
// says nothing about order, or declare a workflow and hand it to a browser
// agent, which is one user at a time and costs a language model call a step.
// The load that actually breaks things is neither: it is fifty sessions
// walking the same journey while the mix runs underneath, and the second
// request of the journey arriving while the first is still in flight.
//
// So a scenario is an ordered list of requests, with waits between them and
// blocks that overlap, executed by the same generator at the same
// concurrency, judged by assertions that produce the verdicts the rest of the
// product already uses. There is no language model in it and no browser: the
// steps are HTTP requests, because that is what this engine sends. Clicking a
// button is the runner's job and a scenario does not pretend otherwise.
//
// Every step is checked against the manifest's safe list before anything is
// sent, exactly as the mix is. A scenario that names a route nobody declared
// safe does not run at all.

// Scenario is one declared journey.
type Scenario struct {
	// Name identifies the scenario in a report and on the command line.
	Name string `yaml:"scenario"`
	// Description is one line for a person reading the result.
	Description string `yaml:"description,omitempty"`
	// RampMs is the window the sessions start inside.
	//
	// Fifty sessions that all start on the same millisecond are a thundering
	// herd rather than traffic, and they measure the connection pool's
	// behaviour at time zero instead of the application's. Spread over a
	// window, drawn from the seed, they arrive the way sessions arrive.
	//
	// A pointer so that zero can be asked for. Somebody who wants every
	// session to start together is asking for the thundering herd on purpose,
	// which is a real thing to test, and an int would have silently given
	// them the default instead.
	RampMs *int `yaml:"ramp_ms,omitempty"`
	// Steps are the requests, in order.
	Steps []Step `yaml:"steps"`
	// Assertions are what must hold when it is over.
	Assertions []Assertion `yaml:"assertions,omitempty"`
}

// Step is one request, a wait, or a set of requests that overlap.
type Step struct {
	// Request is a method and a path, written the way safe_routes is written:
	// "GET /settings/billing". One spelling for both, so the thing you allow
	// and the thing you send look the same on the page.
	Request string `yaml:"request,omitempty"`
	// ThinkMs is how long to wait after this step before the next one. A
	// journey with no waiting in it is a benchmark, not a journey.
	ThinkMs int `yaml:"think_ms,omitempty"`
	// JitterMs adds up to this much to the wait, drawn from the seed. Without
	// it every session marches in step and the requests arrive in ranks.
	JitterMs int `yaml:"jitter_ms,omitempty"`
	// AfterMs is when a branch inside a parallel block starts, relative to
	// the block. This is what an impatient user is: the same submit again
	// three hundred milliseconds later, while the first is still in flight.
	AfterMs int `yaml:"after_ms,omitempty"`
	// Parallel holds branches that overlap rather than following each other.
	Parallel []Step `yaml:"parallel,omitempty"`
}

// Assertion is one thing that must hold.
//
// Exactly one of the four measures is set. They are deliberately few: every
// one of them is something this engine actually observes, and an assertion
// about a subscription row or a confirmation banner belongs to invariants and
// to workflows, which already exist and already see those things.
type Assertion struct {
	// Name is what the result is called. It is the assertion's identity in a
	// report, so it is required.
	Name string `yaml:"name"`
	// Step scopes the assertion to one route, written the same way a step is.
	// Empty means the whole scenario.
	Step string `yaml:"step,omitempty"`

	// EveryRequestSucceeded requires no transport error and no status at or
	// above 400.
	//
	// Stricter than the mix, on purpose. A 404 inside production's mix is
	// production's own traffic and counting it would report the shape's
	// contents as a failure. A 404 inside a declared journey means the
	// journey is broken.
	EveryRequestSucceeded *bool `yaml:"every_request_succeeded,omitempty"`
	// P95BelowMs requires the ninety fifth percentile to be under this.
	P95BelowMs float64 `yaml:"p95_below_ms,omitempty"`
	// ErrorRateBelow requires the share of failed requests to be under this.
	ErrorRateBelow float64 `yaml:"error_rate_below,omitempty"`
	// StatusIn requires every response to carry one of these codes.
	StatusIn []int `yaml:"status_in,omitempty"`
}

// The verdicts a scenario and its assertions produce.
//
// These are the five words the rest of the product already uses, in
// report.Run.Verdict and in a workflow result. A scenario that invented
// "violated" or "regressed" would give a reader a sixth vocabulary to learn
// for no gain, and a report that mixed the two would be unreadable.
const (
	VerdictPass       = "pass"
	VerdictFail       = "fail"
	VerdictBlocked    = "blocked"
	VerdictUnverified = "unverified"
)

// defaultRampMs is the window sessions start inside when a scenario does not
// say. One second, which is long enough that fifty sessions do not arrive
// together and short enough that a four step journey is not mostly ramp.
const defaultRampMs = 1000

// ParseScenario reads a scenario document.
//
// Unknown fields are refused, for the same reason the manifest refuses them: a
// misspelled key that is silently ignored is a scenario that runs and does not
// do what it says, and the person reading the result has no way to tell.
func ParseScenario(data []byte) (*Scenario, error) {
	var s Scenario
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("this is not a scenario document: %w", err)
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// Validate reports the first thing wrong with a scenario.
func (s *Scenario) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("the scenario has no name; give it a 'scenario:' key")
	}
	if len(s.Steps) == 0 {
		return fmt.Errorf("the scenario %q has no steps", s.Name)
	}
	if s.RampMs != nil && *s.RampMs < 0 {
		return fmt.Errorf("the scenario %q has a negative ramp_ms", s.Name)
	}
	for i, step := range s.Steps {
		if err := validateStep(step, fmt.Sprintf("steps[%d]", i), true); err != nil {
			return fmt.Errorf("the scenario %q is not valid: %w", s.Name, err)
		}
	}
	names := map[string]bool{}
	for i, a := range s.Assertions {
		where := fmt.Sprintf("assertions[%d]", i)
		if strings.TrimSpace(a.Name) == "" {
			return fmt.Errorf("the scenario %q has an unnamed assertion at %s", s.Name, where)
		}
		if names[a.Name] {
			return fmt.Errorf("the scenario %q asserts %q twice", s.Name, a.Name)
		}
		names[a.Name] = true
		if n := a.measures(); n != 1 {
			return fmt.Errorf(
				"the assertion %q sets %d measures and must set exactly one of "+
					"every_request_succeeded, p95_below_ms, error_rate_below or status_in",
				a.Name, n)
		}
		if a.Step != "" {
			if _, err := parseRequest(a.Step); err != nil {
				return fmt.Errorf("the assertion %q names a step that is not a request: %w", a.Name, err)
			}
		}
		if len(a.StatusIn) > 0 && a.Step == "" {
			return fmt.Errorf(
				"the assertion %q checks status codes and does not say which step; "+
					"add a 'step:' naming the request", a.Name)
		}
	}
	return nil
}

// measures counts how many of the four an assertion set.
// Measure names which of the four an assertion asked for, or empty when it
// asks for nothing, which Validate refuses.
//
// The strings are the field names, so a report naming a measure and a manifest
// declaring it use one spelling. A second vocabulary for the same four things
// is one more thing a reader has to learn.
func (a Assertion) Measure() string {
	switch {
	case a.EveryRequestSucceeded != nil:
		return "every_request_succeeded"
	case a.P95BelowMs > 0:
		return "p95_below_ms"
	case a.ErrorRateBelow > 0:
		return "error_rate_below"
	case len(a.StatusIn) > 0:
		return "status_in"
	}
	return ""
}

func (a Assertion) measures() int {
	n := 0
	if a.EveryRequestSucceeded != nil {
		n++
	}
	if a.P95BelowMs > 0 {
		n++
	}
	if a.ErrorRateBelow > 0 {
		n++
	}
	if len(a.StatusIn) > 0 {
		n++
	}
	return n
}

// validateStep checks one step. Parallel blocks do not nest: a block inside a
// block has no meaning the flat form cannot express, and allowing it would
// mean writing a scheduler for a shape nobody asked for.
func validateStep(s Step, where string, topLevel bool) error {
	switch {
	case len(s.Parallel) > 0 && s.Request != "":
		return fmt.Errorf("%s is both a request and a parallel block; it must be one or the other", where)
	case len(s.Parallel) > 0 && !topLevel:
		return fmt.Errorf("%s nests a parallel block inside another one", where)
	case len(s.Parallel) > 0:
		for i, branch := range s.Parallel {
			if err := validateStep(branch, fmt.Sprintf("%s.parallel[%d]", where, i), false); err != nil {
				return err
			}
		}
		return nil
	case s.Request == "":
		return fmt.Errorf("%s has no request", where)
	}
	if _, err := parseRequest(s.Request); err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	if s.ThinkMs < 0 || s.JitterMs < 0 || s.AfterMs < 0 {
		return fmt.Errorf("%s has a negative wait", where)
	}
	if s.AfterMs > 0 && topLevel {
		return fmt.Errorf("%s sets after_ms outside a parallel block, where there is nothing for it to be after", where)
	}
	return nil
}

// parseRequest turns "GET /settings/billing" into a route.
func parseRequest(s string) (Route, error) {
	method, path, ok := strings.Cut(strings.TrimSpace(s), " ")
	if !ok {
		return Route{}, fmt.Errorf(
			"%q is not a request; write a method and a path, as in \"GET /settings/billing\"", s)
	}
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, "/") {
		return Route{}, fmt.Errorf("the path in %q does not start with a slash", s)
	}
	if strings.ContainsAny(path, " \t") {
		return Route{}, fmt.Errorf("the path in %q has a space in it", s)
	}
	return Route{Method: strings.ToUpper(method), Path: path}, nil
}

// Routes returns every route the scenario would send, once each.
//
// This is what the safe list is checked against, before anything runs.
func (s *Scenario) Routes() []Route {
	seen := map[string]bool{}
	var out []Route
	add := func(step Step) {
		r, err := parseRequest(step.Request)
		if err != nil || seen[r.String()] {
			return
		}
		seen[r.String()] = true
		out = append(out, r)
	}
	for _, step := range s.Steps {
		if len(step.Parallel) > 0 {
			for _, branch := range step.Parallel {
				add(branch)
			}
			continue
		}
		add(step)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// PlannedRequest is one request the plan will send.
type PlannedRequest struct {
	// Session is which of the sessions this belongs to.
	Session int
	// Iteration is which pass through the journey this is.
	Iteration int
	// Offset is when it is sent, relative to the scenario starting.
	Offset time.Duration
	// Route is what is sent.
	Route Route
}

// Plan is the whole schedule, in the order it will be sent.
type Plan struct {
	Scenario string
	Requests []PlannedRequest
	// Span is when the last request goes out, which is how long the scenario
	// takes if the application answers instantly.
	Span time.Duration
}

// PlanScenario builds the schedule for a seed.
//
// Separated from sending it so that determinism is a property of a pure
// function and can be tested without a server, a clock or a network. A test
// that asserted two runs sent the same requests by running them twice would be
// asserting something about the machine's scheduler.
//
// Each session draws from its own generator, seeded from the run's seed and
// the session's index. A single shared generator would hand out draws in
// whatever order the goroutines reached it, so the same seed would produce a
// different schedule on a busier machine, which is the exact failure this is
// supposed to rule out.
func PlanScenario(s *Scenario, sessions, iterations int, seed int64, startAfter time.Duration) Plan {
	if sessions <= 0 {
		sessions = 1
	}
	if iterations <= 0 {
		iterations = 1
	}
	ramp := defaultRampMs
	if s.RampMs != nil {
		ramp = *s.RampMs
	}

	plan := Plan{Scenario: s.Name}
	for session := 0; session < sessions; session++ {
		rng := rand.New(rand.NewSource(seed + int64(session)))
		cursor := startAfter
		if ramp > 0 {
			cursor += time.Duration(rng.Intn(ramp)) * time.Millisecond
		}
		for iteration := 0; iteration < iterations; iteration++ {
			for _, step := range s.Steps {
				if len(step.Parallel) > 0 {
					advance := time.Duration(0)
					for _, branch := range step.Parallel {
						route, err := parseRequest(branch.Request)
						if err != nil {
							continue
						}
						at := cursor + time.Duration(branch.AfterMs)*time.Millisecond
						plan.Requests = append(plan.Requests, PlannedRequest{
							Session: session, Iteration: iteration, Offset: at, Route: route,
						})
						if reach := at - cursor + wait(branch, rng); reach > advance {
							advance = reach
						}
					}
					cursor += advance
					continue
				}
				route, err := parseRequest(step.Request)
				if err != nil {
					continue
				}
				plan.Requests = append(plan.Requests, PlannedRequest{
					Session: session, Iteration: iteration, Offset: cursor, Route: route,
				})
				cursor += wait(step, rng)
			}
		}
	}

	// Sorted by when they go out, then by session, so the executor walks one
	// list and two plans for one seed are byte for byte the same regardless of
	// the order the sessions were built in.
	sort.SliceStable(plan.Requests, func(i, j int) bool {
		a, b := plan.Requests[i], plan.Requests[j]
		if a.Offset != b.Offset {
			return a.Offset < b.Offset
		}
		if a.Session != b.Session {
			return a.Session < b.Session
		}
		if a.Iteration != b.Iteration {
			return a.Iteration < b.Iteration
		}
		return a.Route.String() < b.Route.String()
	})
	if n := len(plan.Requests); n > 0 {
		plan.Span = plan.Requests[n-1].Offset
	}
	return plan
}

// wait is how long a session pauses after a step.
func wait(s Step, rng *rand.Rand) time.Duration {
	d := time.Duration(s.ThinkMs) * time.Millisecond
	if s.JitterMs > 0 {
		d += time.Duration(rng.Intn(s.JitterMs)) * time.Millisecond
	}
	return d
}
