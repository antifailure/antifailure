package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// What af status says about the size a service is running at.
//
// A size nobody can see is the same as no size. resources.memory is written by
// somebody who is about to trust that one environment cannot starve another,
// and the place they look to find out is this command. The field was read back
// off the running object before this file existed and then nothing read it,
// which is the dead code that looks like a feature.

func TestRenderServices_NamesTheAppliedSizeOnlyWhereThereIsOne(t *testing.T) {
	var buf bytes.Buffer
	e := &Env{Out: NewOutput(&buf, &buf)}
	renderServices(e, []provider.RunningService{
		{Name: "web", Kind: "web", URL: "http://127.0.0.1:8080", Ready: true, Instances: 1},
		{
			Name: "clickhouse", Kind: "worker", State: "running", Ready: true, Instances: 1,
			CPUMillis: 2000, MemoryBytes: 4 * 1024 * 1024 * 1024,
		},
	})
	out := buf.String()

	require.Contains(t, out, "2 CPU, 4Gi",
		"a service running at a size has to say so somewhere a person reads")
	// The control, and the reason the line is conditional. Every service that
	// named no size runs uncapped, and a line saying so on all of them would
	// train the eye past the one that says 4Gi.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "web") {
			require.NotContains(t, line, "CPU",
				"the uncapped line must not carry a size")
			require.NotContains(t, line, "Mi")
		}
	}
}

func TestRenderServices_NamesTheHalfOfTheSizeThatWasApplied(t *testing.T) {
	var buf bytes.Buffer
	e := &Env{Out: NewOutput(&buf, &buf)}
	renderServices(e, []provider.RunningService{
		{Name: "roller", Kind: "worker", State: "running", Ready: true, MemoryBytes: 512 * 1024 * 1024},
		{Name: "api", Kind: "worker", State: "running", Ready: true, CPUMillis: 500},
	})
	out := buf.String()
	require.Contains(t, out, "512Mi")
	require.Contains(t, out, "500m CPU")
}

func TestServicesJSON_CarriesTheSizeInTheUnitsTheManifestUses(t *testing.T) {
	// In the manifest's units rather than in thousandths and bytes, because
	// the reader's next move after seeing it is to compare it against the key
	// they wrote, and a number they have to convert first is one they will not
	// check.
	out := servicesJSON([]provider.RunningService{
		{Name: "clickhouse", CPUMillis: 2000, MemoryBytes: 4 * 1024 * 1024 * 1024},
	})
	require.Len(t, out, 1)
	require.Equal(t, "2", out[0].CPU)
	require.Equal(t, "4Gi", out[0].Memory)
}

func TestServicesJSON_OmitsASizeThatWasNeverApplied(t *testing.T) {
	// Omitted rather than zeroed, which is the opposite of the choice the
	// instance count makes and for the opposite reason: one is what a service
	// with no replicas key is running, and zero is not a size anything is
	// running at. An absent key means uncapped, and there is no number that
	// says that.
	blob, err := json.Marshal(servicesJSON([]provider.RunningService{{Name: "web"}}))
	require.NoError(t, err)
	require.NotContains(t, string(blob), `"cpu"`)
	require.NotContains(t, string(blob), `"memory"`)
	// The count is still there, because absent and one are not the same
	// question there.
	require.Contains(t, string(blob), `"instances":1`)
}
