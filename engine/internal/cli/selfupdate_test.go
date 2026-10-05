package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/edition"
)

const updateArchiveName = "antifailure_1.1.1_linux_amd64"

// updateArchiveFor builds the release archive goos is published as: a tarball
// holding af, or for Windows a zip holding af.exe.
func updateArchiveFor(t *testing.T, goos, defect string) (string, []byte) {
	t.Helper()
	if goos != "windows" {
		return updateArchiveName, updateArchive(t, defect)
	}
	root := "antifailure_1.1.1_windows_amd64"
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	// LICENSE is in every real release and is not something the update
	// installs, so it is the entry an unpacker could skip without reading.
	for _, file := range []string{"af.exe", "LICENSE", "runner/src/main.ts", "runner/package.json", "runner/package-lock.json"} {
		if defect == "missing" && file == "runner/package-lock.json" {
			continue
		}
		name := root + "/" + file
		if defect == "traversal" && file == "af.exe" {
			name = root + "/../af.exe"
		}
		body := "new " + file
		if file == "af.exe" {
			body = "new af"
		}
		h := &zip.FileHeader{Name: name, Method: zip.Store}
		h.SetMode(0755)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if defect == "duplicate-ignored" {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: root + "/LICENSE", Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("a second LICENSE")); err != nil {
			t.Fatal(err)
		}
	}
	if defect == "link" {
		h := &zip.FileHeader{Name: root + "/runner/link", Method: zip.Store}
		h.SetMode(os.ModeSymlink | 0777)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("/tmp/outside")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	data := b.Bytes()
	if defect == "gzip-checksum" {
		// Stored, so the body is in the archive verbatim and one flipped byte
		// of it is a CRC failure rather than a decompression failure. The
		// entry corrupted is one the update ignores, because an unpacker that
		// skips what it does not install would verify nothing about it.
		i := bytes.Index(data, []byte("new LICENSE"))
		if i < 0 {
			t.Fatal("stored body not found")
		}
		data[i] ^= 1
	}
	return root, data
}

func updateArchive(t *testing.T, defect string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, file := range []string{"af", "runner/src/main.ts", "runner/package.json", "runner/package-lock.json"} {
		if defect == "missing" && file == "runner/package-lock.json" {
			continue
		}
		name := updateArchiveName + "/" + file
		kind := byte(tar.TypeReg)
		body := "new " + file
		if defect == "traversal" && file == "af" {
			name = updateArchiveName + "/../af"
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(body)), Mode: 0755, Typeflag: kind, Linkname: "/tmp/outside"}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if defect == "link" {
		if err := tw.WriteHeader(&tar.Header{Name: updateArchiveName + "/runner/link", Typeflag: tar.TypeSymlink, Linkname: "/tmp/outside"}); err != nil {
			t.Fatal(err)
		}
	}
	if defect == "duplicate-ignored" {
		for _, body := range []string{"a LICENSE", "a second LICENSE"} {
			if err := tw.WriteHeader(&tar.Header{Name: updateArchiveName + "/LICENSE", Size: int64(len(body)), Mode: 0644, Typeflag: tar.TypeReg}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if defect == "gzip-checksum" {
		b.Bytes()[b.Len()-8] ^= 1
	}
	return b.Bytes()
}

func updateInstallation(t *testing.T) (string, string) {
	t.Helper()
	return updateInstallationFor(t, "linux")
}

func updateInstallationFor(t *testing.T, goos string) (string, string) {
	t.Helper()
	prefix := t.TempDir()
	executable := filepath.Join(prefix, "bin", releaseBinary(goos))
	runner := filepath.Join(prefix, "share", "antifailure", "runner")
	for _, dir := range []string{filepath.Dir(executable), runner} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(executable, []byte("old binary"), 0711); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runner, "old-source"), []byte("old runner"), 0644); err != nil {
		t.Fatal(err)
	}
	return executable, runner
}

func TestSelfUpdateVerifiedArchive(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		for _, defect := range []string{"none", "custom", "check", "checksum", "missing-checksum", "other-platforms-only", "malformed-checksum", "download", "missing", "traversal", "link", "duplicate-ignored", "gzip-checksum"} {
			t.Run(goos+"/"+defect, func(t *testing.T) {
				testSelfUpdateVerifiedArchive(t, goos, defect)
			})
		}
	}
}

func testSelfUpdateVerifiedArchive(t *testing.T, goos, defect string) {
	executable, runner := updateInstallationFor(t, goos)
	prefix := ""
	if defect == "custom" {
		prefix = filepath.Dir(filepath.Dir(executable))
		customDir := filepath.Join(prefix, "custom-tools")
		if err := os.Mkdir(customDir, 0755); err != nil {
			t.Fatal(err)
		}
		customPath := filepath.Join(customDir, releaseBinary(goos))
		if err := os.Rename(executable, customPath); err != nil {
			t.Fatal(err)
		}
		executable = customPath
	}
	archiveName, archive := updateArchiveFor(t, goos, defect)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			_, _ = fmt.Fprint(w, `{"tag_name":"v1.1.1"}`)
		case "/v1.1.1/" + archiveName + releaseArchiveExt(goos):
			if defect == "download" {
				w.WriteHeader(503)
				return
			}
			_, _ = w.Write(archive)
		case "/v1.1.1/checksums.txt":
			if defect == "missing-checksum" {
				_, _ = fmt.Fprint(w, "")
				return
			}
			if defect == "other-platforms-only" {
				// Every platform but this one, the way a release that
				// shipped no build for it would read.
				_, _ = fmt.Fprintf(w, "%x  antifailure_1.1.1_plan9_amd64.tar.gz\n", sha256.Sum256(archive))
				return
			}
			if defect == "malformed-checksum" {
				_, _ = fmt.Fprintf(w, "not-a-sha256  %s%s\n", archiveName, releaseArchiveExt(goos))
				return
			}
			data := archive
			if defect == "checksum" {
				data = []byte("not the archive")
			}
			// Both formats are listed, the way checksums.txt lists every
			// platform, so a platform that fetched the other one's
			// archive would find a checksum and fail on the content.
			_, _ = fmt.Fprintf(w, "%x  %s%s\n", sha256.Sum256(data), archiveName, releaseArchiveExt(goos))
		default:
			// Any other name is an archive for another platform.
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer s.Close()
	result, err := performUpdate(context.Background(), executable, prefix, "v1.0.0", goos, "amd64", s.URL+"/latest", s.URL, s.Client(), defect == "check")
	switch defect {
	case "none", "custom":
		if err != nil {
			t.Fatal(err)
		}
		if !result.Applied {
			t.Fatal("successful update not reported")
		}
		binary, _ := os.ReadFile(executable)
		if string(binary) != "new af" {
			t.Fatalf("binary was not replaced: %q", binary)
		}
		source, _ := os.ReadFile(filepath.Join(runner, "src", "main.ts"))
		if string(source) != "new runner/src/main.ts" {
			t.Fatal("runner source was not replaced")
		}
		// Mode bits exist to carry over only where the platform has them.
		if goos != "windows" && runtime.GOOS != "windows" {
			info, err := os.Stat(executable)
			if err != nil || info.Mode().Perm() != 0711 {
				t.Fatal("updater changed the executable permissions")
			}
		}
		if goos == "windows" {
			// The binary that was replaced is moved aside, never lost,
			// and holds exactly the old bytes.
			moved, _ := filepath.Glob(filepath.Join(filepath.Dir(executable), replacedPrefix(executable)+"*"))
			if len(moved) != 1 {
				t.Fatalf("the replaced binary was not moved aside exactly once: %v", moved)
			}
			old, _ := os.ReadFile(moved[0])
			if string(old) != "old binary" {
				t.Fatalf("the moved aside binary is not the old one: %q", old)
			}
		}
	case "check":
		if err != nil || result.Applied {
			t.Fatalf("check wrote or failed: %+v %v", result, err)
		}
		binary, _ := os.ReadFile(executable)
		if string(binary) != "old binary" {
			t.Fatal("check replaced the binary")
		}
	default:
		if err == nil {
			t.Fatal("unsafe update was accepted")
		}
		// Each defect has to be refused by the guard written for it, in both
		// archive formats. Any error at all would pass a zip whose own guard
		// was missing if some later step happened to fail instead.
		reason := map[string]string{
			"checksum":          "archive checksum mismatch",
			"missing":           "missing required file runner/package-lock.json",
			"traversal":         "unsafe path in release archive",
			"link":              "contains a link or unsupported entry",
			"duplicate-ignored": "duplicate file in release archive",
			"gzip-checksum":     map[string]string{"linux": "gzip: invalid checksum", "windows": "zip: checksum error"}[goos],
		}[defect]
		if reason != "" && !strings.Contains(err.Error(), reason) {
			t.Fatalf("%s was refused for another reason than its own guard: %v", defect, err)
		}
		// A refused download is caught three times over: by the status
		// code, then by the checksum an empty body cannot match, then
		// by the gzip reader. Only the first of those can say WHY, and
		// an error that reports a checksum mismatch for a release the
		// server refused to send sends somebody looking in the wrong
		// place. So the status is part of the contract, not incidental.
		if defect == "download" && !strings.Contains(err.Error(), "HTTP 503") {
			t.Fatalf("a refused download did not report the refusal: %v", err)
		}
		// An archive no checksum names must be refused BEFORE it is
		// fetched, by the absence of a checksum rather than by a
		// comparison against the empty string. The two are the same
		// verdict here and they are not the same guarantee: a
		// comparison that treats an absent checksum as one more value
		// to compare is one careless "if expected != ''" away from
		// verifying nothing at all.
		if (defect == "missing-checksum" || defect == "other-platforms-only") &&
			!strings.Contains(err.Error(), "release v1.1.1 publishes no build for "+goos+"/amd64") {
			t.Fatalf("an unnamed archive was refused by something other than its missing checksum: %v", err)
		}
		// A checksum that is named and is not a checksum is a damaged
		// release, not a missing build, and says so in its own words.
		if defect == "malformed-checksum" && !strings.Contains(err.Error(), "no valid SHA256 checksum") {
			t.Fatalf("a malformed checksum was refused by something other than its own guard: %v", err)
		}
		binary, _ := os.ReadFile(executable)
		if string(binary) != "old binary" {
			t.Fatal("failed update changed the binary")
		}
		source, _ := os.ReadFile(filepath.Join(runner, "old-source"))
		if string(source) != "old runner" {
			t.Fatal("failed update changed runner source")
		}
	}
}

func TestSelfUpdateDoesNotDowngradeOrReinstall(t *testing.T) {
	for _, current := range []string{"v1.1.1", "v2.0.0"} {
		t.Run(current, func(t *testing.T) {
			executable, _ := updateInstallation(t)
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, `{"tag_name":"v1.1.1"}`)
			}))
			defer s.Close()
			_, err := performUpdate(context.Background(), executable, "", current, "linux", "amd64", s.URL, s.URL, s.Client(), false)
			if current == "v1.1.1" && err != nil {
				t.Fatalf("current release should be a no-op: %v", err)
			}
			if current == "v2.0.0" && (err == nil || !strings.Contains(err.Error(), "downgrade")) {
				t.Fatalf("newer release should refuse downgrade: %v", err)
			}
		})
	}
}

func TestSelfUpdateCannotReplaceEnterpriseWithCommunity(t *testing.T) {
	executable, _ := updateInstallation(t)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"tag_name":"v1.1.1"}`)
	}))
	defer s.Close()
	ctx := edition.With(context.Background(), edition.Status{Name: "enterprise"})
	_, err := performUpdate(ctx, executable, "", "v1.1.1", "linux", "amd64", s.URL, s.URL, s.Client(), false)
	if err == nil || !strings.Contains(err.Error(), "enterprise distribution") {
		t.Fatalf("enterprise update was not refused: %v", err)
	}
	if check := checkCLIRelease(ctx, nil, nil); check.Status != CheckSkip {
		t.Fatalf("enterprise version was compared to community release: %+v", check)
	}
}

func TestUpdateRecoveryAcrossCommitBoundaries(t *testing.T) {
	for _, phase := range []string{"after-backup", "after-source", "after-binary"} {
		t.Run(phase, func(t *testing.T) {
			executable, runner := updateInstallation(t)
			binDir := filepath.Dir(executable)
			lock, err := acquireUpdateLock(filepath.Join(binDir, ".af-update-lock"))
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			stage, err := os.MkdirTemp(binDir, ".af-update-")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stage, "af"), []byte("new binary"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(runner, filepath.Join(stage, "old-runner")); err != nil {
				t.Fatal(err)
			}
			if phase != "after-backup" {
				if err := os.Mkdir(runner, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(runner, "old-source"), []byte("new runner"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "after-binary" {
				if err := os.Rename(filepath.Join(stage, "af"), executable); err != nil {
					t.Fatal(err)
				}
			}
			body, err := json.Marshal(map[string]string{"stage": stage, "runner": runner})
			if err != nil {
				t.Fatal(err)
			}
			if err := writeUpdateJournal(lock, string(body)); err != nil {
				t.Fatal(err)
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if phase != "after-binary" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				_, _ = fmt.Fprint(w, `{"tag_name":"v1.1.1"}`)
			}))
			defer s.Close()
			_, _ = performUpdate(context.Background(), executable, "", "v1.1.1", "linux", "amd64", s.URL, s.URL, s.Client(), false)
			want := "old runner"
			if phase == "after-binary" {
				want = "new runner"
			}
			source, _ := os.ReadFile(filepath.Join(runner, "old-source"))
			if string(source) != want {
				t.Fatalf("%s recovered %q, want %q", phase, source, want)
			}
			if _, err := os.Stat(stage); !os.IsNotExist(err) {
				t.Fatal("recovery left its staging directory behind")
			}
		})
	}
}

func TestUpdateLockReleasesWhenTheHandleCloses(t *testing.T) {
	name := filepath.Join(t.TempDir(), "lock")
	first, err := acquireUpdateLock(name)
	if err != nil {
		t.Fatal(err)
	}
	second, err := acquireUpdateLock(name)
	if second != nil {
		_ = second.Close()
	}
	if err == nil {
		_ = first.Close()
		t.Fatal("two updates acquired the same lock")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := acquireUpdateLock(name)
	if err != nil {
		t.Fatalf("closed owner stranded the lock: %v", err)
	}
	_ = third.Close()
}

func TestSelfUpdateRollsBackRunnerWhenBinaryCannotBeReplaced(t *testing.T) {
	executable, runner := updateInstallation(t)
	stage := t.TempDir()
	if err := os.Mkdir(filepath.Join(stage, "runner"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "af"), []byte("new binary"), 0755); err != nil {
		t.Fatal(err)
	}
	_, err := commitUpdate(updateResult{InstalledPath: executable}, stage, runner, "linux", func(a, b string) error {
		if b == executable {
			return fmt.Errorf("injected permission failure")
		}
		return os.Rename(a, b)
	})
	if err == nil {
		t.Fatal("failed binary replacement reported success")
	}
	binary, _ := os.ReadFile(executable)
	if string(binary) != "old binary" {
		t.Fatal("old binary lost")
	}
	source, _ := os.ReadFile(filepath.Join(runner, "old-source"))
	if string(source) != "old runner" {
		t.Fatal("old runner was not restored")
	}
}

// TestSweepRemovesOnlyStagesThatNeverCommitted is the killed-update cleanup.
//
// A stage holds the downloaded archive and the unpacked release, and the
// deferred removal does not run when the process is killed, so the two states
// have to be told apart by something on disk. old-runner is that marker,
// because a stage holding one may be carrying the only copy of the installed
// runner source.
func TestSweepRemovesOnlyStagesThatNeverCommitted(t *testing.T) {
	binDir := t.TempDir()
	abandoned := filepath.Join(binDir, ".af-update-abandoned")
	committing := filepath.Join(binDir, ".af-update-committing")
	unrelated := filepath.Join(binDir, "runner")
	for _, dir := range []string{abandoned, committing, unrelated, filepath.Join(committing, "old-runner")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(abandoned, "release.tar.gz"), []byte("downloaded"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := sweepAbandonedStages(binDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Fatal("a stage that never reached the commit was left behind")
	}
	if _, err := os.Stat(committing); err != nil {
		t.Fatal("a stage holding the only copy of the old runner was removed")
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatal("the sweep removed a directory that is not a stage")
	}
}

// TestUpdateSweepsAnAbandonedStage is the wiring half. The sweep above can be
// correct and never called, which is the shape of gap that reads as a working
// feature: the function exists, its test passes, and no update ever runs it.
//
// The metadata source refuses here, so the update fails after the sweep. That
// is the point: the cleanup must not depend on the update succeeding, because
// the machine that needs it is the one whose updates keep being interrupted.
func TestUpdateSweepsAnAbandonedStage(t *testing.T) {
	executable, _ := updateInstallation(t)
	abandoned := filepath.Join(filepath.Dir(executable), ".af-update-abandoned")
	if err := os.Mkdir(abandoned, 0755); err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer s.Close()
	if _, err := performUpdate(context.Background(), executable, "", "v1.0.0", "linux", "amd64", s.URL, s.URL, s.Client(), false); err == nil {
		t.Fatal("a refused metadata source was accepted")
	}
	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Fatal("an update ran without sweeping the stage a killed update left")
	}
}

// TestReleaseClientRefusesAPlainHTTPRedirect covers the one part of the
// exchange the far end chooses.
//
// Redirects must be followed, because a GitHub download answers with one. At
// the default policy a redirect to plain HTTP is followed without a word, and
// then whoever is on the path supplies the bytes the published checksum is
// compared against.
func TestReleaseClientRefusesAPlainHTTPRedirect(t *testing.T) {
	// The redirect target is a server that really answers, so that removing the
	// guard does not merely fail to connect. Without a reachable target the
	// test goes red either way and cannot tell a refusal from a broken link,
	// which is the shape of check that passes while defending nothing.
	downgraded := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("bytes from a plain HTTP hop"))
	}))
	defer downgraded.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, downgraded.URL+"/release.tar.gz", http.StatusFound)
	}))
	defer s.Close()
	var body strings.Builder
	err := fetchUpdate(context.Background(), releaseHTTPClient(5*time.Second), s.URL, &body, 1<<20)
	if err == nil {
		t.Fatalf("a release source redirected the download to plain HTTP and it was followed: %q", body.String())
	}
	if !strings.Contains(err.Error(), "away from HTTPS") {
		t.Fatalf("the downgrade was refused for some other reason: %v", err)
	}
	if body.Len() != 0 {
		t.Fatalf("bytes from a downgraded redirect reached the caller: %q", body.String())
	}
}

// TestRecoveryClearsItsOwnStagingDirectory tests recoverUpdate directly.
//
// The same assertion made through performUpdate cannot fail, because the
// abandoned stage sweep that runs immediately afterwards removes the directory
// whether recovery cleared it or not. An assertion covered by a second
// mechanism proves nothing about the first.
func TestRecoveryClearsItsOwnStagingDirectory(t *testing.T) {
	executable, runner := updateInstallation(t)
	binDir := filepath.Dir(executable)
	lock, err := acquireUpdateLock(filepath.Join(binDir, ".af-update-lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	stage, err := os.MkdirTemp(binDir, ".af-update-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "af"), []byte("new binary"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(runner, filepath.Join(stage, "old-runner")); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"stage": stage, "runner": runner})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeUpdateJournal(lock, string(body)); err != nil {
		t.Fatal(err)
	}
	if err := recoverUpdate(lock, binDir, runner); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatal("recovery left its own staging directory behind")
	}
	if _, err := os.Stat(lock.Name() + ".json"); !os.IsNotExist(err) {
		t.Fatal("recovery left its journal behind, so the next update replays it")
	}
}

// TestReleaseClientStillBoundsARedirectChain guards what replacing the policy
// silently gives up. net/http applies its own ten hop limit only while
// CheckRedirect is nil, so a policy that answers nil for every hop follows a
// loop until the request deadline. This calls the policy the production client
// carries rather than a copy of it.
func TestReleaseClientStillBoundsARedirectChain(t *testing.T) {
	client := releaseHTTPClient(5 * time.Second)
	if client.CheckRedirect == nil {
		t.Fatal("the release client carries no redirect policy at all")
	}
	next, err := http.NewRequest(http.MethodGet, "https://example.invalid/release.tar.gz", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(next, make([]*http.Request, 9)); err != nil {
		t.Fatalf("a chain short of the limit was refused: %v", err)
	}
	if err := client.CheckRedirect(next, make([]*http.Request, 10)); err == nil {
		t.Fatal("an unbounded redirect chain was accepted")
	}
}
