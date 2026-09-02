package installsh

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The verification tests need to run the installer on a machine that is missing
// something, and the two ways to arrange that are both wrong on their own.
// Putting a failing stub named `shasum` on PATH does not simulate absence:
// `command -v shasum` still finds it, which is the question the script asks.
// Deleting the real one is not available and would not be sane if it were.
//
// So the machine is rebuilt instead. toolbox is a directory of symlinks to
// every executable the session PATH would have offered, minus the ones named,
// and the session then runs with that as its whole PATH. A tool excluded here
// is absent in exactly the way it is absent on a machine that never had it.

// realPathDirs are the system directories the sessions draw their tools from.
// Held here rather than read from the ambient PATH, so a machine with homebrew
// ahead of /usr/bin cannot quietly reintroduce a tool a test excluded.
var realPathDirs = []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"}

func toolbox(t *testing.T, exclude ...string) string {
	t.Helper()
	skip := map[string]bool{}
	for _, e := range exclude {
		skip[e] = true
	}
	dir := t.TempDir()
	linked := 0
	for _, src := range realPathDirs {
		entries, err := os.ReadDir(src)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if skip[name] {
				continue
			}
			link := filepath.Join(dir, name)
			if _, err := os.Lstat(link); err == nil {
				// The first directory on PATH wins, as it would have.
				continue
			}
			if err := os.Symlink(filepath.Join(src, name), link); err != nil {
				t.Fatalf("linking %s: %v", name, err)
			}
			linked++
		}
	}
	// A toolbox that linked nothing would make every test in this file pass by
	// failing for the wrong reason, which is the shape this repository calls a
	// broken instrument.
	if linked < 50 {
		t.Fatalf("the toolbox holds only %d commands, so it is not a working PATH", linked)
	}
	if _, err := os.Stat(filepath.Join(dir, "tar")); err != nil {
		t.Fatalf("the toolbox has no tar, so the installer could not have got as far as verifying: %v", err)
	}
	return dir
}

// hide removes one tool from the session's PATH for the rest of the test.
//
// Repeated calls accumulate, because the interesting case is a machine with
// none of the three rather than one missing at a time.
func hide(t *testing.T, s *session, tool string) {
	t.Helper()
	s.hidden = append(s.hidden, tool)
	s.path = s.stubs + ":" + toolbox(t, s.hidden...)
	if onPathIn(s.path, tool) {
		t.Fatalf("%s is still reachable after hiding it, so this test proves nothing", tool)
	}
}

// onPath reports whether a tool exists on this machine at all, so a test that
// keeps one tool can say it proved nothing rather than pass vacuously.
func onPath(t *testing.T, tool string) bool {
	t.Helper()
	for _, dir := range realPathDirs {
		if _, err := os.Stat(filepath.Join(dir, tool)); err == nil {
			return true
		}
	}
	return false
}

func onPathIn(path, tool string) bool {
	cmd := exec.Command("/bin/sh", "-c", "command -v "+tool)
	cmd.Env = []string{"PATH=" + path}
	return cmd.Run() == nil
}

// rechecksum republishes checksums.txt over whatever the tarball now holds, so
// a test about what happens AFTER the checksum passes is not accidentally a
// second test of the checksum.
func rechecksum(t *testing.T, s *session) {
	t.Helper()
	tarball := name() + ".tar.gz"
	blob, err := os.ReadFile(filepath.Join(s.fixtures, tarball))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(blob)
	line := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), tarball)
	if err := os.WriteFile(filepath.Join(s.fixtures, "checksums.txt"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repack unpacks the fixture release, lets a test remove something from it, and
// publishes the result with a matching checksum. That combination is the point:
// a release assembled wrong is correctly hashed, so the hash cannot catch it
// and something after the hash has to.
func repack(t *testing.T, s *session, mutate func(stage string)) {
	t.Helper()
	work := t.TempDir()
	if out, err := exec.Command("tar", "-C", work, "-xzf",
		filepath.Join(s.fixtures, name()+".tar.gz")).CombinedOutput(); err != nil {
		t.Fatalf("unpacking the fixture: %v: %s", err, out)
	}
	mutate(filepath.Join(work, name()))
	if out, err := exec.Command("tar", "-C", work, "-czf",
		filepath.Join(s.fixtures, name()+".tar.gz"), name()).CombinedOutput(); err != nil {
		t.Fatalf("repacking the fixture: %v: %s", err, out)
	}
	rechecksum(t, s)
}
