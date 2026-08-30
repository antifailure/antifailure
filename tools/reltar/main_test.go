package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// stamp is the commit date a release would pass in. Any fixed value works; the
// point is that it is not the clock.
var stamp = time.Date(2026, 8, 29, 22, 19, 34, 0, time.UTC)

// stage builds a tree shaped like a release archive: a binary with the
// executable bit, some source, and a nested directory.
func stage(t *testing.T, content string, mode os.FileMode, modTime time.Time) (root, name string) {
	t.Helper()
	root = t.TempDir()
	name = "antifailure_1.2.3_linux_amd64"
	tree := filepath.Join(root, name)

	if err := os.MkdirAll(filepath.Join(tree, "runner", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]struct {
		body string
		mode os.FileMode
	}{
		"af":                  {content, 0o755},
		"LICENSE":             {"MIT\n", mode},
		"runner/package.json": {`{"name":"runner"}`, mode},
		"runner/src/main.ts":  {"export {}\n", mode},
	}
	for rel, f := range files {
		p := filepath.Join(tree, filepath.FromSlash(rel))
		if err := os.WriteFile(p, []byte(f.body), f.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, f.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}
	return root, name
}

func pack(t *testing.T, root, name string) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out.tar.gz")
	if err := write(root, name, out, stamp); err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The property the release depends on. Two trees with the same contents pack to
// the same bytes even though they were staged at different moments, under
// different permissions, in different directories, which is exactly how the two
// halves of a release differ.
func TestSameContentsPackToTheSameBytes(t *testing.T) {
	rootA, name := stage(t, "binary\n", 0o644, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	rootB, _ := stage(t, "binary\n", 0o600, time.Now())

	a := pack(t, rootA, name)
	b := pack(t, rootB, name)

	if !bytes.Equal(a, b) {
		t.Fatalf("two archives of the same contents differ: %d bytes and %d bytes.\n"+
			"Something in the packing is reading the clock, the umask, or the filesystem.",
			len(a), len(b))
	}
}

// The positive control. Without it a command that always wrote the same bytes,
// or wrote nothing at all, would pass the test above and prove nothing. This is
// the check that makes the one above mean something.
func TestDifferentContentsPackToDifferentBytes(t *testing.T) {
	rootA, name := stage(t, "binary\n", 0o644, stamp)
	rootB, _ := stage(t, "a different binary\n", 0o644, stamp)

	if bytes.Equal(pack(t, rootA, name), pack(t, rootB, name)) {
		t.Fatal("two archives of different contents are identical, so the archive " +
			"does not depend on what is in it")
	}
}

// The gzip header is where the second timestamp hides. bytes 4 to 7 are a
// modification time, and `tar -czf` on the machine this was found on wrote the
// wall clock there, so an identical tar still produced a different .tar.gz.
func TestGzipHeaderCarriesNoTimestamp(t *testing.T) {
	root, name := stage(t, "binary\n", 0o644, stamp)
	archive := pack(t, root, name)

	if len(archive) < 10 {
		t.Fatalf("archive is %d bytes, which is not a gzip stream", len(archive))
	}
	for i := 4; i < 8; i++ {
		if archive[i] != 0 {
			t.Fatalf("gzip header byte %d is %#x, so the header carries a modification time",
				i, archive[i])
		}
	}
}

// Everything a reader of the archive can see about who packed it, and when.
func TestEntriesCarryNoBuilderIdentity(t *testing.T) {
	root, name := stage(t, "binary\n", 0o600, time.Now())
	archive := pack(t, root, name)

	var seen []string
	modes := map[string]int64{}
	for _, h := range headers(t, archive) {
		seen = append(seen, h.Name)
		modes[h.Name] = h.Mode

		if !h.ModTime.Equal(stamp) {
			t.Errorf("%s carries mtime %s, not the stamp it was given", h.Name, h.ModTime)
		}
		if h.Uid != 0 || h.Gid != 0 {
			t.Errorf("%s is owned by %d:%d rather than 0:0", h.Name, h.Uid, h.Gid)
		}
		if h.Uname != "" || h.Gname != "" {
			t.Errorf("%s names its owner %q:%q, which is the packing machine's account",
				h.Name, h.Uname, h.Gname)
		}
		if h.Format != tar.FormatGNU {
			t.Errorf("%s is in %v rather than the GNU format this writes on purpose", h.Name, h.Format)
		}
	}

	// af keeps the executable bit. That is the one permission that has to
	// survive, because a release whose binary is not executable is not a
	// release.
	if got := modes[name+"/af"]; got != 0o755 {
		t.Errorf("af is mode %#o, not 0755; the archive would install a binary nobody can run", got)
	}
	// The staged tree was 0600. A release that carried that would ship files
	// the extracting user's group cannot read.
	if got := modes[name+"/LICENSE"]; got != 0o644 {
		t.Errorf("LICENSE is mode %#o, not 0644, so the builder's umask reached the artifact", got)
	}

	// Sorted, so the filesystem's readdir order cannot reorder the archive.
	for i := 1; i < len(seen); i++ {
		if seen[i-1] > seen[i] {
			t.Fatalf("entries are not sorted: %q comes before %q", seen[i-1], seen[i])
		}
	}
}

// An empty staging directory is a release that shipped nothing. It has to be an
// error rather than a valid small archive, because the checksum and the
// signature downstream would happily cover it.
func TestAnEmptyTreeIsRefused(t *testing.T) {
	root := t.TempDir()
	name := "antifailure_1.2.3_linux_amd64"
	if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
		t.Fatal(err)
	}
	// WalkDir yields the directory itself, so "empty" here means the tree has
	// nothing in it but its own root.
	if err := os.Remove(filepath.Join(root, name)); err != nil {
		t.Fatal(err)
	}
	if err := write(root, name, filepath.Join(t.TempDir(), "out.tar.gz"), stamp); err == nil {
		t.Fatal("a missing tree packed without an error")
	}
}

// Leaving the stamp out would take it from the clock, which is the whole bug.
func TestAMissingStampIsRefused(t *testing.T) {
	if _, err := parseTime(""); err == nil {
		t.Fatal("an empty -mtime was accepted")
	}
	epoch, err := parseTime("1787502000")
	if err != nil {
		t.Fatalf("a Unix epoch was refused: %v", err)
	}
	rfc, err := parseTime("2026-08-29T22:19:34-07:00")
	if err != nil {
		t.Fatalf("an RFC 3339 time was refused: %v", err)
	}
	if epoch.Location() != time.UTC || rfc.Location() != time.UTC {
		t.Error("times are not normalised to UTC, so the same instant written two ways would differ")
	}
	if _, err := parseTime("yesterday"); err == nil {
		t.Fatal("an unparseable -mtime was accepted")
	}
}

// A partial file left behind would be hashed by the checksum step as though it
// were the release.
func TestNoPartialFileSurvivesAFailure(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.tar.gz")
	if err := write(root, "does-not-exist", out, stamp); err == nil {
		t.Fatal("archiving a missing tree succeeded")
	}
	if _, err := os.Stat(out + ".partial"); !os.IsNotExist(err) {
		t.Error("a .partial file was left behind")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("an output file was left behind after a failure")
	}
}

func headers(t *testing.T, archive []byte) []*tar.Header {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	var out []*tar.Header
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, h)
	}
	return out
}
