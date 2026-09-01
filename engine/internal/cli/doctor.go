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

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/model"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/internal/state"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
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
			// The remediation is the reason the section exists, and it was the
			// one thing on the page that ran off the right of the terminal: a
			// hundred and one characters against an eighty column screen, hard
			// wrapped mid word by the terminal at the moment somebody's machine
			// is already not working.
			env.Out.Printf("  %s\n    %s\n",
				env.Out.S(StyleBold, c.Name), env.Out.Wrap(c.Remediation, 4))
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

// silentError exits non zero without printing anything more.
//
// It exists for commands whose own output already said what is wrong. Printing
// a second message would either duplicate the report or, in JSON mode, emit a
// second document into a stream a script is parsing.
type silentError struct {
	// code is the exit code to use. Zero means a plain failure.
	code aferrors.ExitCode
}

func (*silentError) Error() string { return "one or more checks failed" }

// ExitCode lets a silent failure still say which kind it was, so a script can
// tell a verification failure from a configuration one.
func (e *silentError) ExitCode() aferrors.ExitCode {
	if e.code == 0 {
		return aferrors.ExitFailure
	}
	return e.code
}

// silent wraps a coded error so its exit code survives but its message is not
// printed again.
func silent(err error) error {
	var coded *aferrors.Error
	if aferrors.As(err, &coded) {
		return &silentError{code: coded.ExitCode()}
	}
	return &silentError{}
}

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
		checkModelKey,
		checkLeftoverEnvironments,
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

// pruneCutoff is the age af env prune treats as stale by default, so that this
// check and that command cannot disagree about which environments are old.
const pruneCutoff = 24 * time.Hour

// checkLeftoverEnvironments counts what this machine is still holding.
//
// af env prune exists for exactly this, and until now the only thing that named
// it was af env list, which is itself a command nobody is ever pointed at. So
// the way to learn that leftovers accumulate was to read the whole command
// reference, which means for practical purposes nobody learned it. Doctor is
// where it belongs: it is the command the quickstart says to run first, it is
// the one people run when something is wrong, and it already reports disk.
//
// Measured rather than assumed to be rare. The machine this was written on was
// holding five environments from failed runs, the oldest forty one hours, none
// of them running and all of them holding a database branch and a network.
//
// A failure to look is a skip, not a warning. The daemon being unreachable is
// already reported by the Docker check above, and saying it twice trains
// somebody to read past both.
func checkLeftoverEnvironments(ctx context.Context, env *Env, _ Prober) CheckResult {
	r := CheckResult{Name: "Leftover environments"}
	r.Remediation = "Remove them with 'af env prune', which takes anything older than a day, " +
		"or one at a time with 'af down --branch <branch>'. 'af env list' shows what is held."

	envs, err := listEnvironments(ctx, env)
	if err != nil {
		r.Status = CheckSkip
		r.Detail = "nothing could be counted, because the runtime did not answer"
		return r
	}
	status, detail := leftoverVerdict(envs, env.Clock.Now())
	r.Status, r.Detail = status, detail
	return r
}

// leftoverVerdict decides what the count means, separately from reading it.
//
// Split out because the interesting cases are about ages, and reaching them
// through the runtime would mean leaving real environments lying around on the
// machine running the tests for a day, which is both slow and the exact fault
// this check reports.
func leftoverVerdict(envs []environment, now time.Time) (CheckStatus, string) {
	if len(envs) == 0 {
		return CheckPass, "none are being held"
	}
	stale := 0
	oldest := time.Duration(0)
	for _, e := range envs {
		age := now.Sub(e.Oldest)
		if age > oldest {
			oldest = age
		}
		if age > pruneCutoff {
			stale++
		}
	}
	if stale == 0 {
		// Held is not the same as leaked. An environment somebody is working in
		// right now is the normal state, and warning about it would make this
		// check noise on the machine of anybody actually using the product.
		return CheckPass, fmt.Sprintf("%s being held, the oldest %s old",
			plural(len(envs), "environment", "environments"), humanAge(oldest))
	}
	return CheckWarn, fmt.Sprintf(
		"%s older than a day, out of %d being held; the oldest is %s old",
		plural(stale, "environment", "environments"), len(envs), humanAge(oldest))
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

// portsProbedPerRange is how many ports of each range the check tries.
//
// Both ranges are probed, because they are different numbers and a machine can
// be out of one and not the other. Checking only the base reported on the
// databases and said nothing at all about the addresses `af up` publishes,
// which are the ports a person actually notices are missing.
const portsProbedPerRange = 20

func checkPortRange(_ context.Context, _ *Env, p Prober) CheckResult {
	r := CheckResult{Name: "Local ports"}
	base, err := dockerutil.PortRangeFrom(p.Getenv)
	if err != nil {
		r.Status = CheckFail
		r.Detail = err.Error()
		r.Remediation = fmt.Sprintf(
			"Set %s to the first port of a free range, or unset it to use the default.",
			dockerutil.PortRangeStartVar)
		return r
	}
	published := base + local.PublishedPortOffset
	r.Remediation = fmt.Sprintf(
		"Free some ports from %d or %d upwards, or set %s to the first port of a range that is free.",
		base, published, dockerutil.PortRangeStartVar)

	free := func(from int) int {
		n := 0
		for i := 0; i < portsProbedPerRange; i++ {
			if err := p.ListenTCP(from + i); err == nil {
				n++
			}
		}
		return n
	}
	freeBase, freePublished := free(base), free(published)
	r.Detail = fmt.Sprintf(
		"%d of %d checked ports are free from %d for databases, and %d of %d from %d for published services",
		freeBase, portsProbedPerRange, base,
		freePublished, portsProbedPerRange, published)
	worst := freeBase
	if freePublished < worst {
		worst = freePublished
	}
	switch {
	case worst == 0:
		r.Status = CheckFail
	case worst < 5:
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

// checkModelKey reports what the agents will plan with.
//
// Doctor is what somebody runs first and it said nothing at all about the
// model, which left the single most confusing thing about this product
// undiscoverable: whether a run will read pages with a model or fall back to
// the deterministic planner. Somebody with a typo in a variable name got a
// green doctor and a run that quietly planned deterministically.
//
// No key is a PASS and not a warning. Running without one is a supported mode:
// workflows still run, still drive a real browser and still produce a verdict.
// A warning would say the opposite, and the whole reason this product works
// with no credential at all is worth stating rather than flagging.
//
// It does not make a call. Doctor is run constantly, often on a laptop with no
// network, and a check that spent money every time would be a check people
// turn off. 'af model test' is the one that proves the key, and this names it.
func checkModelKey(ctx context.Context, env *Env, _ Prober) CheckResult {
	r := CheckResult{Name: "Model key"}

	cfg, err := model.Resolve(ctx, modelChain(env))
	if err != nil {
		// A source that failed, which is almost always a locked keyring or a
		// wrong passphrase. Reported rather than read as "no key", because
		// those look identical from here and only one of them is a problem.
		r.Status = CheckWarn
		r.Detail = "a configured source could not be read: " + err.Error()
		r.Remediation = "Run 'af model show' to see which source failed. Until it is fixed, a key stored there cannot be found and runs will use the deterministic planner."
		return r
	}

	if cfg == nil {
		r.Status = CheckPass
		r.Detail = "none set, so agents use the deterministic planner"
		r.Remediation = "No action needed. Running without a model key is supported: workflows still drive a real browser and still produce a verdict. To have agents read pages and decide what a person would do next, store your own key with 'af model set anthropic'."
		return r
	}

	detail := fmt.Sprintf("%s/%s from %s", cfg.Provider.Name, cfg.Model, cfg.Source)
	switch {
	case cfg.ThroughControlPlane():
		detail += ", through your control plane"
	case cfg.Custom():
		detail += ", at " + cfg.BaseURL
	}
	r.Detail = detail

	// Said before anything about verification, because it is the more expensive
	// mistake of the two. A key that is never tested costs a run; a cap somebody
	// believes in and that is not applied costs whatever the month costs.
	if origin := uncappedControlPlane(env, cfg); origin != "" {
		r.Status = CheckWarn
		r.Detail = detail + ", not capped"
		r.Remediation = fmt.Sprintf(
			"This key goes straight to the provider, so a monthly cap set with "+
				"'af provider budget' on %s is not in force. Run 'af model show' for how to "+
				"route through the control plane instead, or keep this key and know there is "+
				"no ceiling on it.", origin)
		return r
	}

	if rec := model.ReadRecord(env.WorkDir, cfg.Fingerprint); rec != nil {
		r.Status = CheckPass
		r.Detail = detail + ", verified " + rec.VerifiedAt.Format("2006-01-02")
		r.Remediation = "No action needed. Run 'af model test' again to re-check the key."
		return r
	}
	// Present and never proven. A warning rather than a pass, because a key
	// that is set and revoked is indistinguishable from a working one here and
	// the difference costs somebody a whole run to discover.
	r.Status = CheckWarn
	r.Detail = detail + ", never verified"
	r.Remediation = "Run 'af model test'. It makes one cheap call and says exactly what is wrong when the key is revoked, out of credit, or pointed at a model the key cannot use."
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
