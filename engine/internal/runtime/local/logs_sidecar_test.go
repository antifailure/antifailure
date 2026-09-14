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
	all, err := r.Logs(ctx, id, "", 200)
	require.NoError(t, err)
	require.NotEmpty(t, all, "no service logs came back at all, so this control proves nothing")
	for _, l := range all {
		require.NotEqual(t, local.ProxyAlias, l.Service,
			"the sidecar is in the output of a request for every service, which is not what "+
				"af logs is for: %s", l.Text)
	}
}
