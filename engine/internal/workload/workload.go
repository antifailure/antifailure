// Package workload runs a hosted workload definition through the engine.
//
// THE ONE DESIGN DECISION THIS PACKAGE EXISTS TO DEFEND.
//
// Four things can be run against a preview environment and they are not four
// flavours of one thing:
//
//	observed_load     a weighted mix compiled from OTLP or access logs and sent
//	                  at a scale of production's rate. It has routes and
//	                  percentiles and no order.
//	http_scenario     an ordered journey with waits, sessions and assertions.
//	                  It has an order and it has no browser.
//	browser_workflow  a declared workflow driven through a real browser. It has
//	                  steps and a verdict and no request rate.
//	exploration       a seeded wander with a goal, which produces findings
//	                  rather than a pass.
//
// The marketing site implies a single scenario intermediate representation
// that all four compile into. There is no such thing, and building one here to
// make a console's job easier would make the claim structural rather than
// merely wrong. So Kind is an enum, each kind parses its own knobs, each kind
// executes through the command that already exists for it, and each kind
// projects its own measurements. Nothing is shared except the envelope.
//
// WHY A COMMAND RATHER THAN THE SHELL CASE STATEMENT IN THE WORKFLOW.
//
// The adapter before this one was a `case "$AF_COMMAND"` block in
// examples/github-workflow.yml. It had no structured output, so a hosted run
// learned nothing about what happened beyond an exit code. It had no way to
// report a knob it could not honour, so an input the command has no flag for
// was accepted and dropped. And it had no reproducible command, so a person
// looking at a hosted result had no way to get the same run on their own
// machine. One command replaces it and is the single place each of those three
// is answered.
//
// WHY THE REPRODUCIBLE COMMAND IS A PLAIN af COMMAND.
//
// Every result carries the argv that reproduces it, and that argv is
// `af load run ...` or `af test ...`, never `af workload run ...`. A hosted
// result whose command only another hosted caller can run proves nothing. The
// consequence is a constraint on this package rather than a feature of it: a
// knob may exist here only if the plain command has a flag for it, which is
// why every other knob is refused rather than defaulted.
package workload

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// Kind is one of the four things a workload can be.
//
// The strings are the control plane's `workload_kind` enum spelled exactly as
// the database spells them. One vocabulary across the boundary rather than two
// with a translation table between, because a translation table is a place to
// be wrong that has no reason to exist.
type Kind string

const (
	// ObservedLoad is a weighted mix compiled from production telemetry.
	ObservedLoad Kind = "observed_load"
	// HTTPScenario is a declared journey with assertions.
	HTTPScenario Kind = "http_scenario"
	// BrowserWorkflow is a declared workflow driven through a browser.
	BrowserWorkflow Kind = "browser_workflow"
	// Exploration is a seeded wander with a goal.
	Exploration Kind = "exploration"
)

// Kinds is every kind, in the order the control plane's enum declares them.
func Kinds() []Kind {
	return []Kind{ObservedLoad, HTTPScenario, BrowserWorkflow, Exploration}
}

// legacyKinds maps the verbs the dispatch workflow used before this package
// existed onto the kinds they meant.
//
// Kept because a repository whose antifailure.yml was copied months ago still
// dispatches those words, and a hosted button that stops working when the
// engine is upgraded is worse than a second spelling. The result says which
// kind an alias resolved to, so nobody has to guess from the verb.
var legacyKinds = map[string]Kind{
	"load":     ObservedLoad,
	"scenario": HTTPScenario,
	"agents":   BrowserWorkflow,
	"explore":  Exploration,
}

// ParseKind reads a kind, accepting the legacy dispatch verbs.
func ParseKind(s string) (Kind, bool) {
	s = strings.TrimSpace(s)
	for _, k := range Kinds() {
		if string(k) == s {
			return k, true
		}
	}
	if k, ok := legacyKinds[s]; ok {
		return k, true
	}
	return "", false
}

// Request is what the control plane sends, in the shape a workflow_dispatch
// input arrives in: flat strings, every one of them possibly empty.
//
// Strings rather than typed fields on purpose. GitHub delivers workflow inputs
// as strings and the shell that reads them cannot type them either, so parsing
// them here is parsing the real thing rather than something already cleaned up
// by a layer that would have had to guess.
type Request struct {
	// RunID is the control plane's identifier for this execution. Opaque here
	// and echoed back, so a result can be matched to the row that asked for it
	// without the engine knowing what a run row is.
	RunID string
	// Kind selects which of the four commands runs.
	Kind string
	// Select is the comma separated names the command's --only takes.
	Select string
	// Duration is a Go duration, for observed_load only.
	Duration string
	// Scale multiplies production's rate, for observed_load only.
	Scale string
	// Seed is an integer for http_scenario and free text for exploration.
	Seed string
	// Concurrency bounds requests in flight, for http_scenario only.
	Concurrency string
}

// Plan is a request that has been read, with every knob resolved to a concrete
// value and the argv that reproduces it.
//
// Every knob is resolved even when the request left it empty, and the argv
// states it explicitly rather than relying on the flag's default. A command
// line that omits a flag reproduces whatever that flag's default is on the day
// it is run, which is not the same promise as reproducing this run.
type Plan struct {
	RunID string
	Kind  Kind
	// Select is the names passed to --only. Empty means every one, which is
	// legal for browser_workflow alone.
	Select []string
	// Duration and Scale are observed_load's.
	Duration time.Duration
	Scale    float64
	// SeedNumber is http_scenario's --seed. SeedText is exploration's, which
	// takes free text rather than a number.
	SeedNumber int64
	SeedText   string
	// Concurrency is http_scenario's.
	Concurrency int
	// Attempts is browser_workflow's, fixed at the command's own default
	// because no hosted definition may set it yet. Stated rather than implied
	// so the argv can name it.
	Attempts int
	// Refusals is every knob the request set that this kind's command has no
	// flag for. A plan with refusals never runs.
	Refusals []Refusal
}

// Refusal is a knob that was sent and cannot be honoured.
//
// Reported rather than dropped. A hosted definition that says concurrency 200
// and runs at 20 is a run that did not do what its author wrote, and the
// person reading the result has no way to tell. This repository has shipped
// that shape before: load.thresholds.query_count_increase is accepted by the
// manifest, defaulted to 0.2, and evaluated by nothing.
type Refusal struct {
	// Knob is the input's name, spelled the way the control plane spells it.
	Knob string `json:"knob"`
	// Code is the stable identifier for why.
	Code string `json:"code"`
	// Reason is one sentence naming the flag that does not exist.
	Reason string `json:"reason"`
}

// Defaults every plan resolves to when the request leaves a knob empty. They
// are the plain command's own flag defaults, read from the command tree by
// TestTheEmittedArgvSetsExactlyThePlansKnobs rather than trusted here.
const (
	defaultDuration    = 60 * time.Second
	defaultScale       = 1.0
	defaultSeed        = 1
	defaultConcurrency = 20
	defaultAttempts    = 2
)

// maxSelected bounds a selection. The control plane bounds it at fifty and so
// does this, because a command line assembled from an unbounded list is a
// command line somebody can make too long to execute.
const maxSelected = 50

// Parse reads a request into a plan, or refuses it.
//
// Strict on the write boundary. A knob this kind's command has no flag for is
// an error rather than something quietly ignored, and a value that is not the
// shape the flag takes is an error rather than a silent fallback to a default.
// The manifest parser makes the same decision with KnownFields, for the same
// reason: a misspelled key that is dropped produces a run whose result nobody
// can interpret.
func Parse(req Request) (*Plan, error) {
	kind, ok := ParseKind(req.Kind)
	if !ok {
		return nil, aferrors.Coded(aferrors.AFWLD001,
			"kind", strings.TrimSpace(req.Kind),
			"known", strings.Join(kindNames(), ", "))
	}

	p := &Plan{RunID: strings.TrimSpace(req.RunID), Kind: kind}

	names, err := parseSelection(req.Select)
	if err != nil {
		return nil, err
	}
	p.Select = names

	// THE RULE, and it is the only rule: a knob is refused exactly when the
	// plain command this kind runs has no flag for it.
	//
	// Not "when it seems wrong", and not "when the hosted body has no field
	// for it yet". Tying refusal to the command's own flag set is what makes
	// the argv in every result a promise rather than a hope: a knob with a
	// flag is stated on that command line and reproduces, and a knob without
	// one could only ever have been silently dropped.
	//
	// TestARefusalIsExactlyAMissingFlag reads the real command tree and
	// asserts this table against it, so a flag added or removed upstream moves
	// this decision in the same commit.
	switch kind {
	case ObservedLoad:
		// af load run sends whatever load.source points at. There is no
		// selection because there is no --only.
		if len(names) > 0 {
			p.Refusals = append(p.Refusals, refuse("workflows", kind,
				"af load run has no --only flag; the mix is whatever load.source points at"))
		}
		if trimmed(req.Concurrency) != "" {
			p.Refusals = append(p.Refusals, refuse("concurrency", kind,
				"af load run has no --concurrency flag, and load.Options.Concurrency is "+
					"left at its own default of 20 by every caller"))
		}
		p.Duration, err = parseDuration(req.Duration)
		if err != nil {
			return nil, err
		}
		p.Scale, err = parseScale(req.Scale)
		if err != nil {
			return nil, err
		}
		p.SeedNumber, err = parseSeedNumber(req.Seed)
		if err != nil {
			return nil, err
		}
	case HTTPScenario:
		if len(names) == 0 {
			return nil, aferrors.Coded(aferrors.AFWLD004, "kind", string(kind),
				"detail", "af load scenario with no --only runs every scenario in the "+
					"manifest, which is a different run from the one that was saved")
		}
		p.refuseLoadShape(req, kind)
		p.SeedNumber, err = parseSeedNumber(req.Seed)
		if err != nil {
			return nil, err
		}
		p.Concurrency, err = parseConcurrency(req.Concurrency)
		if err != nil {
			return nil, err
		}
	case BrowserWorkflow:
		// Empty is legal and means every workflow, which is what af test with
		// no --only does and what af ci does.
		p.Attempts = defaultAttempts
		p.refuseLoadShape(req, kind)
		if trimmed(req.Seed) != "" {
			p.Refusals = append(p.Refusals, refuse("seed", kind,
				"af test has no --seed flag; a workflow is planned rather than sampled"))
		}
		if trimmed(req.Concurrency) != "" {
			p.Refusals = append(p.Refusals, refuse("concurrency", kind,
				"af test has no --concurrency flag"))
		}
	case Exploration:
		if len(names) == 0 {
			return nil, aferrors.Coded(aferrors.AFWLD004, "kind", string(kind),
				"detail", "af explore with no --only walks every goal in the manifest, "+
					"which is a different run from the one that was saved")
		}
		p.refuseLoadShape(req, kind)
		// Free text rather than a number, because that is what af explore's
		// own --seed takes: the manifest declares a seed as a string and the
		// runner hashes it. Parsing it as an integer here would refuse the
		// seeds the manifest itself carries.
		p.SeedText = trimmed(req.Seed)
		if trimmed(req.Concurrency) != "" {
			p.Refusals = append(p.Refusals, refuse("concurrency", kind,
				"af explore has no --concurrency flag"))
		}
	}

	if len(p.Refusals) > 0 {
		sort.Slice(p.Refusals, func(i, j int) bool { return p.Refusals[i].Knob < p.Refusals[j].Knob })
		return p, aferrors.Coded(aferrors.AFWLD002,
			"kind", string(kind), "knobs", knobList(p.Refusals))
	}
	return p, nil
}

// refuseLoadShape refuses the two knobs only the mix has.
func (p *Plan) refuseLoadShape(req Request, kind Kind) {
	if trimmed(req.Duration) != "" {
		p.Refusals = append(p.Refusals, refuse("duration", kind,
			"only af load run takes --duration; this kind's length is what it declares"))
	}
	if trimmed(req.Scale) != "" {
		p.Refusals = append(p.Refusals, refuse("scale", kind,
			"only af load run takes --scale; there is no production rate to multiply here"))
	}
}

func refuse(knob string, kind Kind, reason string) Refusal {
	return Refusal{
		Knob:   knob,
		Code:   string(aferrors.AFWLD002),
		Reason: fmt.Sprintf("the %s kind cannot set %s: %s", kind, knob, reason),
	}
}

func knobList(rs []Refusal) string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Knob)
	}
	return strings.Join(out, ", ")
}

func kindNames() []string {
	out := make([]string, 0, len(Kinds()))
	for _, k := range Kinds() {
		out = append(out, string(k))
	}
	return out
}

func trimmed(s string) string { return strings.TrimSpace(s) }

// parseSelection splits the comma separated names the --only flags take.
//
// Empty entries are dropped rather than passed on, because the shell that used
// to assemble this list produced them from a trailing comma and `--only ""`
// selects a workflow whose name is the empty string, which is no workflow at
// all and reports as a selection that matched nothing.
func parseSelection(raw string) ([]string, error) {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if strings.ContainsAny(name, ",\"") {
			// af load scenario declares --only as a StringSlice, which reads
			// its values as CSV: a comma splits one name into two and a quote
			// changes where the split lands. A name carrying either would mean
			// something different to the scenario command than to test and
			// explore, and the argv promise is that it means the same thing.
			return nil, aferrors.Coded(aferrors.AFWLD003,
				"knob", "workflows", "value", name,
				"detail", "a name cannot carry a comma or a quote; the scenario command reads its selection as CSV")
		}
		if strings.HasPrefix(name, "-") {
			// A name beginning with a dash would be read by the plain command
			// as a flag rather than as a value, so the argv this package
			// promises reproduces the run would not reproduce it.
			return nil, aferrors.Coded(aferrors.AFWLD003,
				"knob", "workflows", "value", name,
				"detail", "a name cannot begin with a dash; the command would read it as a flag")
		}
		out = append(out, name)
	}
	if len(out) > maxSelected {
		return nil, aferrors.Coded(aferrors.AFWLD003,
			"knob", "workflows", "value", strconv.Itoa(len(out))+" names",
			"detail", "at most "+strconv.Itoa(maxSelected)+" may be selected")
	}
	return out, nil
}

func parseDuration(raw string) (time.Duration, error) {
	raw = trimmed(raw)
	if raw == "" {
		return defaultDuration, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, aferrors.Coded(aferrors.AFWLD003, "knob", "duration", "value", raw,
			"detail", "expected a Go duration such as 60s or 2m")
	}
	// The manifest validator caps a declared duration at fifteen minutes and
	// this cap is the same number for the same reason: a hosted button that
	// can start an hour of traffic against somebody's preview environment is a
	// bill nobody agreed to.
	if d <= 0 || d > 15*time.Minute {
		return 0, aferrors.Coded(aferrors.AFWLD003, "knob", "duration", "value", raw,
			"detail", "must be above zero and at most 15m")
	}
	return d, nil
}

func parseScale(raw string) (float64, error) {
	raw = trimmed(raw)
	if raw == "" {
		return defaultScale, nil
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, aferrors.Coded(aferrors.AFWLD003, "knob", "scale", "value", raw,
			"detail", "expected a decimal multiplier such as 0.1 or 2")
	}
	// NaN before the range check, and it is not a theoretical case. ParseFloat
	// accepts "NaN" and "nan", and every comparison against a NaN is false, so
	// a range check written as `f <= 0 || f > 100` lets it straight through
	// and the rate becomes NaN requests per second. The test that found this
	// was sending deliberately malformed input from the control plane.
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, aferrors.Coded(aferrors.AFWLD003, "knob", "scale", "value", raw,
			"detail", "expected a finite decimal multiplier")
	}
	if f <= 0 || f > 100 {
		return 0, aferrors.Coded(aferrors.AFWLD003, "knob", "scale", "value", raw,
			"detail", "must be above zero and at most 100")
	}
	return f, nil
}

func parseSeedNumber(raw string) (int64, error) {
	raw = trimmed(raw)
	if raw == "" {
		return defaultSeed, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, aferrors.Coded(aferrors.AFWLD003, "knob", "seed", "value", raw,
			"detail", "expected a whole number of zero or more")
	}
	return n, nil
}

func parseConcurrency(raw string) (int, error) {
	raw = trimmed(raw)
	if raw == "" {
		return defaultConcurrency, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 500 {
		return 0, aferrors.Coded(aferrors.AFWLD003, "knob", "concurrency", "value", raw,
			"detail", "expected a whole number between 1 and 500")
	}
	return n, nil
}
