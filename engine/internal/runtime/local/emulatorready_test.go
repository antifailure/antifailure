package local_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The three orderings of "the emulator binds its port" against "the
// application makes its first call", each against real containers.
//
// This is the file the 502 needed. The engine started the emulator container,
// took the daemon's word that a started container is a ready one, and started
// the application; the application called out, the sidecar forwarded to a port
// nothing had bound yet, and the application was handed 502 Bad Gateway. It
// reproduced only when the emulator was slow, so ten consecutive runs of the
// end to end test passed on this machine while the same test failed in CI, and
// the one diagnostic anybody had, the engine's own progress line, said
// "emulator ready".
//
// A fake cannot hold these orderings apart. The whole question is what a real
// server inside a real container has done with its real port by the time the
// engine returns, and every part of that is the daemon's rather than this
// package's. So the arms below differ in ONE thing: when the process inside the
// emulator container binds. Immediately, twenty seconds late, and never.

// emulatorBindDelay is how late the second arm's emulator is.
//
// Twenty seconds, which is inside the range the real ones take: the measurement
// in guides/gcp.md has the gcloud emulators between 17.7 and 51.5 seconds. Long
// enough that an engine which did not wait would certainly have started the
// application first, and short enough to run in a test.
const emulatorBindDelay = 20 * time.Second

// delayedEmulatorCommand serves the same fixed answer as emulatorCommand, after
// sitting on its hands for d.
//
// The sleep is before the listener, not before the shell, so the container is
// running and the alias resolves the whole time. That is the real window: DNS
// answers, the container is up, and the port is closed.
func delayedEmulatorCommand(d time.Duration) []string {
	return []string{"/bin/sh", "-c", fmt.Sprintf(
		"sleep %d; while true; do printf 'HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n"+
			"Content-Length: %d\r\nConnection: close\r\n\r\n%s' | nc -l -p %d; done",
		int(d.Seconds()), len(emulatorBody), emulatorBody, emulatorPort)}
}

// transcript collects the engine's progress lines in the order they were
// emitted, because the claim being tested is an ORDER and the progress stream is
// where the engine states it.
type transcript struct {
	mu    sync.Mutex
	lines []string
}

func (tr *transcript) record(line string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.lines = append(tr.lines, line)
}

func (tr *transcript) all() []string {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return append([]string(nil), tr.lines...)
}

// indexOf is the position of the first line containing want, or -1.
func (tr *transcript) indexOf(want string) int {
	for i, l := range tr.all() {
		if strings.Contains(l, want) {
			return i
		}
	}
	return -1
}

// firstServiceLine is the position of the first line a service wrote, or -1.
//
// Every one of them is prefixed with the service's own name and a colon, which
// is what makes "did the application start" answerable from the transcript
// rather than from a container that has since been removed.
func (tr *transcript) firstServiceLine(service string) int {
	for i, l := range tr.all() {
		if strings.HasPrefix(l, service+":") {
			return i
		}
	}
	return -1
}

// ORDERING ONE. The emulator is listening before the application is created,
// which is the case that always worked, kept as the control: it proves the wait
// costs a healthy environment nothing and that the arms below differ in the one
// thing they claim to.
func TestEmulatorReadiness_AListenerThatIsAlreadyUpLetsTheApplicationThrough(t *testing.T) {
	r := requireRuntime(t)

	cli, err := dockerutil.Client()
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	digest := repoDigest(t, ctx, cli, proberImage)
	id := envID(t, r, "emureadyfast")
	var tr transcript

	_, err = r.Up(ctx, emulatorSpec(id, digest, emulatorCommand, theApplication(), tr.record))
	require.NoError(t, err)

	out := waitForAppOutput(t, ctx, r, id)
	require.Contains(t, out, emulatorBody,
		"the application did not reach an emulator that was listening before it started.\n"+
			"application:\n%s\nsidecar:\n%s", out, sidecarLog(t, ctx, r, id))

	ready := tr.indexOf("probe emulator is accepting connections")
	require.GreaterOrEqual(t, ready, 0,
		"the engine never said the emulator was accepting connections, so it never dialled "+
			"it and this environment came up on the old ordering.\n%s",
		strings.Join(tr.all(), "\n"))
	service := tr.firstServiceLine("app")
	require.GreaterOrEqual(t, service, 0, "the application never started")
	require.Less(t, ready, service,
		"the engine reported the emulator accepting connections AFTER the application had "+
			"already started, which is the ordering the 502 came from.\n%s",
		strings.Join(tr.all(), "\n"))
}

// ORDERING TWO. The emulator is started, does not bind for twenty seconds, and
// then binds. THIS IS THE ARM THAT WAS RED: on the tree this change is built on
// the application ran inside that window and read 502 Bad Gateway from the
// sidecar.
func TestEmulatorReadiness_ADelayedListenerHoldsTheApplicationUntilItBinds(t *testing.T) {
	r := requireRuntime(t)

	cli, err := dockerutil.Client()
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	digest := repoDigest(t, ctx, cli, proberImage)
	id := envID(t, r, "emureadyslow")
	var tr transcript

	started := time.Now()
	_, err = r.Up(ctx, emulatorSpec(
		id, digest, delayedEmulatorCommand(emulatorBindDelay), theApplication(), tr.record))
	require.NoError(t, err, "the environment refused an emulator that binds %s late, which "+
		"is inside the range the real ones take", emulatorBindDelay)
	waited := time.Since(started)

	// The measurement that matters, and it is the application's own output. The
	// application is unchanged from the end to end test: one call, at the
	// instant it starts, to the provider's own hostname. Reaching the emulator
	// means the engine held it back until the port was open, because a call made
	// before that is answered 502 and busybox wget discards the body.
	out := waitForAppOutput(t, ctx, r, id)
	require.Contains(t, out, emulatorBody,
		"the application was started before the emulator bound its port and read the "+
			"sidecar's 502, which is the defect. It waited %s.\napplication:\n%s\nsidecar:\n%s",
		waited, out, sidecarLog(t, ctx, r, id))
	require.NotContains(t, out, "502",
		"the application saw a 502 even though it eventually reached the emulator, so "+
			"something reached the sidecar before the emulator was up.\n%s", out)

	// And the engine said it was waiting. A wait nobody can see is a hang, and
	// this line is the only difference between the two for the twenty seconds it
	// lasts.
	require.GreaterOrEqual(t, tr.indexOf("waiting for the probe emulator"), 0,
		"the engine waited for the emulator and said nothing about it, so a slow emulator "+
			"is indistinguishable from a hung engine.\n%s", strings.Join(tr.all(), "\n"))
	require.Less(t,
		tr.indexOf("probe emulator is accepting connections"), tr.firstServiceLine("app"),
		"the application started before the emulator was reported accepting connections.\n%s",
		strings.Join(tr.all(), "\n"))
	require.GreaterOrEqual(t, waited, emulatorBindDelay,
		"Up returned in %s for an emulator that binds after %s, so it did not wait for it",
		waited, emulatorBindDelay)
}

// ORDERING THREE. The emulator never binds at all. The application must never
// run, the failure must name the emulator, and the environment must not be left
// standing.
func TestEmulatorReadiness_AListenerThatNeverBindsRefusesByNameAndTearsDown(t *testing.T) {
	// A budget of its own, because the default is three minutes and this arm
	// spends all of it. Through the runtime's own reader rather than the process
	// environment, so nothing else in this package sees it.
	r := requireRuntimeWith(t, local.Options{Getenv: func(name string) string {
		if name == "AF_EMULATOR_READY_TIMEOUT" {
			return "25s"
		}
		return ""
	}})

	cli, err := dockerutil.Client()
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	digest := repoDigest(t, ctx, cli, proberImage)
	id := envID(t, r, "emureadynever")
	var tr transcript

	// A container that runs and never listens. This is not a contrived state: it
	// is what an emulator given the wrong command does, what one whose companion
	// database never came up does, and what one that crashed after printing its
	// banner does.
	_, err = r.Up(ctx, emulatorSpec(
		id, digest, []string{"/bin/sh", "-c", "sleep 900"}, theApplication(), tr.record))
	require.Error(t, err,
		"the environment came up with an emulator that is not listening, so the "+
			"application would have been handed a 502 on its first call")
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFRUN049),
		"the failure does not carry the code that explains it: %v", err)

	var coded *aferrors.Error
	require.ErrorAs(t, err, &coded)
	message := coded.Message()
	// "The probe emulator" rather than "probe", and the difference is the whole
	// assertion: the address is af-emu-probe:8080, so a message that had lost
	// the emulator's name entirely would still contain the word probe and this
	// would still pass. It would be a check answering a nearby question.
	require.Contains(t, message, "The probe emulator",
		"the failure does not name which emulator never came up, and an environment can "+
			"run six: %s", message)
	require.Contains(t, message, "af-emu-probe:8080",
		"the failure does not name the address nothing was listening on: %s", message)
	require.Contains(t, message, "25s",
		"the failure does not say how long it waited, so nobody can tell a slow emulator "+
			"from a broken one: %s", message)
	require.Contains(t, coded.NextStep(), "AF_EMULATOR_READY_TIMEOUT",
		"the next step does not name the variable that would give a slow emulator longer: %s",
		coded.NextStep())

	// THE APPLICATION NEVER RAN. This is the half that separates this fix from
	// a better error message: the point is not to explain the 502, it is that
	// nothing was ever in a position to receive one.
	require.Equal(t, -1, tr.firstServiceLine("app"),
		"the application started even though the emulator never bound its port.\n%s",
		strings.Join(tr.all(), "\n"))

	// AND NOTHING IS LEFT STANDING. Neither caller of Up tears down on error,
	// so an environment left behind here is an environment whose every outbound
	// call is answered 502 by the sidecar, which is the misleading symptom this
	// whole change removes.
	left, err := cli.ContainerList(ctx, client.ContainerListOptions{
		All: true, Filters: dockerutil.EnvFilter(id),
	})
	require.NoError(t, err)
	var names []string
	for _, c := range left.Items {
		names = append(names, c.Labels[dockerutil.LabelService]+" ("+
			c.Labels[dockerutil.LabelKind]+")")
	}
	require.Empty(t, names,
		"the failed environment left %d containers on this machine: %s",
		len(names), strings.Join(names, ", "))
}

// emulatorSpec is one environment differing from the next only in the command
// the emulator runs, which is the one variable these arms turn.
func emulatorSpec(
	id, image string, command []string, app provider.ServiceSpec, progress func(string),
) provider.EnvSpec {
	return provider.EnvSpec{
		EnvID: id,
		Egress: &schema.Egress{
			Default: schema.ModeBlock,
			Rules: []schema.EgressRule{
				{Host: "s3.amazonaws.com", Mode: schema.ModeEmulate, Emulator: "probe"},
			},
		},
		Emulators: []provider.EmulatorSpec{{
			Name: "probe", Image: image, Port: emulatorPort, Command: command,
		}},
		Services: []provider.ServiceSpec{app},
		Progress: progress,
	}
}
