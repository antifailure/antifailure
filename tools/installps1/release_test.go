package installps1

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// THE REAL RELEASE, installed and run.
//
// Every other test in this package installs a fixture, which proves the
// installer and nothing about the artifact. This one installs the zips that
// tools/release/build.sh produced for this commit, the same script release.yml
// calls, and then runs the af.exe it placed: `af version` has to report the
// version the archive was built as, and `af doctor` has to run to a verdict.
//
// It needs a built release, so it runs where the Windows install workflow
// builds one and hands it over in AF_INSTALLPS1_DIST. Anywhere else it skips,
// unless AF_INSTALLPS1_REQUIRED says this is the run that must not.
func TestTheBuiltReleaseInstallsAndAFRuns(t *testing.T) {
	requireWindows(t)
	dist := os.Getenv("AF_INSTALLPS1_DIST")
	tag := os.Getenv("AF_INSTALLPS1_VERSION")
	if dist == "" || tag == "" {
		if required() {
			t.Fatal("AF_INSTALLPS1_REQUIRED is set and AF_INSTALLPS1_DIST or AF_INSTALLPS1_VERSION is not, " +
				"so the built release was never installed")
		}
		t.Skip("no built release was handed over in AF_INSTALLPS1_DIST")
	}
	bare := strings.TrimPrefix(tag, "v")
	archive := filepath.Join(dist, "antifailure_"+bare+"_windows_"+osArch()+".zip")
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("the built release has no archive for this machine: %v", err)
	}

	eachHost(t, func(t *testing.T, host string) {
		s := newSession(t, host)
		s.github = newStandIn(t, dist, tag)
		s.env["AF_GITHUB"] = s.github.base()

		r := s.succeeds()
		if !strings.Contains(r.out, "Checksum verified") {
			t.Errorf("the real archive was not verified:\n%s", r.out)
		}

		out, err := exec.Command(s.installed(), "version").CombinedOutput()
		if err != nil {
			t.Fatalf("af version failed: %v: %s", err, out)
		}
		if !strings.Contains(string(out), bare) {
			t.Errorf("af version does not report %s:\n%s", bare, out)
		}
		t.Logf("af version:\n%s", out)

		// A verdict, not a pass. doctor exits non zero when this machine is
		// missing something it needs, and a CI runner is missing a Linux
		// container daemon by design, so the exit code says nothing about the
		// binary. What it must do is run to the end and report.
		doctor := exec.Command(s.installed(), "doctor")
		doctor.Env = append(os.Environ(), "NO_COLOR=1")
		out, err = doctor.CombinedOutput()
		var exited *exec.ExitError
		if err != nil && !errors.As(err, &exited) {
			t.Fatalf("af doctor could not be started: %v", err)
		}
		t.Logf("af doctor:\n%s", out)
		// The header and the last row it prints, so a doctor that started and
		// died part way reads as a failure. The Docker row comes last because
		// every check before it is cheaper.
		for _, want := range []string{"antifailure doctor", "docker daemon"} {
			if !strings.Contains(strings.ToLower(string(out)), want) {
				t.Errorf("af doctor never printed %q, so it did not run to a verdict:\n%s", want, out)
			}
		}
	})
}
