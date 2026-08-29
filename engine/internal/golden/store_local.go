package golden

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// localStore keeps goldens in a directory.
//
// Unglamorous, and the right answer more often than it sounds: a shared runner
// with a volume, a CI cache, an NFS mount. It is also the store the other two
// are checked against, because a bug in the interface shows up here without a
// container to stand up first.
type localStore struct{ root string }

func newLocalStore(raw string) (Store, error) {
	path := raw
	// file:// is accepted because somebody who has written azure_blob and s3
	// URLs in the same manifest will write one here too.
	if strings.HasPrefix(raw, "file://") {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("golden: %q is not a usable file URL: %w", redactURL(raw), err)
		}
		path = u.Path
		if u.Host != "" && u.Host != "localhost" {
			// file://relative/path parses the first segment as a host, which
			// is almost always a missing slash rather than a real host.
			return nil, fmt.Errorf(
				"golden: %q looks like a relative file URL; an absolute one is file:///path", raw)
		}
	}
	if path == "" {
		return nil, fmt.Errorf("golden: the local storage URL names no directory")
	}
	abs, err := filepath.Abs(os.ExpandEnv(path))
	if err != nil {
		return nil, fmt.Errorf("golden: %q is not a usable directory: %w", path, err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("golden: the storage directory %s could not be made: %w", abs, err)
	}
	return &localStore{root: abs}, nil
}

func (s *localStore) Name() string { return "the directory " + s.root }

// resolve turns an object name into a path, and refuses one that would leave
// the directory. Names are built by this package rather than by a user, so
// this is a backstop rather than a defence, and a backstop is cheap.
func (s *localStore) resolve(name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("golden: %q is not a name this store will write", name)
	}
	return filepath.Join(s.root, clean), nil
}

func (s *localStore) Put(ctx context.Context, name string, _ int64, body io.Reader) error {
	path, err := s.resolve(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("golden: %s: %w", filepath.Dir(path), err)
	}
	// Written beside and renamed into place, so that a reader never sees a
	// half written dump and a crash leaves a temporary file rather than a
	// truncated one wearing the real name.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".af-upload-*")
	if err != nil {
		return fmt.Errorf("golden: %s: %w", path, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := io.Copy(tmp, body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("golden: writing %s: %w", name, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("golden: writing %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("golden: writing %s: %w", name, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("golden: writing %s: %w", name, err)
	}
	return ctx.Err()
}

func (s *localStore) Get(ctx context.Context, name string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := s.resolve(name)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if err != nil {
		return nil, fmt.Errorf("golden: reading %s: %w", name, err)
	}
	return f, nil
}

func (s *localStore) List(ctx context.Context, prefix string) ([]Object, error) {
	var out []Object
	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return ctx.Err()
		}
		rel, relErr := filepath.Rel(s.root, path)
		if relErr != nil {
			return relErr
		}
		name := filepath.ToSlash(rel)
		// The temporary files Put leaves behind on a crash are not objects.
		if strings.HasPrefix(filepath.Base(name), ".af-upload-") {
			return nil
		}
		if !strings.HasPrefix(name, prefix) {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		out = append(out, Object{Name: name, Size: info.Size(), Modified: info.ModTime().UTC()})
		return ctx.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("golden: listing %s: %w", s.root, err)
	}
	return out, nil
}

func (s *localStore) Delete(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := s.resolve(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("golden: removing %s: %w", name, err)
	}
	// The version's directory goes too when it empties, so that a listing
	// shows versions rather than the ghosts of versions.
	_ = os.Remove(filepath.Dir(path))
	return nil
}
