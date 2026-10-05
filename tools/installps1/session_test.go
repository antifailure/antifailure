package installps1

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

const version = "v9.9.9"

// required is set by the Windows install workflow. It turns "this machine is
// not Windows" from a skip into a failure, so the one place these tests can run
// cannot report success having run none of them.
func required() bool { return os.Getenv("AF_INSTALLPS1_REQUIRED") != "" }

func requireWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	if required() {
		t.Fatalf("AF_INSTALLPS1_REQUIRED is set and this is %s, so these tests cannot run here", runtime.GOOS)
	}
	t.Skip("install.ps1 runs on Windows only; .github/workflows/windows.yml runs these on Windows runners")
}

// hosts are the two PowerShells a person can paste the install line into.
// Windows PowerShell 5.1 is the one every Windows machine has, and it is the
// one the script's restrictions exist for, so a run that only ever exercised 7
// would be testing the easy half.
func hosts(t *testing.T) []string {
	t.Helper()
	var found []string
	for _, h := range []string{"powershell.exe", "pwsh.exe"} {
		if p, err := exec.LookPath(h); err == nil {
			found = append(found, p)
		} else if required() {
			t.Fatalf("%s is not on PATH, so the installer is not being tested under it: %v", h, err)
		}
	}
	if len(found) == 0 {
		t.Fatal("neither powershell.exe nor pwsh.exe was found")
	}
	return found
}

func eachHost(t *testing.T, test func(t *testing.T, host string)) {
	t.Helper()
	requireWindows(t)
	for _, h := range hosts(t) {
		h := h
		t.Run(strings.TrimSuffix(filepath.Base(h), ".exe"), func(t *testing.T) { test(t, h) })
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(wd))
	// Read, so the script is part of go test's cache key. It is run by
	// PowerShell, so nothing in this package would otherwise open it, and an
	// edited install.ps1 would be reported ok from cache.
	if _, err := os.ReadFile(filepath.Join(root, "install.ps1")); err != nil {
		t.Fatalf("install.ps1 not found from %s: %v", wd, err)
	}
	return root
}

// osArch is the architecture install.ps1 will ask for. The script reads the
// MACHINE's architecture, and go test may be an emulated build on an Arm
// machine, so the fixture publishes both and this only decides which one a
// test expects to see installed.
func osArch() string {
	if a := os.Getenv("PROCESSOR_ARCHITEW6432"); strings.EqualFold(a, "ARM64") {
		return "arm64"
	}
	if strings.EqualFold(os.Getenv("PROCESSOR_ARCHITECTURE"), "ARM64") {
		return "arm64"
	}
	return "amd64"
}

func archiveName(arch string) string { return fmt.Sprintf("antifailure_9.9.9_windows_%s", arch) }

var (
	fakeOnce sync.Once
	fakeExe  []byte
	fakeErr  error
)

// fakeAF is a real executable for this machine, built once per test binary.
func fakeAF(t *testing.T) []byte {
	t.Helper()
	fakeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fakeaf")
		if err != nil {
			fakeErr = err
			return
		}
		out := filepath.Join(dir, "af.exe")
		cmd := exec.Command("go", "build", "-o", out, "./testdata/fakeaf")
		if b, err := cmd.CombinedOutput(); err != nil {
			fakeErr = fmt.Errorf("building the fixture af.exe: %w: %s", err, b)
			return
		}
		fakeExe, fakeErr = os.ReadFile(out)
	})
	if fakeErr != nil {
		t.Fatal(fakeErr)
	}
	return fakeExe
}

// entry is one file in a fixture archive, relative to its top directory.
type entry struct {
	rel  string
	body []byte
}

func defaultEntries(t *testing.T) []entry {
	return []entry{
		{"af.exe", fakeAF(t)},
		{"LICENSE", []byte("MIT\n")},
		{"runner/package.json", []byte("{}\n")},
		{"runner/package-lock.json", []byte("{\"lockfileVersion\": 3}\n")},
		{"runner/src/main.ts", []byte("// fixture\n")},
	}
}

// publish writes both Windows archives and a checksums.txt that matches them.
// The checksum is real, because refusing a download that does not match is the
// behaviour most worth keeping, and a fixture that failed it would look exactly
// like a break.
func publish(t *testing.T, dir string, entries []entry) {
	t.Helper()
	var sums strings.Builder
	for _, arch := range []string{"amd64", "arm64"} {
		name := archiveName(arch)
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for _, e := range entries {
			w, err := zw.Create(name + "/" + e.rel)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write(e.body); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".zip"), buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(buf.Bytes())
		fmt.Fprintf(&sums, "%s  %s.zip\n", hex.EncodeToString(sum[:]), name)
	}
	// A line for a platform this machine is not, so a parser that took the
	// first line rather than the matching one would verify the wrong file.
	fmt.Fprintf(&sums, "%s  antifailure_9.9.9_linux_amd64.tar.gz\n", strings.Repeat("a", 64))
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(sums.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// session is one install: a stand in for github.com serving a fixture release,
// and a prefix of its own.
type session struct {
	t      *testing.T
	host   string
	script string
	github *githubStandIn
	prefix string
	env    map[string]string
}

func newSession(t *testing.T, host string) *session {
	t.Helper()
	fixtures := t.TempDir()
	publish(t, fixtures, defaultEntries(t))
	s := &session{
		t:      t,
		host:   host,
		script: filepath.Join(repoRoot(t), "install.ps1"),
		github: newStandIn(t, fixtures, version),
		prefix: filepath.Join(t.TempDir(), "antifailure"),
		env:    map[string]string{},
	}
	s.env["AF_GITHUB"] = s.github.base()
	s.env["AF_PREFIX"] = s.prefix
	// Every test that is not about PATH declines it, so a run of this package
	// on a developer's machine does not edit their user PATH. The tests that
	// are about PATH unset this and restore what they changed.
	s.env["AF_NO_MODIFY_PATH"] = "1"
	return s
}

type result struct {
	out  string
	code int
}

// run installs through the line a person pastes: the script's text piped into
// Invoke-Expression, in the caller's session, which is the shape `irm | iex`
// produces and the shape whose failure modes the script is written around.
func (s *session) run() result {
	s.t.Helper()
	command := fmt.Sprintf("Get-Content -Raw -LiteralPath '%s' | Invoke-Expression", s.script)
	cmd := exec.Command(s.host, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command)
	cmd.Env = s.environ()
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			s.t.Fatalf("running %s: %v", s.host, err)
		}
		code = ee.ExitCode()
	}
	return result{out: string(out), code: code}
}

// environ is this process's environment with every setting the installer
// reads removed, then the session's own. Windows variable names are case
// insensitive, so the comparison is too: a GITHUB_PATH inherited from the
// runner as Github_Path would otherwise steer every test into the CI branch.
func (s *session) environ() []string {
	var env []string
	for _, kv := range os.Environ() {
		k := kv
		if i := strings.Index(kv, "="); i > 0 {
			k = kv[:i]
		}
		if strings.EqualFold(k, "GITHUB_PATH") || strings.HasPrefix(strings.ToUpper(k), "AF_") {
			continue
		}
		if _, ours := s.env[k]; ours {
			continue
		}
		env = append(env, kv)
	}
	for k, v := range s.env {
		if v == "" {
			continue
		}
		env = append(env, k+"="+v)
	}
	return env
}

func (s *session) installed() string { return filepath.Join(s.prefix, "bin", "af.exe") }

func (s *session) succeeds() result {
	s.t.Helper()
	r := s.run()
	if r.code != 0 {
		s.t.Fatalf("the install failed with exit %d:\n%s", r.code, r.out)
	}
	return r
}

// refuses runs an install that must fail, and checks the three things a
// refusal promises: a non zero exit, a sentence saying why, and nothing
// installed.
func (s *session) refuses(want ...string) result {
	s.t.Helper()
	r := s.run()
	if r.code == 0 {
		s.t.Fatalf("the install exited 0 when it had to refuse:\n%s", r.out)
	}
	for _, w := range want {
		if !strings.Contains(r.out, w) {
			s.t.Errorf("the refusal does not say %q:\n%s", w, r.out)
		}
	}
	if _, err := os.Stat(s.installed()); err == nil {
		s.t.Errorf("a refused install left %s behind", s.installed())
	}
	return r
}
