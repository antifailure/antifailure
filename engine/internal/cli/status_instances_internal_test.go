package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// What af status says about a service running more than one instance.
//
// A count nobody can see is the same as no count. `replicas: 3` is written by
// somebody who is about to trust that three things are running, and the place
// they look to find out is this command.

func TestRenderServices_NamesTheInstanceCountOnlyWhenItIsMoreThanOne(t *testing.T) {
	var buf bytes.Buffer
	e := &Env{Out: NewOutput(&buf, &buf)}
	renderServices(e, []provider.RunningService{
		{Name: "web", Kind: "web", URL: "http://127.0.0.1:8080", Ready: true, Instances: 1},
		{Name: "roller", Kind: "worker", State: "running", Ready: true, Instances: 3},
	})
	out := buf.String()

	require.Contains(t, out, "3 instances",
		"a service running three instances has to say so somewhere a person reads")
	// The control, and the reason the count is conditional. A count of one on
	// every line of every environment anybody has ever run would train the eye
	// to skip the line on the one environment where it says three.
	require.NotContains(t, out, "1 instances")
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "web") {
			require.NotContains(t, line, "instance",
				"the single instance line must not carry a count")
		}
	}
}

func TestServicesJSON_AlwaysCarriesACount(t *testing.T) {
	out := servicesJSON([]provider.RunningService{
		{Name: "roller", Instances: 3},
		// A runtime that predates instance counts reports none, and none is
		// not what it is running. A consumer reading absent as one and a
		// consumer reading absent as unknown would both be reasonable, and
		// only one of them would be right, so the field is never absent.
		{Name: "old"},
	})
	require.Len(t, out, 2)
	require.Equal(t, 3, out[0].Instances)
	require.Equal(t, 1, out[1].Instances)
}
