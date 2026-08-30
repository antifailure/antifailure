package report_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func warning(rule string) report.Finding {
	return report.Finding{Rule: rule, Level: report.LevelWarn, Title: "something to look at"}
}

func failure(rule string) report.Finding {
	return report.Finding{Rule: rule, Level: report.LevelFail, Title: "something that stops the merge"}
}

func TestVerdict_AWarningDoesNotFailTheCheck(t *testing.T) {
	t.Parallel()
	// The whole point of the level. Before it existed, a real finding either
	// failed the build or was printed and forgotten.
	r := run(report.Workflow{Name: "checkout", Verdict: "pass"})
	r.Findings = []report.Finding{warning("migration_lock")}
	require.Equal(t, report.VerdictWarn, r.Verdict())
	require.Contains(t, r.Headline(), "Nothing failed")
}

func TestVerdict_AFailingFindingFailsTheCheck(t *testing.T) {
	t.Parallel()
	r := run(report.Workflow{Name: "checkout", Verdict: "pass"})
	r.Findings = []report.Finding{warning("migration_lint"), failure("egress_surprise")}
	require.Equal(t, report.VerdictFail, r.Verdict())
	// Named rather than counted, because no workflow and no invariant failed
	// and "1 workflow failed" would be a lie.
	require.Equal(t, "something that stops the merge", r.Headline())
}

func TestVerdict_AnIgnoredFindingChangesNothing(t *testing.T) {
	t.Parallel()
	r := run(report.Workflow{Name: "checkout", Verdict: "pass"})
	r.Findings = []report.Finding{{Rule: "migration_lint", Level: report.LevelIgnore, Title: "hidden"}}
	require.Equal(t, report.VerdictPass, r.Verdict())
	require.NotContains(t, r.Markdown(), "hidden")
}

func TestVerdict_AFailedWorkflowStillOutranksAWarning(t *testing.T) {
	t.Parallel()
	r := run(report.Workflow{Name: "checkout", Verdict: "fail"})
	r.Findings = []report.Finding{warning("plan_regression")}
	require.Equal(t, report.VerdictFail, r.Verdict())
}

func TestVerdict_AWarningOutranksBlocked(t *testing.T) {
	t.Parallel()
	// Blocked is a fact about us. A warning is a fact about the change, and
	// reporting the first over the second buries the only actionable half.
	r := run(report.Workflow{Name: "checkout", Verdict: "blocked"})
	r.Findings = []report.Finding{warning("migration_rewrite")}
	require.Equal(t, report.VerdictWarn, r.Verdict())
}

func TestVerdict_AWarningWithNoWorkflowsSaysSo(t *testing.T) {
	t.Parallel()
	r := run()
	r.Findings = []report.Finding{warning("migration_lock")}
	require.Equal(t, report.VerdictWarn, r.Verdict())
	require.Contains(t, r.Headline(), "No workflows ran")
}

func TestMarkdown_FindingsAreWorstFirstAndKeepTheirOrderWithin(t *testing.T) {
	t.Parallel()
	r := run(report.Workflow{Name: "checkout", Verdict: "pass"})
	r.Findings = []report.Finding{
		{Rule: "migration_lint", Level: report.LevelWarn, Title: "first warning"},
		{Rule: "migration_rewrite", Level: report.LevelWarn, Title: "second warning"},
		{Rule: "egress_surprise", Level: report.LevelFail, Title: "the failure"},
	}
	out := r.Markdown()
	require.Less(t, strings.Index(out, "the failure"), strings.Index(out, "first warning"))
	require.Less(t, strings.Index(out, "first warning"), strings.Index(out, "second warning"))
}

func TestMarkdown_AFindingCarriesItsRuleNameRatherThanACode(t *testing.T) {
	t.Parallel()
	// The stable identifier for a finding is the rule name, which is also the
	// manifest key that decides what the finding does.
	r := run(report.Workflow{Name: "checkout", Verdict: "pass"})
	r.Findings = []report.Finding{{
		Rule: "not_null_without_default", Level: report.LevelWarn,
		Title: "NOT NULL column added with no default", Where: "orders",
		Detail: "the table holds 2100000 rows", Fix: "add the column nullable first",
	}}
	out := r.Markdown()
	require.Contains(t, out, "`not_null_without_default`")
	require.Contains(t, out, "on `orders`")
	require.Contains(t, out, "Instead: add the column nullable first")
}

func TestMarkdown_AWarningExplainsWhereTheThresholdLives(t *testing.T) {
	t.Parallel()
	r := run(report.Workflow{Name: "checkout", Verdict: "pass"})
	r.Findings = []report.Finding{warning("migration_lock")}
	require.Contains(t, r.Markdown(), "`policy` block")
}

func TestConfigure_ANilBlockIsTheGateTheProductDescribes(t *testing.T) {
	t.Parallel()
	p := report.Configure(nil)
	require.Equal(t, report.LevelFail, p.EgressSurprise)
	require.Equal(t, report.LevelFail, p.Cleanup)
	require.Equal(t, report.LevelFail, p.Masking)
	require.Equal(t, report.LevelFail, p.MigrationFailed)
	require.Equal(t, report.LevelWarn, p.MigrationLint)
	require.Equal(t, report.LevelWarn, p.MigrationRewrite)
	require.Equal(t, report.LevelWarn, p.PlanRegression)
	require.Equal(t, report.LevelWarn, p.QueryRegression)
	require.Equal(t, report.LevelWarn, p.LoadRegression)
	require.EqualValues(t, report.DefaultLockWarnMS, p.LockWarnMS)
	require.EqualValues(t, report.DefaultLockFailMS, p.LockFailMS)
}

func TestConfigure_TheManifestOverridesEveryLevel(t *testing.T) {
	t.Parallel()
	p := report.Configure(&schema.Policy{
		MigrationLock:    &schema.LockPolicy{WarnMS: 100, FailMS: 400},
		MigrationFailed:  schema.PolicyWarn,
		MigrationRewrite: schema.PolicyFail,
		MigrationLint:    schema.PolicyIgnore,
		PlanRegression:   schema.PolicyFail,
		QueryRegression:  schema.PolicyIgnore,
		LoadRegression:   schema.PolicyFail,
		EgressSurprise:   schema.PolicyWarn,
		Masking:          schema.PolicyWarn,
		Cleanup:          schema.PolicyIgnore,
	})
	require.Equal(t, report.LevelWarn, p.MigrationFailed)
	require.Equal(t, report.LevelFail, p.MigrationRewrite)
	require.Equal(t, report.LevelIgnore, p.MigrationLint)
	require.Equal(t, report.LevelFail, p.PlanRegression)
	require.Equal(t, report.LevelIgnore, p.QueryRegression)
	require.Equal(t, report.LevelFail, p.LoadRegression)
	require.Equal(t, report.LevelWarn, p.EgressSurprise)
	require.Equal(t, report.LevelWarn, p.Masking)
	require.Equal(t, report.LevelIgnore, p.Cleanup)
	require.EqualValues(t, 100, p.LockWarnMS)
	require.EqualValues(t, 400, p.LockFailMS)
}

func TestConfigure_AnUnrecognisedLevelKeepsTheDefault(t *testing.T) {
	t.Parallel()
	// The manifest validator refuses it first. This is the second line: a
	// value nobody recognises must never quietly become the weakest one.
	p := report.Configure(&schema.Policy{EgressSurprise: schema.PolicyLevel("block")})
	require.Equal(t, report.LevelFail, p.EgressSurprise)
}

func TestLockLevel_ThresholdsAreInclusiveAndOrdered(t *testing.T) {
	t.Parallel()
	p := report.Configure(nil)
	require.Equal(t, report.LevelIgnore, p.LockLevel(499))
	require.Equal(t, report.LevelWarn, p.LockLevel(500))
	require.Equal(t, report.LevelWarn, p.LockLevel(1999))
	require.Equal(t, report.LevelFail, p.LockLevel(2000))
	require.Equal(t, report.LevelFail, p.LockLevel(9000))
}

func TestPolicyDefaults_MirrorTheManifestPackage(t *testing.T) {
	t.Parallel()
	// They are stated twice, so a test has to hold them equal. The insights
	// thresholds drifted exactly this way once: the manifest normalised to one
	// pair and the package used another, and af explain printed a figure no
	// caller used.
	require.EqualValues(t, manifest.DefaultLockWarnMS, report.DefaultLockWarnMS)
	require.EqualValues(t, manifest.DefaultLockFailMS, report.DefaultLockFailMS)

	m, err := manifest.Parse([]byte("version: 1\nname: shop\nservices:\n  - name: web\n    port: 3000\n"),
		"antifailure.yaml", "")
	require.NoError(t, err)
	p := report.Configure(m.Policy)
	require.EqualValues(t, manifest.DefaultLockWarnMS, p.LockWarnMS)
	require.EqualValues(t, manifest.DefaultLockFailMS, p.LockFailMS)
	require.Equal(t, report.LevelFail, p.EgressSurprise)
	require.Equal(t, report.LevelWarn, p.MigrationLint)
}
