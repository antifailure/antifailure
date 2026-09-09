package fidelity_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/fidelity"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The number this lane owes: stores declared rather than silently empty, as a
// fraction of the stores present.
//
// A store the report holds out of the score in both directions is a store the
// reader cannot see either way, and that is what an unmeasured verdict is. So
// the census counts, for the same stack, how many of the stores beside the
// primary database the report puts a verdict on at all, and how many of those
// verdicts describe something this environment DID rather than something the
// manifest said.
//
// HOW THE BEFORE NUMBER WAS PRODUCED, because a before number computed by the
// after code is a simulation of the old instrument rather than a measurement
// of it. testdata/stance-census-before-bfa35d94.txt is what the instrument on
// main printed for the same stack, written by running the recorder below in a
// worktree at bfa35d94. Nothing on this branch can produce that file:
// recordedCensus refuses one that does not carry a sentence this build no
// longer emits, so a rerun of the recorder here fails rather than quietly
// restating the after number as the before.
//
// The two manifests are the same stack and are not byte identical, and that is
// the lane rather than a flaw in the comparison. A broker declared topics_only
// now has to list its topics, because a stance the engine acts on has to say
// enough for the engine to act; on main it could not, because there was
// nowhere to say it. The stores, their engines and their stances are the same
// three either way.

var censusOut = flag.String("census-out", "",
	"write the stance census into this directory, for recording what an older instrument printed")

const censusBefore = "stance-census-before-bfa35d94.txt"

// storeNames is every store the manifest declares beside the primary.
//
// The count below is keyed off THIS rather than off the length of the
// dimension's component list, and the difference is not pedantry. A dimension
// is free to carry a component that is not a store: L8.2 adds one for the
// agreement between two stores, and a store counter reading len(Components)
// would have quietly answered four for a stack with three stores. A number
// about stores is counted from the stores.
func storeNames(obs fidelity.Observation) map[string]bool {
	out := map[string]bool{}
	for _, ds := range obs.Manifest.Datastores {
		if ds.Name != schema.PrimaryDatastore {
			out[ds.Name] = true
		}
	}
	return out
}

// census is what the report says about every declared store beside the
// primary, in one line each and in a stable order.
func census(obs fidelity.Observation, inv fidelity.Inventory) string {
	d, ok := inv.Dimension(schema.FidelityDatastores)
	if !ok {
		return "no datastores dimension\n"
	}
	stores := storeNames(obs)
	lines := make([]string, 0, len(d.Components))
	for _, c := range d.Components {
		if !stores[c.Name] {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s", c.Name, c.State, c.Detail))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}

// scored counts the stores the report puts a verdict on at all.
//
// Measured() is the same predicate the score uses for its denominator, so this
// is literally how many of the three stores are in the number rather than a
// count that resembles it.
func scored(obs fidelity.Observation, inv fidelity.Inventory) (in, total int) {
	d, ok := inv.Dimension(schema.FidelityDatastores)
	if !ok {
		return 0, 0
	}
	stores := storeNames(obs)
	for _, c := range d.Components {
		if !stores[c.Name] {
			continue
		}
		total++
		if c.State.Measured() {
			in++
		}
	}
	return in, total
}

func TestRecordTheStanceCensus(t *testing.T) {
	t.Parallel()
	// Run against an OLDER checkout with -census-out, which is how the before
	// file was made. Here it renders and writes nothing, so a run with no flag
	// is still a check rather than a no op.
	obs := analyticsStack(t)
	text := census(obs, fidelity.Build(obs))
	t.Log("\n" + text)
	require.NotEmpty(t, text)
	if *censusOut != "" {
		require.NoError(t, os.WriteFile(
			filepath.Join(*censusOut, censusBefore), []byte(text), 0o600))
	}
}

// recordedCensus reads the census an older instrument printed and refuses one
// this branch could have written.
//
// The guard is the point, and it is the same one score_test.go makes for its
// own before number. Both files are plain text and a simulation is invisible
// in them. This build cannot say "nothing here created a topic" about a broker
// under any observation, because the sentence was deleted with the behaviour
// it described, so a file carrying it came from the instrument it claims to
// record.
func recordedCensus(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", censusBefore))
	require.NoErrorf(t, err, "%s is missing, so there is no before number", censusBefore)
	text := string(body)
	require.Contains(t, text, "nothing here created a topic",
		"the before census does not carry a sentence this build cannot emit, so it was written here")
	return text
}

func TestTheStanceCensusBeforeAndAfter(t *testing.T) {
	t.Parallel()

	before := recordedCensus(t)
	// Three stores, and the instrument on main put a verdict on exactly one of
	// them: the ClickHouse declared golden, reported absent because nothing
	// had branched it. The other two carried a stance somebody chose and were
	// held out of the number in both directions, which is what unmeasured
	// does, so the report could not be asked whether the twin's cache and
	// broker were in the state the manifest declared.
	require.Equal(t, 3, strings.Count(before, "\n"), "the before census is not three stores:\n"+before)
	require.Equal(t, 1, strings.Count(before, "\tabsent\t"))
	require.Equal(t, 2, strings.Count(before, "\tunmeasured\t"))
	require.Equal(t, 0, strings.Count(before, "\tsubstituted\t"))

	obs := analyticsStack(t)
	after := fidelity.Build(obs)
	in, total := scored(obs, after)
	require.Equal(t, 3, total)

	// The count is keyed off the manifest's stores, so a dimension that gains
	// a component which is not one does not change it. L8.2 adds exactly such
	// a component, for the agreement between two stores, and a counter reading
	// the length of the component list would answer four here.
	d, ok := after.Dimension(schema.FidelityDatastores)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(d.Components), total,
		"the dimension carries fewer components than there are stores")
	// All three, and the two that were invisible are now positions the report
	// takes: substituted, in the denominator, with the declared reason beside
	// them. The golden is still absent, which is the same verdict it had and
	// is correct.
	require.Equal(t, 3, in)

	text := census(obs, after)
	require.Equal(t, 1, strings.Count(text, "\tabsent\t"))
	require.Equal(t, 2, strings.Count(text, "\tsubstituted\t"))
	require.Equal(t, 0, strings.Count(text, "\tunmeasured\t"))
	t.Log("\nBEFORE\n" + before + "\nAFTER\n" + text)
}
