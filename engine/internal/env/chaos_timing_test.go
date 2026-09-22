package env_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/fault"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// partitionDaemon is a daemon holding one service container on one network,
// which records the instant it was detached and the instant it was attached
// again. Those two instants are the fault as the daemon saw it, and they are
// what the step's own report is held to.
//
// Every call the partition does not make is left to the embedded nil
// interface, so a step that started calling something else would panic here
// rather than pass against a fake that quietly answered.
type partitionDaemon struct {
	fault.Docker

	mu                         sync.Mutex
	detachedAt, reattachedAt   time.Time
	detachCalls, reattachCalls int
}

const (
	timingEnv       = "ledger-main-abc123"
	timingContainer = "svc0123456789"
	timingNetwork   = "af-net-ledger-main-abc123"
)

func (d *partitionDaemon) labels() map[string]string {
	return map[string]string{
		dockerutil.LabelManaged: dockerutil.ManagedValue,
		dockerutil.LabelEnv:     timingEnv,
		dockerutil.LabelKind:    "service",
		dockerutil.LabelService: "ledger",
	}
}

func (d *partitionDaemon) ContainerList(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
	return client.ContainerListResult{Items: []container.Summary{{
		ID: timingContainer, Names: []string{"/af-svc-ledger"}, Labels: d.labels(), State: container.StateRunning,
	}}}, nil
}

func (d *partitionDaemon) ContainerInspect(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	return client.ContainerInspectResult{Container: container.InspectResponse{
		ID: timingContainer, Name: "/af-svc-ledger",
		Config: &container.Config{Labels: d.labels()},
		State:  &container.State{Status: container.StateRunning, Running: true},
		NetworkSettings: &container.NetworkSettings{Networks: map[string]*network.EndpointSettings{
			timingNetwork: {NetworkID: timingNetwork, Aliases: []string{"ledger"}},
		}},
	}}, nil
}

func (d *partitionDaemon) NetworkDisconnect(context.Context, string, client.NetworkDisconnectOptions) (client.NetworkDisconnectResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.detachedAt, d.detachCalls = time.Now(), d.detachCalls+1
	return client.NetworkDisconnectResult{}, nil
}

func (d *partitionDaemon) NetworkConnect(context.Context, string, client.NetworkConnectOptions) (client.NetworkConnectResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reattachedAt, d.reattachCalls = time.Now(), d.reattachCalls+1
	return client.NetworkConnectResult{}, nil
}

// partition is the fault the report came from, with its waits shortened so
// the test takes under a second. The two waits differ so that a report which
// confused one with the other is caught.
func partition() schema.Fault {
	return schema.Fault{
		Name: "cut-the-service-off-from-the-database", Kind: schema.FaultNetworkPartition,
		Target: schema.FaultTargetService, Service: "ledger",
		After: "150ms", Hold: "400ms",
	}
}

func runPartition(t *testing.T) (report.ChaosFault, *partitionDaemon, time.Time) {
	t.Helper()
	d := &partitionDaemon{}
	inj, err := fault.New(d, timingEnv)
	require.NoError(t, err)
	started := time.Now()
	entry := env.RunOneFaultForTest(t.Context(), inj, partition())
	require.Empty(t, entry.Error)
	require.True(t, entry.Injected, "the partition did not go in, so nothing below measures anything")
	require.True(t, entry.Undone)
	require.Equal(t, 1, d.detachCalls)
	require.Equal(t, 1, d.reattachCalls)
	return entry, d, started
}

// The defect this was written against. A network partition held for its full
// declared five seconds was reported to the agent that asked for it with a
// duration of 0 ms, because the line that set the duration was deferred and
// wrote into a local after the return had already copied it. Every fault that
// did not run a durability proof reported zero, whatever happened.
func TestRunOneFault_ReportsTheStepsRealDuration(t *testing.T) {
	entry, _, started := runPartition(t)
	elapsed := time.Since(started)

	require.GreaterOrEqual(t, entry.DurationMs, int64(550),
		"the step waited 150ms, held the fault 400ms and undid it, and reported %dms", entry.DurationMs)
	require.LessOrEqual(t, entry.DurationMs, elapsed.Milliseconds(),
		"the step reported longer than it took")
}

// What a reader wants from a partition is how long the service was cut off,
// which is not the step's duration: the step includes the wait before the
// fault. The report carries the span the daemon saw between the detach and
// the attach, measured, and the hold the manifest asked for beside it.
func TestRunOneFault_ReportsHowLongTheFaultWasInPlace(t *testing.T) {
	entry, d, _ := runPartition(t)
	daemonSaw := d.reattachedAt.Sub(d.detachedAt)

	require.Equal(t, int64(400), entry.HoldDeclaredMs, "the declared hold did not reach the report")
	require.GreaterOrEqual(t, entry.InPlaceMs, int64(400),
		"the fault was held 400ms and the report says it was in place for %dms", entry.InPlaceMs)
	require.LessOrEqual(t, entry.InPlaceMs, daemonSaw.Milliseconds(),
		"the report says the fault was in place longer than the daemon saw it detached (%s)", daemonSaw)
	require.Less(t, entry.InPlaceMs, entry.DurationMs,
		"the time in place is the whole step, so the wait before the fault was counted as part of it")
}

// The declared wait before a fault used to be read only by the durability
// proof, so a service fault went in the instant its step began whatever the
// manifest said. Measured live, the partition went in 0.47s into a run that
// declared after: 5s.
func TestRunOneFault_WaitsTheDeclaredTimeBeforeTheFault(t *testing.T) {
	_, d, started := runPartition(t)
	require.GreaterOrEqual(t, d.detachedAt.Sub(started), 150*time.Millisecond,
		"the service was detached %s into the step, before the declared 150ms wait", d.detachedAt.Sub(started))
}

// The report says "in place" only for a fault that has something to put back.
// A killed process has no undo, and the manifest's own description of hold
// says that for such a fault it is the wait before the result is read. The
// report package spells that kind as a string, so this holds its spelling to
// the injector's constant.
func TestInPlaceSays_AKilledProcessIsAWaitNotAHold(t *testing.T) {
	f := report.ChaosFault{
		Kind: string(fault.KindProcessKill), Injected: true, Undone: true,
		InPlaceMs: 3004, HoldDeclaredMs: 3000,
	}
	require.Equal(t,
		"followed by a wait of 3.004s (declared 3s) before the result was read, since a killed process has no undo",
		f.InPlaceSays())
}
