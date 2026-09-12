package local

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/docker/docker/api/types/container"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// settleWindow is how long a service with nothing to check is watched before
// it is reported running and unproved.
//
// Five seconds, and it is a bound on catching a crash, not a claim about
// readiness. The commonest way a portless service fails in a twin is an
// outbound call at startup that the egress policy refuses, and the process
// exits within a second or two of the refusal. A single look immediately after
// the start could not see that and reported the service ready; five seconds of
// looking does see it. A service that dies at ten seconds is caught by the
// sweep at the end of Up instead, which costs nothing for every service that
// started before the last one.
const settleWindow = 5 * time.Second

// commandAttempt bounds one run of a health command. A check that hangs is a
// check that did not pass this round, and the loop around it owns the real
// deadline.
const commandAttempt = 10 * time.Second

// Values of dockerutil.LabelReadinessCheck.
const (
	checkHTTP     = "http"
	checkPort     = "port"
	checkCommand  = "command"
	checkSchedule = "schedule"
	checkNone     = "none"
)

// readinessCheckOf names the check a service carries, in the words the label
// records.
func readinessCheckOf(s provider.ServiceSpec) string {
	switch {
	case s.Kind == "cron":
		return checkSchedule
	case s.HealthCommand != "":
		return checkCommand
	case s.Kind == "web" && s.Port > 0 && s.HealthPath != "":
		return checkHTTP
	case s.Kind == "web" && s.Port > 0:
		return checkPort
	default:
		return checkNone
	}
}

// statusReadiness is what Status can say about a RUNNING container.
//
// Status sees containers, not the manifest, and it does not re-run checks. So
// a container that carried a check and is still running reports proved, which
// is what it did at Up, and one that carried none reports unproved, which is
// also what it did at Up. A container from an engine that predates the label
// carries no value and reads as unproved: the conservative direction, because
// the other one reports a promise nothing made.
func statusReadiness(labels map[string]string) provider.Readiness {
	switch labels[dockerutil.LabelReadinessCheck] {
	case checkHTTP, checkPort, checkCommand, checkSchedule:
		return provider.ReadinessProved
	default:
		return provider.ReadinessUnproved
	}
}

// waitCommand runs the health command inside one container until it exits
// zero, the container exits, or the timeout passes.
//
// Through /bin/sh -c, the same shape a service's own command is run in, so the
// same string that works as a compose healthcheck's CMD-SHELL works here. An
// image with no shell cannot run one, and the timeout's message then carries
// the daemon's own error rather than a bare "did not pass".
func (r *Runtime) waitCommand(
	ctx context.Context, s provider.ServiceSpec, id string, timeout time.Duration, progress func(string),
) error {
	deadline := r.clock.Now().Add(timeout)
	attempt := 0
	var lastErr error
	for {
		if err := r.confirmStillRunning(ctx, s, id); err != nil {
			return err
		}
		ok, err := r.execProbe(ctx, id, s.HealthCommand)
		if ok {
			return nil
		}
		if err != nil {
			lastErr = err
		}
		attempt++
		if attempt%10 == 0 {
			progress(fmt.Sprintf("%s: still waiting for its health command", s.Name))
		}
		if !r.clock.Now().Before(deadline) {
			health := s.HealthCommand
			if lastErr != nil {
				health = fmt.Sprintf("%s (the last attempt could not run: %v)", health, lastErr)
			}
			return aferrors.Coded(aferrors.AFRUN050,
				"service", s.Name, "timeout", timeout.Round(time.Second).String(),
				"health", health)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.clock.After(time.Second):
		}
	}
}

// execProbe runs a command once inside a container and reports whether it
// exited zero. An error means it could not be run at all, which is different
// from running and failing, and the caller keeps the two apart.
func (r *Runtime) execProbe(ctx context.Context, id, command string) (bool, error) {
	c, cancel := context.WithTimeout(ctx, commandAttempt)
	defer cancel()
	created, err := r.cli.ContainerExecCreate(c, id, container.ExecOptions{
		Cmd: []string{"/bin/sh", "-c", command},
	})
	if err != nil {
		return false, err
	}
	if err := r.cli.ContainerExecStart(c, created.ID, container.ExecStartOptions{Detach: true}); err != nil {
		return false, err
	}
	for {
		insp, err := r.cli.ContainerExecInspect(c, created.ID)
		if err != nil {
			return false, err
		}
		if !insp.Running {
			return insp.ExitCode == 0, nil
		}
		select {
		case <-c.Done():
			return false, nil
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// observeSettled watches a service with nothing to check for the settle window
// and fails if it exits inside it.
func (r *Runtime) observeSettled(ctx context.Context, s provider.ServiceSpec, id string, timeout time.Duration) error {
	window := settleWindow
	if timeout > 0 && timeout < window {
		window = timeout
	}
	deadline := r.clock.Now().Add(window)
	for {
		if err := r.confirmStillRunning(ctx, s, id); err != nil {
			return err
		}
		if !r.clock.Now().Before(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.clock.After(500 * time.Millisecond):
		}
	}
}

// confirmStillUp is the last look before Up returns: every continuously running
// service container of this environment is still running.
//
// It exists because readiness is measured once per service, in start order, and
// a service that passed its own look can exit while the ones after it come up.
// For a stack of thirty services the first ones started are watched for minutes
// by this at no cost at all; the settle window above only has to cover the last.
// Without it, `af up` could report an environment whose first worker had
// already exited, which is the false pass in its slowest form.
//
// Cron services are skipped, because a cron container runs its job and exits,
// and that exit is the job finishing rather than the service failing.
func (r *Runtime) confirmStillUp(ctx context.Context, envID string, services []provider.RunningService) error {
	list, err := r.cli.ContainerList(ctx, container.ListOptions{
		All: true, Filters: dockerutil.EnvFilter(envID),
	})
	if err != nil {
		return aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", dockerutil.Host())
	}
	sort.Slice(list, func(i, j int) bool {
		return dockerutil.FirstName(list[i].Names) < dockerutil.FirstName(list[j].Names)
	})
	index := map[string]int{}
	for i, s := range services {
		index[s.Name] = i
	}
	var first error
	for _, c := range list {
		if c.Labels[dockerutil.LabelKind] != dockerutil.KindService ||
			c.Labels[dockerutil.LabelServiceKind] == "cron" || c.State == "running" {
			continue
		}
		i, ok := index[c.Labels[dockerutil.LabelService]]
		if !ok || services[i].Readiness == provider.ReadinessFailed {
			continue
		}
		rs := &services[i]
		rs.Ready = false
		rs.Readiness = provider.ReadinessFailed
		rs.State = c.State
		rs.Detail = r.lastLogLines(ctx, c.ID)
		code := 0
		if insp, inspErr := r.cli.ContainerInspect(ctx, c.ID); inspErr == nil && insp.State != nil {
			code = insp.State.ExitCode
			rs.ExitCode = &code
		}
		if first == nil {
			first = aferrors.Coded(aferrors.AFRUN005, "service", rs.Name, "code", strconv.Itoa(code))
		}
	}
	return first
}
