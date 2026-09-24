package docker

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// The data directory's own filesystem, and why one exists at all.
//
// A container's writable layer is the Docker daemon's own disk. So a disk_fill
// fault aimed at a data directory sitting on it would not fill the environment,
// it would fill the machine, and every other container on that machine would
// run out of space with it. The injector refuses that before it acts, and the
// refusal is correct. What it left behind is the gap this file closes: the one
// storage fault anybody reaches for could not land anywhere, on any provider,
// and the refusal named a "dedicated volume" that no manifest key provided.
//
// What is created here, when database.data_filesystem.size_bytes is declared:
//
//   - a volume of a stated size, held in MEMORY. That is the whole containment
//     argument and it is the reason this is not simply a named volume. A named
//     volume lives on the daemon's disk; it is its own mount, so it would pass
//     the injector's device check, and filling it would still fill the machine.
//     Worse, it would fill it having satisfied the guard. A filesystem held in
//     memory has a size fixed at creation and takes no space from anything
//     outside this environment, whether it is empty or full.
//
//   - an ANCHOR container, which holds that volume mounted and does nothing
//     else. It is load bearing and it is not decoration. The local volume
//     driver mounts a memory backed volume on first use and unmounts it when
//     the last container using it stops, so without the anchor a container_kill
//     or container_stop fault would destroy the data directory rather than
//     crash the database: the undo would start the container again, the image's
//     entrypoint would find an empty data directory, initialise a new one, and
//     the durability proof would report every acknowledged commit lost. That
//     was measured on this daemon rather than reasoned about. With the anchor
//     running, the same stop and start comes back with the data intact.
//
//   - a copy of the golden's data directory into it. A memory backed volume is
//     not populated from the image the way an empty disk backed one is, so the
//     copy is done here, by the anchor, before the database container is
//     created. This is the cost of the feature and it is stated rather than
//     hidden: the branch pays a full copy of its data directory instead of the
//     daemon's copy on write, and the database has to fit in the declared size.
//
// The limits are real and are documented on the schema key. The data directory
// does not survive the Docker daemon restarting, and it is memory rather than
// disk, so this is a layout for rehearsing a disk that fills rather than one
// for measuring how a disk performs.

// storageStagingDir is where the anchor mounts the volume.
//
// Deliberately not the data directory. The anchor runs the golden image, and
// the copy reads the image's own data directory: mounting the volume over it
// would hide the very thing being copied.
const storageStagingDir = "/mnt/antifailure-pgdata"

// storageReadyFile is the file the copy is judged by.
//
// PG_VERSION is written by initdb, is present in every data directory Postgres
// will start from, and is refused as empty. Checking it is the difference
// between "the volume has something in it" and "the volume has a data
// directory in it".
const storageReadyFile = "PG_VERSION"

// storageMemoryShare is the most of the daemon's memory a declared filesystem
// may claim.
//
// Half, because the filesystem is the fault's target and the point of the
// fault is to fill it: a declaration that could be filled to more than the
// machine holds is a way to take the daemon down for memory, which is the same
// blast radius the disk guard exists to prevent and the reason this check is
// here rather than left to the kernel.
const storageMemoryShare = 2

// storageVolumeName and storageAnchorName are derived from the environment
// identifier for the same reason branchName is: a retry after a timeout has to
// find what the first attempt made.
func storageVolumeName(envID string) string { return "af-pgdata-" + envID }
func storageAnchorName(envID string) string { return "af-pgdata-anchor-" + envID }

// ensureStorage creates the data directory's filesystem and fills it from img.
//
// It returns the volume to mount at the data directory. Called only when the
// manifest declared a size; every manifest that did not gets the layout it
// always had, and nothing in here runs.
func (p *Provider) ensureStorage(ctx context.Context, envID, img string) (string, error) {
	if err := p.storageFitsTheDaemon(ctx); err != nil {
		return "", err
	}
	vol := storageVolumeName(envID)
	// Removed and recreated rather than reused. A volume left by a previous
	// environment under the same name holds that environment's data directory,
	// and starting a branch on it would be starting on data whose provenance
	// nothing recorded, which is the same reason start() never adopts a
	// container.
	if err := p.removeStorage(ctx, envID); err != nil {
		return "", err
	}
	if _, err := p.cli.VolumeCreate(ctx, client.VolumeCreateOptions{
		Name:   vol,
		Driver: "local",
		DriverOpts: map[string]string{
			"type":   "tmpfs",
			"device": "tmpfs",
			"o":      "size=" + strconv.FormatInt(p.storageBytes, 10),
		},
		Labels: dockerutil.Managed(dockerutil.KindVolume, envID, p.clock.Now()),
	}); err != nil {
		return "", fmt.Errorf("db.docker: create the data directory volume %s: %w", vol, err)
	}
	if err := p.startStorageAnchor(ctx, envID, img, vol); err != nil {
		return "", err
	}
	if err := p.fillStorage(ctx, envID); err != nil {
		return "", err
	}
	return vol, nil
}

// storageFitsTheDaemon refuses a filesystem larger than the machine can hold.
func (p *Provider) storageFitsTheDaemon(ctx context.Context) error {
	info, err := p.cli.Info(ctx, client.InfoOptions{})
	if err != nil {
		// The daemon's memory could not be read, and that is not a reason to
		// refuse the environment: the kernel still caps the filesystem at the
		// size it was created with, so the disk is not at risk either way.
		// Reported by the environment coming up rather than by a refusal that
		// names something nobody can act on.
		return nil
	}
	total := info.Info.MemTotal
	if total <= 0 {
		return nil
	}
	if p.storageBytes*storageMemoryShare <= total {
		return nil
	}
	return aferrors.Coded(aferrors.AFDB042,
		"declared", strconv.FormatInt(p.storageBytes, 10),
		"memory", strconv.FormatInt(total, 10))
}

// startStorageAnchor runs the container that holds the volume mounted.
func (p *Provider) startStorageAnchor(ctx context.Context, envID, img, vol string) error {
	name := storageAnchorName(envID)
	labels := dockerutil.Managed(dockerutil.KindStorage, envID, p.clock.Now())
	resp, err := p.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:  img,
			Labels: labels,
			// The image's entrypoint is bypassed rather than reasoned about.
			// This container is the golden image and it must never start a
			// Postgres: two servers on one data directory is a corruption, and
			// an entrypoint that decided to initialise one would be doing it to
			// the directory the branch is about to run on.
			Entrypoint: []string{"/bin/sh"},
			Cmd:        []string{"-c", "while true; do sleep 3600; done"},
			// The image's health check is pg_isready and there is no Postgres
			// in here, so inheriting it would leave a container that reports
			// itself unhealthy forever. A permanently red health status that
			// means nothing is how a real one stops being read.
			Healthcheck: &container.HealthConfig{Test: []string{"NONE"}},
		},
		HostConfig: &container.HostConfig{
			Binds:         []string{vol + ":" + storageStagingDir},
			RestartPolicy: container.RestartPolicy{Name: "no"},
		},
		Name: name,
	})
	if err != nil {
		if isNoSpace(err) {
			return aferrors.Wrap(err, aferrors.AFRUN020, "detail", err.Error())
		}
		return fmt.Errorf("db.docker: create the data directory anchor %s: %w", name, err)
	}
	if _, err := p.cli.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
		_ = p.remove(context.WithoutCancel(ctx), resp.ID)
		return fmt.Errorf("db.docker: start the data directory anchor %s: %w", name, err)
	}
	return nil
}

// fillStorage copies the golden's data directory into the volume.
//
// Run through the anchor, which is the one container that can see both: the
// image's data directory in its own filesystem, and the volume at the staging
// path. The mode and ownership are set from what the image's directory carries
// rather than from constants, because Postgres refuses a data directory whose
// mode it does not like and the image is the one that knows which user it runs
// as.
func (p *Provider) fillStorage(ctx context.Context, envID string) error {
	anchor := storageAnchorName(envID)
	script := "set -e; " +
		"own=$(stat -c '%u:%g' " + dataDir + "); " +
		"mode=$(stat -c '%a' " + dataDir + "); " +
		"cp -a " + dataDir + "/. " + storageStagingDir + "/; " +
		"chown \"$own\" " + storageStagingDir + "; " +
		"chmod \"$mode\" " + storageStagingDir
	res, err := p.exec(ctx, anchor, script)
	if err != nil {
		return fmt.Errorf("db.docker: copy the data directory into its own filesystem: %w", err)
	}
	if res.ExitCode != 0 {
		// A copy that ran out of room is the one failure with a remedy, and it
		// is the likely one: the declared size has to hold the whole database.
		// Told with both numbers, because "it did not fit" without the size it
		// had to fit into sends somebody guessing.
		if strings.Contains(strings.ToLower(res.Output), "no space left on device") {
			return aferrors.Coded(aferrors.AFDB043,
				"declared", strconv.FormatInt(p.storageBytes, 10),
				"used", p.storageUsed(ctx, anchor))
		}
		return fmt.Errorf("db.docker: copy the data directory into its own filesystem: exit %d: %s",
			res.ExitCode, strings.TrimSpace(res.Output))
	}
	// Read back rather than inferred from the exit code. A copy that reported
	// success and left no data directory behind would produce a branch that
	// starts, accepts connections and holds nothing, which is the exact failure
	// the dataDir constant above was written to close.
	ready, err := p.exec(ctx, anchor, "test -s "+storageStagingDir+"/"+storageReadyFile)
	if err != nil {
		return fmt.Errorf("db.docker: check the copied data directory: %w", err)
	}
	if ready.ExitCode != 0 {
		return fmt.Errorf("db.docker: the data directory was copied into its own filesystem and %s is not there afterwards, so the branch would come up empty",
			storageReadyFile)
	}
	return nil
}

// storageUsed is how much the image's data directory takes, for the message
// that says it did not fit. Best effort: a number that could not be read is
// reported as unknown rather than as zero, which would read as "nothing did not
// fit into something".
func (p *Provider) storageUsed(ctx context.Context, anchor string) string {
	res, err := p.exec(ctx, anchor, "du -sk "+dataDir+" | cut -f1")
	if err != nil || res.ExitCode != 0 {
		return "an unreadable number of"
	}
	kb, err := strconv.ParseInt(strings.TrimSpace(strings.ReplaceAll(res.Output, "\r", "")), 10, 64)
	if err != nil {
		return "an unreadable number of"
	}
	return strconv.FormatInt(kb*1024, 10)
}

// removeStorage takes the anchor and the volume away.
//
// Both, and in that order: the volume cannot be removed while a container has
// it mounted, and the anchor is the container that always has it mounted.
func (p *Provider) removeStorage(ctx context.Context, envID string) error {
	if err := p.remove(ctx, storageAnchorName(envID)); err != nil {
		return err
	}
	if err := dockerutil.RemoveVolume(ctx, p.cli, storageVolumeName(envID)); err != nil {
		return fmt.Errorf("db.docker: remove the data directory volume: %w", err)
	}
	return nil
}

// storageMount is what the branch container mounts, and empty when the manifest
// declared no size.
//
// nocopy, and that word is the difference between one mechanism and two.
//
// Docker copies an image's content at the mount point into a volume it finds
// empty. For a memory backed volume that ordinarily does not happen, because
// the driver has not mounted the tmpfs yet when the copy would run: measured on
// this daemon, a container with content at the mount point sees an empty
// directory. But this environment has an ANCHOR, and the anchor mounts the
// volume BEFORE the branch container is created, so by then the tmpfs IS
// mounted and the copy lands in it. Measured the same way: with an anchor
// holding it, the same container sees the image's file.
//
// So without this word the golden's data reaches the data directory twice over,
// once from fillStorage and once from the daemon, and the second one is
// invisible. A mutation test proved it: deleting the copy in fillStorage left
// every assertion passing, because the daemon was quietly doing the same work.
// A second mechanism nobody knows about is worse than either mechanism alone.
// It rests on an ordering no documentation states, it would put the data there
// with a mode and an owner this package did not choose, and on the day it
// changes the branch comes up as an immaculate empty Postgres, which is the
// exact failure the dataDir constant was written to close.
//
// With it, fillStorage is the only thing that fills this directory, and the
// same mutation now fails with relation "ledger" does not exist.
func (p *Provider) storageMount(vol string) []string {
	if vol == "" {
		return nil
	}
	return []string{vol + ":" + dataDir + ":nocopy"}
}

// execOutputLimit is how much of a command's output is read. Everything run
// here prints a path, a number or an error line.
const execOutputLimit = 64 << 10

// execPollInterval is how often the daemon is asked whether an exec finished.
const execPollInterval = 20 * time.Millisecond

// execResult is what a command inside a container said.
type execResult struct {
	Output   string
	ExitCode int
}

// exec runs a shell command inside a container and waits for it to finish.
//
// The exit code is read only once the daemon says the process is gone. An exec
// that is still running reports exit code zero, so the first answer would read
// an unfinished copy as a successful one.
func (p *Provider) exec(ctx context.Context, id, script string) (execResult, error) {
	created, err := p.cli.ExecCreate(ctx, id, client.ExecCreateOptions{
		Cmd:          []string{"/bin/sh", "-c", script},
		AttachStdout: true,
		AttachStderr: true,
		// A terminal, so the two streams arrive as one unframed stream.
		TTY: true,
	})
	if err != nil {
		return execResult{}, fmt.Errorf("creating an exec in %s: %w", id, err)
	}
	attached, err := p.cli.ExecAttach(ctx, created.ID, client.ExecAttachOptions{TTY: true})
	if err != nil {
		return execResult{}, fmt.Errorf("attaching to an exec in %s: %w", id, err)
	}
	out, readErr := io.ReadAll(io.LimitReader(attached.Reader, execOutputLimit))
	attached.Close()
	if readErr != nil {
		return execResult{Output: string(out)}, fmt.Errorf("reading an exec in %s: %w", id, readErr)
	}
	for {
		insp, err := p.cli.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
		if err != nil {
			if cerrdefs.IsNotFound(err) {
				return execResult{Output: string(out)}, fmt.Errorf("the exec in %s is gone: %w", id, err)
			}
			return execResult{Output: string(out)}, fmt.Errorf("inspecting an exec in %s: %w", id, err)
		}
		if !insp.Running {
			return execResult{Output: string(out), ExitCode: insp.ExitCode}, nil
		}
		select {
		case <-ctx.Done():
			return execResult{Output: string(out)}, ctx.Err()
		case <-time.After(execPollInterval):
		}
	}
}
