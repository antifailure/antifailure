package capacity_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/capacity"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// Whether a node can hold what an environment asked for.
//
// The failure this package answers is downstream of honouring the size at all.
// Emitting a request fixes the placement and creates a second problem: a
// request larger than anything on the cluster is ACCEPTED by the API server
// and then never scheduled, so the pod sits Pending with an event nobody is
// watching and af up waits out its readiness timeout and reports a service
// that did not start. That reads as a slow cluster rather than as a request
// nothing can satisfy.

func node(name string, milliCPU, memoryBytes int64) capacity.Node {
	return capacity.Node{Name: name, MilliCPU: milliCPU, MemoryBytes: memoryBytes}
}

const gib = 1024 * 1024 * 1024

func TestFits_AcceptsAnEnvironmentThereIsRoomFor(t *testing.T) {
	t.Parallel()
	require.NoError(t, capacity.Fits(
		[]capacity.Node{node("a", 4000, 8*gib)},
		[]capacity.Ask{{Service: "web", Instances: 2, MilliCPU: 500, MemoryBytes: gib}},
	))
}

func TestFits_RefusesOneInstanceLargerThanTheRoomiestNode(t *testing.T) {
	t.Parallel()
	// The failure with no remedy. A pod asking for more memory than the
	// largest node has is unschedulable however empty the cluster is, and no
	// amount of waiting changes it, so it is worth a sentence rather than a
	// readiness timeout.
	err := capacity.Fits(
		[]capacity.Node{node("a", 4000, 4*gib), node("b", 8000, 8*gib)},
		[]capacity.Ask{{Service: "clickhouse", Instances: 1, MemoryBytes: 32 * gib}},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), `service "clickhouse" asks for 32Gi of memory per instance`)
	require.Contains(t, err.Error(), "the roomiest node has 8Gi free")
	require.Contains(t, err.Error(), "cannot be placed at all")
}

func TestFits_MeasuresPerInstanceAgainstTheROOMIESTNodeAndNotTheFirst(t *testing.T) {
	t.Parallel()
	// The instance fits on the second node and not on the first. Comparing
	// against whichever node the list happened to start with would refuse an
	// environment the cluster can hold, and a check that says no to something
	// possible is worse than the Pending pod it replaced: it is a wrong
	// answer delivered confidently.
	require.NoError(t, capacity.Fits(
		[]capacity.Node{node("small", 500, 1*gib), node("big", 8000, 16*gib)},
		[]capacity.Ask{{Service: "clickhouse", Instances: 1, MilliCPU: 2000, MemoryBytes: 8 * gib}},
	))
}

func TestFits_RefusesAnEnvironmentLargerThanEverythingFree(t *testing.T) {
	t.Parallel()
	// Each service fits somewhere and the set does not. This is the case a
	// per instance check alone cannot see.
	err := capacity.Fits(
		[]capacity.Node{node("a", 4000, 4*gib), node("b", 4000, 4*gib)},
		[]capacity.Ask{
			{Service: "web", Instances: 3, MemoryBytes: 2 * gib},
			{Service: "clickhouse", Instances: 1, MemoryBytes: 4 * gib},
		},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "the environment asks for 10Gi of memory in total")
	require.Contains(t, err.Error(), "8Gi is free across 2 nodes")
	require.Contains(t, err.Error(), "short by 2Gi")
}

func TestFits_MultipliesTheAskByTheInstanceCount(t *testing.T) {
	t.Parallel()
	// A service asking for replicas: 3 and 2Gi asks the cluster for six, not
	// two. Counting the per instance figure once is how an environment three
	// times the size of the node it was checked against gets accepted.
	one := []capacity.Ask{{Service: "roller", Instances: 1, MemoryBytes: 2 * gib}}
	three := []capacity.Ask{{Service: "roller", Instances: 3, MemoryBytes: 2 * gib}}
	nodes := []capacity.Node{node("a", 4000, 5*gib)}
	require.NoError(t, capacity.Fits(nodes, one))
	require.Error(t, capacity.Fits(nodes, three),
		"three instances of a 2Gi service is 6Gi and the node has 5Gi")
}

func TestFits_ReadsAMissingInstanceCountAsOne(t *testing.T) {
	t.Parallel()
	// Every manifest written before instance counts existed arrives with
	// zero, and zero instances would make an environment look free.
	_, mem := capacity.Total([]capacity.Ask{{Service: "web", MemoryBytes: 2 * gib}})
	require.Equal(t, int64(2*gib), mem)
}

func TestFits_NamesEveryShortfallRatherThanTheFirst(t *testing.T) {
	t.Parallel()
	// An author who lowers the one size the message named and runs again into
	// the next one has been given a sequence of refusals where one would have
	// done.
	err := capacity.Fits(
		[]capacity.Node{node("a", 1000, 2*gib)},
		[]capacity.Ask{
			{Service: "clickhouse", Instances: 1, MilliCPU: 4000, MemoryBytes: 16 * gib},
		},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "of memory per instance")
	require.Contains(t, err.Error(), "CPU per instance")
}

func TestFits_AcceptsAnEnvironmentThatAskedForNothing(t *testing.T) {
	t.Parallel()
	// A manifest that declares no resources is the manifest every environment
	// was before this key was honoured, and refusing those would break every
	// existing repository to enforce a promise none of them made.
	require.NoError(t, capacity.Fits([]capacity.Node{node("a", 100, 1)}, nil))
}

func TestFits_SaysNothingWhenItCouldNotSeeTheNodes(t *testing.T) {
	t.Parallel()
	// No nodes is "this could not be read", not "there is no room". Refusing
	// here would break every cluster where af has namespace scoped access and
	// nothing more, which is a reasonable way to run it. The caller is the one
	// that says the size was not checked.
	require.NoError(t, capacity.Fits(nil,
		[]capacity.Ask{{Service: "clickhouse", Instances: 1, MemoryBytes: 999 * gib}}))
}

func TestEnvironmentsPerNode_DividesTheNodeByTheEnvironment(t *testing.T) {
	t.Parallel()
	// The number this lane owes. It is a division and not a measurement
	// because that is what a request MEANS: the scheduler places against the
	// request, so once the request is real the count is arithmetic the
	// scheduler will agree with.
	got := capacity.EnvironmentsPerNode(node("a", 16000, 64*gib), []capacity.Ask{
		{Service: "web", Instances: 2, MilliCPU: 500, MemoryBytes: gib},
		{Service: "clickhouse", Instances: 1, MilliCPU: 2000, MemoryBytes: 8 * gib},
	})
	require.False(t, got.Unbounded)
	require.Equal(t, int64(3000), got.EnvMilliCPU)
	require.Equal(t, int64(10*gib), got.EnvMemoryBytes)
	require.Equal(t, 5, got.Count, "16 cores over 3, and 64Gi over 10Gi, is 5 and 6")
	require.Equal(t, "cpu", got.Binding, "the dimension that ran out first")
}

func TestEnvironmentsPerNode_NamesMemoryWhenMemoryIsWhatRunsOut(t *testing.T) {
	t.Parallel()
	// Which dimension binds is the actionable half of the number. "Five, and
	// memory is why" tells somebody what to buy; "five" does not.
	got := capacity.EnvironmentsPerNode(node("a", 64000, 16*gib), []capacity.Ask{
		{Service: "clickhouse", Instances: 1, MilliCPU: 1000, MemoryBytes: 4 * gib},
	})
	require.Equal(t, 4, got.Count)
	require.Equal(t, "memory", got.Binding)
}

func TestEnvironmentsPerNode_ReportsUnboundedWhenNothingWasAskedFor(t *testing.T) {
	t.Parallel()
	// The state every environment this engine placed was in before resources
	// were honoured, and the reason nobody could capacity plan. That is not a
	// large number of environments per node, it is NO number: with no request
	// the scheduler has nothing to place against and keeps accepting them
	// until the machine falls over. Printing a very large integer there would
	// read as an answer.
	got := capacity.EnvironmentsPerNode(node("a", 16000, 64*gib), []capacity.Ask{
		{Service: "web", Instances: 2},
	})
	require.True(t, got.Unbounded)
	require.Zero(t, got.Count)
	require.Empty(t, got.Binding)
}

func TestEnvironmentsPerNode_CountsTheDimensionThatWasNamedWhenOnlyOneWas(t *testing.T) {
	t.Parallel()
	// A manifest may cap memory alone, and an environment that named only
	// memory is bounded by memory. Treating the unnamed dimension as a zero
	// divisor rather than as "not a bound" is how that becomes a division by
	// zero or an answer of none.
	got := capacity.EnvironmentsPerNode(node("a", 16000, 64*gib), []capacity.Ask{
		{Service: "clickhouse", Instances: 1, MemoryBytes: 8 * gib},
	})
	require.False(t, got.Unbounded)
	require.Equal(t, 8, got.Count)
	require.Equal(t, "memory", got.Binding)
}

func TestEnvironmentsPerNode_ReportsNoneWhenOneDoesNotFit(t *testing.T) {
	t.Parallel()
	// Zero is a real answer and it has to be reachable, because a node that
	// cannot hold one environment is the case a capacity plan most needs to
	// name.
	got := capacity.EnvironmentsPerNode(node("small", 1000, 2*gib), []capacity.Ask{
		{Service: "clickhouse", Instances: 1, MemoryBytes: 8 * gib},
	})
	require.Equal(t, 0, got.Count)
}

func TestAsksFor_LeavesOutAServiceThatNamedNoSize(t *testing.T) {
	t.Parallel()
	// A zero row would make the environment look like it asked for something
	// and got nothing, and it would put a service with no request into a
	// shortfall message that has nothing to say about it.
	//
	// Tested here rather than once per runtime because there is one of these
	// now. There were two, identical, in the local and the Kubernetes
	// packages, which is the shape serviceSpec had when health_timeout went
	// missing from one of the two copies.
	got := capacity.AsksFor([]provider.ServiceSpec{
		{Name: "web", Port: 8080},
		{Name: "clickhouse", Replicas: 2, MemoryBytes: 4 * gib},
	})
	require.Len(t, got, 1)
	require.Equal(t, capacity.Ask{
		Service: "clickhouse", Instances: 2, MemoryBytes: 4 * gib,
	}, got[0])
}

func TestAsksFor_ReadsTheInstanceCountThroughTheSpecsOwnRule(t *testing.T) {
	t.Parallel()
	// Zero replicas is one instance, and the rule for that lives in
	// provider.ServiceSpec.Instances rather than being restated here. A second
	// spelling of it would be the thing this function was merged to stop.
	got := capacity.AsksFor([]provider.ServiceSpec{{Name: "web", MemoryBytes: gib}})
	require.Len(t, got, 1)
	require.Equal(t, 1, got[0].Instances)
}
