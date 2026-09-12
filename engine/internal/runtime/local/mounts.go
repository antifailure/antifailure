package local

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/volume"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// volumeName is deterministic, which is what makes a named volume persist.
//
// A second Up finds the volume the first one made and the service reads what it
// wrote. A random name would create a fresh empty volume on every run, and the
// key would then be indistinguishable from no key at all except for the disk it
// consumed.
//
// Scoped to the environment, because two environments of the same repository are
// two twins and a volume shared between them is one twin reading the other's
// writes. That is the same rule every other name in this runtime follows.
func volumeName(envID, name string) string {
	return "af-vol-" + envID + "-" + name
}

// ensureVolumes creates the named volumes a service asks for.
//
// Labelled with the environment, because `af down` sweeps by label and a volume
// that is not labelled is a volume that outlives the environment that made it.
// The emulator container was found to leak exactly that way before it shipped:
// it survived Down, held the network open, and made "the state goes away with
// the environment" false in the one way nobody checks.
//
// Idempotent by construction. VolumeCreate on a name that exists returns the
// existing volume rather than an error, which is what makes a second Up reuse
// what the first one wrote.
func (r *Runtime) ensureVolumes(
	ctx context.Context, envID string, s provider.ServiceSpec, journal func(string, string) error,
) error {
	for _, m := range s.Mounts {
		if m.Volume == "" {
			continue
		}
		name := volumeName(envID, m.Volume)
		if err := journal(kindVolume, name); err != nil {
			return err
		}
		_, err := r.cli.VolumeCreate(ctx, volume.CreateOptions{
			Name:   name,
			Labels: r.managed(dockerutil.KindVolume, envID),
		})
		if err != nil {
			return aferrors.Coded(aferrors.AFRUN048,
				"service", s.Name,
				"detail", fmt.Sprintf("the volume %s could not be created: %v", m.Volume, err))
		}
	}
	return nil
}

// volumeMounts is the daemon's own description of the named volumes.
//
// TYPE VOLUME AND NEVER TYPE BIND, and that is the containment decision rather
// than a preference. A bind carries a path on the host, so a container holding
// one can read and write the machine at that path; a named volume is storage the
// daemon owns, reachable only through the daemon that made it. Nothing in this
// runtime constructs a bind mount, the spec it is handed carries no host path it
// could construct one from, and containment_test.go asserts that every mount on
// every container in an environment is of type volume and carries this
// environment's own name.
func volumeMounts(envID string, s provider.ServiceSpec) []mount.Mount {
	var out []mount.Mount
	for _, m := range s.Mounts {
		if m.Volume == "" {
			continue
		}
		out = append(out, mount.Mount{
			Type:   mount.TypeVolume,
			Source: volumeName(envID, m.Volume),
			Target: m.At,
		})
	}
	return out
}

// copyMounts places every file mount inside a container that has been created
// and not yet started.
//
// Before the start, because the whole point is a configuration file the image
// reads at startup. Both ClickHouse services in the published ClickHouse recipe
// read theirs in the first milliseconds, and a file that arrived afterwards
// would leave them running on image defaults: a different topology, reported as
// the one the recipe asked for.
//
// The contents come off the spec rather than off the disk. No path on this
// machine reaches this function, which is what makes it structurally unable to
// bind one. provider.MountSpec carries the reasoning.
func (r *Runtime) copyMounts(ctx context.Context, id string, s provider.ServiceSpec) error {
	for _, m := range s.Mounts {
		if m.Volume != "" {
			continue
		}
		body, err := mountTar(r.clock.Now(), m)
		if err != nil {
			return aferrors.Coded(aferrors.AFRUN048, "service", s.Name, "detail", err.Error())
		}
		// To the root with the whole path in the tar header, the same shape
		// installCA uses, because a copy addressed at a directory requires that
		// directory to already exist in the image and the target of a mount
		// routinely does not.
		if err := r.cli.CopyToContainer(ctx, id, "/", bytes.NewReader(body),
			container.CopyToContainerOptions{}); err != nil {
			return aferrors.Coded(aferrors.AFRUN048,
				"service", s.Name,
				"detail", fmt.Sprintf("writing %s into the container: %v", m.At, err))
		}
	}
	return nil
}

// mountTar builds the archive for one mount.
//
// Every ancestor directory of the target is emitted, at 0755, because Docker's
// extraction creates a parent only when the archive names it and the target of a
// mount is routinely a directory the image does not have:
// /docker-entrypoint-initdb.d does not exist in most images and
// /etc/clickhouse-server/config.d exists in exactly one. The cost of emitting
// them is that an ancestor which DOES exist has its mode set to 0755, which is
// the mode those directories already carry in every image this was tried
// against, and is stated here rather than discovered.
func mountTar(now time.Time, m provider.MountSpec) ([]byte, error) {
	if len(m.Files) == 0 {
		return nil, fmt.Errorf("the mount at %s carries no files", m.At)
	}
	target := strings.TrimPrefix(path.Clean(m.At), "/")
	if target == "" || target == "." {
		return nil, fmt.Errorf("the mount target %q is the container's root", m.At)
	}

	type entry struct {
		name string
		mode int64
		data []byte
	}
	var files []entry
	dirs := map[string]bool{}
	addParents := func(p string) {
		for d := path.Dir(p); d != "." && d != "/" && d != ""; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	for _, f := range m.Files {
		name := target
		if f.Rel != "" {
			rel := path.Clean(f.Rel)
			// The reader produced these from a walk under the source, so a
			// parent segment here means a caller built a spec by hand. Refused
			// rather than cleaned, because a Rel of ../../etc/passwd would
			// otherwise write outside the mount's own target.
			if rel == ".." || strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, "/") {
				return nil, fmt.Errorf("the file %q in the mount at %s leaves its target", f.Rel, m.At)
			}
			name = target + "/" + rel
			dirs[target] = true
		}
		addParents(name)
		mode := f.Mode & 0o777
		if mode == 0 {
			mode = 0o644
		}
		files = append(files, entry{name: name, mode: mode, data: f.Data})
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	names := make([]string, 0, len(dirs))
	for d := range dirs {
		names = append(names, d)
	}
	// Shortest first, so a parent is always written before its children.
	sort.Slice(names, func(i, j int) bool {
		if len(names[i]) != len(names[j]) {
			return len(names[i]) < len(names[j])
		}
		return names[i] < names[j]
	})
	for _, d := range names {
		if err := tw.WriteHeader(&tar.Header{
			Name: d + "/", Mode: 0o755, ModTime: now.UTC(),
			Format: tar.FormatPAX, Typeflag: tar.TypeDir,
		}); err != nil {
			return nil, err
		}
	}
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: f.name, Mode: f.mode, Size: int64(len(f.data)), ModTime: now.UTC(),
			Format: tar.FormatPAX, Typeflag: tar.TypeReg,
		}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
