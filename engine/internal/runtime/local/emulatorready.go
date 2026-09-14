package local

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/client"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/proxyimage"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// Waiting for an emulator to be LISTENING, which is not the same thing as
// started.
//
// The defect this closes, in the order it happened. Up started the emulator
// containers, then the sidecar, then the application. Starting a container
// means the daemon accepted it and the first process is running; the server
// inside binds its port some time after that, and for LocalStack, Azurite and
// the gcloud emulators that is seconds to tens of seconds, spent loading
// providers and creating state directories. So Up returned success with the
// emulator's port closed. The application ran, made its first call, the sidecar
// forwarded it to an address nothing was listening on, and the application read
// 502 Bad Gateway from its own SDK.
//
// Everything about that failure pointed away from the cause. The engine had
// already printed "emulator ready". The 502 is the same status the sidecar
// returns for an emulator this environment is not running at all, so the one
// message a reader had said the opposite of what was wrong. It reproduced only
// when the emulator was slow, which on a warm machine it usually is not: ten
// runs of the end to end test passed on this machine and the CI job failed,
// which is exactly the shape of a race nobody can find.
//
// The probe has to run inside the environment, because an emulator is attached
// to the inner network and nothing else. It publishes no port, the network is
// internal, and the host has no route to it, so there is nothing for the engine
// to dial. The sidecar is the one container on that network the engine can
// reach through the daemon, and it already holds the address, so the probe is
// the sidecar's own binary, run as a second process inside it, dialling the
// emulator once per attempt.
//
// A TCP connection and nothing more. Not an HTTP request: these emulators
// answer six different vendors' protocols, several of them not HTTP at all, and
// a probe that guessed a path would report a healthy emulator as broken the
// first time one of them answered 404 to a request Antifailure invented.
// Accepting a connection is what the sidecar needs of it, and it is what the
// application's first call will get.

// defaultEmulatorReadyTimeout bounds how long ONE emulator may take to bind
// its port, measured from the point every container in the environment has
// been started.
//
// Three minutes, and the measurement behind it is in guides/gcp.md: on this
// machine Bigtable was the slowest of the six at 51.5 seconds from start to
// serving, and the whole set of six took 3.9 minutes of wall clock between
// them. A bound of three minutes each is therefore about three times the
// slowest thing anyone has measured here, which is what a timeout for a third
// party image on an unknown machine should be. It is per emulator rather than
// shared, because the error has to name the one that never came up and a shared
// budget would blame whichever emulator happened to be last in the list.
const defaultEmulatorReadyTimeout = 3 * time.Minute

// The probe's own two numbers.
//
// emulatorDialTimeout is one attempt. Short, because a closed port answers
// immediately and the only thing a long attempt buys is a slower loop.
//
// emulatorProbeFirstPause and emulatorProbeMaxPause are the gap between
// attempts. It starts small so that an emulator which is nearly up costs
// nothing, and grows, because the alternative is several hundred exec calls
// against the daemon in the case where the emulator never binds at all, and a
// daemon this machine has already run out of memory once is not a thing to
// poll at full speed for three minutes.
const (
	emulatorDialTimeout     = 2 * time.Second
	emulatorProbeFirstPause = 200 * time.Millisecond
	emulatorProbeMaxPause   = 2 * time.Second
	// probeReapPause and probeReapAttempts bound the wait for the daemon to
	// record that the probe process has exited, which is a different moment
	// from its output ending.
	probeReapPause    = 50 * time.Millisecond
	probeReapAttempts = 20
)

// emulatorReadyTimeoutVar is the variable that moves the budget.
const emulatorReadyTimeoutVar = "AF_EMULATOR_READY_TIMEOUT"

// env reads one variable, through the runtime's own reader so a test can answer
// without touching the process environment.
func (r *Runtime) env(name string) string {
	get := r.getenv
	if get == nil {
		get = os.Getenv
	}
	return strings.TrimSpace(get(name))
}

// emulatorReadyTimeout resolves the budget for one emulator.
func (r *Runtime) emulatorReadyTimeout() (time.Duration, error) {
	raw := r.env(emulatorReadyTimeoutVar)
	if raw == "" {
		return defaultEmulatorReadyTimeout, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		// Refused rather than ignored, the same way AF_PROXY_IMAGE_TIMEOUT is.
		// Falling back to the default on a typo is how somebody concludes the
		// variable does nothing, and the person setting it is by definition
		// somebody the default already failed.
		return 0, aferrors.Coded(aferrors.AFRUN040, "detail",
			emulatorReadyTimeoutVar+" is set to "+raw+", which is not a positive duration. "+
				"Write it as Go writes one, such as 5m")
	}
	return d, nil
}

// waitEmulatorsReady blocks until every emulator accepts a connection.
//
// Called after the sidecar is up, because the sidecar is the probe, and before
// any service, because the service is what the 502 was served to.
func (r *Runtime) waitEmulatorsReady(
	ctx context.Context,
	envID string,
	emulators []provider.EmulatorSpec,
	progress func(string),
) error {
	if len(emulators) == 0 {
		return nil
	}
	budget, err := r.emulatorReadyTimeout()
	if err != nil {
		return err
	}
	proxy := proxyName(envID)
	for _, e := range emulators {
		if e.Port <= 0 {
			// Nothing to dial. A registration without a port is refused
			// upstream, and a runtime that invented one would be probing an
			// address the sidecar does not forward to.
			continue
		}
		address := net.JoinHostPort(EmulatorAlias(e.Name), strconv.Itoa(e.Port))
		if err := r.waitEmulatorListening(ctx, proxy, e, address, budget, progress); err != nil {
			return err
		}
		progress(fmt.Sprintf("%s emulator is accepting connections at %s", e.Name, address))
	}
	return nil
}

// waitEmulatorListening dials one emulator until it answers or the budget runs
// out.
func (r *Runtime) waitEmulatorListening(
	ctx context.Context,
	proxy string,
	e provider.EmulatorSpec,
	address string,
	budget time.Duration,
	progress func(string),
) error {
	deadline := r.clock.Now().Add(budget)
	pause := emulatorProbeFirstPause
	announced := false
	var last string
	for {
		out, err := r.dialFromProxy(ctx, proxy, address)
		if err == nil {
			return nil
		}
		last = firstLine(out)
		if last == "" {
			last = err.Error()
		}
		if !r.clock.Now().Before(deadline) {
			return aferrors.Coded(aferrors.AFRUN049,
				"emulator", e.Name, "address", address, "timeout", budget.String(),
				"detail", fmt.Sprintf("the last attempt from inside the environment said %q. "+
					"The container runs %s", last, e.Image))
		}
		if !announced {
			// Once, and only if the first attempt failed. A silent minute and a
			// hang look identical, and an emulator that binds immediately
			// should not make anybody read a line about waiting.
			announced = true
			progress(fmt.Sprintf(
				"waiting for the %s emulator to accept connections at %s, up to %s",
				e.Name, address, budget))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.clock.After(pause):
		}
		if pause < emulatorProbeMaxPause {
			pause *= 2
		}
	}
}

// dialFromProxy runs one dial attempt inside the sidecar and returns what the
// probe said.
//
// The exit code is the verdict and the output is the explanation. Reading the
// output alone would mean matching on a message, and a probe whose success is a
// string comparison goes silently green the day the message is reworded.
func (r *Runtime) dialFromProxy(ctx context.Context, proxy, address string) (string, error) {
	created, err := r.cli.ExecCreate(ctx, proxy, client.ExecCreateOptions{
		Cmd: []string{
			proxyimage.BinaryPath,
			"-dial", address,
			"-dial-timeout", emulatorDialTimeout.String(),
		},
		AttachStdout: true, AttachStderr: true,
		// A terminal, so the two streams arrive as one unframed stream. The
		// alternative is Docker's eight byte per frame multiplexing, and the
		// only thing read here is one short line of explanation.
		TTY: true,
	})
	if err != nil {
		return "", err
	}
	attached, err := r.cli.ExecAttach(ctx, created.ID, client.ExecAttachOptions{TTY: true})
	if err != nil {
		return "", err
	}
	out, readErr := io.ReadAll(io.LimitReader(attached.Reader, 4<<10))
	attached.Close()
	if readErr != nil {
		return string(out), readErr
	}
	// The exit code is read only once the daemon says the process is gone. An
	// exec that is still running reports ExitCode 0, so taking the first answer
	// would read a probe that had not finished as a successful one, and a false
	// ready is the failure this file exists to remove rather than to move.
	for attempt := 0; ; attempt++ {
		insp, err := r.cli.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
		if err != nil {
			return string(out), err
		}
		if !insp.Running {
			if insp.ExitCode != 0 {
				return string(out), fmt.Errorf("the probe exited %d", insp.ExitCode)
			}
			return string(out), nil
		}
		if attempt >= probeReapAttempts {
			return string(out), fmt.Errorf(
				"the probe was still running %s after its output ended",
				time.Duration(probeReapAttempts)*probeReapPause)
		}
		select {
		case <-ctx.Done():
			return string(out), ctx.Err()
		case <-r.clock.After(probeReapPause):
		}
	}
}

// tearDownAfterEmulator removes the environment a timed out emulator leaves
// behind, and returns the failure to report.
//
// Torn down rather than left standing, unlike a service that failed to become
// ready. A service that never answered leaves an environment somebody can still
// look inside, and af down is one command away. An emulator that never binds
// leaves an environment where every call the application makes is answered 502
// by the sidecar, which is the misleading symptom this whole change exists to
// remove: leaving it up would preserve the exact thing that cost a day of
// reading the wrong file. Neither caller of Up tears down on error, so it has
// to happen here or not at all.
func (r *Runtime) tearDownAfterEmulator(ctx context.Context, envID string, cause error) error {
	// A context of its own. The caller's may already be done, and a teardown
	// that inherits a cancelled context removes nothing while reporting that it
	// did.
	tctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	if _, err := r.Down(tctx, envID); err != nil {
		// Both, because the first one says what went wrong and the second says
		// what is still on this machine because of it.
		return fmt.Errorf("%w. The environment could not be torn down either, so "+
			"af down has work left to do: %v", cause, err)
	}
	return cause
}

// firstLine is the probe's message without its trailing terminal bytes.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
