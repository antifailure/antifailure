package fidelity_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/fidelity"
	"github.com/antifailure/antifailure/engine/internal/volume"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The defect this lane closes, stated as tests.
//
// dataComponent reported a branch as Reproduced with "N tables over M rows"
// and never compared it against production. A golden built from a staging
// database holding two hundred rows in events scored exactly the same as one
// built from a production holding four billion, in the same words and with the
// same verdict. Everything it printed was true and the verdict was not,
// because there was no denominator anywhere in the engine.

func productionProfile(tables ...volume.Table) *volume.Profile {
	return &volume.Profile{
		CollectedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		Source:      "the database named by PRODUCTION_DATABASE_URL",
		Tables:      tables,
	}
}

func productionTable(name string, rows int64) volume.Table {
	return volume.Table{Name: name, Rows: rows, Analyzed: true}
}

// The headline case. Two hundred rows against four billion is not production.
func TestData_TwoHundredRowsAgainstFourBillionIsASubstitutionAndSaysTheFraction(t *testing.T) {
	t.Parallel()
	obs := full()
	obs.Tables, obs.Rows = 1, 200
	obs.Branch = []volume.TableRows{{Name: "public.events", Rows: 200}}
	obs.Volume = productionProfile(productionTable("public.events", 4_200_000_000))

	c := componentState(t, fidelity.Build(obs), schema.FidelityDatabase, "data")
	require.Equal(t, fidelity.Substituted, c.State,
		"a branch holding two hundred of production's four billion rows scored as reproducing it")
	require.Contains(t, c.Detail, "200 rows against production's 4,200,000,000 rows")
	require.Contains(t, c.Detail, "0.0000047 percent")
	require.Contains(t, c.Detail, "lower bound and not a prediction")
}

// A branch holding production's rows still reports Reproduced, and now says
// what it was measured against rather than asserting it.
func TestData_AFullCopyIsStillAReproductionAndNamesWhatItWasMeasuredAgainst(t *testing.T) {
	t.Parallel()
	obs := full()
	obs.Tables, obs.Rows = 1, 4_198_000_000
	obs.Branch = []volume.TableRows{{Name: "public.events", Rows: 4_198_000_000}}
	obs.Volume = productionProfile(productionTable("public.events", 4_200_000_000))

	c := componentState(t, fidelity.Build(obs), schema.FidelityDatabase, "data")
	require.Equal(t, fidelity.Reproduced, c.State,
		"a faithful copy was demoted, which would make the check something people turn off")
	require.Contains(t, c.Detail, "Measured against the volume profile collected on 2026-09-01")
	require.Contains(t, c.Detail, "99 percent")
	require.NotContains(t, c.Detail, "lower bound",
		"a copy that holds production's rows does not carry the lower bound warning")
}

// With no profile the answer is not a smaller pass. Nothing has been shown.
func TestData_WithNoProfileTheVerdictIsUnmeasuredRatherThanReproduced(t *testing.T) {
	t.Parallel()
	obs := full()
	obs.Branch, obs.Volume = nil, nil

	c := componentState(t, fidelity.Build(obs), schema.FidelityDatabase, "data")
	require.Equal(t, fidelity.Unmeasured, c.State,
		"a branch nothing was compared against was reported as reproducing production")
	require.Contains(t, c.Detail, "12 tables over 184,000 rows")
	require.Contains(t, c.Detail, "no volume profile says what production holds")
	require.Contains(t, c.Detail, "af volume record")
}

// An unmeasured component is in neither half of the score, and the exclusion
// carries the reason. That is the difference between "we did not check" and
// "we checked and it failed", and reporting the first as the second is how a
// report stops being believed.
func TestData_AnUnmeasuredDataComponentLeavesTheScoreAndIsNamed(t *testing.T) {
	t.Parallel()
	obs := full()
	obs.Branch, obs.Volume = nil, nil
	score := fidelity.Build(obs).Score()

	var found string
	for _, e := range score.Excluded {
		if e.Dimension == schema.FidelityDatabase && e.Component == "data" {
			found = e.Because
		}
	}
	require.Contains(t, found, "no volume profile says what production holds",
		"the data component was counted rather than excluded, or excluded without a reason")
}

// The reason a stale profile produced travels into the report unchanged, so
// somebody reading it is told the profile expired rather than told nothing.
func TestData_AStaleProfilesReasonIsWhatTheReportCarries(t *testing.T) {
	t.Parallel()
	obs := full()
	obs.Branch, obs.Volume = nil, nil
	obs.VolumeReason = "the volume profile at .antifailure/volume.json was collected 90 days " +
		"ago, past the 30 days it is allowed to be, so production's row counts are not known"

	c := componentState(t, fidelity.Build(obs), schema.FidelityDatabase, "data")
	require.Equal(t, fidelity.Unmeasured, c.State)
	require.Contains(t, c.Detail, "past the 30 days it is allowed to be")
}

// A subset was already a substitution and stays one. What the profile adds is
// the fraction, which is what somebody wanted when they asked how production
// shaped the slice was.
func TestData_ASubsetKeepsItsVerdictAndGainsTheFraction(t *testing.T) {
	t.Parallel()
	obs := full()
	obs.Subset = true
	obs.Branch = []volume.TableRows{{Name: "public.events", Rows: 1_000_000}}
	obs.Volume = productionProfile(productionTable("public.events", 4_200_000_000))

	c := componentState(t, fidelity.Build(obs), schema.FidelityDatabase, "data")
	require.Equal(t, fidelity.Substituted, c.State)
	require.Contains(t, c.Detail, "production shaped slice")
	require.Contains(t, c.Detail, "0.023 percent")
}

// A branch that could not be counted has nothing to compare, and the one
// unknown is reported once rather than twice.
func TestData_ABranchThatCouldNotBeCountedIsNotAlsoAVolumeFailure(t *testing.T) {
	t.Parallel()
	obs := full()
	obs.BranchReason = "the branch could not be counted: connection refused"
	obs.Volume = productionProfile(productionTable("public.events", 4_200_000_000))

	c := componentState(t, fidelity.Build(obs), schema.FidelityDatabase, "data")
	require.Equal(t, fidelity.Unmeasured, c.State)
	require.Equal(t, obs.BranchReason, c.Detail,
		"one unknown was reported as two")
}

// A profile naming nothing this branch also has answers no question about it,
// and it is not evidence either way.
func TestData_AProfileWithNoTableInCommonIsNotEvidence(t *testing.T) {
	t.Parallel()
	obs := full()
	obs.Branch = []volume.TableRows{{Name: "public.events", Rows: 10}}
	obs.Volume = productionProfile(productionTable("analytics.events", 4_200_000_000))

	c := componentState(t, fidelity.Build(obs), schema.FidelityDatabase, "data")
	require.Equal(t, fidelity.Unmeasured, c.State)
	require.Contains(t, c.Detail, "names no table this branch also has")
}
