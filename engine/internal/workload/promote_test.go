package workload_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/antifailure/antifailure/engine/internal/explore"
	"github.com/antifailure/antifailure/engine/internal/workload"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func walked(name string) explore.Exploration {
	e := explore.Exploration{
		Name: name, Goal: "upgrade to the team plan", Seed: "seed-one", Reached: true,
		Journey: []explore.Move{
			{Kind: "goto", URL: "http://127.0.0.1:46001/settings/billing"},
			{Kind: "fill", Field: "Card number", Value: "4242424242424242"},
			{Kind: "click", Control: "Upgrade plan"},
		},
		Findings: []explore.Finding{{
			Kind: explore.Kind("no_effect"), URL: "http://127.0.0.1:46001/settings",
			Control: "Manage seats", Detail: "pressing it changes nothing",
		}},
	}
	e.Outcome.Verdict = "pass"
	e.Evidence.Trace = "/tmp/x.trace.zip"
	return e
}

func TestAPromotionSaysWhatCompilationCouldNotCarry(t *testing.T) {
	// The whole point of the document. A person reading "promoted from the
	// exploration that found this" will reasonably assume the two do the same
	// thing, and they do not: the workflow is planned again on every run.
	p, err := workload.Promote(walked("upgrade"), "owner", "")
	require.NoError(t, err)

	require.Equal(t, workload.BrowserWorkflow, p.Kind)
	require.NotEmpty(t, p.Dropped, "there is always something a compilation loses")
	joined := strings.Join(p.Dropped, "\n")
	require.Contains(t, joined, "planned again")
	require.Contains(t, joined, "rather than replayed")
	require.Contains(t, joined, "typed into forms")
	require.Contains(t, joined, "friction finding")
	require.Contains(t, joined, "trace, video and screenshot")

	// And the same list is inside the version body, because it is part of what
	// the version IS. A body that lost it would read as a faithful recording a
	// release later.
	require.Equal(t, p.Dropped, p.Body.Dropped)
}

func TestAPromotionCompilesTheManifestBlockThatActuallyParses(t *testing.T) {
	p, err := workload.Promote(walked("upgrade"), "owner", "")
	require.NoError(t, err)

	var block struct {
		Workflows []schema.Workflow `yaml:"workflows"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(p.Body.ManifestBlock), &block),
		"the block is pasted into somebody's antifailure.yaml, so it has to parse as one")
	require.Len(t, block.Workflows, 1)
	w := block.Workflows[0]
	require.Equal(t, "upgrade", w.Name)
	require.Equal(t, "owner", w.Persona)
	require.Equal(t, []string{"discovered"}, w.Tags)
	require.Equal(t, "/settings/billing", w.StartPath)
	require.NotEmpty(t, w.Expect)

	// The selection names the workflow the block declares, or the promoted
	// version selects a name the repository does not have.
	require.Equal(t, []string{w.Name}, p.Body.Select)
}

func TestAnExplorationThatNeverReachedItsGoalIsRefused(t *testing.T) {
	// Stricter than af explore --emit-workflow on purpose. Printing a block
	// for somebody to read and edit is a different act from writing a version
	// a hosted run executes unattended: the expectation a compiled workflow
	// asserts is the goal sentence, and a wander that never got there is no
	// evidence the goal is reachable at all.
	e := walked("upgrade")
	e.Reached = false
	_, err := workload.Promote(e, "owner", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "AF-WLD-010")
	require.Contains(t, err.Error(), "did not reach its goal")

	blocked := walked("upgrade")
	blocked.Outcome.Verdict = "blocked"
	_, err = workload.Promote(blocked, "owner", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no journey to compile")

	unnamed := walked("")
	_, err = workload.Promote(unnamed, "owner", "")
	require.Error(t, err)
}

func TestAPromotionCarriesTheCommandThatWalksItAgain(t *testing.T) {
	p, err := workload.Promote(walked("upgrade"), "owner", "")
	require.NoError(t, err)
	require.Equal(t,
		[]string{"af", "explore", "--seed", "seed-one", "--only", "upgrade"},
		p.Source.Reproduce.Argv)

	// An explicit seed overrides the exploration's own, for somebody replaying
	// a discovery under a seed the manifest has since changed to.
	q, err := workload.Promote(walked("upgrade"), "owner", "seed-two")
	require.NoError(t, err)
	require.Contains(t, q.Source.Reproduce.Command, "seed-two")
	require.Equal(t, "seed-one", q.Source.Seed,
		"the recorded seed is the one the exploration actually ran under")
}

func TestTheJourneyDigestIgnoresWhatChangesBetweenTwoHonestRuns(t *testing.T) {
	// A digest that moved because a persona generated a different email would
	// report drift on every run and be switched off within a week.
	a := walked("upgrade")
	b := walked("upgrade")
	b.Journey[1].Value = "4000000000000002"
	require.Equal(t, workload.JourneyDigest(a), workload.JourneyDigest(b),
		"a value typed into a field is generated per run and is not part of the route")

	c := walked("upgrade")
	c.Journey[2].Control = "Upgrade now"
	require.NotEqual(t, workload.JourneyDigest(a), workload.JourneyDigest(c),
		"a differently named control is a different route")
}

func TestDriftIsReportedRatherThanInferredFromAPassingWorkflow(t *testing.T) {
	p, err := workload.Promote(walked("upgrade"), "owner", "")
	require.NoError(t, err)

	same := workload.CompareJourney(*p, walked("upgrade"))
	require.True(t, same.SameSeed)
	require.False(t, same.Moved)
	require.True(t, same.StillReaches)
	require.Contains(t, same.Detail, "unchanged")

	moved := walked("upgrade")
	moved.Journey = append(moved.Journey, explore.Move{Kind: "click", Control: "Confirm"})
	d := workload.CompareJourney(*p, moved)
	require.True(t, d.Moved)
	require.Equal(t, 3, d.WasSteps)
	require.Equal(t, 4, d.NowSteps)
	require.Contains(t, d.Detail, "route to the goal has changed")

	gone := walked("upgrade")
	gone.Reached = false
	g := workload.CompareJourney(*p, gone)
	require.False(t, g.StillReaches)
	require.Contains(t, g.Detail, "no longer reachable")
	require.Contains(t, g.Detail, "can still be passing")

	// Two different seeds cannot be compared, and saying so is the point: a
	// drift report over two seeds is noise wearing a finding's clothes.
	other := walked("upgrade")
	other.Seed = "seed-nine"
	o := workload.CompareJourney(*p, other)
	require.False(t, o.SameSeed)
	require.Contains(t, o.Detail, "says nothing about the application")
}

func TestTheBodyDigestIsTheControlPlanesCanonicalForm(t *testing.T) {
	// Two implementations of one digest is a place to drift, so the shape is
	// pinned against a value computed by hand from the documented rule: keys
	// sorted at every depth, absent optional fields omitted, arrays in order.
	//
	// sha256 of {"dropped":["b"],"manifestBlock":"m","select":["a"]}
	body := workload.PromotionBody{
		Select: []string{"a"}, ManifestBlock: "m", Dropped: []string{"b"},
	}
	require.Equal(t,
		"4a09d5d971101834b488654062f29b2087acd00b571f8210a4c0ccd564f7e321",
		workload.BodyDigest(body),
		"this digest is sha256 of the canonical bytes written out by hand in the "+
			"comment above; if it moved, the two sides of the boundary no longer agree")

	// The properties that make it a digest rather than a hash of a struct.
	sameOrder := workload.PromotionBody{
		ManifestBlock: "m", Select: []string{"a"}, Dropped: []string{"b"},
	}
	require.Equal(t, workload.BodyDigest(body), workload.BodyDigest(sameOrder),
		"field order in Go is not part of the definition")

	// An absent optional field is omitted rather than written as an empty
	// array, because the control plane's canonical form drops undefined and a
	// digest that disagreed would make a promotion write a duplicate version.
	withoutDropped := workload.PromotionBody{Select: []string{"a"}, ManifestBlock: "m"}
	require.NotEqual(t, workload.BodyDigest(body), workload.BodyDigest(withoutDropped))

	different := workload.PromotionBody{
		Select: []string{"a"}, ManifestBlock: "m2", Dropped: []string{"b"},
	}
	require.NotEqual(t, workload.BodyDigest(body), workload.BodyDigest(different))
	require.Len(t, workload.BodyDigest(body), 64)
}
