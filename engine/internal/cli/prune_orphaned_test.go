package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// On 2026-09-22 Docker refused a network with "all predefined address pools
// have been fully subnetted". It held thirty networks and fourteen were
// Antifailure networks nothing was attached to, left by test runs killed before
// their teardown ran. af env prune could not be pointed at them: its only
// selector is age from the oldest resource, so a cutoff young enough to reach
// the twelve hour old orphans also reached the environments being filmed. These
// pin --orphaned, which selects on what an environment holds rather than how
// old it is.

// inventoryOf builds the flat list a runtime reports, so the plan is driven
// through groupEnvironments, which is where the network counts are read.
type invItem struct {
	env, kind string
	age       time.Duration
	attached  int // networks only; -1 means the runtime could not count it
	state     string
}

func inventoryOf(items ...invItem) []provider.Resource {
	var out []provider.Resource
	for i, it := range items {
		res := provider.Resource{
			Kind: it.kind, ID: it.env + "-" + strconv.Itoa(i), EnvID: it.env,
			CreatedAt: pruneNow.Add(-it.age), Labels: map[string]string{},
		}
		if it.kind == "network" && it.attached >= 0 {
			res.Labels["attached"] = strconv.Itoa(it.attached)
		}
		if it.state != "" {
			res.Labels["state"] = it.state
		}
		out = append(out, res)
	}
	return out
}

// incidentMachine is what the daemon held, reduced to one of each shape.
func incidentMachine() *fakePruner {
	return newFakePruner(groupEnvironments(inventoryOf(
		// A conformance run killed before its cleanup: two networks, nothing
		// on them, five days old.
		invItem{env: "afcurl1m71b02", kind: "network", age: 5 * 24 * time.Hour},
		invItem{env: "afcurl1m71b02", kind: "network", age: 5 * 24 * time.Hour},
		// The same, with the sidecar created and never started. A created
		// container holds no endpoint, so its networks read as unattached.
		invItem{env: "testcapture2", kind: "network", age: 12 * time.Hour},
		invItem{env: "testcapture2", kind: "network", age: 12 * time.Hour},
		invItem{env: "testcapture2", kind: "container/sidecar", age: 12 * time.Hour, state: "created"},
		// The environment being filmed: running, attached, an hour old.
		invItem{env: "ledger-main-3e9f66", kind: "network", age: time.Hour, attached: 4},
		invItem{env: "ledger-main-3e9f66", kind: "network", age: time.Hour, attached: 2},
		invItem{env: "ledger-main-3e9f66", kind: "container/service", age: time.Hour, state: "running"},
		// Being brought up by another repository right now: networks made,
		// the database branch attached, no service container yet. The
		// inventory does not list the branch, so only the attachment shows it.
		invItem{env: "orders-feature-1a2b3c", kind: "network", age: 2 * time.Hour, attached: 1},
		invItem{env: "orders-feature-1a2b3c", kind: "network", age: 2 * time.Hour, attached: 0},
		// Networks nothing is on, but made four minutes ago: an af up waiting
		// on its sidecar image looks exactly like this.
		invItem{env: "fresh-branch-9f9f9f", kind: "network", age: 4 * time.Minute},
		invItem{env: "fresh-branch-9f9f9f", kind: "network", age: 4 * time.Minute},
		// Something running that holds no endpoint on its own networks, a
		// container on the host's network for one. Nothing attached is not
		// the same as nothing running, and running wins.
		invItem{env: "hostnet-5d5d5d", kind: "network", age: 2 * 24 * time.Hour},
		invItem{env: "hostnet-5d5d5d", kind: "container/service", age: 2 * 24 * time.Hour, state: "running"},
		// Old networks whose attachments could not be read.
		invItem{env: "unreadable-0c0c0c", kind: "network", age: 3 * 24 * time.Hour, attached: -1},
		// Old environment re-upped a minute ago: its networks are days old and
		// its sidecar was created a minute ago and not yet started.
		invItem{env: "reupped-7e7e7e", kind: "network", age: 3 * 24 * time.Hour},
		invItem{env: "reupped-7e7e7e", kind: "container/sidecar", age: time.Minute, state: "created"},
	))...)
}

func TestEnvPruneOrphaned_ListsOnlyEnvironmentsWithNothingAttached(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := pruneEnvFor(&buf, FormatText)
	machine := incidentMachine()

	require.NoError(t, runPrune(context.Background(), e,
		pruneOptions{orphaned: true, olderThan: orphanCutoff}, machine))
	require.Empty(t, machine.downed, "a bare --orphaned run called Down")

	text := buf.String()
	require.Contains(t, text, "afcurl1m71b02", "two networks and nothing else were not listed")
	require.Contains(t, text, "testcapture2", "a created, never started sidecar kept its networks off the list")
	require.NotContains(t, text, "ledger-main-3e9f66", "a running environment was listed")
	require.NotContains(t, text, "orders-feature-1a2b3c",
		"an environment with its database attached, and nothing else yet, was listed")
	require.NotContains(t, text, "fresh-branch-9f9f9f", "networks four minutes old were listed")
	require.NotContains(t, text, "unreadable-0c0c0c", "networks nobody could count were called empty")
	require.NotContains(t, text, "hostnet-5d5d5d", "an environment with a running container was listed")
	require.NotContains(t, text, "reupped-7e7e7e",
		"old networks with a container created a minute ago were listed: age has to come from the newest resource")
	require.Contains(t, text, "Orphaned on this machine for longer than 1h")
	require.Contains(t, text, "af env prune --orphaned --older-than 1h --yes")
}

func TestEnvPruneOrphaned_YesRemovesExactlyTheOrphans(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := pruneEnvFor(&buf, FormatText)
	machine := incidentMachine()

	require.NoError(t, runPrune(context.Background(), e,
		pruneOptions{orphaned: true, olderThan: orphanCutoff, remove: true}, machine))
	require.ElementsMatch(t, []string{"afcurl1m71b02", "testcapture2"}, machine.downed)
}

func TestEnvPruneOrphaned_JSONSaysWhichSelectorRan(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := pruneEnvFor(&buf, FormatJSON)
	require.NoError(t, runPrune(context.Background(), e,
		pruneOptions{orphaned: true, olderThan: orphanCutoff}, incidentMachine()))
	var doc PruneJSON
	require.NoError(t, json.Unmarshal(buf.Bytes(), &doc))
	require.True(t, doc.Orphaned, "a script cannot tell an orphan plan from an age plan")
	require.Equal(t, "af env prune --orphaned --older-than 1h --yes", doc.Proceed)
	require.Len(t, doc.WouldRemove, 2)
}

func TestEnvPruneOrphaned_CutoffDefaultsToAnHourUnlessTyped(t *testing.T) {
	t.Parallel()
	require.Equal(t, time.Hour, pruneCutoffFor(pruneCutoff, true, false),
		"with --orphaned and no --older-than, the day long default would have missed "+
			"the twelve hour old orphans that filled the daemon")
	require.Equal(t, 10*time.Minute, pruneCutoffFor(10*time.Minute, true, true),
		"an --older-than somebody typed was overridden")
	require.Equal(t, pruneCutoff, pruneCutoffFor(pruneCutoff, false, false),
		"a plain prune's default moved")
}

func TestEnvPruneWithoutOrphanedIsUnchanged(t *testing.T) {
	t.Parallel()
	// The age selector still reads the oldest resource and ignores
	// attachments: a running environment over a day old is still listed.
	var buf bytes.Buffer
	e := pruneEnvFor(&buf, FormatText)
	require.NoError(t, runPrune(context.Background(), e, pruneOptions{olderThan: pruneCutoff}, incidentMachine()))
	text := buf.String()
	require.Contains(t, text, "reupped-7e7e7e")
	require.Contains(t, text, "unreadable-0c0c0c")
	require.NotContains(t, text, "testcapture2")
}

func TestLeftoverVerdict_NamesOrphanedNetworks(t *testing.T) {
	t.Parallel()
	envs, _ := incidentMachine().environments(context.Background())
	status, detail := leftoverVerdict(envs, pruneNow)
	require.Equal(t, CheckWarn, status)
	require.Contains(t, detail, "2 environments hold networks with nothing attached",
		"doctor reported the machine without saying that it was holding address ranges for nothing")
	require.Contains(t, detail, "af env prune --orphaned")

	status, detail = leftoverVerdict(envs[:1], pruneNow)
	require.Equal(t, CheckWarn, status)
	require.Contains(t, detail, "1 environment holds networks with nothing attached")
}
