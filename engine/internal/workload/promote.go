package workload

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/explore"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Promotion turns a discovery into a check that runs on every pull request.
//
// THE THING THIS FILE REFUSES TO PRETEND.
//
// A compiled workflow does not replay the exploration. The explorer wandered,
// found a route, and wrote down where it went; the workflow that comes out of
// that is handed to a planner which works out its own route from the same
// starting point towards the same goal. Two runs of the promoted workflow can
// therefore take different paths, and the day the application changes, the
// promoted workflow will take a third one and still pass.
//
// That is not a defect to fix here. It is what makes the workflow survive a
// redesign, which is the whole reason a declared workflow is worth more than a
// recorded one. But a promotion that did not SAY it would be a lie by omission,
// because a person reading "promoted from the exploration that found this" will
// reasonably assume the two do the same thing.
//
// So every promotion carries three things nothing else in this product carries:
// the list of what compilation could not take with it, spelled out one sentence
// each; a digest of the journey the exploration actually walked, so a later
// exploration from the same seed can be compared against it and drift can be
// seen rather than guessed at; and the exact af explore command that produced
// the discovery, so somebody can go and look.

// PromotionSchema names the wire format.
const PromotionSchema = "antifailure.workload.promotion/v1"

// Promotion is a discovery, compiled, with everything the compilation dropped.
type Promotion struct {
	Schema string `json:"schema"`
	// Kind is always browser_workflow. An exploration compiles into a declared
	// workflow and into nothing else: it drives a browser, so it cannot become
	// an HTTP scenario, and it has no request rate, so it cannot become a mix.
	Kind Kind          `json:"kind"`
	Body PromotionBody `json:"body"`
	// BodyDigest is sha256 of the body in the canonical form the control plane
	// uses: keys sorted at every depth, absent fields omitted. It is what
	// answers "is this the same definition" for a promotion that would
	// otherwise write a duplicate version.
	BodyDigest string          `json:"body_digest"`
	Source     PromotionSource `json:"source"`
	// Dropped is what the compilation could not carry over, one sentence each.
	// Never empty, because there is always something.
	Dropped []string `json:"dropped"`
	// Notes are the compiler's own remarks about this exploration, which are
	// about what it saw rather than about what compilation loses.
	Notes []string `json:"notes"`
}

// PromotionBody is the version body the control plane stores, in exactly the
// shape its browser_workflow schema declares.
type PromotionBody struct {
	// Select is the workflow name, which is the exploration's name. It finds
	// nothing until the manifest block below is pasted into the repository,
	// which is why the block is part of the body rather than a note beside it.
	Select []string `json:"select"`
	// ManifestBlock is the YAML a person adds to antifailure.yaml.
	ManifestBlock string `json:"manifestBlock"`
	// Dropped is the same list as the promotion's, carried into the body
	// because it is part of what the version IS. A version that lost it would
	// read as a faithful recording a release later.
	Dropped []string `json:"dropped"`
}

// PromotionSource is where the discovery came from and how to go back to it.
type PromotionSource struct {
	Exploration string `json:"exploration"`
	Goal        string `json:"goal"`
	Seed        string `json:"seed"`
	Reached     bool   `json:"reached"`
	// JourneySteps and JourneyDigest describe the path the exploration walked.
	//
	// The digest is the drift anchor. Re-running the same goal from the same
	// seed against an unchanged application walks the same path and produces
	// the same digest; a different digest means the application moved under
	// the promoted workflow, which is the moment somebody should look at it
	// rather than the moment a green check quietly stops meaning anything.
	JourneySteps  int    `json:"journey_steps"`
	JourneyDigest string `json:"journey_digest"`
	// Findings are what the exploration noticed on the way. They are recorded
	// and deliberately not turned into expectations: "pressing Upgrade plan
	// changes nothing" is a defect to fix, and a workflow asserting it would
	// go green the day somebody broke it differently.
	Findings []string `json:"findings,omitempty"`
	// Reproduce is the af explore command that found this.
	Reproduce Reproduce `json:"reproduce"`
}

// droppedByCompilation is what a promotion always loses, stated once.
//
// A list rather than a paragraph because it goes into a version body and into
// a console, and because each line is a separate thing somebody might
// reasonably have assumed survived.
func droppedByCompilation(e explore.Exploration) []string {
	out := []string{
		"the workflow is planned again from the start path rather than replayed, " +
			"so it can take a different route to the same goal and still pass",
		"the values the exploration typed into forms are not carried over; the " +
			"workflow's persona supplies its own",
		"the exploration's seed does not steer the workflow, because a declared " +
			"workflow makes no random choices to seed",
		"the pages the exploration visited on the way are not asserted, only the " +
			"goal it was looking for",
	}
	if n := len(e.Findings); n > 0 {
		noun := "friction findings are"
		if n == 1 {
			noun = "friction finding is"
		}
		out = append(out, fmt.Sprintf(
			"%d %s recorded and not asserted, because a defect to fix is not an "+
				"outcome to require", n, noun))
	}
	if e.Evidence.Trace != "" || e.Evidence.Video != "" || e.Evidence.Screenshot != "" {
		out = append(out, "the trace, video and screenshot belong to the exploration's own "+
			"run and are not carried into the workflow")
	}
	return out
}

// Promote compiles one exploration into a versioned workflow definition.
//
// It refuses an exploration that did not reach its goal, and that is stricter
// than af explore --emit-workflow, deliberately. Printing a block for somebody
// to read and edit is a different act from writing a version a hosted run will
// execute unattended: the expectation a compiled workflow asserts is the goal
// sentence, and a wander that never got there has no evidence the goal is
// reachable at all. Promoting one would create a check that has never passed
// and cannot say why.
func Promote(e explore.Exploration, persona string, seed string) (*Promotion, error) {
	switch {
	case e.Outcome.Verdict == VerdictBlocked:
		return nil, aferrors.Coded(aferrors.AFWLD010, "exploration", e.Name,
			"detail", "the exploration was blocked, so it has no journey to compile")
	case !e.Reached:
		return nil, aferrors.Coded(aferrors.AFWLD010, "exploration", e.Name,
			"detail", "the exploration did not reach its goal, so the workflow it would "+
				"compile into would assert something nothing has ever shown to be reachable")
	case strings.TrimSpace(e.Name) == "":
		return nil, aferrors.Coded(aferrors.AFWLD010, "exploration", "(unnamed)",
			"detail", "an exploration with no name compiles into a workflow nothing can select")
	}

	workflow, notes := explore.Compile(e, persona)
	block, err := manifestBlock(workflow)
	if err != nil {
		return nil, aferrors.Coded(aferrors.AFWLD010, "exploration", e.Name,
			"detail", "the compiled workflow could not be written as YAML: "+err.Error())
	}

	dropped := droppedByCompilation(e)
	body := PromotionBody{
		Select:        []string{workflow.Name},
		ManifestBlock: block,
		Dropped:       dropped,
	}

	replay := Plan{Kind: Exploration, Select: []string{e.Name}, SeedText: orSeed(seed, e.Seed)}
	p := &Promotion{
		Schema:     PromotionSchema,
		Kind:       BrowserWorkflow,
		Body:       body,
		BodyDigest: BodyDigest(body),
		Source: PromotionSource{
			Exploration:   e.Name,
			Goal:          e.Goal,
			Seed:          e.Seed,
			Reached:       e.Reached,
			JourneySteps:  len(e.Journey),
			JourneyDigest: JourneyDigest(e),
			Findings:      findingLines(e),
			Reproduce: Reproduce{
				Argv:    replay.Argv(),
				Command: replay.Command(),
				Note: "run this from the repository root to walk the same goal from the " +
					"same seed and compare the journey digest",
			},
		},
		Dropped: dropped,
		Notes:   notes,
	}
	return p, nil
}

func orSeed(preferred, fallback string) string {
	if strings.TrimSpace(preferred) != "" {
		return preferred
	}
	return fallback
}

func findingLines(e explore.Exploration) []string {
	out := make([]string, 0, len(e.Findings))
	for _, f := range e.Findings {
		out = append(out, fmt.Sprintf("%s at %s: %s", f.Kind, f.URL, f.Detail))
	}
	return out
}

// JourneyDigest is a stable fingerprint of the path an exploration walked.
//
// The moves rather than the pages, because a page whose address carries a
// session identifier differs between two runs of an unchanged application and
// the moves do not. Values typed into fields are excluded for the same reason:
// a persona's generated address changes per run and would make every journey
// look different from every other.
func JourneyDigest(e explore.Exploration) string {
	h := sha256.New()
	for _, m := range e.Journey {
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\n", m.Kind, m.URL, m.Field, m.Control)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Drift compares a promotion against a fresh exploration of the same goal.
//
// The question it answers is the one a promotion cannot answer on its own: is
// the check still checking what it was promoted for. A promoted workflow is
// planned again on every run, so it goes on passing while the route it was
// found on disappears, and nothing about the workflow's own result would say
// so.
type Drift struct {
	Exploration string `json:"exploration"`
	// SameSeed says whether the two were walked from the same seed. A drift
	// report over two different seeds says nothing, so this is reported rather
	// than assumed.
	SameSeed bool `json:"same_seed"`
	// Moved is true when the journey digests differ.
	Moved bool `json:"moved"`
	// StillReaches says whether the fresh exploration got to the goal at all,
	// which is the more serious of the two answers.
	StillReaches bool   `json:"still_reaches"`
	WasSteps     int    `json:"was_steps"`
	NowSteps     int    `json:"now_steps"`
	WasDigest    string `json:"was_digest"`
	NowDigest    string `json:"now_digest"`
	Detail       string `json:"detail"`
}

// CompareJourney reports whether an exploration has moved since it was
// promoted.
func CompareJourney(p Promotion, now explore.Exploration) Drift {
	d := Drift{
		Exploration:  p.Source.Exploration,
		SameSeed:     p.Source.Seed == now.Seed,
		StillReaches: now.Reached,
		WasSteps:     p.Source.JourneySteps,
		NowSteps:     len(now.Journey),
		WasDigest:    p.Source.JourneyDigest,
		NowDigest:    JourneyDigest(now),
	}
	d.Moved = d.WasDigest != d.NowDigest
	switch {
	case !d.SameSeed:
		d.Detail = "the two explorations were walked from different seeds, so a difference " +
			"between them says nothing about the application"
	case !now.Reached:
		d.Detail = "the goal is no longer reachable from this seed, and the promoted workflow " +
			"is planned again on every run so it can still be passing"
	case d.Moved:
		d.Detail = "the route to the goal has changed since this workflow was promoted"
	default:
		d.Detail = "the route to the goal is unchanged"
	}
	return d
}

// manifestBlock renders the workflow as the YAML somebody pastes.
func manifestBlock(w schema.Workflow) (string, error) {
	body, err := yaml.Marshal(struct {
		Workflows []schema.Workflow `yaml:"workflows"`
	}{Workflows: []schema.Workflow{w}})
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// BodyDigest is sha256 of a version body in the control plane's canonical
// form.
//
// Canonical means keys sorted at every depth and absent fields omitted, which
// is what the control plane's own digestOf does. Two implementations of the
// same digest is a place to drift, so the shape is stated here rather than
// inferred: an object is {"key":value} with keys sorted bytewise, an array is
// [a,b] in order, and everything else is its JSON literal. A round trip test
// against a fixture keeps the two honest.
func BodyDigest(b PromotionBody) string {
	sum := sha256.Sum256([]byte(canonicalJSON(bodyMap(b))))
	return hex.EncodeToString(sum[:])
}

func bodyMap(b PromotionBody) map[string]any {
	m := map[string]any{
		"select":        toAnySlice(b.Select),
		"manifestBlock": b.ManifestBlock,
	}
	// Omitted rather than written as an empty array, because the control plane
	// treats an absent optional field and an empty one as the same definition
	// and its canonical form drops undefined.
	if len(b.Dropped) > 0 {
		m["dropped"] = toAnySlice(b.Dropped)
	}
	return m
}

func toAnySlice(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

func canonicalJSON(v any) string {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			key, _ := json.Marshal(k)
			parts = append(parts, string(key)+":"+canonicalJSON(t[k]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			parts = append(parts, canonicalJSON(e))
		}
		return "[" + strings.Join(parts, ",") + "]"
	default:
		body, err := json.Marshal(v)
		if err != nil {
			return "null"
		}
		return string(body)
	}
}
