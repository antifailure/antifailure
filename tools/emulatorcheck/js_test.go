package emulatorcheck_test

// The JavaScript half of the same claim, and it is run a different way on
// purpose.
//
// The AWS SDK for JavaScript reads no proxy variable at all. That is not a
// gap in this suite, it is the reason the environment's real mechanism is DNS
// rather than the proxy variables: every external name resolves to the
// sidecar, which terminates TLS with an authority the environment already
// trusts, so a client that ignores every variable still arrives there. This
// test stands that up with containers, a router answering on 443, and one
// hosts entry per name, and then runs an unmodified Node application against
// it. The application names no endpoint.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/emulator"
	"github.com/antifailure/antifailure/tools/emulatorcheck"
)

const (
	jsNetwork = "af-emulatorcheck-net"
	jsRouter  = "af-emulatorcheck-route"
	jsApp     = "af-emulatorcheck-jsapp"
)

func TestJavaScriptSDK_ReachesTheEmulatorThroughDNSWithNoEndpointOverride(t *testing.T) {
	dir := t.TempDir()

	// The environment's certificate authority. One authority, minted here,
	// handed to the router that signs with it and to the application that
	// trusts it, which is what an environment does when it terminates TLS.
	sidecar, err := emulatorcheck.NewSidecar(live.container.Address)
	require.NoError(t, err)
	defer sidecar.Close()
	keyPEM, err := sidecar.CAKeyPEM()
	require.NoError(t, err)
	certPath := filepath.Join(dir, "ca.pem")
	keyPath := filepath.Join(dir, "ca.key")
	require.NoError(t, os.WriteFile(certPath, sidecar.CA(), 0o644))
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0o600))

	binary := buildRouter(t, dir)
	app := jsAppDir(t)
	installNodeModules(t, app)

	// An INTERNAL network, which is what an environment's inner network is.
	// The application has nowhere to send a packet the router does not carry,
	// and the probe proves that rather than assuming it.
	run(t, "docker", "network", "rm", jsNetwork)
	mustRun(t, "docker", "network", "create", "--internal", jsNetwork)
	t.Cleanup(func() { run(t, "docker", "network", "rm", jsNetwork) })

	// The emulator the Go suite already started, reached by name on this
	// network. One emulator rather than two, because a second copy of a half
	// gigabyte container proves nothing the first does not.
	mustRun(t, "docker", "network", "connect", jsNetwork, live.container.Name)
	t.Cleanup(func() { run(t, "docker", "network", "disconnect", jsNetwork, live.container.Name) })

	image := emulatorImage(t)
	run(t, "docker", "rm", "-f", jsRouter)
	mustRun(t, "docker", "run", "-d", "--name", jsRouter,
		"--network", jsNetwork,
		"-v", binary+":/emuroute:ro",
		"-v", certPath+":/ca.pem:ro",
		"-v", keyPath+":/ca.key:ro",
		"--entrypoint", "/emuroute",
		image,
		"-listen", ":443",
		"-emulator", live.container.Name+":"+fmt.Sprint(emulator.AWSPort),
		"-ca-cert", "/ca.pem", "-ca-key", "/ca.key")
	t.Cleanup(func() { run(t, "docker", "rm", "-f", jsRouter) })
	routerIP := waitForRouter(t)

	// The environment's resolver, in the smallest form this test can build.
	// Every name the application will reach for answers at the router, which
	// is what DNS inside an environment does for every external name.
	names := []string{
		"sts.amazonaws.com", "sts.us-east-1.amazonaws.com",
		"s3.amazonaws.com", "s3.us-east-1.amazonaws.com",
		"af-emulatorcheck-js.s3.amazonaws.com",
		"af-emulatorcheck-js.s3.us-east-1.amazonaws.com",
		"sqs.us-east-1.amazonaws.com",
		// Outside the surface, and resolved here on purpose: a name that did
		// not resolve would fail as a DNS error, which is not the same thing
		// as being refused and would not prove the refusal at all.
		"lambda.us-east-1.amazonaws.com",
	}
	args := []string{"run", "--rm", "--name", jsApp, "--network", jsNetwork,
		"-v", app + ":/jsapp:ro",
		"-v", certPath + ":/ca.pem:ro",
		"-e", "NODE_EXTRA_CA_CERTS=/ca.pem",
		"-e", "AWS_REGION=us-east-1",
		"-e", "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
		"-e", "AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
	for _, n := range names {
		args = append(args, "--add-host", n+":"+routerIP)
	}
	args = append(args, "--entrypoint", "node", image, "/jsapp/probe.mjs")

	out, err := exec.Command("docker", args...).CombinedOutput()
	t.Logf("node said:\n%s", out)
	require.NoError(t, err, "the JavaScript application failed against the emulator")
	require.NotContains(t, string(out), "EMULATORCHECK_JS_FAILED")

	var results []struct {
		Name   string `json:"name"`
		Detail string `json:"detail"`
	}
	line := findLine(t, string(out), "EMULATORCHECK_JS ")
	require.NoError(t, json.Unmarshal([]byte(line), &results))
	names = nil
	for _, r := range results {
		names = append(names, r.Name)
	}
	require.Contains(t, names, "sts.GetCallerIdentity")
	require.Contains(t, names, "s3.PutObject and GetObject")
	require.Contains(t, names, "sqs.SendMessage and ReceiveMessage")
	require.Contains(t, names, "lambda is refused")
	require.Contains(t, names, "no route out")

	// What the router saw, which is the evidence rather than the story.
	decisions := routerDecisions(t)
	require.NotEmpty(t, decisions)

	var virtualHosted, refusedLambda bool
	for _, d := range decisions {
		if d.Emulated {
			require.True(t, strings.HasSuffix(hostOnly(d.Host), "amazonaws.com"),
				"%s is not an AWS endpoint, so something told the SDK where to go", d.Host)
			require.True(t, strings.HasPrefix(d.Authorization, "AWS4-HMAC-SHA256"),
				"the Authorization header was rewritten on the way to %s", d.Host)
		}
		if strings.HasPrefix(d.Host, "af-emulatorcheck-js.s3.") && d.Emulated {
			virtualHosted = true
		}
		if strings.HasPrefix(d.Host, "lambda.") {
			require.False(t, d.Emulated, "a Lambda call reached the emulator")
			refusedLambda = true
		}
	}
	require.True(t, virtualHosted,
		"no request carried the bucket in the Host header, so virtual hosted addressing "+
			"was not exercised by the JavaScript SDK")
	require.True(t, refusedLambda, "the refusal was not exercised")
}

func buildRouter(t *testing.T, dir string) string {
	t.Helper()
	arch := strings.TrimSpace(mustRun(t, "docker", "version", "--format", "{{.Server.Arch}}"))
	binary := filepath.Join(dir, "emuroute")
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/emuroute")
	cmd.Env = append(os.Environ(),
		"CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch, "GOFLAGS=")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "building the router for linux/%s: %s", arch, out)
	return binary
}

func jsAppDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "jsapp"))
	require.NoError(t, err)
	return abs
}

// installNodeModules materialises the application's dependencies from its
// lockfile. npm ci rather than npm install, so the versions are the ones the
// lockfile pins and a run cannot silently drift onto a newer SDK.
func installNodeModules(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, "node_modules", "@aws-sdk")); err == nil {
		return
	}
	cmd := exec.Command("npm", "ci", "--no-audit", "--no-fund")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "installing the JavaScript SDK: %s", out)
}

func emulatorImage(t *testing.T) string {
	t.Helper()
	e, ok := emulator.Named(emulator.AWSName)
	require.True(t, ok)
	return e.Container().Image
}

// waitForRouter returns the router's address on the environment's network once
// it is answering, and fails with its log rather than a timeout if it is not.
func waitForRouter(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		logs, _ := exec.Command("docker", "logs", jsRouter).CombinedOutput()
		if strings.Contains(string(logs), "answering on") {
			ip, err := exec.Command("docker", "inspect", "-f",
				"{{(index .NetworkSettings.Networks \""+jsNetwork+"\").IPAddress}}",
				jsRouter).Output()
			require.NoError(t, err)
			address := strings.TrimSpace(string(ip))
			require.NotEmpty(t, address, "the router has no address on the environment's network")
			return address
		}
		time.Sleep(time.Second)
	}
	logs, _ := exec.Command("docker", "logs", jsRouter).CombinedOutput()
	t.Fatalf("the router never answered:\n%s", logs)
	return ""
}

func routerDecisions(t *testing.T) []emulatorcheck.Observation {
	t.Helper()
	logs, err := exec.Command("docker", "logs", jsRouter).CombinedOutput()
	require.NoError(t, err)
	var out []emulatorcheck.Observation
	for _, line := range strings.Split(string(logs), "\n") {
		_, body, found := strings.Cut(line, emulatorcheck.ObservationPrefix)
		if !found {
			continue
		}
		var o emulatorcheck.Observation
		require.NoError(t, json.Unmarshal([]byte(body), &o))
		out = append(out, o)
	}
	return out
}

func findLine(t *testing.T, out, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	t.Fatalf("the application printed no %q line:\n%s", prefix, out)
	return ""
}

func hostOnly(host string) string {
	if h, _, found := strings.Cut(host, ":"); found {
		return h
	}
	return host
}

func mustRun(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	require.NoError(t, err, "%s %s: %s", name, strings.Join(args, " "), out)
	return string(out)
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	_ = exec.Command(name, args...).Run()
}
