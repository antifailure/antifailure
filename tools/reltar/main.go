// Command reltar writes a release archive whose bytes are a function of its
// contents and nothing else.
//
// It exists because the release archives were not reproducible and nobody
// noticed, because the check was pointed at the wrong artifact. `just
// reproducible` compared bin/af, the binaries were identical, and the gate went
// green. The thing a person downloads is the .tar.gz, the thing checksums.txt
// names is the .tar.gz, and the thing cosign signs is that checksums file, so
// the archive is the artifact whose reproducibility is worth anything. Two
// builds of one commit produced four different archives every time.
//
// Three separate causes, each of which `tar -czf` supplies for free:
//
//   - Every entry carried the moment the release was staged. The workflow
//     builds the tree with cp, and cp writes a fresh mtime, so the archive
//     recorded the wall clock of the packaging step.
//   - The gzip header carries its own timestamp, so even an identical tar
//     compressed twice differs in bytes 4 to 7.
//   - Ownership, permissions and entry order came from the builder: the uid,
//     the gid, the umask, and whatever order the filesystem returned.
//
// So the archive is written here instead, where each of those is a decision
// rather than an accident. Everything variable is pinned:
//
//	names       sorted, so the filesystem's readdir order cannot matter
//	mtime       one value, passed in, derived from the commit
//	uid/gid     0, with no user or group names
//	mode        0755 for directories and executables, 0644 for everything else
//	format      GNU, chosen rather than inferred
//	gzip        no embedded modification time, one compression level
//
// The mode normalisation is the one that looks like overreach and is not: a
// builder with umask 002 stages files as 0664 and one with umask 022 stages
// them as 0644, so without this the archive depends on a setting of the machine
// that built it. What has to survive is the executable bit on af, and that does.
package main

import (
	"archive/tar"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func main() {
	dir := flag.String("C", ".", "directory holding the tree to archive")
	out := flag.String("o", "", "archive to write")
	mtime := flag.String("mtime", "", "modification time for every entry, as a Unix epoch or RFC 3339")
	flag.Parse()

	if *out == "" {
		fail("-o is required")
	}
	if flag.NArg() != 1 {
		fail("expected exactly one directory name to archive, got %d", flag.NArg())
	}
	when, err := parseTime(*mtime)
	if err != nil {
		fail("%v", err)
	}

	if err := write(*dir, flag.Arg(0), *out, when); err != nil {
		fail("%v", err)
	}
}

// parseTime accepts what the two callers actually have. The release workflow
// has a commit date in RFC 3339 from `git show -s --format=%cI`, and
// SOURCE_DATE_EPOCH, the convention every other reproducible build uses, is
// seconds. Accepting both means neither caller has to convert, and a conversion
// nobody checks is a place for the timestamp to drift back to wall clock.
func parseTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, fmt.Errorf("-mtime is required. Leaving it out would take " +
			"the time from the clock, which is the bug this command exists to remove")
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.Unix(seconds, 0).UTC(), nil
	}
	when, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("-mtime %q is neither a Unix epoch nor RFC 3339: %w", value, err)
	}
	return when.UTC(), nil
}

func write(root, name, out string, when time.Time) error {
	tree := filepath.Join(root, name)
	info, err := os.Stat(tree)
	if err != nil {
		return fmt.Errorf("reading the tree to archive: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", tree)
	}

	paths, err := collect(tree)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		// An empty archive is a green build that shipped nothing, which is
		// exactly the failure mode this whole area keeps producing.
		return fmt.Errorf("%s is empty, so there is nothing to release", tree)
	}

	// Written to a temporary file and renamed, so an interrupted run leaves no
	// half archive for the checksum step to hash.
	tmp := out + ".partial"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)

	// BestCompression rather than the default, named rather than inherited: the
	// level is part of the output bytes, so leaving it implicit would make the
	// archive depend on a default that a Go release is free to change.
	zw, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		f.Close()
		return err
	}
	// Left at its zero value on purpose. gzip's header carries a modification
	// time and a name, and the writer emits 0 and "" for these, which is what
	// makes two runs agree. Setting ModTime here would put the clock straight
	// back into the bytes.
	zw.Header = gzip.Header{OS: 255}

	tw := tar.NewWriter(zw)
	for _, p := range paths {
		if err := add(tw, root, p, when); err != nil {
			f.Close()
			return fmt.Errorf("%s: %w", p, err)
		}
	}
	if err := tw.Close(); err != nil {
		f.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, out)
}

// collect returns every path under tree, relative to root, sorted.
//
// Sorted because filepath.WalkDir already reads directories in lexical order
// but the guarantee is worth stating rather than inheriting: the property being
// defended is that the byte order of the archive does not depend on the
// filesystem, and a sort makes that true by construction instead of by the
// behaviour of a function somebody could swap.
func collect(tree string) ([]string, error) {
	var paths []string
	root := filepath.Dir(tree)
	err := filepath.WalkDir(tree, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

func add(tw *tar.Writer, root, rel string, when time.Time) error {
	full := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	if err != nil {
		return err
	}

	header := &tar.Header{
		Name:    rel,
		ModTime: when,
		Uid:     0,
		Gid:     0,
		// Empty rather than "root". A name is looked up on the machine that
		// packed the archive, so writing one would carry that machine's
		// /etc/passwd into the artifact.
		Uname: "",
		Gname: "",
		// Chosen rather than inferred. Left unset, the writer picks the
		// smallest format the header fits, so adding one file with a long path
		// would silently change the encoding of every archive.
		Format: tar.FormatGNU,
	}

	switch {
	case info.IsDir():
		header.Typeflag = tar.TypeDir
		header.Name = rel + "/"
		header.Mode = 0o755
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(full)
		if err != nil {
			return err
		}
		header.Typeflag = tar.TypeSymlink
		header.Linkname = filepath.ToSlash(target)
		header.Mode = 0o777
	case info.Mode().IsRegular():
		header.Typeflag = tar.TypeReg
		header.Size = info.Size()
		// The executable bit is the only permission that means anything here,
		// and af needs it. Everything else is normalised so the builder's umask
		// cannot reach the artifact.
		if info.Mode()&0o111 != 0 {
			header.Mode = 0o755
		} else {
			header.Mode = 0o644
		}
	default:
		// A device node, socket or fifo in a release archive is a mistake, and
		// tar would encode one without comment.
		return fmt.Errorf("%s is a %s, which does not belong in a release archive", rel, kind(info.Mode()))
	}

	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	if header.Typeflag != tar.TypeReg {
		return nil
	}

	f, err := os.Open(full)
	if err != nil {
		return err
	}
	defer f.Close()
	written, err := io.Copy(tw, f)
	if err != nil {
		return err
	}
	if written != header.Size {
		// The file changed under us between the Lstat and the read. tar would
		// write a truncated or over-long entry and the archive would be
		// corrupt in a way only the person extracting it finds out about.
		return fmt.Errorf("read %d bytes for a %d byte header; the file changed while it was being archived",
			written, header.Size)
	}
	return nil
}

func kind(mode fs.FileMode) string {
	switch {
	case mode&fs.ModeDevice != 0:
		return "device"
	case mode&fs.ModeSocket != 0:
		return "socket"
	case mode&fs.ModeNamedPipe != 0:
		return "named pipe"
	default:
		return strings.TrimSpace(mode.String())
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "reltar: "+format+"\n", args...)
	os.Exit(1)
}
