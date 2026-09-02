package installsh

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Verification that cannot verify must refuse.
//
// install.sh checked the download against its published checksum on the happy
// path and passed on every unhappy one. Four separate ways, all of them
// silent or nearly so, all of them ending in an installed binary:
//
//   checksums.txt did not download   printed a warning, installed anyway
//   the archive was not named in it  printed NOTHING, installed anyway
//   no sha256 tool on the machine    printed a warning, installed anyway
//   the archive was missing a file   raw cp error from set -e, no message
//
// The first three are the same defect wearing three costumes: a step whose
// whole job is to establish trust, reporting success when it established
// nothing. That is worse than having no check, because the README tells the
// reader the download is checked and the reader believes it. The fourth is a
// different shape, a real failure delivered as somebody else's error message.
//
// Every test here asserts three things together, because any one of them alone
// can be true of a broken installer: the script exits non zero, it says which
// thing was missing, and af is not on disk afterwards. An installer that
// refuses loudly and leaves the binary behind has not refused.

// refuses runs the installer and asserts it failed closed.
func refuses(t *testing.T, s *session, want ...string) string {
	t.Helper()
	out, err := s.run()
	if err == nil {
		t.Fatalf("the installer succeeded when verification could not be done:\n%s", out)
	}
	for _, w := range want {
		contains(t, out, w)
	}
	if _, err := os.Stat(filepath.Join(s.binDir(), "af")); err == nil {
		t.Error("af was installed anyway, so the refusal was only a message")
	}
	if _, err := os.Stat(filepath.Join(s.home, ".zshrc")); err == nil {
		t.Error("a refused install still edited the profile")
	}
	return out
}

// TestNoChecksumFileRefuses covers the release that published no checksums.txt,
// or a network that swallowed it. The old script said "warning: no checksum
// file was published" and installed the binary in the next line.
func TestNoChecksumFileRefuses(t *testing.T) {
	s := newSession(t)
	if err := os.Remove(filepath.Join(s.fixtures, "checksums.txt")); err != nil {
		t.Fatal(err)
	}
	out := refuses(t, s, "checksums.txt")
	// The warning wording is what the old fail open printed. Its absence is
	// part of the fix: a reader who greps their scrollback for "warning" must
	// not find this one still there.
	absent(t, out, "warning: no checksum file")
}

// TestAnArchiveNotNamedInChecksumsRefuses is the worst of the four, because it
// printed nothing at all. checksums.txt downloaded, the grep for this platform's
// archive found no line, `expected` was empty, and the whole verification block
// was skipped in silence. A checksums.txt that names three platforms and not
// yours installs unverified and looks exactly like an install that was checked.
func TestAnArchiveNotNamedInChecksumsRefuses(t *testing.T) {
	s := newSession(t)
	other := fmt.Sprintf("%s  antifailure_9.9.9_plan9_mips.tar.gz\n", strings.Repeat("a", 64))
	if err := os.WriteFile(filepath.Join(s.fixtures, "checksums.txt"), []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	refuses(t, s, name()+".tar.gz", "checksums.txt")
}

// TestAnEmptyChecksumFileRefuses is the same hole reached by a different route:
// a zero byte or truncated checksums.txt names nothing, so no line matches.
func TestAnEmptyChecksumFileRefuses(t *testing.T) {
	s := newSession(t)
	if err := os.WriteFile(filepath.Join(s.fixtures, "checksums.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	refuses(t, s, "checksums.txt")
}

// TestNoSha256ToolRefuses covers the machine with neither shasum nor sha256sum
// nor openssl. The session PATH is already narrow, so this hides the real ones
// behind stubs that fail, which is what a machine without them behaves like.
func TestNoSha256ToolRefuses(t *testing.T) {
	s := newSession(t)
	for _, tool := range []string{"shasum", "sha256sum", "openssl"} {
		hide(t, s, tool)
	}
	out := refuses(t, s, "sha256")
	absent(t, out, "warning: no sha256 tool")
}

// A machine with only one of the three still verifies. Refusing where the work
// can actually be done would be the opposite mistake: an installer nobody can
// run is not safer than one that checks the hash with the tool that is there.
func TestAnyOneSha256ToolIsEnough(t *testing.T) {
	for _, keep := range []string{"shasum", "sha256sum", "openssl"} {
		t.Run(keep, func(t *testing.T) {
			s := newSession(t)
			for _, tool := range []string{"shasum", "sha256sum", "openssl"} {
				if tool != keep {
					hide(t, s, tool)
				}
			}
			if !onPath(t, keep) {
				t.Skipf("%s is not on this machine, so keeping it proves nothing", keep)
			}
			out := s.install()
			contains(t, out, "Checksum verified")
			if _, err := os.Stat(filepath.Join(s.binDir(), "af")); err != nil {
				t.Errorf("af was not installed with %s available: %v", keep, err)
			}
		})
	}
}

// TestACorruptArchiveThatMatchesItsChecksumIsStillRefused is the case a hash
// cannot catch: the published checksum is of the corrupt bytes. tar then fails,
// and the old script let set -e deliver tar's own error, so the reader got
// "gzip: unexpected end of file" with nothing naming Antifailure, no
// remediation, and no way to tell a bad download from a bug.
func TestACorruptArchiveThatMatchesItsChecksumIsStillRefused(t *testing.T) {
	s := newSession(t)
	tarball := filepath.Join(s.fixtures, name()+".tar.gz")
	if err := os.WriteFile(tarball, []byte("this is not a gzip stream"), 0o644); err != nil {
		t.Fatal(err)
	}
	rechecksum(t, s)
	refuses(t, s, "could not be unpacked")
}

// TestAnArchiveWithNoBinaryIsRefused covers a release assembled wrong: the
// archive unpacks, its checksum matches, and there is no af inside it. The old
// script ran `install` with stderr silenced, fell through to cp, and exited on
// cp's error about a file the reader has never heard of.
func TestAnArchiveWithNoBinaryIsRefused(t *testing.T) {
	s := newSession(t)
	repack(t, s, func(stage string) {
		if err := os.Remove(filepath.Join(stage, "af")); err != nil {
			t.Fatal(err)
		}
	})
	refuses(t, s, "no af")
}

// The runner is the second half of what the archive promises, and the reason
// the next command the installer prints can work at all. An archive without it
// installs an af whose `af runner install` has nothing to install.
func TestAnArchiveWithNoRunnerIsRefused(t *testing.T) {
	s := newSession(t)
	repack(t, s, func(stage string) {
		if err := os.RemoveAll(filepath.Join(stage, "runner")); err != nil {
			t.Fatal(err)
		}
	})
	refuses(t, s, "no runner")
}

// The lockfile is what makes two people installing one release get one tree,
// and its absence is said out loud rather than either passed over or refused.
//
// Not refused on purpose, and the reason is the one that decides where the line
// between "warn" and "die" goes in this script. install.sh ships on every push
// to main, independently of release.yml, so it runs against releases built
// before it existed, and every archive up to and including v0.1.1 shipped no
// lockfile. Requiring one would have turned a dependency pinning defect into an
// installer that refuses to install anything at all until the next tag. Refuse
// what cannot be VERIFIED; say plainly what is missing where the thing still
// works.
func TestAnArchiveWithNoRunnerLockfileInstallsAndSaysSo(t *testing.T) {
	s := newSession(t)
	repack(t, s, func(stage string) {
		if err := os.Remove(filepath.Join(stage, "runner", "package-lock.json")); err != nil {
			t.Fatal(err)
		}
	})
	out, err := s.run()
	if err != nil {
		t.Fatalf("an archive with no lockfile refused to install:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(s.binDir(), "af")); statErr != nil {
		t.Errorf("af was not installed: %v", statErr)
	}
	if !strings.Contains(out, "package-lock.json") {
		t.Errorf("nothing said the release shipped no lockfile:\n%s", out)
	}
	if !strings.Contains(out, "af runner install") {
		t.Errorf("the warning does not say what it will affect:\n%s", out)
	}
}

// And an archive that has one says nothing about it, so the warning means
// something when it appears.
func TestAnArchiveWithALockfileSaysNothingAboutOne(t *testing.T) {
	s := newSession(t)
	out := s.install()
	absent(t, out, "shipped no runner/package-lock.json")
}

// The happy path still says it verified, in words, so the reader can tell a
// checked install from an unchecked one by reading rather than by trusting.
func TestAGoodInstallSaysItVerified(t *testing.T) {
	s := newSession(t)
	out := s.install()
	contains(t, out, "Checksum verified")
}
