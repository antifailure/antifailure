package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeUpgradeRunner returns a canned outcome or error and records the check
// argument, so the tool is exercised without a network, a release or a binary
// to swap, and a test can prove the check flag reached the runner.
type fakeUpgradeRunner struct {
	outcome UpgradeOutcome
	err     error
	called  bool
	check   bool
}

func (f *fakeUpgradeRunner) run(_ context.Context, check bool) (UpgradeOutcome, error) {
	f.called = true
	f.check = check
	return f.outcome, f.err
}

func callUpgrade(t *testing.T, run upgradeRunner, args map[string]any) (map[string]any, *Fault) {
	t.Helper()
	tool := newUpgradeTool(&Project{ID: "repo"}, run)
	out, fault := tool.Handler(context.Background(), &Call{Caller: "cli", Project: "repo"}, args)
	if fault != nil {
		return nil, fault
	}
	doc, ok := out.(map[string]any)
	require.True(t, ok, "the projection is a document")
	return doc, nil
}

func TestUpgrade_CheckReportsBothVersionsAndChangesNothing(t *testing.T) {
	// The runner is made to claim it applied and to carry a path, so the
	// projection's own guarantee that a check changes nothing is what is under
	// test rather than a runner that happened to return false.
	fake := &fakeUpgradeRunner{outcome: UpgradeOutcome{
		Current: "v1.5.1", Latest: "v1.6.0", Applied: true, Path: "/usr/local/bin/af",
	}}

	doc, fault := callUpgrade(t, fake.run, map[string]any{"project_id": "repo", "check": true})
	require.Nil(t, fault)

	require.True(t, fake.called)
	require.True(t, fake.check, "the check flag reaches the runner")
	require.Equal(t, "upgrade", doc["kind"])
	require.Equal(t, "v1.5.1", doc["current_version"])
	require.Equal(t, "v1.6.0", doc["latest_version"])
	// A check looks and changes nothing, whatever the runner returned.
	require.Equal(t, false, doc["applied"])
	require.Equal(t, false, doc["restart_required"])
	require.NotContains(t, doc, "installed_path", "a check reports no install path")
	require.Contains(t, doc["summary"], "without the check option",
		"a newer release tells the caller how to install it")
}

func TestUpgrade_CheckOnAlreadyLatestSaysSo(t *testing.T) {
	fake := &fakeUpgradeRunner{outcome: UpgradeOutcome{
		Current: "v1.6.0", Latest: "v1.6.0", Applied: false, Path: "/usr/local/bin/af",
	}}

	doc, fault := callUpgrade(t, fake.run, map[string]any{"project_id": "repo", "check": true})
	require.Nil(t, fault)
	require.Equal(t, false, doc["applied"])
	require.Contains(t, doc["summary"], "latest stable release")
	require.Contains(t, doc["summary"], "Nothing to upgrade")
}

func TestUpgrade_AppliedSetsRestartRequired(t *testing.T) {
	fake := &fakeUpgradeRunner{outcome: UpgradeOutcome{
		Current: "v1.5.1", Latest: "v1.6.0", Applied: true, Path: "/usr/local/bin/af",
	}}

	doc, fault := callUpgrade(t, fake.run, map[string]any{"project_id": "repo"})
	require.Nil(t, fault)

	require.False(t, fake.check, "the default action is not a check")
	require.Equal(t, true, doc["applied"])
	require.Equal(t, true, doc["restart_required"], "a swap the running server has not picked up needs a restart")
	require.Equal(t, "/usr/local/bin/af", doc["installed_path"])
	require.Contains(t, doc["summary"], "restarted", "the caller is told the running server keeps the old code")
	require.Contains(t, doc["summary"], "v1.6.0")
}

func TestUpgrade_AlreadyLatestApplyChangesNothing(t *testing.T) {
	fake := &fakeUpgradeRunner{outcome: UpgradeOutcome{
		Current: "v1.6.0", Latest: "v1.6.0", Applied: false, Path: "/usr/local/bin/af",
	}}

	doc, fault := callUpgrade(t, fake.run, map[string]any{"project_id": "repo"})
	require.Nil(t, fault)
	require.Equal(t, false, doc["applied"])
	require.Equal(t, false, doc["restart_required"])
	require.Contains(t, doc["summary"], "already the latest")
}

func TestUpgrade_RunnerErrorIsARefusalThatChangedNothing(t *testing.T) {
	fake := &fakeUpgradeRunner{err: errors.New("archive checksum mismatch; the installed version was not changed")}

	doc, fault := callUpgrade(t, fake.run, map[string]any{"project_id": "repo"})
	require.Nil(t, doc)
	require.NotNil(t, fault)
	require.Equal(t, FaultSafetyUnavailable, fault.Code)
	require.Contains(t, fault.Detail, "left every file unchanged")
	// The product's own reason is surfaced so the caller has a next step.
	require.Contains(t, fault.Detail, "checksum mismatch")
}

func TestUpgrade_NilRunnerIsRefusedNotReportedCurrent(t *testing.T) {
	doc, fault := callUpgrade(t, nil, map[string]any{"project_id": "repo"})
	require.Nil(t, doc)
	require.NotNil(t, fault)
	require.Equal(t, FaultUnsupported, fault.Code)
	require.Contains(t, fault.Detail, "cannot upgrade itself")
}

func TestUpgrade_RefusesACallForAnotherProject(t *testing.T) {
	fake := &fakeUpgradeRunner{outcome: UpgradeOutcome{Current: "v1.6.0", Latest: "v1.6.0"}}
	tool := newUpgradeTool(&Project{ID: "repo"}, fake.run)
	_, fault := tool.Handler(context.Background(), &Call{Caller: "cli", Project: "repo"},
		map[string]any{"project_id": "other"})
	require.NotNil(t, fault, "a call naming another project is refused")
	require.False(t, fake.called, "the runner never runs for a mismatched project")
}
