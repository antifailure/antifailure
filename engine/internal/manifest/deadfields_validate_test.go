package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The three fields a manifest could declare and the engine would discard.
//
// replicas, resources.cpu and resources.memory were in schemas/manifest.v1.json
// and in the reference, so a reader found them, wrote them, and got nothing:
// the local runtime never mentions replicas, both Kubernetes Deployments
// hardcode one, and neither runtime emits a resource requirement at all. A
// manifest asking for three instances of a worker ran one and the run went
// green having proved nothing about the case its author was worried about.
//
// Refused rather than honoured here on purpose. Honouring them is real work in
// both runtimes and it is somebody else's lane; the defect this closes is the
// silence, and the silence is closed by saying no.

func TestParse_RefusesReplicasBecauseNothingReadsIt(t *testing.T) {
	t.Parallel()
	// Three, because three is the value somebody writes to reproduce a bug
	// that only happens at more than one instance, and one instance is what
	// they used to get.
	body := minimal + "    replicas: 3\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, "Nothing reads replicas")
	require.Contains(t, msg, "would run one instance whatever this says")
}

func TestParse_RefusesReplicasEvenWhenItAsksForTheOneItWouldGet(t *testing.T) {
	t.Parallel()
	// replicas: 1 is the value whose outcome is correct today, and it is
	// refused too. Accepting it teaches the author that the key works, and
	// nothing tells them otherwise on the day they change it to three.
	msg := messages(problems(t, mustFail(t, minimal+"    replicas: 1\n")))
	require.Contains(t, msg, "Nothing reads replicas")
}

func TestParse_RefusesResourcesCPUBecauseNothingReadsIt(t *testing.T) {
	t.Parallel()
	body := minimal + "    resources:\n      cpu: \"2\"\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, "Nothing reads resources.cpu")
	require.Contains(t, msg, "would run with no CPU limit at all")
}

func TestParse_RefusesResourcesMemoryBecauseNothingReadsIt(t *testing.T) {
	t.Parallel()
	body := minimal + "    resources:\n      memory: 512Mi\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, "Nothing reads resources.memory")
	require.Contains(t, msg, "would run with no memory limit at all")
}

func TestParse_RefusalNamesTheServiceAndEveryFieldItCarries(t *testing.T) {
	t.Parallel()
	// Two services, one of them carrying all three keys, so that the report
	// names which service the author has to edit rather than the first one in
	// the file. A message naming the wrong service is worse than none: it
	// sends somebody to a line that is correct.
	body := `
version: 1
name: shop
services:
  - name: web
    port: 3000
  - name: roller
    kind: worker
    replicas: 3
    resources:
      cpu: 500m
      memory: 2Gi
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `service "roller" would run one instance`)
	require.Contains(t, msg, `service "roller" would run with no CPU limit`)
	require.Contains(t, msg, `service "roller" would run with no memory limit`)
	require.NotContains(t, msg, `service "web" would run`)
}

func TestParse_AcceptsAManifestThatDeclaresNoneOfThem(t *testing.T) {
	t.Parallel()
	// The control that makes the three above mean something. Normalization
	// used to fill all three in on every manifest, so a refusal keyed on the
	// value rather than on the declaration would have refused every manifest
	// in the repository, and a check that says no to everything says nothing.
	m := mustParse(t, minimal)
	require.Zero(t, m.Services[0].Replicas)
	require.Nil(t, m.Services[0].Resources)
}

func TestParse_AcceptsAnEmptyResourcesBlock(t *testing.T) {
	t.Parallel()
	// An empty block promises nothing, so there is nothing to refuse. The
	// refusal is keyed on the leaf an author wrote, not on the parent.
	mustParse(t, minimal+"    resources: {}\n")
}
