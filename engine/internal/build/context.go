package build

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// Context is the set of files a build sees, and a hash of exactly that.
//
// The hash is the point. It is taken over the normalized tar, which means two
// checkouts of the same commit on two machines produce the same value, and a
// rebuild with nothing changed can be skipped without asking the daemon. That
// only works if the tar carries no incidental variation, so modification
// times, owners, and directory order are all fixed rather than copied from the
// filesystem. Every one of those differs between two clones of the same
// repository.
//
// It also puts a bound on what is sent. A repository with a stray four
// gigabyte fixture should fail with a message naming the file, not stream for
// six minutes and then fail inside the daemon.
type Context struct {
	// Root is the directory the context was built from.
	Root string
	// Digest is sha256 of the normalized tar, hex encoded.
	Digest string
	// Files is every path included, sorted, relative and slash separated.
	Files []string
	// Bytes is the total size of the file contents included.
	Bytes int64
	// Excluded counts the paths .dockerignore left out, which is the number
	// worth printing when a build is slower than somebody expected.
	Excluded int
	// tarball holds the built archive.
	tarball []byte
}

// ContextOptions configure a context walk.
type ContextOptions struct {
	// Root is the directory to walk.
	Root string
	// Ignore is the .dockerignore to apply. Nil excludes nothing.
	Ignore *Ignore
	// Service names the manifest service this context is for. It appears in
	// error messages, where "the build context for web is too large" is
	// actionable and "the build context is too large" is not.
	Service string
	// MaxBytes is the total size of file contents. Zero uses the default.
	MaxBytes int64
	// MaxFiles is the number of entries. Zero uses the default.
	MaxFiles int
}

// The defaults are generous enough for a real monorepo and small enough that
// hitting one means something is wrong: a build directory that is not ignored,
// or a fixture nobody meant to commit.
const (
	DefaultMaxBytes = 2 << 30 // 2 GiB
	DefaultMaxFiles = 200_000
)

func (o ContextOptions) withDefaults() ContextOptions {
	if o.MaxBytes <= 0 {
		o.MaxBytes = DefaultMaxBytes
	}
	if o.MaxFiles <= 0 {
		o.MaxFiles = DefaultMaxFiles
	}
	if o.Service == "" {
		o.Service = "this service"
	}
	if o.Ignore == nil {
		o.Ignore = &Ignore{}
	}
	return o
}

// epoch is the modification time every entry gets.
//
// Not zero, because some tools treat a zero time as missing, and not now,
// because then the digest would change on every run and the cache would never
// hit. A fixed date in the past is unambiguous and stable forever.
var epoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// NewContext walks a directory and builds the context for it.
//
// Symlinks are followed only when they stay inside the root. A link pointing
// outside is dropped rather than resolved, because a build context that reads
// /etc or a sibling checkout is not reproducible on anyone else's machine, and
// it is how a secret from outside the repository ends up in an image layer.
func NewContext(opts ContextOptions) (*Context, error) {
	opts = opts.withDefaults()
	absRoot, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, err
	}
	ig := opts.Ignore

	type entry struct {
		rel  string
		size int64
		mode fs.FileMode
		link string
	}
	var entries []entry
	var total int64
	excluded := 0

	walkErr := filepath.WalkDir(absRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// A file that vanished mid walk is normal in a repository somebody
			// is working in. Anything else is reported.
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if p == absRoot {
			return nil
		}
		rel, relErr := filepath.Rel(absRoot, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)

		if drop, _ := ig.Excluded(rel, d.IsDir()); drop {
			excluded++
			if d.IsDir() {
				// Skipping the whole subtree is what makes ignoring
				// node_modules fast rather than merely correct.
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}

		info, infoErr := d.Info()
		if infoErr != nil {
			if errors.Is(infoErr, fs.ErrNotExist) {
				return nil
			}
			return infoErr
		}
		mode := info.Mode()

		switch {
		case mode&fs.ModeSymlink != 0:
			target, linkErr := os.Readlink(p)
			if linkErr != nil {
				return nil
			}
			if !withinRoot(absRoot, p, target) {
				// Dropped rather than resolved. A context that reads outside
				// the repository is not reproducible anywhere else, and it is
				// how a file nobody audited ends up in an image layer.
				excluded++
				return nil
			}
			entries = append(entries, entry{rel: rel, mode: mode, link: filepath.ToSlash(target)})
			return nil
		case !mode.IsRegular():
			// Sockets, devices, and named pipes are not source code and Docker
			// cannot use them.
			excluded++
			return nil
		}

		total += info.Size()
		if total > opts.MaxBytes {
			return aferrors.Coded(aferrors.AFBLD003,
				"service", opts.Service,
				"size", "over "+humanBytes(opts.MaxBytes)+" at "+rel,
				"limit", humanBytes(opts.MaxBytes))
		}
		entries = append(entries, entry{rel: rel, size: info.Size(), mode: mode})
		if len(entries) > opts.MaxFiles {
			return aferrors.Coded(aferrors.AFBLD004,
				"service", opts.Service, "count", fmt.Sprint(opts.MaxFiles), "path", rel)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	// Sorted, so the archive does not depend on the order the filesystem
	// happened to return. Two clones of one commit must hash the same.
	sort.Slice(entries, func(a, b int) bool { return entries[a].rel < entries[b].rel })

	var buf strings.Builder
	sum := sha256.New()
	tw := tar.NewWriter(io.MultiWriter(hashOnly{sum}, &writerTo{&buf}))

	files := make([]string, 0, len(entries))
	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.rel,
			Mode:     int64(normalizeMode(e.mode)),
			ModTime:  epoch,
			Format:   tar.FormatPAX,
			Uid:      0,
			Gid:      0,
			Uname:    "",
			Gname:    "",
			Typeflag: tar.TypeReg,
			Size:     e.size,
		}
		if e.link != "" {
			hdr.Typeflag, hdr.Linkname, hdr.Size = tar.TypeSymlink, e.link, 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if e.link == "" && e.size > 0 {
			if err := copyFileInto(tw, filepath.Join(absRoot, filepath.FromSlash(e.rel)), e.size); err != nil {
				return nil, err
			}
		}
		files = append(files, e.rel)
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}

	return &Context{
		Root:     absRoot,
		Digest:   hex.EncodeToString(sum.Sum(nil)),
		Files:    files,
		Bytes:    total,
		Excluded: excluded,
		tarball:  []byte(buf.String()),
	}, nil
}

// Tar returns a reader over the archive to send to the daemon.
func (c *Context) Tar() io.Reader { return strings.NewReader(string(c.tarball)) }

// Size is the archive's size in bytes.
func (c *Context) Size() int { return len(c.tarball) }

// Has reports whether a path is in the context, which is how a plan checks for
// a Dockerfile without touching the disk a second time and getting a different
// answer than the build will.
func (c *Context) Has(p string) bool {
	i := sort.SearchStrings(c.Files, p)
	return i < len(c.Files) && c.Files[i] == p
}

// normalizeMode reduces a file mode to the only distinction that matters
// inside an image, which is whether it is executable.
//
// Copying the real mode would make the digest depend on the umask of whoever
// cloned the repository, so the same commit would produce two different
// digests on two machines and the cache would never hit across them.
func normalizeMode(m fs.FileMode) fs.FileMode {
	if m&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

// withinRoot reports whether a symlink stays inside the context.
func withinRoot(root, linkPath, target string) bool {
	resolved := target
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(linkPath), target)
	}
	resolved = filepath.Clean(resolved)
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func copyFileInto(w io.Writer, path string, size int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	n, err := io.Copy(w, io.LimitReader(f, size))
	if err != nil {
		return err
	}
	if n < size {
		// The file shrank between the walk and the read. The tar header
		// already declares the old size, so the archive would be corrupt;
		// padding keeps it valid and the next build picks up the new content.
		_, err = w.Write(make([]byte, size-n))
	}
	return err
}

// hashOnly adapts a hash to an io.Writer that never fails, so a MultiWriter
// over it cannot short circuit the tar writer.
type hashOnly struct{ w io.Writer }

func (h hashOnly) Write(p []byte) (int, error) {
	_, _ = h.w.Write(p)
	return len(p), nil
}

type writerTo struct{ b *strings.Builder }

func (w *writerTo) Write(p []byte) (int, error) { return w.b.Write(p) }

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
