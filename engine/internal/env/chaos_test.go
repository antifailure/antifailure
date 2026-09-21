package env_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/fault"
	"github.com/antifailure/antifailure/engine/internal/pgcrash"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// gate is the resolved policy a manifest with no policy block produces, which
// is the one almost every run uses.
func gate() report.Policy { return report.Configure(nil) }

func TestChaosFindings_ALostCommitFailsAndAnUnverifiedRunWarns(t *testing.T) {
	// The two levels are the point of this translation. A commit that returned
	// success and is not there stops the merge; a run that could not establish
	// its claim is reported and does not. Collapsing them is how a project
	// learns to ignore both.
	got := env.ChaosFindings(
		report.ChaosFault{Name: "postgres-crash", Injected: true, Undone: true},
		&pgcrash.Result{
			Problems: []pgcrash.Problem{{
				Rule: pgcrash.RuleLostCommit, Count: 8,
				Title:  "8 transactions the client was told were committed are gone",
				Detail: "8 of 2870 acknowledged commits are absent after recovery",
				Fix:    "Check synchronous_commit and fsync on this database.",
			}},
			Unverified: []pgcrash.Problem{{
				Rule:  pgcrash.RuleChecksumsOff,
				Title: "Data page checksums are off on this cluster",
			}},
		},
		gate(),
	)
	require.Len(t, got, 2)

	require.Equal(t, pgcrash.RuleLostCommit, got[0].Rule)
	require.Equal(t, report.LevelFail, got[0].Level,
		"a lost acknowledged commit does not stop the merge by default")
	require.Equal(t, 8, got[0].Count, "the count was dropped, so the report would say how many the sample held")
	require.Equal(t, "fault postgres-crash", got[0].Where,
		"a finding that does not name its fault cannot be traced back to one")

	require.Equal(t, pgcrash.RuleChecksumsOff, got[1].Rule)
	require.Equal(t, report.LevelWarn, got[1].Level,
		"something that could not be checked was reported at the level of something that was")
}

func TestChaosFindings_AFaultThatWouldNotGoInIsNotAPass(t *testing.T) {
	// A fault that was refused has measured nothing, and the one thing that
	// must not happen is for it to disappear. Everything downstream of it
	// would then describe a system that was never broken, with no sign that
	// anything went wrong.
	got := env.ChaosFindings(
		report.ChaosFault{Name: "postgres-crash", Error: "AF-CHS-004: no process in the container matches"},
		nil, gate(),
	)
	require.Len(t, got, 1)
	require.Equal(t, env.RuleFaultRefused, got[0].Rule)
	require.Equal(t, report.LevelWarn, got[0].Level)
	require.Contains(t, got[0].Detail, "Nothing measured after it means anything")
	require.Contains(t, got[0].Detail, "no process in the container matches",
		"the finding does not carry what the container said, so a reader cannot fix it")
}

func TestChaosFindings_AFaultRefusedAsUnsafeDoesNotReachPastItself(t *testing.T) {
	// Filmed live: a disk fill refused as unsafe, then a crash proof that ran
	// AFTER it and proved every acknowledged commit survived, then a finding
	// telling the reader that nothing measured after the refusal meant
	// anything. The refusal is decided before the fault acts, so it touched
	// nothing, and the one thing its finding must not do is discredit the
	// faults that ran against the environment it left alone.
	const said = "AF-CHS-005: The fault fill-the-data-volume is refused because its effect " +
		"would reach past af-db-ledger: it shares a filesystem with its parent"
	got := env.ChaosFindings(
		report.ChaosFault{Name: "fill-the-data-volume", Error: said, Refused: true},
		nil, gate(),
	)
	require.Len(t, got, 1)
	require.Equal(t, env.RuleFaultUnsafe, got[0].Rule,
		"a refusal was reported under the rule whose sentence invalidates what came after it")
	require.NotContains(t, got[0].Detail, "Nothing measured after it means anything",
		"the finding still tells a reader the rest of the run is worthless")
	require.Contains(t, got[0].Detail, "changed nothing the other faults in this run measured",
		"the finding does not say the other faults' results stand, which is the fact a reader needs")
	require.Contains(t, got[0].Detail, "was not established",
		"the finding no longer says the declared claim went unestablished, so it reads as a pass")
	require.Contains(t, got[0].Detail, "shares a filesystem with its parent",
		"the finding does not carry the refusal, so a reader cannot see what to change")
	// Still unverified, on purpose: the manifest declared a claim and it was
	// not established. Lowering this is reporting that as nothing to see.
	require.Equal(t, report.LevelWarn, got[0].Level)
}

func TestChaosFindings_AnUndoThatFailedIsLeftInPlaceNotRefused(t *testing.T) {
	// An undo that fails puts its error in Error, the same field a fault that
	// never went in uses. Reading Error first reported this fault, which is
	// STILL APPLIED to the environment, as one that "was not applied", and the
	// finding that says to tear the environment down never fired.
	got := env.ChaosFindings(
		report.ChaosFault{
			Name: "node-down", Injected: true, Undone: false,
			Error: "AF-CHS-003: the undo could not start the container",
		},
		nil, gate(),
	)
	require.Len(t, got, 1)
	require.Equal(t, env.RuleFaultNotUndone, got[0].Rule,
		"a fault still applied to the environment was reported as one that never went in")
	require.Contains(t, got[0].Detail, "its undo failed")
	require.Contains(t, got[0].Detail, "could not start the container",
		"the finding dropped why the undo failed")
	require.NotContains(t, got[0].Detail, "was not applied")
}

func TestRefusedAsUnsafe_ReadsTheCodeNotTheSentence(t *testing.T) {
	// The injector's refusal, wrapped the way the injector wraps it, is a
	// refusal. A failure to inject, which also mentions refusing in plenty of
	// daemon messages, is not. Keyed on the catalog code so rewording either
	// sentence cannot move a fault across the line.
	refusal := aferrors.WithOp(aferrors.Coded(aferrors.AFCHS005,
		"fault", "fill", "target", "db", "detail", "it shares a filesystem"), "inject")
	require.True(t, env.RefusedAsUnsafeForTest(refusal))

	failure := aferrors.Coded(aferrors.AFCHS003,
		"fault", "fill", "target", "db", "detail", "connection refused")
	require.False(t, env.RefusedAsUnsafeForTest(failure),
		"a fault that tried to go in and failed was read as one refused before it acted")
	require.False(t, env.RefusedAsUnsafeForTest(nil))
}

func TestChaosFindings_AFaultLeftInPlaceIsReported(t *testing.T) {
	// A fault whose undo did not run leaves a broken environment behind, and
	// the next thing to touch it will be measuring the damage.
	got := env.ChaosFindings(
		report.ChaosFault{Name: "node-down", Injected: true, Undone: false}, nil, gate(),
	)
	require.Len(t, got, 1)
	require.Equal(t, env.RuleFaultNotUndone, got[0].Rule)
	require.Contains(t, got[0].Detail, "still broken")
}

func TestChaosFindings_ACleanRunRaisesNothing(t *testing.T) {
	// The liveness arm. Without it, a translation that raised a finding for
	// every fault would pass all three tests above.
	got := env.ChaosFindings(
		report.ChaosFault{Name: "postgres-crash", Injected: true, Undone: true},
		&pgcrash.Result{}, gate(),
	)
	require.Empty(t, got, "a fault that landed, was undone and found nothing raised a finding")
}

func TestChaosFindings_HonoursAProjectThatTurnedTheLevelsDown(t *testing.T) {
	// The level comes from the policy and is never hard coded at the call
	// site, which is the contract every other family in this repository
	// follows. A project that set chaos_failure to warn gets a warn.
	quiet := report.Policy{ChaosFailure: report.LevelWarn, ChaosUnverified: report.LevelIgnore}
	got := env.ChaosFindings(
		report.ChaosFault{Name: "f", Injected: true, Undone: true},
		&pgcrash.Result{
			Problems:   []pgcrash.Problem{{Rule: pgcrash.RuleLostCommit}},
			Unverified: []pgcrash.Problem{{Rule: pgcrash.RuleNoCrash}},
		},
		quiet,
	)
	require.Len(t, got, 2)
	require.Equal(t, report.LevelWarn, got[0].Level)
	require.Equal(t, report.LevelIgnore, got[1].Level)
}

func TestFaultFrom_CarriesTheManifestFaultIntoTheOneTheInjectorRuns(t *testing.T) {
	// The translation between the two shapes is small and it is the whole of
	// the wiring between a manifest and a container: a kind carried across
	// wrongly is a fault that does something other than what the file says.
	got := env.FaultFromForTest(schema.Fault{
		Name: "postgres-crash", Kind: schema.FaultProcessKill,
		Target: schema.FaultTargetDatabase, Process: "postgres: checkpointer",
	})
	require.Equal(t, "postgres-crash", got.Name)
	require.Equal(t, fault.KindProcessKill, got.Kind)
	require.Equal(t, fault.RoleDatabase, got.Target.Role)
	require.Empty(t, got.Target.Service)
	require.Equal(t, "postgres: checkpointer", got.Process)
	// A kind that touches no filesystem gets no path, because the faults that
	// do not touch one refuse a path rather than ignoring it.
	require.Empty(t, got.Path)
	require.NoError(t, fault.Validate(got), "the translated fault is not one the injector would run")

	svc := env.FaultFromForTest(schema.Fault{
		Name: "cut-the-network", Kind: schema.FaultNetworkPartition,
		Target: schema.FaultTargetService, Service: "api",
	})
	require.Equal(t, fault.RoleService, svc.Target.Role)
	require.Equal(t, "api", svc.Target.Service)
	require.NoError(t, fault.Validate(svc))

	fill := env.FaultFromForTest(schema.Fault{
		Name: "fill", Kind: schema.FaultDiskFill, Target: schema.FaultTargetDatabase,
		HeadroomBytes: 1 << 24, MaxFillBytes: 1 << 28,
	})
	require.NotEmpty(t, fill.Path, "a fill with no directory would be refused by the injector")
	require.Equal(t, int64(1<<24), fill.HeadroomBytes)
	require.Equal(t, int64(1<<28), fill.MaxFillBytes)
	require.NoError(t, fault.Validate(fill))

	ro := env.FaultFromForTest(schema.Fault{
		Name: "read-only", Kind: schema.FaultReadOnlyData, Target: schema.FaultTargetDatabase,
	})
	require.NotEmpty(t, ro.Path)
	require.NoError(t, fault.Validate(ro))
}

func TestWantsCrashProof_RunsOnlyAroundAFaultAimedAtTheDatabase(t *testing.T) {
	on, off := true, false
	db := schema.Fault{Target: schema.FaultTargetDatabase}
	svc := schema.Fault{Target: schema.FaultTargetService, Service: "api"}

	require.True(t, env.WantsCrashProofForTest(db, &schema.CrashRecovery{Enabled: &on}))
	require.False(t, env.WantsCrashProofForTest(db, &schema.CrashRecovery{Enabled: &off}),
		"a project that turned the proof off still got one")
	require.False(t, env.WantsCrashProofForTest(db, nil))
	// A proof around a fault aimed at a service would be asserting that
	// killing the application did not lose a commit the application never made.
	require.False(t, env.WantsCrashProofForTest(svc, &schema.CrashRecovery{Enabled: &on}))
}

func TestRecoveryOf_CarriesTheProofIntoTheShapeAReportRenders(t *testing.T) {
	res := pgcrash.Result{
		KilledSignal: 9,
		Recovery: pgcrash.Recovery{
			RedoStart: "0/1950478", RedoEnd: "0/197AD88", Unclean: true,
		},
		Before: pgcrash.Control{State: "in production", TimeLine: 1},
		After:  pgcrash.Control{State: "in production", TimeLine: 1, ChecksumVersion: 1},
		Reconciliation: pgcrash.Reconciliation{
			Acknowledged: 897, Present: 902, LostCount: 0,
			PhantomCount: 0, UnresolvedLanded: 5, Consistent: true,
		},
		Relations: pgcrash.Relations{Checked: true, Agreed: true, HeapRows: 902, IndexRows: 902},
		Downtime:  9833 * time.Millisecond,
	}
	got := env.RecoveryOfForTest(res)
	// The crash came from the container's exit status and not from the log,
	// which is the case a report must not render as "nothing crashed".
	require.True(t, got.Crashed)
	require.Equal(t, 9, got.Signal)
	require.True(t, got.Replayed)
	require.Equal(t, "0/1950478", got.RedoStart)
	require.Equal(t, 897, got.Acknowledged)
	require.Equal(t, 5, got.InFlightLanded)
	require.True(t, got.ChecksumsOn)
	require.Equal(t, int64(9833), got.DowntimeMs)
	require.True(t, got.Verified)
}
