// Package installps1 tests install.ps1 by running it, under both Windows
// PowerShell 5.1 and PowerShell 7, against a stand in for github.com.
//
// It is the Windows twin of tools/installsh and holds the installer to the same
// promises: the newest release is found without the rate limited API, nothing
// unverified is installed, a refusal says what the server actually answered,
// and the install ends with af reachable rather than with homework.
//
// Every test here needs Windows, and skips anywhere else, because install.ps1
// refuses to run anywhere else. The Windows install workflow sets
// AF_INSTALLPS1_REQUIRED, which turns that skip into a failure, so the one place
// these can run cannot pass having run none of them.
package installps1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The positive control, first. Without an install this accepts, every refusal
// below would pass against a script that refused everything.
func TestTheNewestReleaseInstallsAndRuns(t *testing.T) {
	eachHost(t, func(t *testing.T, host string) {
		s := newSession(t, host)
		r := s.succeeds()

		for _, want := range []string{"Checksum verified", "Installed " + version} {
			if !strings.Contains(r.out, want) {
				t.Errorf("the install did not say %q:\n%s", want, r.out)
			}
		}
		// Asked the redirect, not the API.
		asked := strings.Join(s.github.paths(), "\n")
		if !strings.Contains(asked, "/"+repo+"/releases/latest") {
			t.Errorf("the newest release was not resolved through releases/latest:\n%s", asked)
		}
		if !strings.Contains(asked, archiveName(osArch())+".zip") {
			t.Errorf("the archive for this machine, %s, was not the one downloaded:\n%s", archiveName(osArch()), asked)
		}

		// What was placed is what was published, and it runs.
		out, err := exec.Command(s.installed(), "version").CombinedOutput()
		if err != nil {
			t.Fatalf("the installed af.exe does not run: %v: %s", err, out)
		}
		if !strings.Contains(string(out), "9.9.9 (fixture)") {
			t.Errorf("the installed af.exe is not the published one: %s", out)
		}
		// The runner lands where af runner install looks for a source.
		for _, rel := range []string{`share\antifailure\runner\src\main.ts`, `share\antifailure\runner\package.json`} {
			if _, err := os.Stat(filepath.Join(s.prefix, rel)); err != nil {
				t.Errorf("%s was not installed: %v", rel, err)
			}
		}
	})
}

func TestARequestedVersionIsInstalledWithoutAskingWhichIsNewest(t *testing.T) {
	eachHost(t, func(t *testing.T, host string) {
		s := newSession(t, host)
		s.env["AF_VERSION"] = version
		// The newest release lookup is made to fail, so a pass here proves
		// the lookup was not made rather than that it happened to work.
		s.github.set(func(g *githubStandIn) { g.status = 403 })
		r := s.succeeds()
		if !strings.Contains(r.out, "Installed "+version) {
			t.Errorf("the requested version was not installed:\n%s", r.out)
		}
	})
}

// THE PROMISE THAT MATTERS MOST. A download that does not match its published
// checksum is never installed.
func TestADownloadThatDoesNotMatchItsChecksumIsRefused(t *testing.T) {
	eachHost(t, func(t *testing.T, host string) {
		s := newSession(t, host)
		sums := filepath.Join(s.github.fixtures, "checksums.txt")
		blob, err := os.ReadFile(sums)
		if err != nil {
			t.Fatal(err)
		}
		// Replace this machine's line with a hash of something else.
		var rewritten []string
		for _, line := range strings.Split(strings.TrimSpace(string(blob)), "\n") {
			if strings.HasSuffix(line, archiveName(osArch())+".zip") {
				other := sha256.Sum256([]byte("not the archive"))
				line = hex.EncodeToString(other[:]) + "  " + archiveName(osArch()) + ".zip"
			}
			rewritten = append(rewritten, line)
		}
		if err := os.WriteFile(sums, []byte(strings.Join(rewritten, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		s.refuses("does not match its published checksum")
	})
}

// A checksums.txt that did not arrive is the case somebody tampering with the
// download arranges, so it refuses rather than warns.
func TestAMissingChecksumsFileIsRefused(t *testing.T) {
	eachHost(t, func(t *testing.T, host string) {
		s := newSession(t, host)
		s.github.set(func(g *githubStandIn) { g.missing["checksums.txt"] = true })
		s.refuses("checksums.txt", "refuses to install", "answered 404")
	})
}

func TestAChecksumsFileWithNoLineForThisArchiveIsRefused(t *testing.T) {
	eachHost(t, func(t *testing.T, host string) {
		s := newSession(t, host)
		line := strings.Repeat("a", 64) + "  antifailure_9.9.9_linux_amd64.tar.gz\n"
		if err := os.WriteFile(filepath.Join(s.github.fixtures, "checksums.txt"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
		s.refuses("names no " + archiveName(osArch()) + ".zip")
	})
}

// The rate limit is reported as a rate limit, not as "there is no release".
func TestARateLimitedLookupSaysSo(t *testing.T) {
	eachHost(t, func(t *testing.T, host string) {
		s := newSession(t, host)
		s.github.set(func(g *githubStandIn) { g.status = 403 })
		s.refuses("answered 403", "asked for too much", "AF_VERSION")
	})
}

func TestNothingAnsweringIsNotReportedAsNoRelease(t *testing.T) {
	eachHost(t, func(t *testing.T, host string) {
		s := newSession(t, host)
		s.env["AF_GITHUB"] = deadAddress(t)
		r := s.refuses("nothing answered")
		if strings.Contains(r.out, "published no release") {
			t.Errorf("an unreachable github.com was reported as a repository with no release:\n%s", r.out)
		}
	})
}

func TestARedirectSomewhereElseIsBlamedOnTheNetwork(t *testing.T) {
	eachHost(t, func(t *testing.T, host string) {
		s := newSession(t, host)
		s.github.set(func(g *githubStandIn) { g.elsewhere = "https://portal.example.invalid/login" })
		s.refuses("proxy or a sign-in portal")
	})
}

func TestARepositoryWithNoReleaseSaysSo(t *testing.T) {
	eachHost(t, func(t *testing.T, host string) {
		s := newSession(t, host)
		s.github.set(func(g *githubStandIn) { g.tag = "" })
		s.refuses("has published no release")
	})
}

// A redirect is somebody else's bytes, and one that is not a tag is refused
// rather than composed into a URL.
func TestARedirectToSomethingThatIsNotATagIsRefused(t *testing.T) {
	eachHost(t, func(t *testing.T, host string) {
		s := newSession(t, host)
		s.github.set(func(g *githubStandIn) { g.tag = "v1%3Fx=1" })
		s.refuses("not a release tag")
	})
}

// The two causes of a 404 on the archive send the reader in opposite
// directions, and the message has to say which one this is.
func TestAReleaseWithNoWindowsBuildIsToldApartFromNoSuchRelease(t *testing.T) {
	eachHost(t, func(t *testing.T, host string) {
		s := newSession(t, host)
		s.github.set(func(g *githubStandIn) { g.missing[archiveName(osArch())+".zip"] = true })
		s.refuses("does not include the build for windows " + osArch())

		typo := newSession(t, host)
		typo.env["AF_VERSION"] = "v9.9"
		typo.refuses("there is no release v9.9")
	})
}

// A hash proves the bytes arrived intact and says nothing about the release
// having been assembled with every file in it.
func TestAnIncompleteReleaseIsRefusedEvenWithAMatchingChecksum(t *testing.T) {
	eachHost(t, func(t *testing.T, host string) {
		s := newSession(t, host)
		var entries []entry
		for _, e := range defaultEntries(t) {
			if e.rel != "runner/package.json" {
				entries = append(entries, e)
			}
		}
		publish(t, s.github.fixtures, entries)
		s.refuses("no runner/package.json in it", "incomplete")
	})
}

// Windows will not overwrite a running .exe. Somebody reinstalling while an
// editor holds af open as its MCP server is the ordinary case, not an edge.
func TestReinstallingOverARunningAFSucceeds(t *testing.T) {
	eachHost(t, func(t *testing.T, host string) {
		s := newSession(t, host)
		s.succeeds()

		held := exec.Command(s.installed(), "hold")
		var heldOut bytes.Buffer
		held.Stdout = &heldOut
		if err := held.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = held.Process.Kill(); _ = held.Wait() })
		deadline := time.Now().Add(20 * time.Second)
		for !strings.Contains(heldOut.String(), "holding") {
			if time.Now().After(deadline) {
				t.Fatal("the installed af.exe never started holding its image open")
			}
			time.Sleep(50 * time.Millisecond)
		}

		// Prove the precondition: the running image really cannot be
		// overwritten, so this test is about the case it names.
		if err := os.WriteFile(s.installed(), []byte("x"), 0o755); err == nil {
			t.Fatal("the running af.exe could be overwritten, so this machine does not reproduce the case")
		}

		s.succeeds()
		out, err := exec.Command(s.installed(), "version").CombinedOutput()
		if err != nil || !strings.Contains(string(out), "9.9.9 (fixture)") {
			t.Fatalf("after reinstalling, af.exe does not run: %v: %s", err, out)
		}
		if _, err := os.Stat(s.installed() + ".old"); err != nil {
			t.Errorf("the running af.exe was not moved aside to af.exe.old: %v", err)
		}
	})
}

// In a GitHub Actions job the next step is a new process, so PATH is handed on
// through GITHUB_PATH, once, with no byte order mark in front of it.
func TestInCIThePathIsHandedToTheNextStepOnce(t *testing.T) {
	eachHost(t, func(t *testing.T, host string) {
		s := newSession(t, host)
		ghPath := filepath.Join(t.TempDir(), "github_path")
		if err := os.WriteFile(ghPath, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		s.env["GITHUB_PATH"] = ghPath
		delete(s.env, "AF_NO_MODIFY_PATH")

		s.succeeds()
		s.succeeds()

		blob, err := os.ReadFile(ghPath)
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(s.prefix, "bin")
		if bytes.HasPrefix(blob, []byte{0xEF, 0xBB, 0xBF}) {
			t.Errorf("GITHUB_PATH starts with a byte order mark, so the runner reads its first entry wrong")
		}
		lines := strings.Fields(strings.TrimSpace(string(blob)))
		if len(lines) != 1 || lines[0] != want {
			t.Errorf("GITHUB_PATH holds %q after two installs, want exactly [%s]", lines, want)
		}
	})
}

// A refusal stops the script where it is refused, and stops the process that
// ran it, which is what makes a CI job that installs with
// `powershell -Command "irm ... | iex"` fail rather than go on to run an af
// that is not there.
//
// A non zero exit alone does not prove that. A refusal that only printed and
// returned was measured to exit 1 anyway, by crashing three steps later on the
// checksums file it never downloaded, after printing three refusals for one
// cause. So this asserts the shape of a real stop: one refusal, then the
// installer's own closing sentence, then nothing.
func TestARefusalStopsTheScriptAndTheProcess(t *testing.T) {
	eachHost(t, func(t *testing.T, host string) {
		s := newSession(t, host)
		s.github.set(func(g *githubStandIn) { g.status = 500 })
		r := s.refuses("answered 500", "Antifailure was not installed")
		if n := strings.Count(r.out, "antifailure: "); n != 1 {
			t.Errorf("one cause produced %d refusals, so the first one did not stop the script:\n%s", n, r.out)
		}
		for _, later := range []string{"Downloading", "checksums.txt"} {
			if strings.Contains(r.out, later) {
				t.Errorf("the script went on to %q after refusing:\n%s", later, r.out)
			}
		}
	})
}

// The user PATH, written for real, in the registry. The test puts the prefix
// under the real profile, because the entry is written as %USERPROFILE%\...
// and that has to expand to where af actually is, and it restores the value it
// found whatever happens.
func TestTheUserPathIsWrittenOnceAndExpandsToAF(t *testing.T) {
	eachHost(t, func(t *testing.T, host string) {
		profile := os.Getenv("USERPROFILE")
		if profile == "" {
			t.Fatal("USERPROFILE is not set")
		}
		before := userPath(t, host)
		t.Cleanup(func() { setUserPath(t, host, before) })

		s := newSession(t, host)
		s.prefix = filepath.Join(profile, fmt.Sprintf("af-installps1-test-%d", time.Now().UnixNano()))
		t.Cleanup(func() { _ = os.RemoveAll(s.prefix) })
		s.env["AF_PREFIX"] = s.prefix
		delete(s.env, "AF_NO_MODIFY_PATH")

		r := s.succeeds()
		if !strings.Contains(r.out, "Added %USERPROFILE%") {
			t.Errorf("the install did not report the entry it added:\n%s", r.out)
		}
		s.succeeds()

		after := userPath(t, host)
		entry := `%USERPROFILE%` + strings.TrimPrefix(filepath.Join(s.prefix, "bin"), profile)
		if n := strings.Count(strings.ToLower(after), strings.ToLower(entry)); n != 1 {
			t.Fatalf("the user PATH holds %d copies of %s after two installs:\n%s", n, entry, after)
		}
		// Every entry that was there before is still there, unexpanded.
		if before != "" && !strings.HasPrefix(after, strings.TrimRight(before, ";")) {
			t.Errorf("the user PATH lost or expanded what it held before.\nbefore: %s\nafter:  %s", before, after)
		}

		// A new process, started the way a new terminal is, finds af.
		out, err := exec.Command(host, "-NoLogo", "-NoProfile", "-Command",
			`$env:Path = [Environment]::GetEnvironmentVariable('Path','User'); (Get-Command af).Source`).CombinedOutput()
		if err != nil || !strings.EqualFold(strings.TrimSpace(string(out)), s.installed()) {
			t.Errorf("a new process does not resolve af to the installed binary: %v: %s", err, out)
		}
	})
}

// userPath reads the raw, unexpanded user PATH from the registry.
func userPath(t *testing.T, host string) string {
	t.Helper()
	out, err := exec.Command(host, "-NoLogo", "-NoProfile", "-Command",
		`$k = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment'); `+
			`[Console]::Out.Write([string]$k.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames))`).Output()
	if err != nil {
		t.Fatalf("reading the user PATH: %v", err)
	}
	return string(out)
}

func setUserPath(t *testing.T, host, value string) {
	t.Helper()
	script := `$k = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment'); ` +
		`$v = [Console]::In.ReadToEnd(); ` +
		`if ($v) { $k.SetValue('Path', $v, [Microsoft.Win32.RegistryValueKind]::ExpandString) } else { $k.DeleteValue('Path', $false) }`
	cmd := exec.Command(host, "-NoLogo", "-NoProfile", "-Command", script)
	cmd.Stdin = strings.NewReader(value)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("restoring the user PATH failed, and it now differs from what it was: %v: %s", err, out)
	}
}
