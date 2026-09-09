// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

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
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

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

// mintLicence signs a licence the way tools/licensegen does and returns the
// token together with the environment that makes this binary trust it.
//
// It builds the wire form rather than calling a helper, for the same reason the
// licence package's own tests do: the thing being exercised is what the binary
// parses, and a convenient shape would prove the binary parses that instead.
func mintLicence(t *testing.T, features ...string) map[string]string {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	f := make([]string, 0, len(features))
	f = append(f, features...)
	claims := map[string]any{
		"id": "lic-airgap", "org": "acme", "plan": "enterprise",
		"features":   f,
		"issued_at":  time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		"expires_at": time.Now().AddDate(1, 0, 0).UTC().Format(time.RFC3339),
		"kid":        "k1",
	}
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	token := "aflic_" + base64.RawURLEncoding.EncodeToString(payload) +
		"." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, payload))

	return map[string]string{
		"AF_LICENSE_KEY":         token,
		"AF_ORG":                 "acme",
		"AF_LICENSE_PUBLIC_KEYS": "k1=" + base64.RawURLEncoding.EncodeToString(pub),
	}
}

// The refusal that is the whole safety property of the mode.
//
// An operator who sets AF_AIR_GAPPED on an installation whose licence does not
// include the feature must not get a running engine with the network open. The
// belief is what does the damage, so the binary stops instead. Tested here
// rather than only in the package, because what was missing everywhere else in
// this directory was a line in main, and a package test would have been written
// against the function that was already there.
func TestTheBinaryRefusesToStartAirGappedWithoutTheLicence(t *testing.T) {
	t.Parallel()
	cmd := exec.Command(enterpriseBinary(t), "--help")
	cmd.Env = append(os.Environ(), "GOWORK=off", "AF_AIR_GAPPED=1")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()

	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit,
		"the binary started with the network open while its configuration asked for an air gap")
	require.Equal(t, 3, exit.ExitCode())
	require.Contains(t, stderr.String(), "air_gapped")
	require.Contains(t, stderr.String(), "believing it was sealed")
}

func TestTheBinarySealsItselfWhenTheLicenceAllowsIt(t *testing.T) {
	t.Parallel()
	env := mintLicence(t, "air_gapped")
	env["AF_AIR_GAPPED"] = "1"
	env["AF_AIR_GAPPED_ALLOW"] = "registry.internal:5000"

	out := run(t, env, "--help")
	require.Contains(t, out, "air gapped: nothing outside the operator's own network is reachable",
		"the enterprise binary started without saying the air gap was in force")
	require.Contains(t, out, "registry.internal:5000",
		"an operator needs to see what their own allow list resolved to, not just that one exists")
}

func TestWithoutTheVariableTheBinarySaysNothingAboutAnAirGap(t *testing.T) {
	t.Parallel()
	out := run(t, mintLicence(t, "air_gapped"), "--help")
	require.NotContains(t, out, "air gapped",
		"a banner on every invocation is a banner people stop reading")
}

func TestAnAirGapAllowListWithATypoStopsTheBinary(t *testing.T) {
	t.Parallel()
	env := mintLicence(t, "air_gapped")
	env["AF_AIR_GAPPED"] = "1"
	env["AF_AIR_GAPPED_ALLOW"] = "https://registry.internal/v2/"

	cmd := exec.Command(enterpriseBinary(t), "--help")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()

	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit,
		"an allow list entry that can never match must not be accepted in silence")
	require.Equal(t, 3, exit.ExitCode())
}
