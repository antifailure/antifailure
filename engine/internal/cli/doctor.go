package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/state"
)

// CheckStatus is the outcome of one doctor check.
type CheckStatus string

const (
	// CheckPass means the check succeeded.
	CheckPass CheckStatus = "pass"
	// CheckFail means Antifailure cannot run until this is fixed.
	CheckFail CheckStatus = "fail"
	// CheckWarn means it will run, with a limitation the user should know.
	CheckWarn CheckStatus = "warn"
	// CheckSkip means the check does not apply here.
	CheckSkip CheckStatus = "skip"
)

// CheckResult is one check's outcome.
//
// Every result carries a remediation, including the passing ones, so that the
// catalog can generate the troubleshooting page from the same source the
// command uses. A check with no remediation is a check that tells a user their
// machine is broken and leaves them there, which is worse than not checking.
type CheckResult struct {
	Name        string      `json:"name"`
	Status      CheckStatus `json:"status"`
	Detail      string      `json:"detail"`
	Remediation string      `json:"remediation,omitempty"`
}

// DoctorReport is the JSON form of af doctor.
type DoctorReport struct {
	OK       bool          `json:"ok"`
	Platform string        `json:"platform"`
	Checks   []CheckResult `json:"checks"`
}

// Prober abstracts the system so that every check is testable without the
// system being in the state the check is about. Simulating a missing Docker
// daemon by uninstalling Docker is not a test anyone runs twice.
type Prober interface {
	// LookPath reports whether a binary is on the path, and where.
	LookPath(name string) (string, error)
	// DockerInfo returns the daemon's version and platform, or an error.
	DockerInfo(ctx context.Context) (version, osType string, err error)
	// DialTimeout attempts a TCP connection.
	DialTimeout(network, address string, timeout time.Duration) error
	// LookupHost resolves a name.
	LookupHost(host string) ([]string, error)
	// FreeDiskBytes reports free bytes on the volume holding a path.
	FreeDiskBytes(path string) (uint64, error)
	// ListenTCP reports whether a port can be bound on loopback.
	ListenTCP(port int) error
	// Getenv reads the environment.
	Getenv(key string) string
	// Stat reports whether a path exists.
	Stat(path string) (os.FileInfo, error)
}

type systemProber struct{ getenv func(string) string }

func (p systemProber) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (p systemProber) DockerInfo(ctx context.Context) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// The Docker CLI is used rather than the SDK so that the check reports the
	// same thing the user would see typing the command themselves, including
	// a context or socket misconfiguration the SDK would paper over.
	out, err := exec.CommandContext(ctx, "docker", "info",
		"--format", "{{.ServerVersion}} {{.OSType}}").Output()
	if err != nil {
		return "", "", fmt.Errorf("docker info: %w", err)
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) < 2 {
		return "", "", fmt.Errorf("docker info returned %q", strings.TrimSpace(string(out)))
	}
	return fields[0], fields[1], nil
}

func (p systemProber) DialTimeout(network, address string, timeout time.Duration) error {
	c, err := net.DialTimeout(network, address, timeout)
	if err != nil {
		return err
	}
	return c.Close()
}

func (p systemProber) LookupHost(host string) ([]string, error) { return net.LookupHost(host) }

func (p systemProber) ListenTCP(port int) error {
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return err
	}
	return l.Close()
}

func (p systemProber) Getenv(key string) string { return p.getenv(key) }

func (p systemProber) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }

func newDoctorCommand(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that this machine can run Antifailure, and say how to fix what cannot",
		Long: strings.TrimSpace(`
Every check names what to do about a failure. A diagnostic that tells you
something is wrong and stops is worse than no diagnostic, because it costs the
same attention and yields nothing.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report := RunDoctor(cmd.Context(), env, systemProber{getenv: env.Getenv})
			return renderDoctor(env, report)
		},
	}
}

func renderDoctor(env *Env, report DoctorReport) error {
	if env.Out.Format == FormatJSON {
		if err := env.Out.JSON(report); err != nil {
			return err
		}
		if !report.OK {
			return errDoctorFailed
		}
		return nil
	}

	env.Out.Section("Antifailure doctor")
	env.Out.Printf("  %s\n", env.Out.S(StyleDim, report.Platform))
	env.Out.Println("")
	for _, c := range report.Checks {
		env.Out.Status(symbolFor(c.Status), c.Name, c.Detail)
	}

	var problems []CheckResult
	for _, c := range report.Checks {
		if c.Status == CheckFail || c.Status == CheckWarn {
			problems = append(problems, c)
		}
	}
	if len(problems) > 0 {
		env.Out.Section("What to do")
		for _, c := range problems {
			env.Out.Printf("  %s\n    %s\n", env.Out.S(StyleBold, c.Name), c.Remediation)
		}
	}
	env.Out.Println("")
	if report.OK {
		env.Out.Printf("  %s\n", env.Out.S(StyleGood, "This machine can run Antifailure."))
		return nil
	}
	return errDoctorFailed
}

func symbolFor(s CheckStatus) string {
	switch s {
	case CheckPass:
		return SymbolOK
	case CheckFail:
		return SymbolFail
	case CheckWarn:
		return SymbolWarn
	default:
		return SymbolSkip
	}
}

// errDoctorFailed makes a failed doctor run a non zero exit without printing a
// second error message; the report itself already said what is wrong.
var errDoctorFailed = &silentError{}

type silentError struct{}

func (*silentError) Error() string { return "one or more checks failed" }

// RunDoctor executes every check. It is exported so that af up can run the
// subset it depends on before doing any work, rather than failing halfway
// through with a confusing message.
func RunDoctor(ctx context.Context, env *Env, p Prober) DoctorReport {
	checks := []func(context.Context, *Env, Prober) CheckResult{
		checkDocker,
		checkDockerPlatform,
		checkDiskSpace,
		checkStateDirectory,
		checkPortRange,
		checkDNS,
		checkOutbound,
		checkKernelIsolation,
		checkProxyEnvironment,
		checkGit,
	}
	report := DoctorReport{
		OK:       true,
		Platform: fmt.Sprintf("%s/%s, Go %s", runtime.GOOS, runtime.GOARCH, runtime.Version()),
	}
	for _, fn := range checks {
		r := fn(ctx, env, p)
		if r.Status == CheckFail {
			report.OK = false
		}
		report.Checks = append(report.Checks, r)
	}
	return report
}

func checkDocker(ctx context.Context, _ *Env, p Prober) CheckResult {
	r := CheckResult{Name: "Docker daemon"}
	if _, err := p.LookPath("docker"); err != nil {
		r.Status = CheckFail
		r.Detail = "the docker command is not on the path"
		r.Remediation = dockerInstallHint()
		return r
	}
	version, osType, err := p.DockerInfo(ctx)
	if err != nil {
		r.Status = CheckFail
		r.Detail = "the daemon did not respond"
		r.Remediation = dockerStartHint()
		return r
	}
	r.Status = CheckPass
	r.Detail = fmt.Sprintf("version %s, %s containers", version, osType)
	r.Remediation = dockerStartHint()
	return r
}

func dockerInstallHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "Install Docker Desktop from https://docker.com/products/docker-desktop, or run 'brew install --cask docker'."
	case "linux":
		return "Install Docker Engine following https://docs.docker.com/engine/install, then add yourself to the docker group and log in again."
	default:
		return "Install Docker for your platform from https://docs.docker.com/get-docker."
	}
}

func dockerStartHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "Start Docker Desktop and wait for its status to read Running, then run 'af doctor' again."
	case "linux":
		return "Run 'sudo systemctl start docker'. If 'docker info' works with sudo but not without, add yourself to the docker group with 'sudo usermod -aG docker $USER' and log in again."
	default:
		return "Start the Docker daemon, then run 'af doctor' again."
	}
}

// checkDockerPlatform reports the consequences of the host platform rather
// than pretending every host behaves the same.
func checkDockerPlatform(ctx context.Context, _ *Env, p Prober) CheckResult {
	r := CheckResult{Name: "Runtime isolation"}
	_, osType, err := p.DockerInfo(ctx)
	if err != nil {
		r.Status = CheckSkip
		r.Detail = "not checked because the daemon is unreachable"
		r.Remediation = dockerStartHint()
		return r
	}
	if osType != "linux" {
		r.Status = CheckWarn
		r.Detail = osType + " containers"
		r.Remediation = "Antifailure's network isolation needs Linux containers. Switch Docker to Linux containers."
		return r
	}
	switch runtime.GOOS {
	case "darwin", "windows":
		// Everything runs inside Docker's virtual machine, which is where the
		// network namespace lives. Isolation works; the note is that a
		// container's view of the network is the machine's, not the host's.
		r.Status = CheckPass
		r.Detail = "Linux containers inside the Docker virtual machine"
		r.Remediation = "No action needed. Environments are isolated inside the Docker virtual machine rather than on the host network."
	default:
		r.Status = CheckPass
		r.Detail = "Linux containers on the host kernel"
		r.Remediation = "No action needed."
	}
	return r
}

func checkDiskSpace(_ context.Context, env *Env, p Prober) CheckResult {
	r := CheckResult{Name: "Disk space"}
	const needed = 20 << 30 // 20 GiB: images, a golden, and a branch or two
	free, err := p.FreeDiskBytes(env.WorkDir)
	if err != nil {
		r.Status = CheckSkip
		r.Detail = "could not be determined"
		r.Remediation = "Check free space by hand; an environment needs roughly 20 GiB for images, a golden, and a branch."
		return r
	}
	r.Detail = fmt.Sprintf("%s free", humanBytes(free))
	r.Remediation = "Free space, or run 'docker system prune' to reclaim unused images and build cache."
	if free < needed {
		r.Status = CheckWarn
		return r
	}
	r.Status = CheckPass
	return r
}

func checkStateDirectory(_ context.Context, env *Env, p Prober) CheckResult {
	r := CheckResult{Name: "State directory"}
	dir := filepath.Join(env.WorkDir, state.DirName)
	r.Remediation = fmt.Sprintf("Make sure %s is writable. It holds the journal, which is what makes teardown reliable.", dir)

	info, err := p.Stat(dir)
	if err != nil {
		// Absent is normal before the first run. Writability of the parent is
		// what actually matters.
		if _, perr := p.Stat(env.WorkDir); perr != nil {
			r.Status = CheckFail
			r.Detail = "the working directory is not readable"
			return r
		}
		r.Status = CheckPass
		r.Detail = "will be created on first use"
		return r
	}
	if !info.IsDir() {
		r.Status = CheckFail
		r.Detail = dir + " exists and is not a directory"
		return r
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		r.Status = CheckWarn
		r.Detail = fmt.Sprintf("mode %04o is readable by other users", perm)
		r.Remediation = fmt.Sprintf("Run 'chmod 700 %s'. It holds the journal and local handles.", dir)
		return r
	}
	r.Status = CheckPass
	r.Detail = "present and private"
	return r
}

// portRangeStart is where the local runtime allocates published ports. It is
// high enough to avoid the ephemeral range on every platform.
const portRangeStart = 43000

func checkPortRange(_ context.Context, _ *Env, p Prober) CheckResult {
	r := CheckResult{Name: "Local ports"}
	r.Remediation = fmt.Sprintf(
		"Free some ports from %d upwards, or set AF_PORT_RANGE_START to a range that is free.", portRangeStart)
	free := 0
	for i := 0; i < 20; i++ {
		if err := p.ListenTCP(portRangeStart + i); err == nil {
			free++
		}
	}
	r.Detail = fmt.Sprintf("%d of 20 checked ports are free from %d", free, portRangeStart)
	switch {
	case free == 0:
		r.Status = CheckFail
	case free < 5:
		r.Status = CheckWarn
	default:
		r.Status = CheckPass
	}
	return r
}

func checkDNS(_ context.Context, _ *Env, p Prober) CheckResult {
	r := CheckResult{Name: "DNS resolution"}
	r.Remediation = "Check your resolver configuration. Builds need to reach package registries by name."
	if _, err := p.LookupHost("registry.npmjs.org"); err != nil {
		r.Status = CheckFail
		r.Detail = "registry.npmjs.org did not resolve"
		return r
	}
	r.Status = CheckPass
	r.Detail = "public names resolve"
	return r
}

func checkOutbound(_ context.Context, _ *Env, p Prober) CheckResult {
	r := CheckResult{Name: "Outbound access"}
	r.Remediation = "Antifailure needs outbound HTTPS to pull images and packages. If you are behind a proxy, set HTTPS_PROXY."
	if err := p.DialTimeout("tcp", "registry-1.docker.io:443", 5*time.Second); err != nil {
		r.Status = CheckWarn
		r.Detail = "the container registry was not reachable"
		return r
	}
	r.Status = CheckPass
	r.Detail = "the container registry is reachable"
	return r
}

// checkKernelIsolation reports whether the host can enforce the environment's
// default deny egress directly, which only matters on Linux. Everywhere else
// the enforcement happens inside Docker's virtual machine.
func checkKernelIsolation(_ context.Context, _ *Env, p Prober) CheckResult {
	r := CheckResult{Name: "Packet filtering"}
	if runtime.GOOS != "linux" {
		r.Status = CheckSkip
		r.Detail = "handled inside the Docker virtual machine on this platform"
		r.Remediation = "No action needed. Rules are applied inside the environment's own network namespace."
		return r
	}
	if _, err := p.LookPath("nft"); err == nil {
		r.Status = CheckPass
		r.Detail = "nftables is available"
		r.Remediation = "No action needed."
		return r
	}
	if _, err := p.LookPath("iptables"); err == nil {
		r.Status = CheckPass
		r.Detail = "iptables is available as a fallback"
		r.Remediation = "No action needed. Installing nftables would be slightly faster."
		return r
	}
	r.Status = CheckWarn
	r.Detail = "neither nftables nor iptables was found on the path"
	r.Remediation = "Install nftables. The sidecar applies rules inside the environment's namespace, so the host tools are only needed for the fallback path."
	return r
}

func checkProxyEnvironment(_ context.Context, _ *Env, p Prober) CheckResult {
	r := CheckResult{Name: "Corporate proxy"}
	var set []string
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy"} {
		if p.Getenv(k) != "" {
			set = append(set, k)
		}
	}
	sort.Strings(set)
	if len(set) == 0 {
		r.Status = CheckSkip
		r.Detail = "no proxy variables are set"
		r.Remediation = "No action needed."
		return r
	}
	r.Status = CheckPass
	r.Detail = strings.Join(set, ", ") + " are set and will be honoured for builds"
	r.Remediation = "No action needed. The proxy is used for builds and image pulls. It is never used from inside an environment, whose only egress path is the sidecar."
	return r
}

func checkGit(_ context.Context, env *Env, p Prober) CheckResult {
	r := CheckResult{Name: "Git repository"}
	r.Remediation = "Run Antifailure from inside a Git repository. The branch name is what an environment is keyed on."
	if _, err := p.LookPath("git"); err != nil {
		r.Status = CheckWarn
		r.Detail = "the git command is not on the path"
		return r
	}
	dir := env.WorkDir
	for {
		if _, err := p.Stat(filepath.Join(dir, ".git")); err == nil {
			r.Status = CheckPass
			r.Detail = "found at " + dir
			return r
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	r.Status = CheckWarn
	r.Detail = "no repository was found above the working directory"
	return r
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}
