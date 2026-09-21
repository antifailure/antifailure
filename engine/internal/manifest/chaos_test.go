package manifest_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// chaosManifest is the minimal manifest with a chaos block appended.
func chaosManifest(block string) string {
	return strings.TrimRight(minimal, "\n") + "\n" + strings.TrimLeft(block, "\n")
}

func TestChaos_DefaultsAreTheOnesTheSchemaPublishes(t *testing.T) {
	t.Parallel()
	m := mustParse(t, chaosManifest(`
chaos:
  enabled: true
  faults:
    - name: postgres-crash
      kind: process_kill
      process: "postgres: checkpointer"
`))
	f := m.Chaos.Faults[0]
	// A default written here and a default written in the schema are two
	// copies of one decision, and the day they disagree a manifest read by
	// this package means something different from the same manifest validated
	// against the published schema.
	require.Equal(t, schema.FaultTargetDatabase, f.Target)
	require.Equal(t, "5s", f.After)
	require.Equal(t, "3s", f.Hold)
	// The fill bounds are defaulted only for the kind that fills, because the
	// validator refuses them on every other kind.
	require.Zero(t, f.HeadroomBytes)
	require.Zero(t, f.MaxFillBytes)

	cr := m.Chaos.CrashRecovery
	require.NotNil(t, cr, "an absent crash_recovery block did not normalise into one")
	require.NotNil(t, cr.Enabled)
	require.True(t, *cr.Enabled,
		"the durability proof defaulted to off, so a fault would be injected with nothing measuring the result")
	require.Equal(t, 8, cr.Writers)
	require.Equal(t, 200, cr.CommitsBeforeFault)
	require.Equal(t, "2m", cr.RecoveryTimeout)
}

func TestChaos_ADisabledProofStaysDisabled(t *testing.T) {
	t.Parallel()
	// Enabled is a pointer precisely so that "absent" and "written as false"
	// are different values. A bool would make an author who wrote false get
	// the default back.
	m := mustParse(t, chaosManifest(`
chaos:
  enabled: true
  crash_recovery:
    enabled: false
  faults:
    - name: node-down
      kind: container_kill
`))
	require.NotNil(t, m.Chaos.CrashRecovery.Enabled)
	require.False(t, *m.Chaos.CrashRecovery.Enabled)
	// And the rest of the block still gets its defaults, so a project that
	// turns the proof off and on again finds the same numbers.
	require.Equal(t, 8, m.Chaos.CrashRecovery.Writers)
}

func TestChaos_DiskFillGetsItsBoundsAndNothingElseDoes(t *testing.T) {
	t.Parallel()
	m := mustParse(t, chaosManifest(`
chaos:
  enabled: true
  faults:
    - name: fill-the-volume
      kind: disk_fill
`))
	f := m.Chaos.Faults[0]
	require.Equal(t, int64(16<<20), f.HeadroomBytes)
	require.Equal(t, int64(1<<30), f.MaxFillBytes)
	require.Greater(t, f.MaxFillBytes, f.HeadroomBytes,
		"the defaults themselves would be refused by the validator")
}

func TestChaos_RefusesAFaultThatCouldNotBeRunAsWritten(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		block string
		path  string
		says  string
	}{
		"a process kill that names no process": {
			block: "\nchaos:\n  enabled: true\n  faults:\n    - name: crash\n      kind: process_kill\n",
			path:  "chaos.faults[0].process",
			says:  "names no process",
		},
		"a process named on a kind that kills none": {
			block: "\nchaos:\n  enabled: true\n  faults:\n    - name: stall\n      kind: container_pause\n      process: postgres\n",
			path:  "chaos.faults[0].process",
			says:  "kills no process",
		},
		"a service this manifest does not declare": {
			block: "\nchaos:\n  enabled: true\n  faults:\n    - name: kill-the-worker\n      kind: container_kill\n      target: service\n      service: worker\n",
			path:  "chaos.faults[0].service",
			says:  "which this manifest does not declare",
		},
		"a service target that names none": {
			block: "\nchaos:\n  enabled: true\n  faults:\n    - name: kill-a-service\n      kind: container_kill\n      target: service\n",
			path:  "chaos.faults[0].service",
			says:  "targets a service and names none",
		},
		"a service name on a database target": {
			block: "\nchaos:\n  enabled: true\n  faults:\n    - name: crash\n      kind: container_kill\n      service: web\n",
			path:  "chaos.faults[0].service",
			says:  "its target is database",
		},
		"headroom on a kind that fills nothing": {
			block: "\nchaos:\n  enabled: true\n  faults:\n    - name: crash\n      kind: container_kill\n      headroom_bytes: 1048576\n",
			path:  "chaos.faults[0].headroom_bytes",
			says:  "which fills nothing",
		},
		"a fill cap at or below the headroom": {
			block: "\nchaos:\n  enabled: true\n  faults:\n    - name: fill\n      kind: disk_fill\n      headroom_bytes: 1073741824\n      max_fill_bytes: 1048576\n",
			path:  "chaos.faults[0].max_fill_bytes",
			says:  "refuses every fill it is asked for",
		},
		"two faults with one name": {
			block: "\nchaos:\n  enabled: true\n  faults:\n    - name: crash\n      kind: container_kill\n    - name: crash\n      kind: container_stop\n",
			path:  "chaos.faults[1].name",
			says:  "are both called",
		},
		"a block that is on and declares nothing": {
			block: "\nchaos:\n  enabled: true\n",
			path:  "chaos.faults",
			says:  "reports a recovery nobody caused",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := problems(t, mustFail(t, chaosManifest(tc.block)))
			var paths []string
			var found bool
			for _, p := range got {
				paths = append(paths, p.Path)
				// The message and the hint together, because a refusal is
				// both halves: what is wrong and what to write instead, and
				// which half carries a given sentence is an editing choice.
				if p.Path == tc.path && strings.Contains(p.Message+" "+p.Hint, tc.says) {
					found = true
					require.NotEmpty(t, p.Hint, "a refusal with no hint tells an author to go and work it out")
				}
			}
			require.Truef(t, found,
				"expected a problem at %s saying %q, got problems at %v", tc.path, tc.says, paths)
		})
	}
}

func TestChaos_AcceptsEveryKindWrittenCorrectly(t *testing.T) {
	t.Parallel()
	// The liveness arm for the table above. A validator that refused every
	// chaos block would pass every case there and fail this one.
	m := mustParse(t, chaosManifest(`
chaos:
  enabled: true
  crash_recovery:
    writers: 16
    commits_before_fault: 1000
    synchronous_commit: "off"
    recovery_timeout: 5m
  faults:
    - name: crash-the-backend
      kind: process_kill
      process: "postgres: checkpointer"
      after: 10s
      hold: 1s
    - name: take-the-node-away
      kind: container_kill
    - name: stop-it-cleanly
      kind: container_stop
    - name: freeze-it
      kind: container_pause
    - name: cut-the-network
      kind: network_partition
      target: service
      service: web
    - name: make-the-data-read-only
      kind: read_only_data
    - name: fill-the-volume
      kind: disk_fill
      headroom_bytes: 16777216
      max_fill_bytes: 268435456
`))
	require.Len(t, m.Chaos.Faults, 7, "one fault per kind, so no kind is accepted only in theory")
	require.Equal(t, "off", m.Chaos.CrashRecovery.SynchronousCommit)
	require.Equal(t, "5m", m.Chaos.CrashRecovery.RecoveryTimeout)
}

func TestChaos_AnAbsentBlockChangesNothing(t *testing.T) {
	t.Parallel()
	// Off by default has to mean absent rather than present and empty, or a
	// project that never asked for any of this would start normalising a block
	// it did not write.
	m := mustParse(t, minimal)
	require.Nil(t, m.Chaos, "a manifest with no chaos block gained one")
}
