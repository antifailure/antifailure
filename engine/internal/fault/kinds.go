package fault

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// fillFileName is what KindDiskFill writes. The name says what it is and who
// wrote it, because somebody will find this file in a data directory while
// looking for something else.
const fillFileName = ".antifailure-fault-fill"

// processKill sends a signal to one process inside the container, chosen by a
// substring of its command line.
//
// The pid is read from the container's own process table rather than from the
// host's, so the number is in the namespace the signal is sent in. Sending a
// host pid into a container is how a chaos tool kills something outside the
// blast radius it advertised.
//
// There is no undo. A killed process is killed, and the recovery is the
// system's own: that is the fault. An undo that restarted it would be this
// package doing the recovery it claims to be measuring.
func (i *Injector) processKill(ctx context.Context, c Container, f Fault) (string, func(context.Context) error, error) {
	before, err := i.processes(ctx, c, f.Process)
	if err != nil {
		return "", nil, codedExec(f, c, err.Error())
	}
	if len(before) == 0 {
		// No match is a refusal and not a quiet success. A fault that killed
		// nothing, reported nothing and let a recovery check pass is exactly
		// the shape of instrument this repository keeps finding in itself.
		return "", nil, aferrors.Coded(aferrors.AFCHS004,
			"fault", f.Name, "target", name(c), "process", f.Process,
			"detail", "no process in the container matches, so nothing would have been killed")
	}
	sig := signalOf(f)
	pids := make([]string, 0, len(before))
	for _, p := range before {
		pids = append(pids, strconv.Itoa(p.pid))
	}
	if _, err := i.sh(ctx, c.ID, "kill -"+shellQuote(sig)+" "+strings.Join(pids, " ")); err != nil {
		return "", nil, codedExec(f, c, err.Error())
	}
	lines := make([]string, 0, len(before))
	for _, p := range before {
		lines = append(lines, fmt.Sprintf("pid %d (%s)", p.pid, p.command))
	}
	evidence := fmt.Sprintf("sent SIG%s to %s", strings.ToUpper(sig), strings.Join(lines, ", "))
	return evidence, nil, nil
}

// containerKill sends a signal to the container's main process. The undo
// starts the container again.
func (i *Injector) containerKill(ctx context.Context, c Container, f Fault) (string, func(context.Context) error, error) {
	sig := signalOf(f)
	if _, err := i.cli.ContainerKill(ctx, c.ID, client.ContainerKillOptions{Signal: sig}); err != nil {
		return "", nil, codedExec(f, c, err.Error())
	}
	evidence := fmt.Sprintf("sent SIG%s to the main process of %s", strings.ToUpper(sig), name(c))
	if state, err := i.inspect(ctx, c.ID); err == nil && state.State != nil {
		evidence += fmt.Sprintf("; the container is %s with exit code %d",
			state.State.Status, state.State.ExitCode)
	}
	return evidence, i.startUndo(c), nil
}

// containerStop asks the container to stop, which is SIGTERM followed by
// SIGKILL after a grace period. The undo starts it again.
func (i *Injector) containerStop(ctx context.Context, c Container, f Fault) (string, func(context.Context) error, error) {
	if _, err := i.cli.ContainerStop(ctx, c.ID, client.ContainerStopOptions{}); err != nil {
		return "", nil, codedExec(f, c, err.Error())
	}
	evidence := fmt.Sprintf("stopped %s, which is SIGTERM and then SIGKILL", name(c))
	if state, err := i.inspect(ctx, c.ID); err == nil && state.State != nil {
		evidence += fmt.Sprintf("; the container is %s with exit code %d",
			state.State.Status, state.State.ExitCode)
	}
	return evidence, i.startUndo(c), nil
}

// startUndo starts a container again, and treats one that is already running
// as done.
func (i *Injector) startUndo(c Container) func(context.Context) error {
	return func(ctx context.Context) error {
		if i.running(ctx, c.ID) {
			return nil
		}
		if _, err := i.cli.ContainerStart(ctx, c.ID, client.ContainerStartOptions{}); err != nil {
			return fmt.Errorf("fault: starting %s again: %w", name(c), err)
		}
		return nil
	}
}

// containerPause freezes every process in the container. The undo thaws it.
func (i *Injector) containerPause(ctx context.Context, c Container, f Fault) (string, func(context.Context) error, error) {
	if !i.running(ctx, c.ID) {
		return "", nil, aferrors.Coded(aferrors.AFCHS004,
			"fault", f.Name, "target", name(c), "process", "",
			"detail", "the container is not running, so there is nothing to freeze")
	}
	if _, err := i.cli.ContainerPause(ctx, c.ID, client.ContainerPauseOptions{}); err != nil {
		return "", nil, codedExec(f, c, err.Error())
	}
	if !i.paused(ctx, c.ID) {
		// The daemon accepted the request and the container is not frozen.
		// Reported rather than assumed, because every assertion that follows
		// would be measuring an environment that never stalled.
		return "", nil, aferrors.Coded(aferrors.AFCHS004,
			"fault", f.Name, "target", name(c), "process", "",
			"detail", "the daemon accepted the pause and the container is not paused")
	}
	undo := func(ctx context.Context) error {
		// The thaw is asked for unconditionally, and its effect is read back
		// afterwards. This used to ask first whether the container was
		// paused and return success when the answer was no, and paused()
		// answers no for an inspect that FAILED as well as for a container
		// that is thawed. So a daemon that stumbled once turned the undo into
		// a success that left the database frozen: on 2026-09-22 CI reported
		// "the undo ran and the container is still frozen" on a pull request
		// that did not touch this package. A container that is not paused,
		// not running or gone answers the thaw with a conflict or not found,
		// and that is the state the undo wants, so neither is an error.
		if _, err := i.cli.ContainerUnpause(ctx, c.ID, client.ContainerUnpauseOptions{}); err != nil &&
			!cerrdefs.IsConflict(err) && !cerrdefs.IsNotFound(err) {
			return fmt.Errorf("fault: thawing %s: %w", name(c), err)
		}
		state, err := i.inspect(ctx, c.ID)
		switch {
		case cerrdefs.IsNotFound(err):
			return nil
		case err != nil:
			return fmt.Errorf("fault: the thaw of %s was sent and could not be confirmed: %w", name(c), err)
		case state.State != nil && state.State.Paused:
			return fmt.Errorf("fault: %s is still frozen after the daemon accepted the thaw", name(c))
		}
		return nil
	}
	return fmt.Sprintf("froze every process in %s with the cgroup freezer", name(c)), undo, nil
}

// networkPartition detaches the container from every Antifailure network it is
// attached to. The undo attaches it again, with the aliases it had.
//
// The aliases are recorded before the detach and replayed on the way back,
// because they are what the rest of the environment resolves the service by.
// Reconnecting without them leaves a container that is on the network and that
// nothing can find, which looks like a partition that never healed.
func (i *Injector) networkPartition(ctx context.Context, c Container, f Fault) (string, func(context.Context) error, error) {
	state, err := i.inspect(ctx, c.ID)
	if err != nil {
		return "", nil, codedExec(f, c, err.Error())
	}
	if state.NetworkSettings == nil || len(state.NetworkSettings.Networks) == 0 {
		return "", nil, aferrors.Coded(aferrors.AFCHS004,
			"fault", f.Name, "target", name(c), "process", "",
			"detail", "the container is on no network, so detaching it would change nothing")
	}
	type attachment struct {
		id      string
		name    string
		aliases []string
	}
	var was []attachment
	for netName, settings := range state.NetworkSettings.Networks {
		if settings == nil {
			continue
		}
		was = append(was, attachment{id: settings.NetworkID, name: netName, aliases: settings.Aliases})
	}
	sort.Slice(was, func(a, b int) bool { return was[a].name < was[b].name })
	for _, a := range was {
		if _, err := i.cli.NetworkDisconnect(ctx, a.id, client.NetworkDisconnectOptions{
			Container: c.ID, Force: true,
		}); err != nil {
			return "", nil, codedExec(f, c, fmt.Sprintf("detaching from %s: %v", a.name, err))
		}
	}
	undo := func(ctx context.Context) error {
		var firstErr error
		for _, a := range was {
			_, err := i.cli.NetworkConnect(ctx, a.id, client.NetworkConnectOptions{
				Container:      c.ID,
				EndpointConfig: &network.EndpointSettings{Aliases: a.aliases},
			})
			// Already attached is the undo's own success, not a failure. A
			// second Undo must not report a problem it created.
			if err != nil && !strings.Contains(err.Error(), "already exists") && firstErr == nil {
				firstErr = fmt.Errorf("fault: attaching %s to %s again: %w", name(c), a.name, err)
			}
		}
		return firstErr
	}
	names := make([]string, 0, len(was))
	for _, a := range was {
		names = append(names, a.name)
	}
	return fmt.Sprintf("detached %s from %s", name(c), strings.Join(names, ", ")), undo, nil
}

// readOnlyData removes write permission from a directory inside the container,
// so a write that reaches the filesystem fails with EACCES.
//
// The modes are read back before they are changed and restored from what was
// read, rather than set to a mode this package believes is right. A directory
// restored to a guessed mode is a directory this package quietly reconfigured.
func (i *Injector) readOnlyData(ctx context.Context, c Container, f Fault) (string, func(context.Context) error, error) {
	dir := f.Path
	if strings.TrimSpace(dir) == "" {
		return "", nil, aferrors.Coded(aferrors.AFCHS002, "kind", string(f.Kind),
			"detail", "it names no directory, and the caller did not resolve one")
	}
	q := shellQuote(dir)
	before, err := i.sh(ctx, c.ID, "stat -c '%a %U' "+q)
	if err != nil {
		return "", nil, codedExec(f, c, err.Error())
	}
	parts := fields(before)
	if len(parts) != 2 {
		return "", nil, codedExec(f, c, "the mode and owner of "+dir+" could not be read, so there would be nothing to restore")
	}
	mode, owner := parts[0], parts[1]

	if _, err := i.sh(ctx, c.ID, "chmod a-w "+q); err != nil {
		return "", nil, codedExec(f, c, err.Error())
	}
	undo := func(ctx context.Context) error {
		if _, err := i.sh(ctx, c.ID, "chmod "+shellQuote(mode)+" "+q); err != nil {
			return fmt.Errorf("fault: restoring the mode of %s in %s: %w", dir, name(c), err)
		}
		return nil
	}

	after, err := i.sh(ctx, c.ID, "stat -c %a "+q)
	if err != nil {
		_ = undo(context.WithoutCancel(ctx))
		return "", nil, codedExec(f, c, err.Error())
	}
	if strings.TrimSpace(after) == mode {
		_ = undo(context.WithoutCancel(ctx))
		return "", nil, aferrors.Coded(aferrors.AFCHS004,
			"fault", f.Name, "target", name(c), "process", "",
			"detail", "the mode of "+dir+" is unchanged at "+mode+" after chmod, so nothing was made read only")
	}

	// The mode changed, and that is not the claim. The claim is that a write
	// now fails, so a write is attempted, as the user that owns the directory
	// rather than as whoever this exec would default to. Root ignores a
	// directory's mode entirely: probed as root, a data directory that was
	// just made read only reports that writing to it still works, and a fault
	// that reported "made read only" on the strength of the chmod alone would
	// be a fault that changed nothing for the process it was aimed at.
	if refused, err := i.writeRefused(ctx, c, owner, dir); err != nil {
		_ = undo(context.WithoutCancel(ctx))
		return "", nil, codedExec(f, c, err.Error())
	} else if !refused {
		// AF-CHS-004, not AF-CHS-005. The mode WAS changed and then put back,
		// so this fault acted and changed nothing, which is exactly what 004
		// says. 005 means refused BEFORE acting because the effect would reach
		// past the target, and a reader of a chaos run relies on that code
		// meaning one thing: it is what tells a refusal that left the
		// environment untouched from a fault that went in.
		_ = undo(context.WithoutCancel(ctx))
		return "", nil, aferrors.Coded(aferrors.AFCHS004,
			"fault", f.Name, "target", name(c), "path", dir,
			"detail", "a write into it by its owner "+owner+" still succeeds after the mode was changed to "+
				strings.TrimSpace(after)+", so this fault would change nothing. A directory owned by root cannot be "+
				"made read only by its mode, because root ignores it")
	}

	return fmt.Sprintf("changed the mode of %s in %s from %s to %s, and a write by its owner %s is now refused",
		dir, name(c), mode, strings.TrimSpace(after), owner), undo, nil
}

// probeFileName is what the write probe creates and removes.
const probeFileName = ".antifailure-fault-probe"

// writeRefused reports whether a write into dir by user is refused.
//
// Any file it manages to create is removed at once, because a probe that left
// a file behind would be a fault of its own inside somebody's data directory.
func (i *Injector) writeRefused(ctx context.Context, c Container, user, dir string) (bool, error) {
	probe := shellQuote(dir + "/" + probeFileName)
	res, err := i.execAs(ctx, c.ID, user, []string{"/bin/sh", "-c",
		"touch " + probe + " 2>/dev/null; code=$?; rm -f " + probe + " 2>/dev/null; exit $code"})
	if err != nil {
		return false, err
	}
	return res.ExitCode != 0, nil
}

// diskFill writes one file into a directory until the filesystem holding it
// has less than the stated headroom free.
//
// It is refused unless that directory is a mount point of its own. On a shared
// filesystem, which is what a container's writable layer is, filling it fills
// the daemon's disk and every other container on the machine runs out of space
// with it. That is a blast radius outside the environment, and this package's
// one promise is that there is none.
func (i *Injector) diskFill(ctx context.Context, c Container, f Fault) (string, func(context.Context) error, error) {
	dir := f.Path
	if strings.TrimSpace(dir) == "" {
		return "", nil, aferrors.Coded(aferrors.AFCHS002, "kind", string(f.Kind),
			"detail", "it names no directory, and the caller did not resolve one")
	}
	q := shellQuote(dir)
	// mountpoint -q is not in every image, so the test is the one every image
	// can do: a directory on its own filesystem has a different device number
	// from its parent.
	same, err := i.sh(ctx, c.ID, "stat -c %d "+q+" && stat -c %d "+q+"/..")
	if err != nil {
		return "", nil, codedExec(f, c, err.Error())
	}
	devs := fields(same)
	if len(devs) != 2 {
		return "", nil, codedExec(f, c, "the device numbers of "+dir+" and its parent could not be read")
	}
	if devs[0] == devs[1] {
		return "", nil, aferrors.Coded(aferrors.AFCHS005,
			"fault", f.Name, "target", name(c), "path", dir,
			"detail", "it shares a filesystem with its parent, which is the container's writable layer and therefore the daemon's own disk, so filling it would fill every other container on this machine with it")
	}
	// A filesystem of its own is necessary and it is not sufficient.
	//
	// The device check above answers "is this directory a mount", and a mount
	// can still be a slice of the daemon's disk: a named volume is its own
	// mount, passes that check, and filling it takes the machine's space just
	// as surely as filling the writable layer does. It would take it HAVING
	// satisfied the guard, which is worse than being refused. So the mount is
	// read from the daemon and it has to be one this environment created with a
	// size fixed at creation, which is the only arrangement whose blast radius
	// this package can state.
	if err := i.boundedToThisEnvironment(ctx, c, f, dir); err != nil {
		return "", nil, err
	}
	free, err := i.freeBytes(ctx, c, dir)
	if err != nil {
		return "", nil, codedExec(f, c, err.Error())
	}
	want := free - f.HeadroomBytes
	if want > f.MaxFillBytes {
		return "", nil, aferrors.Coded(aferrors.AFCHS005,
			"fault", f.Name, "target", name(c), "path", dir,
			"detail", fmt.Sprintf("filling it to %d bytes of headroom would take %d bytes and the cap is %d",
				f.HeadroomBytes, want, f.MaxFillBytes))
	}
	if want <= 0 {
		return "", nil, aferrors.Coded(aferrors.AFCHS004,
			"fault", f.Name, "target", name(c), "process", "",
			"detail", fmt.Sprintf("the filesystem already has %d bytes free, which is under the %d of headroom asked for", free, f.HeadroomBytes))
	}
	target := dir + "/" + fillFileName
	// The undo is registered by returning it, and the file is written after,
	// so a fill that is interrupted halfway still has something to remove.
	undo := func(ctx context.Context) error {
		if _, err := i.sh(ctx, c.ID, "rm -f "+shellQuote(target)); err != nil {
			return fmt.Errorf("fault: removing the fill file from %s in %s: %w", dir, name(c), err)
		}
		return nil
	}
	blocks := want / (1 << 20)
	if blocks < 1 {
		blocks = 1
	}
	if _, err := i.sh(ctx, c.ID, fmt.Sprintf("dd if=/dev/zero of=%s bs=1048576 count=%d 2>/dev/null; sync",
		shellQuote(target), blocks)); err != nil {
		// The fill failed partway, which is the state the undo was written
		// for, so it runs before the refusal is returned.
		_ = undo(context.WithoutCancel(ctx))
		return "", nil, codedExec(f, c, err.Error())
	}
	after, err := i.freeBytes(ctx, c, dir)
	if err != nil {
		_ = undo(context.WithoutCancel(ctx))
		return "", nil, codedExec(f, c, err.Error())
	}
	if after >= free {
		_ = undo(context.WithoutCancel(ctx))
		return "", nil, aferrors.Coded(aferrors.AFCHS004,
			"fault", f.Name, "target", name(c), "process", "",
			"detail", fmt.Sprintf("the filesystem holding %s reports %d bytes free after the fill and %d before, so nothing was filled", dir, after, free))
	}
	return fmt.Sprintf("filled %s in %s from %d bytes free to %d", dir, name(c), free, after), undo, nil
}

// boundedToThisEnvironment refuses a fill whose size this package cannot state.
//
// Read from the daemon rather than from inside the container, for the reason
// every ownership check in this package is: what a container says about itself
// is what a fault could have changed, and the daemon is the one party that
// knows which volume it mounted where and how it was created.
//
// The three questions, and each is a separate refusal because they send
// somebody to different places: is the data directory a mount at all, is the
// mount a volume this environment owns, and is that volume's size fixed.
func (i *Injector) boundedToThisEnvironment(ctx context.Context, c Container, f Fault, dir string) error {
	refuse := func(detail string) error {
		return aferrors.Coded(aferrors.AFCHS005,
			"fault", f.Name, "target", name(c), "path", dir, "detail", detail)
	}
	state, err := i.inspect(ctx, c.ID)
	if err != nil {
		return codedExec(f, c, err.Error())
	}
	var mounted *container.MountPoint
	for idx := range state.Mounts {
		if strings.TrimSuffix(state.Mounts[idx].Destination, "/") == strings.TrimSuffix(dir, "/") {
			mounted = &state.Mounts[idx]
			break
		}
	}
	if mounted == nil {
		return refuse("the daemon reports no mount at it, so the filesystem it is on is one this environment did not create and whose size nothing here can state")
	}
	if mounted.Type != mount.TypeVolume || mounted.Name == "" {
		return refuse(fmt.Sprintf("it is a %s mount rather than a volume this environment created, so how much of the machine filling it would take is not this run's to know",
			mounted.Type))
	}
	vol, err := i.cli.VolumeInspect(ctx, mounted.Name, client.VolumeInspectOptions{})
	if err != nil {
		return codedExec(f, c, fmt.Sprintf("inspecting the volume %s mounted at %s: %v", mounted.Name, dir, err))
	}
	if !dockerutil.IsOurs(vol.Volume.Labels) || vol.Volume.Labels[dockerutil.LabelEnv] != i.envID {
		return refuse("the volume " + mounted.Name + " mounted at it belongs to something other than this environment, and a fault may only fill what its own environment owns")
	}
	// The size, read from the options the volume was created with. A volume
	// without one is a directory on the daemon's disk however it is mounted,
	// and filling it fills the machine.
	if vol.Volume.Options["type"] != "tmpfs" || !strings.Contains(vol.Volume.Options["o"], "size=") {
		return refuse("the volume " + mounted.Name + " mounted at it has no size fixed at creation, so filling it would take space from the daemon's disk and every other container on this machine")
	}
	return nil
}

// freeBytes is how much room the filesystem holding dir has.
//
// Read in 1 KiB blocks with POSIX output, because df's default block size
// differs between the coreutils and busybox builds this runs against and a
// number whose unit depends on the image is not a number.
func (i *Injector) freeBytes(ctx context.Context, c Container, dir string) (int64, error) {
	out, err := i.sh(ctx, c.ID, "df -P -k "+shellQuote(dir)+" | tail -1")
	if err != nil {
		return 0, err
	}
	cols := fields(out)
	if len(cols) < 4 {
		return 0, fmt.Errorf("fault: df said %q, which has no available column", strings.TrimSpace(out))
	}
	kb, err := strconv.ParseInt(cols[3], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("fault: df's available column is %q, which is not a number", cols[3])
	}
	return kb * 1024, nil
}

// processes lists the processes inside a container whose command line
// contains match.
func (i *Injector) processes(ctx context.Context, c Container, match string) ([]process, error) {
	// ps -e -o pid=,args= is the form both procps and busybox accept, and the
	// empty headers keep the output free of a line that would match nothing
	// and parse as a pid.
	out, err := i.sh(ctx, c.ID, "ps -e -o pid=,args= 2>/dev/null || ps -o pid=,args=")
	if err != nil {
		return nil, err
	}
	var found []process
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		space := strings.IndexByte(line, ' ')
		if space <= 0 {
			continue
		}
		pid, err := strconv.Atoi(line[:space])
		if err != nil {
			continue
		}
		command := strings.TrimSpace(line[space+1:])
		if !strings.Contains(command, match) {
			continue
		}
		// The process running the search must never be a candidate. It is in
		// the table by construction, it contains the pattern by construction,
		// and killing it would report a kill that happened to the instrument.
		if strings.Contains(command, "ps -e -o pid=") || strings.Contains(command, "/bin/sh -c ps ") {
			continue
		}
		found = append(found, process{pid: pid, command: command})
	}
	sort.Slice(found, func(a, b int) bool { return found[a].pid < found[b].pid })
	return found, nil
}

type process struct {
	pid     int
	command string
}

// fields splits on any run of whitespace, including the carriage returns a TTY
// exec adds.
func fields(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
}

// shellQuote wraps a value in single quotes for /bin/sh.
//
// Every value that reaches a shell here comes from a manifest, so it is
// attacker adjacent in the only sense that matters: a path with a space in it
// must not become two arguments, and one with a semicolon in it must not
// become two commands.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
