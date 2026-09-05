package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/golden"
	"github.com/antifailure/antifailure/engine/internal/verify"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// mineProvenance is the provenance string a fake provider stamps on this
// project's own versions.
const mineProvenance = "test-project@1"

func goldensFrom(versions ...provider.GoldenVersion) readGoldens {
	return func(context.Context) ([]provider.GoldenVersion, string, error) {
		return versions, mineProvenance, nil
	}
}

func noPublished() readPublishedGoldens {
	return func(context.Context) ([]golden.Object, string, error) { return nil, "", nil }
}

func policyOf(retain int, maxAge time.Duration) readGoldenPolicy {
	return func() (env.GoldenPolicy, error) {
		return env.GoldenPolicy{Retain: retain, MaxAge: maxAge}, nil
	}
}

// ---------------------------------------------------------------------------
// inspect_goldens
// ---------------------------------------------------------------------------

func TestInspectGoldens_AnotherProjectsVersionIsNotBranchable(t *testing.T) {
	t.Parallel()
	// The pool is shared. Two unrelated projects that declare no masking rules
	// hash their rules to the same value, so provenance is the only field that
	// can answer whether a version may be branched here, and a listing that
	// showed somebody else's as usable is what this exists to prevent.
	now := time.Now()
	tool := newInspectGoldensTool(testProject(t),
		goldensFrom(provider.GoldenVersion{
			ID: "gv_20260830044013_74234e98", CreatedAt: now,
			Verified: true, Provenance: "some-other-project@1",
		}),
		noPublished(), policyOf(3, 0))

	out := mustInvoke(t, tool, `{"project_id":"test-project"}`).(goldensResult)

	require.Len(t, out.Versions, 1)
	require.False(t, out.Versions[0].Mine)
	require.Empty(t, out.Branchable,
		"a version made for another project must never be offered as branchable")
	require.Contains(t, out.Summary, "NONE of them can be branched")
}

func TestInspectGoldens_AnUnverifiedVersionIsNotBranchable(t *testing.T) {
	t.Parallel()
	// A golden that failed verification is never published, so it cannot be
	// branched, so no environment can hold it. Reporting it as available would
	// be offering something the engine itself refuses.
	tool := newInspectGoldensTool(testProject(t),
		goldensFrom(provider.GoldenVersion{
			ID: "gv_20260830044013_74234e98", CreatedAt: time.Now(),
			Verified: false, Provenance: mineProvenance,
		}),
		noPublished(), policyOf(3, 0))

	out := mustInvoke(t, tool, `{"project_id":"test-project"}`).(goldensResult)

	require.Empty(t, out.Branchable)
	require.True(t, out.Versions[0].Mine)
	require.False(t, out.Versions[0].Verified)
}

func TestInspectGoldens_NamesTheNewestVerifiedVersionThisProjectCanBranch(t *testing.T) {
	t.Parallel()
	now := time.Now()
	tool := newInspectGoldensTool(testProject(t),
		goldensFrom(
			provider.GoldenVersion{
				ID: "gv_20260801000000_aaaaaaaa", CreatedAt: now.Add(-72 * time.Hour),
				Verified: true, Provenance: mineProvenance,
			},
			provider.GoldenVersion{
				ID: "gv_20260830044013_74234e98", CreatedAt: now,
				Verified: true, Provenance: mineProvenance,
			},
		),
		noPublished(), policyOf(3, 0))

	out := mustInvoke(t, tool, `{"project_id":"test-project"}`).(goldensResult)

	require.Equal(t, "gv_20260830044013_74234e98", out.Branchable,
		"newest first, so the branchable one is the freshest copy of production")
}

func TestInspectGoldens_ReportsAnUnreadableStoreAsUnavailableAndNotAsEmpty(t *testing.T) {
	t.Parallel()
	// An empty list and an unreachable store look identical and mean opposite
	// things. Reporting the second as the first says "nothing is published"
	// about a store nobody could read.
	tool := newInspectGoldensTool(testProject(t),
		goldensFrom(provider.GoldenVersion{
			ID: "gv_20260830044013_74234e98", CreatedAt: time.Now(),
			Verified: true, Provenance: mineProvenance,
		}),
		func(context.Context) ([]golden.Object, string, error) {
			return nil, "", errors.New("the bucket refused the request")
		},
		policyOf(3, 0))

	out := mustInvoke(t, tool, `{"project_id":"test-project"}`).(goldensResult)

	require.NotEmpty(t, out.PublishedUnavailable)
	require.Empty(t, out.Published)
	require.Contains(t, out.Summary, "could not be read")
}

func TestInspectGoldens_SaysWhenTheNewestCopyIsStale(t *testing.T) {
	t.Parallel()
	tool := newInspectGoldensTool(testProject(t),
		goldensFrom(provider.GoldenVersion{
			ID: "gv_20260801000000_aaaaaaaa", CreatedAt: time.Now().Add(-100 * time.Hour),
			Verified: true, Provenance: mineProvenance,
		}),
		noPublished(), policyOf(3, 24*time.Hour))

	out := mustInvoke(t, tool, `{"project_id":"test-project"}`).(goldensResult)

	require.NotNil(t, out.Policy)
	require.True(t, out.Policy.Stale)
	require.Contains(t, out.Summary, "behind production")
}

func TestInspectGoldens_SaysNothingCanBeBroughtUpWhenThereAreNoGoldens(t *testing.T) {
	t.Parallel()
	tool := newInspectGoldensTool(testProject(t), goldensFrom(), noPublished(), policyOf(3, 0))

	out := mustInvoke(t, tool, `{"project_id":"test-project"}`).(goldensResult)

	require.Contains(t, out.Summary, "no goldens at all")
	require.Empty(t, out.Branchable)
}

// ---------------------------------------------------------------------------
// prepare_golden
// ---------------------------------------------------------------------------

func TestPrepareGolden_VerifyWithoutAVersionIsRefusedAtSubmission(t *testing.T) {
	t.Parallel()
	// Refused here rather than minutes later inside the run, because a bad
	// argument reported as a failed experiment costs a caller the whole wait.
	tool := newPrepareGoldenTool(testProject(t), nil,
		func(context.Context, string, string) (goldenOutcome, error) {
			t.Fatal("nothing must be started for a call that cannot be carried out")
			return goldenOutcome{}, nil
		})

	_, fault := invoke(t, tool, `{"project_id":"test-project","action":"verify"}`)

	require.NotNil(t, fault)
	require.Equal(t, FaultInvalidArgument, fault.Code)
	require.Equal(t, "version", fault.Field)
}

func TestPrepareGolden_RefreshCannotBePointedAtAnExistingVersion(t *testing.T) {
	t.Parallel()
	tool := newPrepareGoldenTool(testProject(t), nil,
		func(context.Context, string, string) (goldenOutcome, error) {
			t.Fatal("nothing must be started for a call that cannot be carried out")
			return goldenOutcome{}, nil
		})

	_, fault := invoke(t, tool,
		`{"project_id":"test-project","action":"refresh","version":"gv_20260830044013_74234e98"}`)

	require.NotNil(t, fault)
	require.Equal(t, "version", fault.Field)
}

func TestPrepareGolden_RefusesAnActionThatIsNotOneOfTheThree(t *testing.T) {
	t.Parallel()
	tool := newPrepareGoldenTool(testProject(t), nil, nil)

	_, fault := invoke(t, tool, `{"project_id":"test-project","action":"publish"}`)

	require.NotNil(t, fault)
	require.Equal(t, FaultInvalidArgument, fault.Code)
}

func TestGoldenBody_NeverReproducesWhatTheDetectorsMatched(t *testing.T) {
	t.Parallel()
	// The value a detector matched is by definition the unmasked production
	// data this whole subsystem exists to keep out of a copy. A result that
	// quoted it would be the leak, in a document a model reads.
	body := goldenBody(goldenOutcome{
		Action: "refresh", Version: "gv_20260830044013_74234e98", Verified: false,
		Report: verify.Report{
			Tables: 12, Columns: 40, RowsSampled: 4000, SampleSize: 100,
			Findings: []verify.Finding{{
				Schema: "public", Table: "users", Column: "email",
				Detector: "email", Example: "ada@real-customer.example", Rows: 97,
			}},
		},
	})

	raw, err := json.Marshal(body)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "ada@real-customer.example",
		"the matched value is production data and must not reach a result")
	require.Contains(t, string(raw), "public.users")
	require.Contains(t, string(raw), "email")
	require.Contains(t, body.Summary, "NOT published")
}

func TestGoldenBody_TreatsAColumnNobodyCouldReadAsNotAPass(t *testing.T) {
	t.Parallel()
	// A column nobody could read is not a column that passed. The engine's own
	// Clean() counts a skip, and this result has to say so rather than
	// reporting zero findings as a clean bill of health.
	body := goldenBody(goldenOutcome{
		Action: "verify", Version: "gv_20260830044013_74234e98", Verified: false,
		Report: verify.Report{
			Tables: 12, Columns: 40, RowsSampled: 4000,
			Skipped: []string{"public.orders.notes: could not be read"},
		},
	})

	require.Contains(t, body.Summary, "could not read")
	require.Contains(t, body.Summary, "not branchable")

	var skipped bool
	for _, m := range body.Metrics {
		if m.Name == "columns_not_readable" {
			skipped = m.Breached
		}
	}
	require.True(t, skipped, "an unreadable column must breach rather than pass silently")
}

func TestGoldenBody_SaysAVerifiedVersionIsBranchable(t *testing.T) {
	t.Parallel()
	body := goldenBody(goldenOutcome{
		Action: "refresh", Version: "gv_20260830044013_74234e98", Verified: true,
		Rows: 40000, Tables: 12,
		Report: verify.Report{Tables: 12, Columns: 40, RowsSampled: 4000},
	})

	require.Contains(t, body.Summary, "verified")
	require.Contains(t, body.Summary, "can branch environments from it")
}

// ---------------------------------------------------------------------------
// remove_old_goldens
// ---------------------------------------------------------------------------

// destroyRecorder counts real deletions, so a test can prove none happened.
type destroyRecorder struct {
	deleted []string
	err     error
}

func (d *destroyRecorder) destroy(_ context.Context, version string) error {
	if d.err != nil {
		return d.err
	}
	d.deleted = append(d.deleted, version)
	return nil
}

func manyOwnGoldens(n int) []provider.GoldenVersion {
	now := time.Now()
	out := make([]provider.GoldenVersion, 0, n)
	ids := []string{
		"gv_20260901000000_aaaaaaaa", "gv_20260831000000_bbbbbbbb",
		"gv_20260830000000_cccccccc", "gv_20260829000000_dddddddd",
		"gv_20260828000000_eeeeeeee",
	}
	for i := 0; i < n && i < len(ids); i++ {
		out = append(out, provider.GoldenVersion{
			ID: ids[i], CreatedAt: now.Add(-time.Duration(i) * 24 * time.Hour),
			Verified: true, Provenance: mineProvenance,
		})
	}
	return out
}

func TestRemoveOldGoldens_PlansByDefaultAndDeletesNothing(t *testing.T) {
	t.Parallel()
	rec := &destroyRecorder{}
	tool := newRemoveOldGoldensTool(testProject(t),
		goldensFrom(manyOwnGoldens(4)...), policyOf(2, 0), rec.destroy)

	out := mustInvoke(t, tool, `{"project_id":"test-project"}`).(goldenSweepResult)

	require.True(t, out.Planned)
	require.Empty(t, rec.deleted, "nothing may be deleted without a confirmation")
	require.NotEmpty(t, out.ConfirmWith)
	require.Contains(t, out.Summary, "Nothing has been deleted")
}

func TestRemoveOldGoldens_NeverConsidersAnotherProjectsVersions(t *testing.T) {
	t.Parallel()
	// The pool is shared. Sweeping every version on the machine destroys
	// another repository's copies and enforces one retention count across all
	// of them, which is what running this in one repository used to do.
	rec := &destroyRecorder{}
	versions := append(manyOwnGoldens(2), provider.GoldenVersion{
		ID: "gv_20260701000000_ffffffff", CreatedAt: time.Now().Add(-500 * time.Hour),
		Verified: true, Provenance: "some-other-project@1",
	})
	tool := newRemoveOldGoldensTool(testProject(t),
		goldensFrom(versions...), policyOf(1, 0), rec.destroy)

	out := mustInvoke(t, tool, `{"project_id":"test-project"}`).(goldenSweepResult)

	require.Equal(t, 1, out.OtherProjects)
	for _, id := range out.ConfirmWith {
		require.NotEqual(t, "gv_20260701000000_ffffffff", id,
			"another project's version must never be offered for deletion")
	}
	for _, d := range out.Decisions {
		require.NotEqual(t, "gv_20260701000000_ffffffff", d.Version)
	}
}

func TestRemoveOldGoldens_NeverProposesTheNewestVerifiedVersion(t *testing.T) {
	t.Parallel()
	// A project with nothing left to branch cannot bring an environment up at
	// all, which is worse than the disk it saved. The rule belongs to
	// golden.Sweep; what this proves is that the tool defers to it rather than
	// deciding retention for itself.
	rec := &destroyRecorder{}
	tool := newRemoveOldGoldensTool(testProject(t),
		goldensFrom(manyOwnGoldens(3)...), policyOf(1, 0), rec.destroy)

	out := mustInvoke(t, tool, `{"project_id":"test-project"}`).(goldenSweepResult)

	require.NotContains(t, out.ConfirmWith, "gv_20260901000000_aaaaaaaa",
		"the newest verified version is never removed, whatever the count says")
}

func TestRemoveOldGoldens_CarriesOutAMatchingConfirmation(t *testing.T) {
	t.Parallel()
	rec := &destroyRecorder{}
	read := goldensFrom(manyOwnGoldens(4)...)
	tool := newRemoveOldGoldensTool(testProject(t), read, policyOf(2, 0), rec.destroy)

	plan := mustInvoke(t, tool, `{"project_id":"test-project"}`).(goldenSweepResult)
	require.NotEmpty(t, plan.ConfirmWith)

	body, err := json.Marshal(map[string]any{
		"project_id": "test-project", "confirm_versions": plan.ConfirmWith,
	})
	require.NoError(t, err)

	out := mustInvoke(t, tool, string(body)).(goldenSweepResult)

	require.False(t, out.Planned)
	require.Equal(t, len(plan.ConfirmWith), out.Removed)
	require.Equal(t, plan.ConfirmWith, rec.deleted)
}

func TestRemoveOldGoldens_RefusesAConfirmationThatDoesNotMatchThePlan(t *testing.T) {
	t.Parallel()
	rec := &destroyRecorder{}
	tool := newRemoveOldGoldensTool(testProject(t),
		goldensFrom(manyOwnGoldens(4)...), policyOf(2, 0), rec.destroy)

	_, fault := invoke(t, tool,
		`{"project_id":"test-project","confirm_versions":["gv_20260901000000_aaaaaaaa"]}`)

	require.NotNil(t, fault)
	require.Equal(t, FaultInvalidArgument, fault.Code)
	require.Equal(t, "confirm_versions", fault.Field)
	require.Empty(t, rec.deleted, "a refused confirmation must delete nothing")
	require.Contains(t, fault.Detail, "gv_20260901000000_aaaaaaaa",
		"the refusal has to name the version that is not in the plan")
	require.Contains(t, fault.Detail, "version",
		"a refusal about goldens must not talk about environments")
}

func TestRemoveOldGoldens_ReportsTheProvidersRefusalAgainstTheVersion(t *testing.T) {
	t.Parallel()
	// Almost always something still branched from it, which is the provider's
	// refusal and the only place that knows. Reported per version so somebody
	// can tear that environment down.
	rec := &destroyRecorder{err: errors.New("AF-DB-005: an environment is branched from it")}
	read := goldensFrom(manyOwnGoldens(4)...)
	tool := newRemoveOldGoldensTool(testProject(t), read, policyOf(2, 0), rec.destroy)

	plan := mustInvoke(t, tool, `{"project_id":"test-project"}`).(goldenSweepResult)
	body, err := json.Marshal(map[string]any{
		"project_id": "test-project", "confirm_versions": plan.ConfirmWith,
	})
	require.NoError(t, err)

	out := mustInvoke(t, tool, string(body)).(goldenSweepResult)

	require.Equal(t, 0, out.Removed)
	require.Equal(t, len(plan.ConfirmWith), out.Refused)
	require.Contains(t, out.Summary, "refused")
}

func TestRemoveOldGoldens_IsDeclaredDestructive(t *testing.T) {
	t.Parallel()
	tool := newRemoveOldGoldensTool(testProject(t), nil, nil, nil)
	require.False(t, tool.ReadOnly)
	require.True(t, tool.Destructive)
}

func TestEveryGoldenToolRequiresTheProjectAssertion(t *testing.T) {
	t.Parallel()
	p := testProject(t)
	cases := []struct {
		tool *Tool
		body string
	}{
		{newInspectGoldensTool(p, goldensFrom(), noPublished(), policyOf(3, 0)),
			`{"project_id":"some-other-project"}`},
		{newPrepareGoldenTool(p, nil,
			func(context.Context, string, string) (goldenOutcome, error) {
				return goldenOutcome{}, nil
			}),
			`{"project_id":"some-other-project","action":"pull"}`},
		{newRemoveOldGoldensTool(p, goldensFrom(), policyOf(3, 0),
			(&destroyRecorder{}).destroy),
			`{"project_id":"some-other-project"}`},
	}
	for _, c := range cases {
		require.Contains(t, c.tool.Input.Required, "project_id", "tool %s", c.tool.Name)

		_, fault := invoke(t, c.tool, c.body)
		require.NotNil(t, fault, "tool %s answered a call naming another project", c.tool.Name)
		require.Equal(t, FaultProjectMismatch, fault.Code, "tool %s", c.tool.Name)
	}
}

func TestEveryGoldenToolBoundsEveryArgument(t *testing.T) {
	t.Parallel()
	p := testProject(t)
	for _, tool := range []*Tool{
		newInspectGoldensTool(p, nil, nil, nil),
		newPrepareGoldenTool(p, nil, nil),
		newRemoveOldGoldensTool(p, nil, nil, nil),
	} {
		requireBounded(t, tool.Name, tool.Input)
	}
}
