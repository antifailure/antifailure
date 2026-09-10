package local_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The number this lane owes, measured rather than asserted.
//
// The claim is "0 lines of application change and 0 endpoint overrides", and
// the only way to measure that is to run ONE application, unchanged, in two
// environments that differ in nothing but the egress policy, and show that the
// outcome differs while the application does not. A test that ran a
// purpose-built application against an emulator would prove that the emulator
// answers, which nobody doubts, and would say nothing at all about the number.
//
// So `application` below is built once, used twice, and compared for equality
// between the two runs. The comparison is the measurement.
//
// WHAT THIS DOES NOT PROVE, said here rather than left to be assumed. The
// application speaks plain HTTP, because busybox's wget is what alpine ships
// and its handling of a custom certificate authority is not something this
// test should be discovering. The TLS half is proved in the sidecar's own
// package, where TestEmulate_AVerifyingClientReachesTheEmulatorAtTheProvidersHostname
// completes a real handshake for s3.amazonaws.com against the environment
// authority, with verification ON, and receives the emulator's body. Driving a
// vendor SDK through the whole path belongs to the Wave 3 lanes, which own the
// emulators.

// emulatorPort is what the stand-in emulator listens on.
const emulatorPort = 8080

// emulatorBody is what it answers with, and it is deliberately something no
// refusal and no error page could contain.
const emulatorBody = "AF-EMULATOR-ANSWERED-ListBucketResult"

// emulatorCommand serves one fixed answer with busybox nc.
//
// A stand-in rather than LocalStack, because this lane builds the routing and
// the Wave 3 lanes supply the emulators. What is being measured is the path,
// and a path that carries this carries anything.
//
// nc rather than httpd, and the reason is measured rather than assumed:
// alpine's busybox is built WITHOUT the httpd applet. `command -v httpd` in
// alpine:3.20 answers nothing while `command -v wget` answers /usr/bin/wget,
// which is how the first version of this file came to rely on one and reach
// for the other. The shell then exits at "httpd: not found", the container
// stops, nothing is listening on the port, and the sidecar answers 502. That
// failure arrives looking exactly like broken routing, and this test said so
// in as many words while the routing was fine.
//
// apk add busybox-extras is not the alternative it looks like. An emulator
// joins the inner network and nothing else, deliberately, so it has no route
// out and cannot fetch a package. A stand-in that needed the network to start
// would be testing the opposite of the containment this mode promises.
var emulatorCommand = []string{"/bin/sh", "-c", fmt.Sprintf(
	"while true; do printf 'HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n"+
		"Content-Length: %d\r\nConnection: close\r\n\r\n%s' | nc -l -p %d; done",
	len(emulatorBody), emulatorBody, emulatorPort)}

// theApplication is the unmodified application, built once.
//
// It asks for the provider's own hostname. There is no endpoint override, no
// AWS_ENDPOINT_URL, no base URL variable, and no client constructed one way
// for tests. This value is used verbatim in both environments below and the
// test asserts that it was.
func theApplication() provider.ServiceSpec {
	return provider.ServiceSpec{
		Name: "app", Image: proberImage, Kind: "worker",
		Command: "wget -T 10 -q -O - http://s3.amazonaws.com/ 2>&1 | tr -d '\\n'; " +
			"echo; echo AF-APP-DONE; sleep 240\n",
	}
}

func TestEmulate_TheSameApplicationReachesTheEmulatorAndIsRefusedWithoutIt(t *testing.T) {
	r := requireRuntime(t)

	cli, err := dockerutil.Client()
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	digest := repoDigest(t, ctx, cli, proberImage)

	// The control environment. No emulate rule and no emulator, so the
	// default of block decides and the application is refused. This is what
	// makes the second run mean something: without it, a body that arrived
	// could have arrived from anywhere.
	withoutID := envID(t, r, "emulatewithout")
	withoutApp := theApplication()
	_, err = r.Up(ctx, provider.EnvSpec{
		EnvID:    withoutID,
		Egress:   &schema.Egress{Default: schema.ModeBlock},
		Services: []provider.ServiceSpec{withoutApp},
	})
	require.NoError(t, err)
	withoutOut := waitForAppOutput(t, ctx, r, withoutID)
	require.NotContains(t, withoutOut, emulatorBody,
		"the application reached the emulator in an environment that has none")

	// The same application, one rule different.
	withID := envID(t, r, "emulatewith")
	withApp := theApplication()
	_, err = r.Up(ctx, provider.EnvSpec{
		EnvID: withID,
		Egress: &schema.Egress{
			Default: schema.ModeBlock,
			Rules: []schema.EgressRule{
				{Host: "s3.amazonaws.com", Mode: schema.ModeEmulate, Emulator: "probe"},
			},
		},
		Emulators: []provider.EmulatorSpec{{
			Name: "probe", Image: digest, Port: emulatorPort, Command: emulatorCommand,
		}},
		Services: []provider.ServiceSpec{withApp},
	})
	require.NoError(t, err)
	withOut := waitForAppOutput(t, ctx, r, withID)
	if !strings.Contains(withOut, emulatorBody) {
		// The sidecar's words, because the application cannot carry them.
		// busybox wget prints "server returned error" and DISCARDS the body on
		// a non-200, and the body is the only place the sidecar says which
		// failure this is: an emulator this environment is not running, or one
		// it is running but could not reach. Those are different bugs in
		// different files and they were the same three words on the terminal.
		t.Fatalf("the application did not reach the emulator. It is identical in both "+
			"runs, so the difference is in the environment, and the sidecar below says "+
			"where.\napplication:\n%s\nsidecar:\n%s",
			withOut, sidecarLog(t, ctx, r, withID))
	}

	// THE MEASUREMENT. Everything the application is, compared between the two
	// runs. Equal means the number is zero.
	require.Equal(t, withoutApp, withApp,
		"the application differs between the run that reached the emulator and the run "+
			"that did not, so the number this lane publishes is not zero")

	// And the second half of the number: nothing in the environment tells the
	// application where to go. An endpoint override is what this mode exists
	// to remove, so its absence is asserted rather than assumed.
	for name := range withApp.Env {
		upper := strings.ToUpper(name)
		require.False(t,
			strings.Contains(upper, "ENDPOINT") || strings.Contains(upper, "BASE_URL"),
			"the application is given %s, which is an endpoint override, and this mode "+
				"exists so that none is needed", name)
	}
	require.Empty(t, withApp.Env,
		"the application is given %d variables; the claim is that it needs none",
		len(withApp.Env))
}

// sidecarLog is what the sidecar said, for a failure the application cannot
// explain. Empty rather than fatal when it cannot be read, because it is a
// diagnostic on a path that has already failed and a second failure here would
// replace the first one's message with its own.
func sidecarLog(t *testing.T, ctx context.Context, r *local.Runtime, id string) string {
	t.Helper()
	lines, err := r.Logs(ctx, id, local.ProxyAlias, 200)
	if err != nil {
		return "could not be read: " + err.Error()
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Text)
		b.WriteString("\n")
	}
	return b.String()
}

// waitForAppOutput reads the application's log until it says it finished.
func waitForAppOutput(
	t *testing.T, ctx context.Context, r *local.Runtime, id string,
) string {
	t.Helper()
	deadline := time.Now().Add(4 * time.Minute)
	for {
		lines, err := r.Logs(ctx, id, "app", 200)
		require.NoError(t, err)
		var out strings.Builder
		for _, l := range lines {
			out.WriteString(l.Text)
			out.WriteString("\n")
		}
		text := out.String()
		if strings.Contains(text, "AF-APP-DONE") {
			return text
		}
		if time.Now().After(deadline) {
			t.Fatalf("the application never reported. Its output was:\n%s", text)
		}
		time.Sleep(2 * time.Second)
	}
}
