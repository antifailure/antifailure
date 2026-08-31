// Package installsh tests install.sh by running it.
//
// The script had a defect that reading it would not obviously show and that no
// test could have caught, because there was no test: it decided BIN_DIR was
// missing from PATH, printed an export line, and then eight lines later printed
// three bare `af` commands to run next. Every one of them answered "command not
// found". `docs/plan/STATUS.md` called install.sh proven with "the failure path
// is tested", and nothing anywhere ran it.
//
// So these run it, for real, in a throwaway HOME with a fake curl serving a
// fixture release. That covers the parts a reader cannot check by eye: which
// profile each shell gets, what a second run does to that profile, and whether
// the commands the script prints actually work when pasted.
package installsh

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const version = "v9.9.9"

// name is what install.sh builds from uname, which for the platforms this
// release supports is exactly GOOS and GOARCH.
func name() string {
	return fmt.Sprintf("antifailure_9.9.9_%s_%s", runtime.GOOS, runtime.GOARCH)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(wd))
	if _, err := os.Stat(filepath.Join(root, "install.sh")); err != nil {
		t.Fatalf("install.sh not found from %s: %v", wd, err)
	}
	return root
}

// fixture builds the release install.sh will "download": a tarball holding an
// af that reports a version, a runner directory, and a checksums.txt that
// matches. The checksum has to be real, because refusing a download that does
// not match its checksum is behaviour worth keeping and a fixture that fails it
// would be indistinguishable from a break.
func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	stage := filepath.Join(dir, name())
	if err := os.MkdirAll(filepath.Join(stage, "runner"), 0o755); err != nil {
		t.Fatal(err)
	}
	af := "#!/bin/sh\necho \"antifailure 9.9.9 (fixture)\"\n"
	if err := os.WriteFile(filepath.Join(stage, "af"), []byte(af), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "runner", "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tarball := name() + ".tar.gz"
	cmd := exec.Command("tar", "-C", dir, "-czf", filepath.Join(dir, tarball), name())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tar: %v: %s", err, out)
	}
	blob, err := os.ReadFile(filepath.Join(dir, tarball))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(blob)
	line := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), tarball)
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// stub puts a curl on PATH that answers from the fixture directory instead of
// the network. install.sh calls it two ways, `curl -fsSL URL -o FILE` and
// `curl -fsSL URL`, and this handles both.
func stub(t *testing.T, fixtures string) string {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
url=""; out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out=$2; shift 2 ;;
    -*) shift ;;
    *) url=$1; shift ;;
  esac
done
f="` + fixtures + `/${url##*/}"
[ -f "$f" ] || exit 22
if [ -n "$out" ]; then cp "$f" "$out"; else cat "$f"; fi
`
	p := filepath.Join(dir, "curl")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// session is one install, in its own HOME, with its own PATH.
type session struct {
	t     *testing.T
	home  string
	path  string
	env   map[string]string
	stubs string
	root  string
}

func newSession(t *testing.T) *session {
	t.Helper()
	s := &session{
		t:     t,
		home:  t.TempDir(),
		stubs: stub(t, fixture(t)),
		root:  repoRoot(t),
		env:   map[string]string{"SHELL": "/bin/zsh"},
	}
	s.path = s.stubs + ":/usr/bin:/bin:/usr/sbin:/sbin"
	return s
}

func (s *session) binDir() string { return filepath.Join(s.home, ".antifailure", "bin") }

// install pipes install.sh into sh, which is how curl | sh delivers it: the
// script arrives on stdin and there is no stdin left to prompt on.
func (s *session) install() string {
	s.t.Helper()
	env := []string{
		"HOME=" + s.home,
		"PATH=" + s.path,
		"AF_VERSION=" + version,
		"TERM=dumb",
	}
	for k, v := range s.env {
		env = append(env, k+"="+v)
	}
	cmd := exec.Command("/bin/sh", "-c", "cat "+filepath.Join(s.root, "install.sh")+" | sh")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.t.Fatalf("install.sh failed: %v\n%s", err, out)
	}
	return string(out)
}

func (s *session) read(rel string) string {
	s.t.Helper()
	b, err := os.ReadFile(filepath.Join(s.home, rel))
	if err != nil {
		s.t.Fatalf("reading %s: %v", rel, err)
	}
	return string(b)
}

func contains(t *testing.T, out, want string) {
	t.Helper()
	if !strings.Contains(out, want) {
		t.Errorf("output does not contain %q\n--- output ---\n%s", want, out)
	}
}

func absent(t *testing.T, out, unwanted string) {
	t.Helper()
	if strings.Contains(out, unwanted) {
		t.Errorf("output should not contain %q\n--- output ---\n%s", unwanted, out)
	}
}

// The defect this file exists for. Every `af` the script prints has to be
// reachable, either because it is already on PATH or because the step that
// puts it there comes first and is presented as a prerequisite rather than as
// an aside.
func TestNoBareAfIsPrintedBeforeThePathStep(t *testing.T) {
	s := newSession(t)
	out := s.install()

	lines := strings.Split(out, "\n")
	firstAf, firstFix := -1, -1
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if firstAf < 0 && strings.HasPrefix(trimmed, "af ") {
			firstAf = i
		}
		if firstFix < 0 && strings.Contains(l, "export PATH=") {
			firstFix = i
		}
	}
	if firstAf < 0 {
		t.Fatalf("the installer printed no next steps at all\n%s", out)
	}
	if firstFix < 0 {
		t.Fatalf("af is not on PATH and the installer printed no way to fix that\n%s", out)
	}
	if firstAf < firstFix {
		t.Errorf("a bare af command is printed at line %d, before the PATH step at line %d\n%s", firstAf, firstFix, out)
	}
	contains(t, out, "2. Then:")
	// The escape hatch, so a reader who does not want to touch their PATH is
	// still given something that runs.
	contains(t, out, s.binDir()+"/af")
}

// The printed instructions are run rather than pattern matched, because a
// message that looks right and does not work is the failure being fixed.
func TestThePrintedInstructionsActuallyWork(t *testing.T) {
	s := newSession(t)
	out := s.install()

	var cmds []string
	for _, l := range strings.Split(out, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "echo 'export PATH=") || strings.HasPrefix(t, "export PATH=") {
			cmds = append(cmds, t)
		}
	}
	if len(cmds) != 2 {
		t.Fatalf("expected the two PATH commands, got %v\n%s", cmds, out)
	}

	// Run exactly what was printed, then start a genuinely new interactive
	// shell that inherits none of it and ask it for af.
	script := strings.Join(cmds, "\n") + "\ncommand -v af || exit 1\n"
	first := exec.Command("/bin/zsh", "-c", script)
	first.Env = []string{"HOME=" + s.home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}
	if o, err := first.CombinedOutput(); err != nil {
		t.Fatalf("the printed commands did not put af on PATH: %v\n%s", err, o)
	}

	second := exec.Command("/bin/zsh", "-ic", "af")
	second.Env = []string{"HOME=" + s.home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}
	o, err := second.CombinedOutput()
	if err != nil {
		t.Fatalf("a new terminal could not find af: %v\n%s", err, o)
	}
	if !strings.Contains(string(o), "antifailure 9.9.9") {
		t.Errorf("a new terminal ran something, but not the installed af: %s", o)
	}
}

// The full path is printed for a reader who will not edit their PATH, so it
// has to be a path that runs.
func TestTheFullPathEscapeHatchRuns(t *testing.T) {
	s := newSession(t)
	out := s.install()
	contains(t, out, s.binDir()+"/af")

	cmd := exec.Command(filepath.Join(s.binDir(), "af"))
	o, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the full path the installer printed does not run: %v\n%s", err, o)
	}
	if !strings.Contains(string(o), "antifailure 9.9.9") {
		t.Errorf("full path ran the wrong thing: %s", o)
	}
}

func TestAlreadyOnPathSaysNothingAboutPath(t *testing.T) {
	s := newSession(t)
	// The directory does not exist yet, which is the honest shape of somebody
	// who put it on PATH in their profile before ever installing.
	s.path = s.binDir() + ":" + s.path
	out := s.install()

	contains(t, out, "Next:")
	contains(t, out, "af doctor")
	absent(t, out, "not on your PATH")
	absent(t, out, "export PATH=")
	absent(t, out, "AF_ADD_TO_PATH")
}

func TestEachShellIsToldAboutItsOwnProfile(t *testing.T) {
	darwin := runtime.GOOS == "darwin"
	bashProfile := "~/.bashrc"
	if darwin {
		bashProfile = "~/.bash_profile"
	}
	cases := []struct {
		shell  string
		want   []string
		unwant []string
	}{
		{shell: "/bin/zsh", want: []string{">> ~/.zshrc", "export PATH="}, unwant: []string{"bashrc", "bash_profile", "fish"}},
		{shell: "/bin/bash", want: []string{">> " + bashProfile, "export PATH="}, unwant: []string{"zshrc", "fish"}},
		{shell: "/usr/local/bin/fish", want: []string{"fish_add_path", "config.fish"}, unwant: []string{"export PATH=", "zshrc", "bashrc"}},
		{shell: "/bin/ksh", want: []string{"login shell is ksh", "~/.profile", "export PATH="}, unwant: []string{"zshrc", "bashrc", "fish"}},
	}
	for _, c := range cases {
		t.Run(strings.TrimPrefix(filepath.Base(c.shell), ""), func(t *testing.T) {
			s := newSession(t)
			s.env["SHELL"] = c.shell
			out := s.install()
			for _, w := range c.want {
				contains(t, out, w)
			}
			for _, u := range c.unwant {
				absent(t, out, u)
			}
		})
	}
}

// An empty SHELL rather than an absent one, because bash in sh mode fills an
// absent one in from the passwd entry, so the branch under test is only
// reachable on macOS by leaving it set and empty. A getent that fails stands in
// for a machine that does not have one, which is every macOS machine and is the
// state this branch is actually for.
func TestUnknownShellSaysSoRatherThanGuessing(t *testing.T) {
	s := newSession(t)
	s.env["SHELL"] = ""
	if err := os.WriteFile(filepath.Join(s.stubs, "getent"), []byte("#!/bin/sh\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := s.install()
	contains(t, out, "because SHELL is")
	contains(t, out, "export PATH=")
	absent(t, out, ">> ~/")
	for _, f := range []string{".zshrc", ".bashrc", ".bash_profile", ".profile"} {
		if _, err := os.Stat(filepath.Join(s.home, f)); err == nil {
			t.Errorf("guessed at %s for an unknown shell", f)
		}
	}
}

func TestZdotdirIsRespected(t *testing.T) {
	s := newSession(t)
	zdot := filepath.Join(s.home, "cfg", "zsh")
	if err := os.MkdirAll(zdot, 0o755); err != nil {
		t.Fatal(err)
	}
	s.env["ZDOTDIR"] = zdot
	s.env["AF_ADD_TO_PATH"] = "1"
	s.install()
	if _, err := os.Stat(filepath.Join(zdot, ".zshrc")); err != nil {
		t.Fatalf("ZDOTDIR was ignored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.home, ".zshrc")); err == nil {
		t.Error("wrote ~/.zshrc while ZDOTDIR named somewhere else")
	}
}

func TestNoProfileIsTouchedWithoutBeingAsked(t *testing.T) {
	s := newSession(t)
	s.install()
	for _, f := range []string{".zshrc", ".bashrc", ".bash_profile", ".profile", ".config/fish/config.fish"} {
		if _, err := os.Stat(filepath.Join(s.home, f)); err == nil {
			t.Errorf("the installer wrote %s without being asked", f)
		}
	}
}

func TestAddToPathWritesOnceHoweverOftenItRuns(t *testing.T) {
	s := newSession(t)
	s.env["AF_ADD_TO_PATH"] = "1"

	first := s.install()
	contains(t, first, "Added af to your PATH in ~/.zshrc")
	after := s.read(".zshrc")
	if !strings.Contains(after, `export PATH="$HOME/.antifailure/bin:$PATH"`) {
		t.Fatalf("~/.zshrc does not export the bin dir:\n%s", after)
	}

	second := s.install()
	if s.read(".zshrc") != after {
		t.Errorf("a second run changed ~/.zshrc\nbefore:\n%s\nafter:\n%s", after, s.read(".zshrc"))
	}
	// And it says why it did nothing rather than repeating the first message.
	contains(t, second, "already puts af on the PATH")
}

// Somebody who added the line by hand wrote the expanded path where the
// installer writes $HOME. Not recognising that is how a profile collects a
// duplicate on every run.
func TestAHandWrittenLineCountsAsAlreadyThere(t *testing.T) {
	s := newSession(t)
	line := fmt.Sprintf("export PATH=\"%s:$PATH\"\n", s.binDir())
	if err := os.WriteFile(filepath.Join(s.home, ".zshrc"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	s.env["AF_ADD_TO_PATH"] = "1"
	s.install()
	if got := s.read(".zshrc"); got != line {
		t.Errorf("~/.zshrc was appended to when the line was already there:\n%s", got)
	}
}

// GitHub Actions gives every step a fresh PATH, so a workflow that installs in
// one step and runs af in the next needs GITHUB_PATH written. The documented
// workflow does exactly that and did not work.
func TestGithubActionsGetsPathForLaterSteps(t *testing.T) {
	s := newSession(t)
	gp := filepath.Join(t.TempDir(), "github_path")
	if err := os.WriteFile(gp, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s.env["GITHUB_PATH"] = gp

	out := s.install()
	contains(t, out, "for the rest of this job")
	contains(t, out, "af doctor")
	absent(t, out, "export PATH=")

	body, err := os.ReadFile(gp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(body)) != s.binDir() {
		t.Errorf("GITHUB_PATH is %q, want %q", string(body), s.binDir())
	}

	s.install()
	body, err = os.ReadFile(gp)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(body), s.binDir()); got != 1 {
		t.Errorf("GITHUB_PATH names the bin dir %d times after two installs, want 1:\n%s", got, body)
	}
	// A CI job must not have its profile edited either.
	if _, err := os.Stat(filepath.Join(s.home, ".zshrc")); err == nil {
		t.Error("the installer wrote ~/.zshrc in a CI job")
	}
}

// AF_PREFIX with no HOME is an ordinary shape inside a container, and the
// script dereferences HOME in several places that have to survive it.
func TestNoHomeStillInstalls(t *testing.T) {
	s := newSession(t)
	prefix := filepath.Join(t.TempDir(), "opt", "af")
	env := []string{
		"PATH=" + s.path,
		"AF_VERSION=" + version,
		"AF_PREFIX=" + prefix,
		"AF_ADD_TO_PATH=1",
		"TERM=dumb",
	}
	cmd := exec.Command("/bin/sh", "-c", "cat "+filepath.Join(s.root, "install.sh")+" | sh")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh failed with no HOME: %v\n%s", err, out)
	}
	contains(t, string(out), "HOME is not set")
	contains(t, string(out), filepath.Join(prefix, "bin")+"/af")
	if _, err := os.Stat(filepath.Join(prefix, "bin", "af")); err != nil {
		t.Fatalf("af was not installed: %v", err)
	}
}

// A bin dir outside HOME cannot be written as $HOME/... into a profile, and
// writing it that way would produce a line that silently points nowhere.
func TestAPrefixOutsideHomeIsWrittenAbsolute(t *testing.T) {
	s := newSession(t)
	prefix := filepath.Join(t.TempDir(), "opt", "af")
	s.env["AF_PREFIX"] = prefix
	s.env["AF_ADD_TO_PATH"] = "1"
	s.install()
	got := s.read(".zshrc")
	want := fmt.Sprintf("export PATH=\"%s/bin:$PATH\"", prefix)
	if !strings.Contains(got, want) {
		t.Errorf("~/.zshrc has %q, want it to contain %q", got, want)
	}
	if strings.Contains(got, "$HOME/opt") {
		t.Errorf("a prefix outside HOME was written as $HOME:\n%s", got)
	}
}

// The checksum refusal is the one failure path STATUS already claimed was
// tested. It was not.
func TestABadChecksumRefusesToInstall(t *testing.T) {
	s := newSession(t)
	// Rewrite the fixture's checksums.txt to something that cannot match.
	fx := filepath.Join(s.stubs, "..")
	_ = fx
	// The stub reads from the fixture dir baked into it, so find it back out
	// of the script rather than threading it through.
	script, err := os.ReadFile(filepath.Join(s.stubs, "curl"))
	if err != nil {
		t.Fatal(err)
	}
	dir := ""
	for _, l := range strings.Split(string(script), "\n") {
		if strings.HasPrefix(l, `f="`) {
			dir = strings.TrimPrefix(strings.SplitN(l, "/${url##*/}", 2)[0], `f="`)
		}
	}
	if dir == "" {
		t.Fatal("could not find the fixture directory in the curl stub")
	}
	bad := fmt.Sprintf("%s  %s.tar.gz\n", strings.Repeat("0", 64), name())
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("/bin/sh", "-c", "cat "+filepath.Join(s.root, "install.sh")+" | sh")
	cmd.Env = []string{"HOME=" + s.home, "PATH=" + s.path, "AF_VERSION=" + version, "SHELL=/bin/zsh", "TERM=dumb"}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("a mismatched checksum installed anyway:\n%s", out)
	}
	contains(t, string(out), "does not match its published checksum")
	if _, err := os.Stat(filepath.Join(s.binDir(), "af")); err == nil {
		t.Error("af was installed despite the checksum mismatch")
	}
}
