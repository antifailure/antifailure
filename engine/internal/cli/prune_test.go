package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/golden"
	"github.com/antifailure/antifailure/engine/internal/reaper"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// A bare `af env prune` in an empty directory removed nine environments and
// eighteen resources belonging to other sessions on a shared machine:
// afcinv2a6c7sla, afcjrn2utacnz, afcjrn2mj0tke, afcinv2ai6osuh,
// af-fidelity-main-6d7ab1, afceg6aycnfr, afclog12lvdva, probe and lane6sealed.
// Its help said it "prints what it would do before doing it". It printed each
// removal as it performed it. These tests pin the help to the truth: a bare
// run lists and stops, and only --yes removes.

// fakePruner is a machine holding environments, which remembers what was
// taken from it.
type fakePruner struct {
	held    map[string]environment
	order   []string
	downed  []string
	closed  bool
	failing map[string]error
}

func newFakePruner(envs ...environment) *fakePruner {
	f := &fakePruner{held: map[string]environment{}, failing: map[string]error{}}
	for _, e := range envs {
		f.held[e.ID] = e
		f.order = append(f.order, e.ID)
	}
	return f
}

func (f *fakePruner) environments(context.Context) ([]environment, error) {
	out := make([]environment, 0, len(f.held))
	for _, id := range f.order {
		if e, ok := f.held[id]; ok {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakePruner) down(_ context.Context, envID string) (provider.Teardown, error) {
	f.downed = append(f.downed, envID)
	if err, ok := f.failing[envID]; ok {
		return provider.Teardown{Pending: []provider.PendingResource{{ID: envID, Reason: err.Error()}}}, err
	}
	e := f.held[envID]
	delete(f.held, envID)
	return provider.Teardown{Removed: e.Resources}, nil
}

func (f *fakePruner) close() error { f.closed = true; return nil }

// resourcesHeld is the count the incident was measured in: eighteen went.
func (f *fakePruner) resourcesHeld() int {
	n := 0
	for _, e := range f.held {
		n += e.Resources
	}
	return n
}

var pruneNow = time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

// prose collapses the renderer's line breaks, so an assertion about what a
// help text says is not also an assertion about where it wrapped.
func prose(s string) string { return strings.Join(strings.Fields(s), " ") }

func pruneEnv(id string, age time.Duration, resources int, services ...string) environment {
	return environment{
		ID: id, Resources: resources, Running: resources - 1,
		Services: services, Oldest: pruneNow.Add(-age),
	}
}

// sharedMachine is the shape the incident found: two environments over a day
// old that belong to somebody else, and one fresh one.
func sharedMachine() *fakePruner {
	return newFakePruner(
		pruneEnv("af-fidelity-main-6d7ab1", 41*time.Hour, 2, "web"),
		pruneEnv("lane6sealed", 30*time.Hour, 1, "af-proxy"),
		pruneEnv("af-fresh-branch-1a2b3c", 20*time.Minute, 3, "web", "worker"),
	)
}

func pruneEnvFor(buf *bytes.Buffer, format Format) *Env {
	out := NewOutput(buf, buf)
	out.Format = format
	return &Env{Out: out, Clock: clock.NewFake(pruneNow), Getenv: func(string) string { return "" }}
}

func TestEnvPrune_BareRunListsThePlanAndRemovesNothing(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := pruneEnvFor(&buf, FormatText)
	machine := sharedMachine()
	before := machine.resourcesHeld()

	err := runPrune(context.Background(), e, pruneOptions{olderThan: pruneCutoff}, machine)
	require.NoError(t, err)

	// The measurement that matters: the machine holds exactly what it held.
	require.Empty(t, machine.downed, "a bare run called Down")
	require.Equal(t, before, machine.resourcesHeld(), "a bare run changed the resource count")
	require.True(t, machine.closed, "the runtime was not closed")

	text := buf.String()
	// The plan names each stale environment with its age and resources, and
	// leaves the fresh one out.
	require.Contains(t, text, "af-fidelity-main-6d7ab1")
	require.Contains(t, text, "lane6sealed")
	require.NotContains(t, text, "af-fresh-branch-1a2b3c")
	require.Contains(t, text, "41h")
	require.Regexp(t, `af-fidelity-main-6d7ab1\s+2\s+1\s+41h\s+web`, text,
		"the row does not carry resources, running, age and services")
	// The cutoff is visible even though nobody typed it.
	require.Contains(t, text, "Older than 24h on this machine")
	// It says that it stopped, and how to go on.
	require.Contains(t, text, "2 environments would be removed, 3 resources. Nothing has been removed.")
	require.Contains(t, text, "af env prune --older-than 24h --yes")
	require.NotContains(t, text, "removed af-", "a bare run reported a removal")
}

func TestEnvPrune_YesRemovesExactlyWhatWasListed(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := pruneEnvFor(&buf, FormatText)
	machine := sharedMachine()

	err := runPrune(context.Background(), e, pruneOptions{olderThan: pruneCutoff, remove: true}, machine)
	require.NoError(t, err)

	require.Equal(t, []string{"af-fidelity-main-6d7ab1", "lane6sealed"}, machine.downed,
		"--yes did not remove exactly the two stale environments, oldest first")
	require.Equal(t, 3, machine.resourcesHeld(), "the fresh environment did not survive")
	_, fresh := machine.held["af-fresh-branch-1a2b3c"]
	require.True(t, fresh)

	text := buf.String()
	require.Contains(t, text, "removed af-fidelity-main-6d7ab1 (2 resources)")
	require.Contains(t, text, "removed lane6sealed (1 resources)")
	require.Contains(t, text, "2 environments removed, 3 resources.")
	require.NotContains(t, text, "would be removed")
}

func TestEnvPrune_OlderThanIsHonouredAndShown(t *testing.T) {
	t.Parallel()

	t.Run("zero takes everything and says 0s", func(t *testing.T) {
		var buf bytes.Buffer
		e := pruneEnvFor(&buf, FormatText)
		machine := sharedMachine()
		require.NoError(t, runPrune(context.Background(), e, pruneOptions{olderThan: 0}, machine))
		require.Empty(t, machine.downed)
		text := buf.String()
		require.Contains(t, text, "af-fresh-branch-1a2b3c", "a zero cutoff left the fresh environment out of the plan")
		require.Contains(t, text, "Older than 0s on this machine")
		require.Contains(t, text, "3 environments would be removed, 6 resources.")
		require.Contains(t, text, "af env prune --older-than 0s --yes")
	})

	t.Run("a cutoff nothing is past lists nothing and says which cutoff", func(t *testing.T) {
		var buf bytes.Buffer
		e := pruneEnvFor(&buf, FormatText)
		machine := sharedMachine()
		require.NoError(t, runPrune(context.Background(), e, pruneOptions{olderThan: 72 * time.Hour, remove: true}, machine))
		require.Empty(t, machine.downed, "--yes with a cutoff nothing is past removed something")
		require.Contains(t, buf.String(), "Nothing on this machine is older than 72h. Nothing was removed.")
	})

	t.Run("with --yes the cutoff still bounds what goes", func(t *testing.T) {
		var buf bytes.Buffer
		e := pruneEnvFor(&buf, FormatText)
		machine := sharedMachine()
		require.NoError(t, runPrune(context.Background(), e, pruneOptions{olderThan: 36 * time.Hour, remove: true}, machine))
		require.Equal(t, []string{"af-fidelity-main-6d7ab1"}, machine.downed)
	})
}

func TestEnvPrune_JSONIsAPlanDocumentUntilYes(t *testing.T) {
	t.Parallel()

	t.Run("bare", func(t *testing.T) {
		var buf bytes.Buffer
		e := pruneEnvFor(&buf, FormatJSON)
		machine := sharedMachine()
		require.NoError(t, runPrune(context.Background(), e, pruneOptions{olderThan: pruneCutoff}, machine))
		require.Empty(t, machine.downed, "a JSON plan removed something")

		var doc PruneJSON
		require.NoError(t, json.Unmarshal(buf.Bytes(), &doc), buf.String())
		require.True(t, doc.DryRun)
		require.Equal(t, "24h", doc.OlderThan)
		require.Equal(t, "machine", doc.Scope)
		require.Len(t, doc.WouldRemove, 2)
		require.Equal(t, "af-fidelity-main-6d7ab1", doc.WouldRemove[0].EnvID)
		require.Equal(t, 2, doc.WouldRemove[0].Resources)
		require.Equal(t, []string{"web"}, doc.WouldRemove[0].Services)
		require.InDelta(t, 41, doc.WouldRemove[0].AgeHours, 0.01)
		require.Empty(t, doc.Removed)
		require.Zero(t, doc.ResourcesRemoved)
		require.Equal(t, "af env prune --older-than 24h --yes", doc.Proceed)

		// The keys are present even when empty, so a consumer never has to
		// guess whether a missing list means none or means not reported.
		var raw map[string]any
		require.NoError(t, json.Unmarshal(buf.Bytes(), &raw))
		require.Contains(t, raw, "removed")
		require.Contains(t, raw, "would_remove")
	})

	t.Run("yes", func(t *testing.T) {
		var buf bytes.Buffer
		e := pruneEnvFor(&buf, FormatJSON)
		machine := sharedMachine()
		require.NoError(t, runPrune(context.Background(), e, pruneOptions{olderThan: pruneCutoff, remove: true}, machine))

		var doc PruneJSON
		require.NoError(t, json.Unmarshal(buf.Bytes(), &doc), buf.String())
		require.False(t, doc.DryRun)
		require.Empty(t, doc.WouldRemove)
		require.Len(t, doc.Removed, 2)
		require.Equal(t, 2, doc.Removed[0].Removed)
		require.Equal(t, 3, doc.ResourcesRemoved)
		require.Zero(t, doc.Pending)
		require.Empty(t, doc.Proceed)
	})
}

// A teardown the runtime could not finish is reported per environment and
// turns into exit 10, the way af down does it, rather than being averaged into
// a count.
func TestEnvPrune_ARefusedTeardownIsNamedAndExits10(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := pruneEnvFor(&buf, FormatText)
	machine := sharedMachine()
	machine.failing["lane6sealed"] = errors.New("the network is still attached")

	err := runPrune(context.Background(), e, pruneOptions{olderThan: pruneCutoff, remove: true}, machine)
	coded := codedError(t, err)
	require.Equal(t, 10, int(coded.ExitCode()))
	require.Contains(t, buf.String(), "lane6sealed: the network is still attached")
	require.Contains(t, buf.String(), "removed af-fidelity-main-6d7ab1")
}

// The flags decide one thing: whether anything is removed. --dry-run beats
// --yes, because somebody who typed both is asking to look.
func TestEnvPrune_FlagsExistAndDryRunBeatsYes(t *testing.T) {
	t.Parallel()
	cmd := newEnvPruneCommand(&Env{})
	for _, name := range []string{"yes", "dry-run", "older-than"} {
		require.NotNil(t, cmd.Flags().Lookup(name), "af env prune has no --%s", name)
	}
	require.Equal(t, "24h0m0s", cmd.Flags().Lookup("older-than").DefValue)
	require.Equal(t, "false", cmd.Flags().Lookup("yes").DefValue)
	// pruneRemoves(dryRun, yes)
	require.False(t, pruneRemoves(true, true), "--dry-run --yes removed")
	require.True(t, pruneRemoves(false, true), "--yes alone did not remove")
	require.False(t, pruneRemoves(false, false), "a bare run removes")
	require.False(t, pruneRemoves(true, false), "--dry-run alone removed")
}

// The help text is what the evaluator read as a safety promise. It has to
// state the real one.
func TestEnvPrune_HelpSaysABareRunRemovesNothing(t *testing.T) {
	t.Parallel()
	long := prose(newEnvPruneCommand(&Env{}).Long)
	require.Contains(t, long, "Run bare, it removes nothing.")
	require.Contains(t, long, "Removal needs --yes")
	require.NotContains(t, long, "prints what it would do before doing it")
}

func TestPruneCutoffLabel(t *testing.T) {
	t.Parallel()
	require.Equal(t, "0s", pruneCutoffLabel(0))
	require.Equal(t, "24h", pruneCutoffLabel(24*time.Hour))
	require.Equal(t, "90m", pruneCutoffLabel(90*time.Minute))
	require.Equal(t, "1m30s", pruneCutoffLabel(90*time.Second))
}

// ---------------------------------------------------------------------------
// af env reap: the same shape. Its sweep already had a dry run with the same
// predicate as the real one; what changes is that the dry run is now what a
// bare invocation does.
// ---------------------------------------------------------------------------

type fakeSweep struct {
	askedDryRun []bool
	result      *env.ReapResult
}

func (f *fakeSweep) sweep(_ context.Context, dryRun bool) (*env.ReapResult, error) {
	f.askedDryRun = append(f.askedDryRun, dryRun)
	out := &env.ReapResult{Teardowns: map[string]*env.Teardown{}}
	out.Scanned = f.result.Scanned
	for _, o := range f.result.Outcomes {
		if dryRun {
			o.Removed = 0
		}
		out.Outcomes = append(out.Outcomes, o)
	}
	return out, nil
}

func expiredMachine() *fakeSweep {
	return &fakeSweep{result: &env.ReapResult{Result: reaper.Result{
		Scanned: 4,
		Outcomes: []reaper.Outcome{
			{Expired: reaper.Expired{EnvID: "afclog12lvdva", ExpiresAt: pruneNow.Add(-3 * time.Hour), Overdue: 3 * time.Hour, Resources: 2}, Removed: 2},
			{Expired: reaper.Expired{EnvID: "probe", ExpiresAt: pruneNow.Add(-time.Hour), Overdue: time.Hour, Resources: 3}, Removed: 3},
		},
	}}}
}

func TestEnvReap_BareRunPlansAndRemovesNothing(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := pruneEnvFor(&buf, FormatText)
	machine := expiredMachine()

	require.NoError(t, runReap(context.Background(), e, machine.sweep, false))
	require.Equal(t, []bool{true}, machine.askedDryRun, "a bare reap asked the orchestrator for a real sweep")

	text := buf.String()
	require.Contains(t, text, "afclog12lvdva")
	require.Contains(t, text, "probe")
	require.Contains(t, text, "would-remove")
	require.Contains(t, text, "2 environments would be removed. Nothing has been removed.")
	require.Contains(t, text, "af env reap --yes")
	require.NotContains(t, text, "--dry-run")
}

func TestEnvReap_YesSweepsForReal(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := pruneEnvFor(&buf, FormatText)
	machine := expiredMachine()

	require.NoError(t, runReap(context.Background(), e, machine.sweep, true))
	require.Equal(t, []bool{false}, machine.askedDryRun, "--yes did not ask for a real sweep")
	require.Contains(t, buf.String(), "2 of 4 environments removed, 5 resources.")
}

func TestEnvReap_JSONPlanSaysDryRunAndWouldRemove(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := pruneEnvFor(&buf, FormatJSON)
	machine := expiredMachine()
	require.NoError(t, runReap(context.Background(), e, machine.sweep, false))

	var doc ReapSummaryJSON
	require.NoError(t, json.Unmarshal(buf.Bytes(), &doc), buf.String())
	require.True(t, doc.DryRun)
	require.Zero(t, doc.Removed)
	require.Len(t, doc.Environments, 2)
	require.Equal(t, "would-remove", doc.Environments[0].Outcome)
}

func TestEnvReap_FlagsExist(t *testing.T) {
	t.Parallel()
	cmd := newEnvReapCommand(&Env{})
	require.NotNil(t, cmd.Flags().Lookup("yes"))
	require.NotNil(t, cmd.Flags().Lookup("dry-run"))
	require.Contains(t, prose(cmd.Long), "Run bare, it lists them and removes nothing")
}

// ---------------------------------------------------------------------------
// af golden gc: scoped to one project already, but a golden is shared by every
// branch on the machine and the command had no preview at all.
// ---------------------------------------------------------------------------

func goldenDecisions() goldenSweep {
	return goldenSweep{
		keep: 1, source: "database.golden.retain", skipped: 3,
		decisions: []golden.Decision{
			{Version: golden.Version{ID: "gv_new"}, Remove: false, Reason: "newest verified"},
			{Version: golden.Version{ID: "gv_old"}, Remove: true, Reason: "past the 1 kept"},
			{Version: golden.Version{ID: "gv_older"}, Remove: true, Reason: "past the 1 kept"},
		},
	}
}

type fakeDestroyer struct{ destroyed []string }

func (f *fakeDestroyer) destroy(_ context.Context, id string) error {
	f.destroyed = append(f.destroyed, id)
	return nil
}

func TestGoldenGC_BareRunListsAndRemovesNothing(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := pruneEnvFor(&buf, FormatText)
	d := &fakeDestroyer{}

	require.NoError(t, runGoldenGC(context.Background(), e, goldenDecisions(), d.destroy, false))
	require.Empty(t, d.destroyed, "a bare gc destroyed a golden")

	text := buf.String()
	require.Contains(t, text, "Would remove 2, keeping 1 from database.golden.retain. Nothing has been removed.")
	require.Contains(t, text, "would remove gv_old")
	require.Contains(t, text, "would remove gv_older")
	require.Contains(t, text, "gv_new: newest verified")
	require.Contains(t, text, "3 belong to other projects on this machine and were left alone.")
	require.Contains(t, text, "af golden gc --yes")
}

func TestGoldenGC_YesRemovesExactlyTheListed(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := pruneEnvFor(&buf, FormatText)
	d := &fakeDestroyer{}

	require.NoError(t, runGoldenGC(context.Background(), e, goldenDecisions(), d.destroy, true))
	require.Equal(t, []string{"gv_old", "gv_older"}, d.destroyed)
	require.Contains(t, buf.String(), "Removed 2, kept 1, keeping 1 from database.golden.retain.")
	require.NotContains(t, buf.String(), "would remove")
}

func TestGoldenGC_JSONPlanDocument(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := pruneEnvFor(&buf, FormatJSON)
	d := &fakeDestroyer{}
	require.NoError(t, runGoldenGC(context.Background(), e, goldenDecisions(), d.destroy, false))
	require.Empty(t, d.destroyed)

	var doc GoldenGCJSON
	require.NoError(t, json.Unmarshal(buf.Bytes(), &doc), buf.String())
	require.True(t, doc.DryRun)
	require.Equal(t, []string{"gv_old", "gv_older"}, doc.WouldRemove)
	require.Zero(t, doc.Removed)
	require.Equal(t, 1, doc.Kept)
	require.Equal(t, 3, doc.OtherProjects)
	require.Equal(t, "af golden gc --yes", doc.Proceed)
}

func TestGoldenGC_FlagsExist(t *testing.T) {
	t.Parallel()
	cmd := newGoldenGCCommand(&Env{})
	require.NotNil(t, cmd.Flags().Lookup("yes"))
	require.Contains(t, prose(cmd.Long), "removes nothing")
}

// ---------------------------------------------------------------------------
// The structural half. Nothing that runs unattended in this repository may
// invoke one of these three commands without --yes, or CI would start printing
// plans and leaving environments behind, which is the failure in the other
// direction. Paired with the behavioural tests above, which are what make the
// bare form safe to leave in the docs.
// ---------------------------------------------------------------------------

// unattendedInvocation is a line that RUNS one of the three commands: on its
// own line or after a GitHub Actions run:, not a mention in prose or a comment.
var unattendedInvocation = regexp.MustCompile(
	`(?m)^\s*(?:(?:-\s+)?run:\s+)?(?:\S+=\S+\s+)*(?:[./\w-]*/)?af\s+(env\s+prune|env\s+reap|golden\s+gc)\b([^\n]*)`)

func TestNothingUnattendedRunsAPruneWithoutYes(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "..")
	trees := []string{".github", "justfile", "tools", "deploy", "examples", "web/apps/api/src", "console/src"}
	var offenders []string
	for _, tree := range trees {
		start := filepath.Join(root, tree)
		if _, err := os.Stat(start); err != nil {
			continue
		}
		err := filepath.WalkDir(start, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "node_modules" || d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, ".md") || strings.HasSuffix(path, "_test.go") ||
				strings.HasSuffix(path, ".test.ts") {
				return nil
			}
			offenders = append(offenders, pruneInvocationsWithoutYes(t, path)...)
			return nil
		})
		require.NoError(t, err)
	}
	require.Empty(t, offenders,
		"an unattended caller runs a removal without --yes, so it would now print a plan and remove nothing")
}

func pruneInvocationsWithoutYes(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var out []string
	for _, m := range unattendedInvocation.FindAllStringSubmatch(string(data), -1) {
		if strings.Contains(m[2], "--yes") {
			continue
		}
		out = append(out, path+": "+strings.TrimSpace(m[0]))
	}
	return out
}

// The guard has to be able to say no, and the shapes it must see are the
// ones automation actually writes.
func TestPruneInvocationGuard_SeesTheShapesAutomationWrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
		return p
	}
	bad := write("bad.yml", "steps:\n  - run: af env prune --older-than 0s\n")
	require.Len(t, pruneInvocationsWithoutYes(t, bad), 1)
	badJust := write("justfile", "clean:\n    af env reap\n")
	require.Len(t, pruneInvocationsWithoutYes(t, badJust), 1)
	badSh := write("clean.sh", "AF_LOG=debug ./bin/af golden gc --keep 2\n")
	require.Len(t, pruneInvocationsWithoutYes(t, badSh), 1)

	good := write("good.yml", "steps:\n  - run: af env prune --older-than 0s --yes\n  - run: af env reap --yes\n")
	require.Empty(t, pruneInvocationsWithoutYes(t, good))
	mention := write("prose.sh", "# af env prune is documented as listing first\necho 'run af env prune yourself'\n")
	require.Empty(t, pruneInvocationsWithoutYes(t, mention))
}
