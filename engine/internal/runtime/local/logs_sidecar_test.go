package local_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
)

// TestLogs_TheSidecarIsReadableByNameAndIsNotAService is the diagnostic half.
//
// af logs asked for the sidecar returned zero lines and no error, because Logs
// skipped every container whose kind was not a service and the sidecar's is
// KindSidecar. There was already a caller: the end to end emulate test prints
// the sidecar's log when the application fails to reach the emulator, and on the
// real 502 it printed the word "sidecar:" followed by nothing. The one place the
// reason was written down was the one place nobody could read.
func TestLogs_TheSidecarIsReadableByNameAndIsNotAService(t *testing.T) {
	r := requireRuntime(t)

	cli, err := dockerutil.Client()
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	digest := repoDigest(t, ctx, cli, proberImage)
	id := envID(t, r, "emureadylogs")

	_, err = r.Up(ctx, emulatorSpec(id, digest, emulatorCommand, theApplication(), nil))
	require.NoError(t, err)

	byName, err := r.Logs(ctx, id, local.ProxyAlias, 200)
	require.NoError(t, err)
	require.NotEmpty(t, byName,
		"the sidecar's log is empty when it is asked for by name, so every diagnostic that "+
			"prints it prints nothing and reads as the sidecar having had nothing to say")
	var text strings.Builder
	for _, l := range byName {
		require.Equal(t, local.ProxyAlias, l.Service,
			"a line attributed to %q came back when the sidecar was asked for", l.Service)
		text.WriteString(l.Text)
		text.WriteString("\n")
	}
	require.Contains(t, text.String(), `"event":"ready"`,
		"what came back is not the sidecar's log: it does not carry the line the sidecar "+
			"prints when it binds.\n%s", text.String())

	// The control, and it is the half a widening would break silently. The
	// sidecar is not a service, so a request for everything must not start
	// including it: it would put the environment's whole decision log into the
	// output of af logs, where the application's own output is what somebody is
	// reading.
	//
	// Waited for rather than read once, and that is the whole of this test's
	// history with the machine it runs on. Up returns as soon as a worker's
	// container is RUNNING, because waitReady has nothing further to wait for:
	// "a worker is ready when it is running, and asking for more would mean
	// inventing a protocol the application does not speak". So when Up returns
	// the application has printed nothing, and whether it has printed anything
	// by the time this line runs is decided by how long the two reads above
	// take on this daemon. On a loaded machine they cost longer than the
	// application's first request and the control passes; on a clean runner
	// they do not. On 2026-09-21 it read an empty slice and reported "no
	// service logs came back at all" against #532, which changes three
	// Terraform defaults and a changelog fragment and no Go at all. A control
	// decided by the speed of the machine says nothing about containment, and
	// it spends its failures on whoever pushed last.
	//
	// The half above needs no such wait, and the asymmetry is not an oversight:
	// Up cannot return until waitProxyReady has SEEN `"event":"ready"` in that
	// very log, so the sidecar has demonstrably spoken before this test starts.
	all := waitForEveryServiceToSpeak(t, ctx, r, id)
	for _, l := range all {
		require.NotEqual(t, local.ProxyAlias, l.Service,
			"the sidecar is in the output of a request for every service, which is not what "+
				"af logs is for: %s", l.Text)
	}
}

// waitForEveryServiceToSpeak makes the request af logs makes when nobody names
// a service, and waits until the application has finished saying its piece.
//
// It waits for the application's own end marker rather than for the first line
// to appear, which costs nothing here and buys the control a COMPLETE log to
// examine: a partial read could miss a sidecar line that a widening had added
// at the end of it, and a control that samples half the output is a weaker
// answer to the question this test asks.
//
// The deadline matches the sibling helper in emulate_e2e_test.go, which has
// polled for this same marker since it was written. The two are separate
// because that one asks for the application BY NAME and this one asks for
// every service, and it is the difference between those two requests that the
// sidecar can be wrongly included in.
func waitForEveryServiceToSpeak(
	t *testing.T, ctx context.Context, r *local.Runtime, id string,
) []local.LogLine {
	t.Helper()
	deadline := time.Now().Add(4 * time.Minute)
	for {
		lines, err := r.Logs(ctx, id, "", 200)
		require.NoError(t, err)
		var text strings.Builder
		for _, l := range lines {
			text.WriteString(l.Text)
			text.WriteString("\n")
		}
		if strings.Contains(text.String(), "AF-APP-DONE") {
			return lines
		}
		if time.Now().After(deadline) {
			t.Fatalf("the application never reported, so this control proves nothing. "+
				"%d lines came back from a request for every service:\n%s",
				len(lines), text.String())
		}
		time.Sleep(250 * time.Millisecond)
	}
}
