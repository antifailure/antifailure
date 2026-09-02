package main_test

// The test this binary did not have.
//
// Everything under ee/engine has unit tests and this file is the only place
// that runs the thing a customer runs. That distinction is not academic: the
// policy hook was tested to a hundred percent and this binary never constructed
// one, so every unit test passed while the shipped enterprise edition refused
// no environment at all. A test that builds the binary and reads what it says
// about itself is the only one that could have caught that.
//
// It builds rather than calling a function, because what was missing was a line
// in main and a function test would have been written against the function that
// was already there.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	buildOnce sync.Once
	binary    string
	buildErr  error
)

// enterpriseBinary builds ee/engine/cmd/af once for the whole package.
func enterpriseBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "af-ee-*")
		if err != nil {
			buildErr = err
			return
		}
		out := filepath.Join(dir, "af")
		if runtime.GOOS == "windows" {
			out += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", out, ".")
		cmd.Env = append(os.Environ(), "GOWORK=off")
		if combined, err := cmd.CombinedOutput(); err != nil {
			buildErr = err
			t.Logf("go build: %s", combined)
			return
		}
		binary = out
	})
	require.NoError(t, buildErr)
	return binary
}

// run invokes the binary and returns what it wrote to standard error.
//
// Standard error rather than standard output on purpose: every command here has
// a --output json form, and a startup banner on standard output would break all
// of them. Asserting on the stream the banner is supposed to use is also the
// assertion that it did not go to the other one.
func run(t *testing.T, env map[string]string, args ...string) string {
	t.Helper()
	cmd := exec.Command(enterpriseBinary(t), args...)
	cmd.Env = append(os.Environ(), "GOWORK=off")
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stderr strings.Builder
	cmd.Stdout = nil
	cmd.Stderr = &stderr
	_ = cmd.Run()
	return stderr.String()
}

// runStdout invokes the binary and returns what it wrote to standard output.
//
// The other helper reads standard error because it was written for the startup
// banner. What a command prints about the installation is output, not a banner,
// and asserting on the right stream is half of what these tests are for.
func runStdout(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command(enterpriseBinary(t), args...)
	cmd.Env = append(os.Environ(), "GOWORK=off")
	var stdout strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = nil
	require.NoError(t, cmd.Run())
	return stdout.String()
}

// af version and af license status must agree about which binary this is.
//
// They did not. af license status asked the context, which this binary fills in
// at startup, and said enterprise. af version printed a package variable that no
// build ever stamped and said community, in this binary, on the command an
// auditor runs to record what they are running. Both were green in every unit
// test because a unit test of the community command tree attaches nothing and
// community is the right answer there.
func TestAfVersionSaysThisIsTheEnterpriseEdition(t *testing.T) {
	t.Parallel()

	text := runStdout(t, "version")
	require.Contains(t, text, "enterprise edition",
		"the enterprise binary reported the wrong edition on the command that names it")

	var version struct {
		Edition string `json:"edition"`
	}
	require.NoError(t, json.Unmarshal([]byte(runStdout(t, "version", "-o", "json")), &version))
	require.Equal(t, "enterprise", version.Edition)

	var licence struct {
		Edition string `json:"edition"`
	}
	require.NoError(t, json.Unmarshal([]byte(runStdout(t, "license", "status", "-o", "json")), &licence))
	require.Equal(t, version.Edition, licence.Edition,
		"one binary answered two different editions to two commands")
}

func policyFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestTheBinaryRegistersTheOrganizationPolicy(t *testing.T) {
	t.Parallel()
	out := run(t, map[string]string{
		"AF_ORG_POLICY_FILE": policyFile(t, "denied_hosts: [api.stripe.com]\n"),
	}, "--help")

	require.Contains(t, out, "organization policy: egress deny list (1 hosts)",
		"the enterprise binary started without saying any policy was in force")
}

func TestWithNoPolicyFileTheBinarySaysNothingAboutOne(t *testing.T) {
	t.Parallel()
	out := run(t, map[string]string{"AF_ORG_POLICY_FILE": ""}, "--help")

	require.NotContains(t, out, "organization policy",
		"a banner on every invocation is a banner people stop reading")
}

// A policy somebody asked for and this binary could not read must stop it.
// Starting anyway means every environment is created without being checked and
// nothing in the output says so, which is the community behaviour somebody paid
// to change.
func TestAnUnreadablePolicyStopsTheBinary(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "absent.yaml")

	cmd := exec.Command(enterpriseBinary(t), "--help")
	cmd.Env = append(os.Environ(), "GOWORK=off", "AF_ORG_POLICY_FILE="+missing)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()

	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	require.Equal(t, 3, exit.ExitCode())
	require.Contains(t, stderr.String(), "AF_ORG_POLICY_FILE")
}
