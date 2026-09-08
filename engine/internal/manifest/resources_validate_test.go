package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// resources.cpu and resources.memory, once they are read.
//
// Both spent one release refused outright, and that was the right answer for
// exactly as long as they did nothing: the schema documented them, the
// reference rendered them, a manifest carrying them parsed without a word, and
// neither runtime emitted a resource requirement at all, so a cap written here
// was enforced nowhere and the service carrying it ran with none. Both
// runtimes apply the value now, so the refusal is gone and what is left are
// the rules a runtime cannot rescue.
//
// The file this replaces was deadfields_validate_test.go, which held the
// refusals. replicas left it one release earlier for the same reason.

func TestParse_AcceptsAServiceAskingForASize(t *testing.T) {
	t.Parallel()
	// The value the whole field exists for, and the shape somebody copies out
	// of a Deployment.
	m := mustParse(t, minimal+"    resources:\n      cpu: 500m\n      memory: 2Gi\n")
	require.Equal(t, "500m", m.Services[0].Resources.CPU)
	require.Equal(t, "2Gi", m.Services[0].Resources.Memory)
}

func TestParse_AcceptsACPUWrittenAsAFractionOfACore(t *testing.T) {
	t.Parallel()
	// 0.5 and 500m are the same share and both are written in the wild.
	// Refusing either would be refusing the spelling rather than the value.
	m := mustParse(t, minimal+"    resources:\n      cpu: \"0.5\"\n")
	require.Equal(t, "0.5", m.Services[0].Resources.CPU)
}

func TestParse_RefusesACPUThatIsNotAQuantity(t *testing.T) {
	t.Parallel()
	// Refused rather than dropped, which is the whole argument of this lane
	// wearing a different hat. Nothing validates a manifest against the JSON
	// Schema at parse time, so the pattern in schemas/manifest.v1.json does
	// not stand between an author and this check.
	msg := messages(problems(t, mustFail(t, minimal+"    resources:\n      cpu: half\n")))
	require.Contains(t, msg, `Service "web" asks for "half" of CPU, which is not a quantity.`)
	require.Contains(t, msg, "thousandths with an m")
}

func TestParse_RefusesACPUShareThatRoundsToNothing(t *testing.T) {
	t.Parallel()
	// A thousandth of a core is the finest thing Kubernetes expresses and the
	// finest thing Docker's NanoCPUs holds without rounding to zero. Below it
	// the value is not a small request, it is a typo, and accepting it would
	// silently produce a container with no cap at all.
	msg := messages(problems(t, mustFail(t, minimal+"    resources:\n      cpu: \"0.0001\"\n")))
	require.Contains(t, msg,
		`Service "web" asks for "0.0001" of CPU, which is a fraction of a thousandth of a core.`)
	require.Contains(t, msg, "would round to no cap at all")
	// And NOT the message for a value that is not a quantity, which is what
	// this said before the two were separated. 0.0001 is a quantity, and
	// telling its author it is not sends them looking for a typo that is not
	// there.
	require.NotContains(t, msg, `"0.0001" of CPU, which is not a quantity`)
}

func TestParse_RefusesAZeroCPUShare(t *testing.T) {
	t.Parallel()
	// A written zero and an omitted key are different statements. An omitted
	// key says the service runs uncapped, which is what every service had
	// before this key was honoured. A written zero asks for no CPU at all,
	// and a container given none runs nothing.
	msg := messages(problems(t, mustFail(t, minimal+"    resources:\n      cpu: \"0\"\n")))
	require.Contains(t, msg, `Service "web" asks for "0" of CPU, which is no CPU at all.`)
	require.Contains(t, msg, "The smallest share is 1m.")
}

func TestParse_RefusesAMemorySizeWithNoUnit(t *testing.T) {
	t.Parallel()
	// The one place this deliberately refuses something Kubernetes accepts.
	// "memory: 512" there is 512 bytes, and nobody who writes it means 512
	// bytes: they mean megabytes, and the container they get is refused by the
	// daemon for being under its floor, several seconds into an af up, with a
	// message about a daemon constant rather than about the line they wrote.
	msg := messages(problems(t, mustFail(t, minimal+"    resources:\n      memory: \"512\"\n")))
	require.Contains(t, msg, `Service "web" asks for "512" of memory, which is not a quantity.`)
	require.Contains(t, msg, "nobody who writes 512 means 512 bytes")
}

func TestParse_RefusesAMemorySizeUnderWhatAContainerMayHave(t *testing.T) {
	t.Parallel()
	// Six megabytes is the Docker daemon's own floor. Refusing it here names
	// the key; letting it through names a daemon constant at a point where
	// the manifest is no longer on screen.
	msg := messages(problems(t, mustFail(t, minimal+"    resources:\n      memory: 4Mi\n")))
	require.Contains(t, msg, `Service "web" asks for "4Mi" of memory, which is under what a container may have.`)
	require.Contains(t, msg, "6Mi")
}

func TestParse_DoesNotCapTheSizeFromTheManifest(t *testing.T) {
	t.Parallel()
	// THERE IS NO CEILING HERE, on purpose, and this is the test that says so.
	// A ceiling would have to be a constant, and a constant cannot know the
	// machine: 64Gi is absurd on a laptop and unremarkable on a cluster node.
	// The runtime refuses what it cannot place, against the free space it
	// actually read, and names the shortfall. "This cluster does not have
	// that" is a better answer than "the manifest may not say that".
	m := mustParse(t, minimal+"    resources:\n      cpu: \"64\"\n      memory: 512Gi\n")
	require.Equal(t, "512Gi", m.Services[0].Resources.Memory)
}

func TestParse_NamesTheServiceCarryingTheBadSize(t *testing.T) {
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
      cpu: lots
      memory: heaps
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `Service "roller" asks for "lots" of CPU`)
	require.Contains(t, msg, `Service "roller" asks for "heaps" of memory`)
	require.NotContains(t, msg, `Service "web" asks for`)
}

func TestParse_AcceptsAManifestThatNamesNoSize(t *testing.T) {
	t.Parallel()
	// The control that makes every refusal above mean something. Normalization
	// fills neither key in, so a manifest that says nothing about size carries
	// no resources block at all, and a check keyed on the value rather than on
	// the declaration would have refused every manifest in the repository. A
	// check that says no to everything says nothing.
	m := mustParse(t, minimal)
	require.Nil(t, m.Services[0].Resources)
}

func TestParse_AcceptsAnEmptyResourcesBlock(t *testing.T) {
	t.Parallel()
	// An empty block promises nothing, so there is nothing to check. The
	// refusal is keyed on the leaf an author wrote, never on the parent.
	mustParse(t, minimal+"    resources: {}\n")
}
