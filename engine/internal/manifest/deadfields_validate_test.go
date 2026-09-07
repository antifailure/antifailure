package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The two fields a manifest could declare and the engine would discard.
//
// replicas, resources.cpu and resources.memory were all three in
// schemas/manifest.v1.json and in the reference, so a reader found them, wrote
// them, and got nothing. All three were refused rather than honoured, because
// the defect being closed was the silence.
//
// replicas is honoured now, in both runtimes, and its tests moved to
// replicas_validate_test.go. The other two are still refused, and still for
// the reason in the comment on deadFields: neither runtime emits a resource
// requirement at all, so a cap written here is enforced nowhere.

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
	// Two services, one of them carrying both keys, so that the report names
	// which service the author has to edit rather than the first one in the
	// file. A message naming the wrong service is worse than none: it sends
	// somebody to a line that is correct.
	body := `
version: 1
name: shop
services:
  - name: web
    port: 3000
  - name: roller
    kind: worker
    resources:
      cpu: 500m
      memory: 2Gi
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `service "roller" would run with no CPU limit`)
	require.Contains(t, msg, `service "roller" would run with no memory limit`)
	require.NotContains(t, msg, `service "web" would run`)
}

func TestParse_AcceptsAManifestThatDeclaresNoneOfThem(t *testing.T) {
	t.Parallel()
	// The control that makes the two above mean something. Normalization used
	// to fill all three in on every manifest, so a refusal keyed on the value
	// rather than on the declaration would have refused every manifest in the
	// repository, and a check that says no to everything says nothing.
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
