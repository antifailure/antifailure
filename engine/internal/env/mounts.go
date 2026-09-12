package env

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// readMounts turns what a service declared into what a runtime receives.
//
// THE BOUNDARY THIS IS, said before what it does. This is the only place a
// mount touches the host's filesystem. Every runtime is handed CONTENTS, so no
// runtime can bind a host path even if binding would be the shorter code, and
// the confinement rules are checked once here rather than once per runtime with
// a chance of disagreeing. The reasoning is on provider.MountSpec.
//
// It is read ONCE, before anything starts. That is a semantic worth stating
// because the alternative, a live view, is what a bind mount gives and what
// people expect from compose: a source edited after the environment came up
// does NOT reach a running container, and a source deleted after it came up
// cannot break one. For a rehearsal that is the better of the two, because the
// environment is then a function of the tree as it was when it started rather
// than of whatever the tree becomes while it runs.
//
// A missing source is an ERROR rather than an empty mount. A service whose
// configuration file did not arrive starts on its image's defaults and reports
// itself running, which is a different topology reported as the one the recipe
// asked for, and that is the exact failure mounts were built to stop.
func readMounts(root string, svc schema.Service) ([]provider.MountSpec, error) {
	if len(svc.Mounts) == 0 {
		return nil, nil
	}
	out := make([]provider.MountSpec, 0, len(svc.Mounts))
	for _, m := range svc.Mounts {
		if m.IsVolume() {
			out = append(out, provider.MountSpec{At: m.At, Volume: m.Volume})
			continue
		}
		files, err := readMountFiles(root, svc.Name, m)
		if err != nil {
			return nil, err
		}
		out = append(out, provider.MountSpec{At: m.At, Files: files})
	}
	return out, nil
}

// readMountFiles reads one repository path, a file or a whole directory.
func readMountFiles(root, service string, m schema.Mount) ([]provider.MountFile, error) {
	full := filepath.Join(root, filepath.FromSlash(m.Path))
	info, err := os.Lstat(full)
	if err != nil {
		return nil, mountErr(service, fmt.Sprintf("%s: %v", m.Path, err))
	}
	// Lstat rather than Stat, so that the source ITSELF being a link is seen
	// here rather than followed. Validation refuses a link that leaves the
	// repository, and it can only do that when it knows the root; a caller that
	// assembled a manifest by hand reaches this function with neither check
	// having run, and this is the last place to notice.
	if info.Mode()&fs.ModeSymlink != 0 {
		target, resolveErr := resolveInside(root, full)
		if resolveErr != nil {
			return nil, mountErr(service, resolveErr.Error())
		}
		full = target
		info, err = os.Stat(full)
		if err != nil {
			return nil, mountErr(service, fmt.Sprintf("%s: %v", m.Path, err))
		}
	}
	if info.IsDir() {
		return readMountDir(root, service, m, full)
	}
	if info.Size() > schema.MaxMountBytes {
		return nil, mountErr(service, fmt.Sprintf(
			"%s is %d bytes and a mount may carry %d", m.Path, info.Size(), schema.MaxMountBytes))
	}
	body, err := os.ReadFile(full)
	if err != nil {
		return nil, mountErr(service, fmt.Sprintf("%s: %v", m.Path, err))
	}
	return []provider.MountFile{{Mode: int64(info.Mode().Perm()), Data: body}}, nil
}

// readMountDir reads a directory, refusing what it cannot carry honestly.
//
// The three refusals are the three ways a directory is not a bag of files, and
// each one is refused rather than skipped. A skipped entry is a container that
// started with most of what it asked for, which is the shape of the failure
// this whole key exists to remove: somebody would find out from the behaviour
// of a service rather than from a message naming the file.
func readMountDir(root, service string, m schema.Mount, full string) ([]provider.MountFile, error) {
	var files []provider.MountFile
	var total int64
	err := filepath.WalkDir(full, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(full, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.Type()&fs.ModeSymlink != 0 {
			target, resolveErr := resolveInside(root, p)
			if resolveErr != nil {
				return resolveErr
			}
			p = target
		}
		info, statErr := os.Stat(p)
		if statErr != nil {
			return statErr
		}
		if !info.Mode().IsRegular() {
			// A socket, a device or a fifo. There is nothing to copy and
			// pretending otherwise would put an empty file where the service
			// expects a device.
			return fmt.Errorf("%s is not a regular file", path.Join(m.Path, rel))
		}
		if len(files) >= schema.MaxMountFiles {
			return fmt.Errorf("%s holds more than %d files", m.Path, schema.MaxMountFiles)
		}
		total += info.Size()
		if total > schema.MaxMountBytes {
			return fmt.Errorf("%s holds more than %d bytes", m.Path, schema.MaxMountBytes)
		}
		body, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		files = append(files, provider.MountFile{
			Rel: rel, Mode: int64(info.Mode().Perm()), Data: body,
		})
		return nil
	})
	if err != nil {
		return nil, mountErr(service, err.Error())
	}
	if len(files) == 0 {
		return nil, mountErr(service, fmt.Sprintf(
			"%s is a directory with no files in it, so the mount would put nothing at %s",
			m.Path, m.At))
	}
	// Already in a stable order, because filepath.WalkDir reads every
	// directory in lexical order and says so in its documentation. There used
	// to be a sort here as well, and no test could tell it was there: a line
	// whose removal nothing can detect is a claim nothing checks, so the
	// guarantee is the walk's and TestReadMounts_CarriesADirectoryInAStableOrder
	// holds it to that.
	return files, nil
}

// resolveInside follows a link and refuses a target outside the repository.
//
// The refusal is the containment rule, at the last point it can be applied. A
// mount copies what it names into a container that runs the code being
// rehearsed, so a link out of the tree would hand that container a file off the
// machine. Nothing stops somebody committing such a link, and nothing else here
// would notice: the path is lexically fine, the file exists, and the read
// succeeds.
func resolveInside(root, p string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("the repository root cannot be resolved: %w", err)
	}
	target, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("%s cannot be resolved: %w", p, err)
	}
	rel, err := filepath.Rel(realRoot, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf(
			"%s is a link that leads outside the repository, and a mount copies what it "+
				"names into a container running the code under rehearsal",
			filepath.Base(p))
	}
	return target, nil
}

func mountErr(service, detail string) error {
	return aferrors.Coded(aferrors.AFRUN048, "service", service, "detail", detail)
}
