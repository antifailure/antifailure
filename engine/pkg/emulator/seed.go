package emulator

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// Creating, inside the emulators, the cloud resources production declares.
//
// THE DEFECT THIS CLOSES. The emulators start EMPTY. Every LocalStack
// container in this build carries PERSISTENCE=0, every Azurite container is
// fresh, and fake-gcs-server is told to keep its backend in memory, all for
// the same good reason: a twin that inherited the last twin's buckets would be
// reproducible only by accident. The consequence nobody had closed is that a
// bucket, a queue, a topic and a table that exist in production existed
// NOWHERE in the twin. An application that reads its own bucket on startup met
// an emulator that had none, and the failure it reported was a missing bucket,
// which is a true sentence about the twin and a false one about production.
//
// WHAT THIS FILE IS, AND WHAT IT IS NOT. It is a PLAN, decided before anything
// runs, and a runner for it. The plan is a pure function of the declarations,
// which is what makes the whole of it testable with no daemon, and it is why
// the honesty below can be asserted rather than hoped for.
//
// THE HONESTY IS THE FEATURE, and it is the half that is easy to skip. An
// emulator honours some of what a declaration says and not the rest:
// fake-gcs-server answers US-CENTRAL1 whatever location a bucket asks for,
// LocalStack stores a lifecycle rule and never expires an object, and no
// emulator here has a KMS key that a production bucket is encrypted with. A
// twin quietly lacking what production has, while reporting success, is the
// exact failure this product exists to prevent. So every attribute a
// declaration carries is accounted for: the ones this build reproduces are
// reproduced and then READ BACK OUT of the emulator, and every other one is
// named in the result with the reason it was not. The accounting is by
// subtraction rather than by a list of known gaps, in Step.unreproduced below,
// so an attribute nobody thought about is reported rather than dropped.
//
// ASKING THE EMULATOR IS THE VERDICT. A create that answered 200 is not
// evidence that anything exists: every action below ends by reading the
// resource back, and an action whose read does not find it is ABSENT with what
// the emulator said, never a pass. The read also runs FIRST, which is what
// makes a second `af up` against a standing environment work: DynamoDB answers
// ResourceInUseException and Secrets Manager answers ResourceExistsException to
// a create for something that is already there, and both of those are the
// resource being present rather than a failure.

// State is what became of one declared thing in the twin.
//
// The vocabulary is internal/fidelity's, deliberately, because these results
// are about to be read by somebody who has just read that report and a second
// set of words for the same five ideas would be a second thing to learn. The
// one that matters is Unmeasured: it is never counted as a pass or as a
// failure, and it always carries the reason.
type State string

const (
	// Unmeasured is a thing whose state could not be determined, or that this
	// build does not attempt. It carries the reason, which is the only thing
	// that makes an unmeasured result useful.
	Unmeasured State = "unmeasured"
	// Absent is a thing the declaration asked for and the emulator does not
	// have after this tried to put it there.
	Absent State = "absent"
	// Refused is a thing the environment's own policy does not route, so the
	// emulator was never asked. The environment is doing what it was told and
	// it still does not reproduce this.
	Refused State = "refused"
	// Substituted is something that stands in and behaves, and is not the
	// thing itself. A secret whose value is a placeholder is the case this
	// build has: production's value must never be copied into a twin, so the
	// secret exists and what it holds does not.
	Substituted State = "substituted"
	// Reproduced is the real thing, created and then read back out of the
	// emulator.
	Reproduced State = "reproduced"
)

// Measured reports whether a state says anything about the twin.
func (s State) Measured() bool { return s != Unmeasured }

// Request is one HTTP request to send to an emulator, at the provider's own
// hostname.
//
// It carries no host: the host is chosen when the plan runs, from the
// candidates the step lists and the routes the environment actually has, so
// one plan is correct in an environment that routes the apex and in one that
// routes the regional spelling.
//
// Path, Query and Body may carry {{name}} placeholders, filled from what an
// earlier request in the same step captured. SQS is why: GetQueueAttributes
// takes the queue URL rather than the queue name, and the queue URL is
// something the emulator hands back rather than something this can compose,
// since composing it would mean writing LocalStack's own account number into
// this build.
type Request struct {
	Method string
	Path   string
	Query  string
	Header map[string]string
	Body   string
	// Capture maps a top level field of a JSON response body to the
	// placeholder name a later request refers to it by.
	Capture map[string]string
}

// Response is what came back.
type Response struct {
	Status int
	Body   string
}

// OK reports whether the emulator answered rather than refused.
func (r Response) OK() bool { return r.Status >= 200 && r.Status < 300 }

// Action is one thing to put into the twin and the read that proves it.
//
// Verify is not optional and there is no way to declare an action without one.
// An action whose verdict came from the create's own status code would report
// a resource as present on the strength of the emulator saying "accepted",
// which is the shape of every instrument this repository has caught measuring
// nothing.
type Action struct {
	// Name is what this puts into the twin, in the words somebody reads: "the
	// bucket", "versioning".
	Name string
	// Create is what puts it there, in order. Empty is legal for an action
	// that only reads, which is how an attribute that the create request
	// already carried is proved separately from the resource itself.
	Create []Request
	// Verify asks the emulator whether it is there. It runs BEFORE Create as
	// well as after, so a second run against a standing environment neither
	// fails on a create that refuses a duplicate nor reports success without
	// looking.
	Verify Request
	// Expect is a substring the verify response body must contain. Empty means
	// any answer in the two hundreds is enough, which is right for a read
	// whose whole answer is the resource.
	Expect string
	// SubstitutedReason, when set, downgrades a successful outcome from
	// reproduced to substituted and says why. A secret exists in the twin and
	// what it holds is a placeholder, and calling that reproduced would be the
	// single most dangerous sentence this package could print.
	SubstitutedReason string
}

// Step is the plan for one declaration, decided before anything runs.
type Step struct {
	Declaration provider.CloudResource
	// Emulator is the emulator that answers for it, empty when none in this
	// build does.
	Emulator string
	// Kind is the emulator's own word for what this is: "bucket", "queue".
	Kind string
	// Hosts are the provider hostnames this could be sent to, in preference
	// order. The first one the environment routes to Emulator is used, and a
	// step none of whose hosts are routed is Refused rather than attempted,
	// because the application would be refused at that host too.
	Hosts []string
	// Actions are what to do, in order.
	Actions []Action
	// Consumed is the set of attribute keys this step accounts for.
	//
	// It is declared rather than derived from the requests, because an
	// attribute can be accounted for without appearing in one: `region`
	// chooses the hostname, and a declaration saying a queue is not FIFO is
	// honoured by not making it one. Everything the declaration carries and
	// this does not name is reported unreproduced, which is the subtraction
	// that makes the accounting complete.
	Consumed []string
	// Reasons carries the reason for one unreproduced attribute, keyed by the
	// attribute. An attribute with no reason here gets the general one, and
	// the entries that are here are the ones where the general sentence would
	// send somebody looking in the wrong place.
	Reasons map[string]string
	// Outcomes are verdicts decided at plan time, which is every attribute
	// this build does not reproduce and, for a resource type nothing here
	// answers for, the resource itself.
	Outcomes []Outcome
}

// Outcome is what became of one named thing.
type Outcome struct {
	// Name is the thing: the resource, or one attribute of it.
	Name  string `json:"name"`
	State State  `json:"state"`
	// Reason is what was found, or for an unmeasured outcome why it could not
	// be. It is never empty.
	Reason string `json:"reason"`
}

// Result is what became of one declaration.
type Result struct {
	Declaration provider.CloudResource `json:"-"`
	Emulator    string                 `json:"emulator,omitempty"`
	Kind        string                 `json:"kind,omitempty"`
	// Host is the hostname the requests were sent to, empty when none were.
	Host     string    `json:"host,omitempty"`
	Outcomes []Outcome `json:"outcomes"`
}

// Worst is the weakest state in the result, which is the verdict for the
// resource as a whole.
//
// The weakest rather than an average, for the reason internal/fidelity gives
// at its own rank function: a reader deciding whether to trust a run needs the
// one thing that was not reproduced, not the mean of the things that were.
func (r Result) Worst() State {
	worst := Reproduced
	for _, o := range r.Outcomes {
		if rankState(o.State) < rankState(worst) {
			worst = o.State
		}
	}
	return worst
}

func rankState(s State) int {
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
	}
	return 0
}

// Lines renders the result for somebody watching `af up`.
//
// One line for the resource and one for each thing that is not reproduced. A
// resource whose every part is reproduced costs ONE line, and a run in which
// everything reproduced prints no reason at all, which is what stops this
// being a message that always prints and therefore says nothing.
func (r Result) Lines() []string {
	head := fmt.Sprintf("%s: %s %q is %s",
		r.Declaration.Type, r.Kind, r.Declaration.Name, r.Worst())
	if r.Emulator != "" {
		head = fmt.Sprintf("%s: %s %q is %s in the %s emulator",
			r.Declaration.Type, r.Kind, r.Declaration.Name, r.Worst(), r.Emulator)
	}
	out := []string{head}
	for _, o := range r.Outcomes {
		if o.State == Reproduced {
			continue
		}
		out = append(out, fmt.Sprintf("  %s is %s: %s", o.Name, o.State, o.Reason))
	}
	return out
}

// planner turns one declaration into the plan for it.
type planner func(d provider.CloudResource) Step

// seedPlanners holds what this build knows how to create, keyed by the
// provider's own resource type.
//
// Registered from each cloud's own file in an init, exactly as the emulators
// themselves are, so that three lanes adding three clouds do not all edit one
// map literal and meet in a merge.
var seedPlanners = map[string]planner{}

func registerSeed(resourceType string, p planner) {
	if _, dup := seedPlanners[resourceType]; dup {
		panic(fmt.Sprintf("emulator: two seed planners are registered for %q", resourceType))
	}
	seedPlanners[resourceType] = p
}

// SeedTypes lists the resource types this build creates inside an emulator.
func SeedTypes() []string {
	out := make([]string, 0, len(seedPlanners))
	for t := range seedPlanners {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// PlanSeeding turns declarations into the plan for creating them.
//
// Every declaration produces a step, including one whose type nothing in this
// build answers for. A declaration silently absent from the plan would be a
// resource missing from the twin that nothing ever mentioned, which is the
// failure this whole file exists to remove rather than to relocate.
func PlanSeeding(declarations []provider.CloudResource) []Step {
	steps := make([]Step, 0, len(declarations))
	for _, d := range declarations {
		p, ok := seedPlanners[d.Type]
		if !ok {
			steps = append(steps, Step{
				Declaration: d,
				Outcomes: []Outcome{{
					Name:  d.Name,
					State: Unmeasured,
					Reason: fmt.Sprintf(
						"no emulator in this build creates a %s, so the twin does not have "+
							"this one and nothing here measured what production has", d.Type),
				}},
			})
			continue
		}
		step := p(d)
		step.Declaration = d
		step.Outcomes = append(step.Outcomes, unreproduced(step)...)
		steps = append(steps, step)
	}
	return steps
}

// consumed is the step's Consumed field as a set.
func (s Step) consumed() map[string]bool {
	out := make(map[string]bool, len(s.Consumed))
	for _, k := range s.Consumed {
		out[k] = true
	}
	return out
}

// unreproduced names every attribute the step does not account for.
func unreproduced(s Step) []Outcome {
	used := s.consumed()
	keys := make([]string, 0, len(s.Declaration.Attributes))
	for k := range s.Declaration.Attributes {
		if used[k] {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Outcome, 0, len(keys))
	for _, k := range keys {
		reason, ok := s.Reasons[k]
		if !ok {
			reason = fmt.Sprintf(
				"this build does not reproduce it, so the %s in the twin does not carry it",
				s.Kind)
		}
		out = append(out, Outcome{
			Name:   k,
			State:  Unmeasured,
			Reason: fmt.Sprintf("%s (production declares %s)", reason, s.Declaration.Attributes[k]),
		})
	}
	return out
}

// Send issues one request inside the environment, to a provider hostname.
//
// The transport belongs to the caller because there is only one that proves
// anything: the route the application itself has. A Send that dialled the
// emulator's container address directly would create the bucket and prove
// nothing about whether the application can reach it.
type Send func(ctx context.Context, host string, r Request) (Response, error)

// Seeder runs a plan.
type Seeder struct {
	// Send issues one request.
	Send Send
	// Routes reports whether the environment's own egress policy sends a
	// hostname TO THIS EMULATOR, which is two questions and not one. A host
	// that is routed somewhere else is not routed here, and seeding it would
	// create a bucket inside whatever other emulator answers for it.
	//
	// A nil Routes accepts every host, which is what a test with one emulator
	// and no policy wants.
	Routes func(emulatorName, host string) bool
}

// Run carries out the plan and returns what became of each declaration.
//
// It never returns an error. A step that could not be done is a result saying
// so, because the alternative is an `af up` that fails because a lifecycle
// rule could not be reproduced, and the twin is more useful with the bucket in
// it and the rule named as missing than it is not existing at all.
func (s Seeder) Run(ctx context.Context, steps []Step) []Result {
	out := make([]Result, 0, len(steps))
	for _, step := range steps {
		out = append(out, s.runStep(ctx, step))
	}
	return out
}

func (s Seeder) runStep(ctx context.Context, step Step) Result {
	res := Result{Declaration: step.Declaration, Emulator: step.Emulator, Kind: step.Kind}
	if len(step.Actions) == 0 {
		res.Outcomes = append(res.Outcomes, step.Outcomes...)
		return res
	}
	host, routed := s.host(step.Emulator, step.Hosts)
	if !routed {
		res.Outcomes = append(res.Outcomes, Outcome{
			Name:  step.Declaration.Name,
			State: Refused,
			Reason: fmt.Sprintf(
				"no egress rule in this environment routes %s to the %s emulator, so the "+
					"application would be refused at that hostname too",
				strings.Join(step.Hosts, " or "), step.Emulator),
		})
		res.Outcomes = append(res.Outcomes, step.Outcomes...)
		return res
	}
	res.Host = host
	captured := map[string]string{}
	for _, a := range step.Actions {
		res.Outcomes = append(res.Outcomes, s.runAction(ctx, host, a, captured))
	}
	res.Outcomes = append(res.Outcomes, step.Outcomes...)
	return res
}

// host picks the first candidate the environment routes to this emulator.
func (s Seeder) host(emulatorName string, candidates []string) (string, bool) {
	for _, h := range candidates {
		if s.Routes == nil || s.Routes(emulatorName, h) {
			return h, true
		}
	}
	return "", false
}

// runAction reads, creates if the read did not find it, and reads again.
func (s Seeder) runAction(
	ctx context.Context, host string, a Action, captured map[string]string,
) Outcome {
	if resp, err := s.ask(ctx, host, a.Verify, captured); err == nil &&
		resp.OK() && strings.Contains(resp.Body, a.Expect) {
		// An action with nothing to create is one whose value went into the
		// twin with the resource, and it is read back separately so that its
		// verdict is its own. Saying "already present" about it would be true
		// and would send a reader looking for a previous run that never
		// happened.
		if len(a.Create) == 0 {
			return a.outcome("read back out of the emulator")
		}
		return a.outcome("it was already present in the emulator and was read back")
	}
	// A create that fails is not the verdict, and this is deliberate rather
	// than lax. The read below decides, and the commonest failure here is a
	// create refused BECAUSE the thing is already there: DynamoDB answers
	// ResourceInUseException and Secrets Manager answers ResourceExistsException
	// to a duplicate, and treating either as fatal would break a second `af up`
	// against a standing environment. What the create said is carried into the
	// message only when the read then does not find it.
	var refusal string
	for _, req := range a.Create {
		resp, err := s.ask(ctx, host, req, captured)
		switch {
		case err != nil:
			refusal = err.Error()
		case !resp.OK():
			refusal = fmt.Sprintf("%d %s", resp.Status, oneLine(resp.Body))
		}
	}
	resp, err := s.ask(ctx, host, a.Verify, captured)
	switch {
	case err != nil:
		return Outcome{
			Name:  a.Name,
			State: Unmeasured,
			Reason: fmt.Sprintf(
				"the emulator could not be asked whether it is there: %v", err),
		}
	case !resp.OK():
		return Outcome{
			Name:  a.Name,
			State: Absent,
			Reason: fmt.Sprintf("the emulator answered %d to the read that would find it: %s%s",
				resp.Status, oneLine(resp.Body), because(refusal)),
		}
	case !strings.Contains(resp.Body, a.Expect):
		return Outcome{
			Name:  a.Name,
			State: Absent,
			Reason: fmt.Sprintf("the emulator answered without %q, which is what would say it "+
				"is there: %s%s", a.Expect, oneLine(resp.Body), because(refusal)),
		}
	}
	return a.outcome("created in the emulator and read back out of it")
}

// outcome is the verdict for an action that succeeded, which is reproduced
// unless the action itself says it stands in for something.
func (a Action) outcome(detail string) Outcome {
	if a.SubstitutedReason != "" {
		return Outcome{Name: a.Name, State: Substituted, Reason: a.SubstitutedReason}
	}
	return Outcome{Name: a.Name, State: Reproduced, Reason: detail}
}

// ask fills the request's placeholders, sends it, and records what it captured.
func (s Seeder) ask(
	ctx context.Context, host string, r Request, captured map[string]string,
) (Response, error) {
	if r.Method == "" {
		return Response{}, fmt.Errorf("the request has no method")
	}
	filled := r
	filled.Path = fill(r.Path, captured)
	filled.Query = fill(r.Query, captured)
	filled.Body = fill(r.Body, captured)
	resp, err := s.Send(ctx, host, filled)
	if err != nil {
		return resp, err
	}
	capture(r.Capture, resp.Body, captured)
	return resp, nil
}

// capture reads the named top level fields out of a JSON response body.
//
// A body that is not JSON, or a field that is not there, captures nothing and
// is not an error: the placeholder then stays unfilled, the request that used
// it does not find what it was looking for, and the action reports absent with
// the emulator's own answer. Failing here instead would replace a sentence
// about the twin with a sentence about a parser.
func capture(fields map[string]string, body string, into map[string]string) {
	if len(fields) == 0 {
		return
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		return
	}
	for field, name := range fields {
		if v, ok := decoded[field].(string); ok {
			into[name] = v
		}
	}
}

// fill replaces {{name}} with what was captured under that name.
func fill(s string, captured map[string]string) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	for name, value := range captured {
		s = strings.ReplaceAll(s, "{{"+name+"}}", value)
	}
	return s
}

// because carries what the create said into a message about the read.
//
// Only when the read failed. A create's refusal is noise when the thing is
// there anyway, and it is the whole explanation when it is not.
func because(refusal string) string {
	if refusal == "" {
		return ""
	}
	return ". Creating it said: " + refusal
}

// oneLine is a body short enough to sit in a progress line.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const limit = 160
	if len(s) > limit {
		return s[:limit] + "..."
	}
	return s
}
