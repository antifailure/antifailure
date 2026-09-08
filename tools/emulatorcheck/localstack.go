package emulatorcheck

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/emulator"
)

// Container is a running emulator, addressed the way the sidecar addresses it.
type Container struct {
	// Name is the container's name, which is also the alias the sidecar would
	// resolve on the inner network.
	Name string
	// Address is host:port on this machine, which stands in for the address
	// the sidecar forwards to inside the environment.
	Address string
	// Started is how long the container took to answer its own health check,
	// measured from before the create.
	Started time.Duration
	// PeakMemory is the container's memory usage once it is answering, in
	// bytes, as the daemon reports it.
	PeakMemory int64
}

// StartAWS starts the emulator the AWS surface is declared with.
//
// The image and every environment variable come from the declaration in
// engine/pkg/emulator rather than from a literal here, so this drives the
// container an environment would actually get. A test that started a
// differently configured LocalStack would prove something about a container
// nobody ships.
func StartAWS(port int) (*Container, error) {
	e, ok := emulator.Named(emulator.AWSName)
	if !ok {
		return nil, fmt.Errorf("no aws emulator is built in")
	}
	spec := e.Container()

	name := "af-emulatorcheck-aws"
	_ = exec.Command("docker", "rm", "-f", name).Run()

	args := []string{
		"run", "-d", "--name", name,
		"-p", fmt.Sprintf("127.0.0.1:%d:%d", port, spec.Port),
	}
	// Sorted, so that the command this runs is the same command twice and a
	// failure can be reproduced from the log by hand.
	for _, k := range sortedKeys(spec.Env) {
		args = append(args, "-e", k+"="+spec.Env[k])
	}
	args = append(args, spec.Image)

	start := time.Now()
	if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("starting %s: %v: %s", spec.Image, err, out)
	}

	c := &Container{Name: name, Address: fmt.Sprintf("127.0.0.1:%d", port)}
	if err := waitReady(name, spec.Port); err != nil {
		logs, _ := exec.Command("docker", "logs", "--tail", "40", name).CombinedOutput()
		return nil, fmt.Errorf("%w\ncontainer log:\n%s", err, logs)
	}
	c.Started = time.Since(start)
	c.PeakMemory = memoryOf(name)
	return c, nil
}

// Stop removes the container. Removing one that is already gone succeeds,
// because a cleanup that fails on the work it already did turns one failure
// into two.
func (c *Container) Stop() {
	if c == nil {
		return
	}
	_ = exec.Command("docker", "rm", "-f", c.Name).Run()
}

// Health returns the emulator's own view of which services it loaded.
//
// It is asked from INSIDE the container rather than through the published
// port, because it is the emulator's answer about itself and not a test of the
// routing. The routing is what the SDK tests exercise.
func (c *Container) Health() (map[string]string, error) {
	out, err := exec.Command("docker", "exec", c.Name,
		"/opt/code/localstack/.venv/bin/python", "-c", healthProbe).Output()
	if err != nil {
		return nil, fmt.Errorf("asking the emulator what it loaded: %w", err)
	}
	var body struct {
		Services map[string]string `json:"services"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		return nil, fmt.Errorf("the emulator's health answer did not parse: %w", err)
	}
	return body.Services, nil
}

const healthProbe = `
import urllib.request, sys
sys.stdout.write(urllib.request.urlopen(
    "http://127.0.0.1:4566/_localstack/health", timeout=120).read().decode())
`

// ReadyTimeout is how long the emulator is given to answer.
//
// Generous on purpose. This machine has run this suite at a load average of
// 65 with a hundred other containers on it, where the same container that
// answers in well under a minute idle took a quarter of an hour. A timeout
// tuned to an idle machine turns somebody else's build into this lane's
// failure.
var ReadyTimeout = 20 * time.Minute

func waitReady(name string, port int) error {
	deadline := time.Now().Add(ReadyTimeout)
	for time.Now().Before(deadline) {
		out, err := exec.Command("docker", "logs", name).CombinedOutput()
		if err == nil && strings.Contains(string(out), "Ready.") {
			return nil
		}
		state, _ := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", name).Output()
		if strings.TrimSpace(string(state)) == "false" {
			return fmt.Errorf("the emulator container exited before it was ready")
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("the emulator did not become ready within %s", ReadyTimeout)
}

// memoryOf reports the container's memory usage in bytes, or zero when the
// daemon cannot say. Zero is reported as zero rather than guessed at: a
// measurement this lane publishes has to come from somewhere.
func memoryOf(name string) int64 {
	out, err := exec.Command("docker", "stats", "--no-stream",
		"--format", "{{.MemUsage}}", name).Output()
	if err != nil {
		return 0
	}
	// "559.6MiB / 7.654GiB"
	field, _, ok := strings.Cut(strings.TrimSpace(string(out)), " /")
	if !ok {
		return 0
	}
	return parseSize(field)
}

func parseSize(s string) int64 {
	units := []struct {
		suffix string
		scale  float64
	}{
		{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"B", 1},
	}
	for _, u := range units {
		if !strings.HasSuffix(s, u.suffix) {
			continue
		}
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, u.suffix), 64)
		if err != nil {
			return 0
		}
		return int64(n * u.scale)
	}
	return 0
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// DockerAvailable reports whether there is a daemon to talk to.
//
// AF_REQUIRE_DOCKER turns an absent daemon into a failure rather than a skip,
// the way the engine's suites already do, because a suite that reports ok
// having skipped itself is the defect this repository keeps finding in its own
// instruments.
func DockerAvailable() (bool, error) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		if os.Getenv("AF_REQUIRE_DOCKER") != "" {
			return false, fmt.Errorf("AF_REQUIRE_DOCKER is set and the daemon is unreachable: %w", err)
		}
		return false, nil
	}
	return true, nil
}
