// Package installsh tests install.sh by running it.
//
// The script had a defect that reading it would not obviously show and that no
// test could have caught, because there was no test: it decided BIN_DIR was
// missing from PATH, printed an export line, and then printed three bare `af`
// commands to run next. Every one of them answered "command not found".
// `docs/plan/STATUS.md` called install.sh proven with "the failure path is
// tested", and nothing anywhere ran it.
//
// It now puts af on the PATH itself, which raises the stakes on all of this: it
// writes to a real file in somebody's home directory by default. So these run
// it, for real, in a throwaway HOME with a fake curl serving a fixture release.
// They check the parts a reader cannot check by eye: which profile each shell
// gets, that three runs leave one line, that a genuinely new terminal finds af,
// and that no branch which failed to set PATH up prints a bare `af` anyway.
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
	// Read rather than stat, and read the whole thing.
	//
	// The script is run through `sh`, so nothing in this package opens it and
	// go test's cache never learns it is an input. `just test-tools` runs
	// `go test ./...` with no -count, and it reported ok on a deliberately
	// broken install.sh from cache: the one gate protecting the installer went
	// green without running. Reading the file here puts it in the cache key,
	// so editing install.sh invalidates these tests.
	if _, err := os.ReadFile(filepath.Join(root, "install.sh")); err != nil {
		t.Fatalf("install.sh not found from %s: %v", wd, err)
	}
	return root
}

// fixture builds the release install.sh will "download": a tarball holding an
// af that reports a version, a runner directory, and a checksums.txt that
// matches. The checksum has to be real, because refusing a download that does
// not match its checksum is behaviour worth keeping and a fixture that failed
// it would be indistinguishable from a break.
func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	stage := filepath.Join(dir, name())
	if err := os.MkdirAll(filepath.Join(stage, "runner", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	// src/main.ts is the file the engine stats to decide a directory is a
	// runner, so the fixture has to carry it for a placement test to mean
	// anything.
	if err := os.WriteFile(filepath.Join(stage, "runner", "src", "main.ts"), []byte("// fixture\n"), 0o644); err != nil {
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
	if err := os.WriteFile(filepath.Join(dir, "curl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// session is one install, in its own HOME, with its own PATH.
type session struct {
	t        *testing.T
	home     string
	path     string
	env      map[string]string
	stubs    string
	fixtures string
	root     string
}

func newSession(t *testing.T) *session {
	t.Helper()
	fx := fixture(t)
	s := &session{
		t:        t,
		home:     t.TempDir(),
		fixtures: fx,
		stubs:    stub(t, fx),
		root:     repoRoot(t),
		env:      map[string]string{"SHELL": "/bin/zsh"},
	}
	s.path = s.stubs + ":/usr/bin:/bin:/usr/sbin:/sbin"
	return s
}

func (s *session) binDir() string { return filepath.Join(s.home, ".antifailure", "bin") }

// install pipes install.sh into sh, which is how curl | sh delivers it: the
// script arrives on stdin and there is no stdin left to prompt on.
func (s *session) install() string {
	s.t.Helper()
	out, err := s.run()
	if err != nil {
		s.t.Fatalf("install.sh failed: %v\n%s", err, out)
	}
	// Twice now a change has put a raw shell error where a message belongs: an
	// append to a read only profile reporting "Permission denied" before the
	// explanation, and a helper called without its argument reporting "unbound
	// variable" where the next steps go. Both left a zero exit in some branches
	// and both looked fine in the branch under test, so this is checked on
	// every install rather than in one test.
	for _, leak := range []string{"unbound variable", "sh: line", "Permission denied", "command not found"} {
		if strings.Contains(out, leak) {
			s.t.Errorf("a raw shell error leaked into the output: %q\n--- output ---\n%s", leak, out)
		}
	}
	assertNumberedStepsAreIndented(s.t, out)
	return out
}

func (s *session) run() (string, error) {
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
	return string(out), err
}

func (s *session) read(rel string) string {
	s.t.Helper()
	b, err := os.ReadFile(filepath.Join(s.home, rel))
	if err != nil {
		s.t.Fatalf("reading %s: %v", rel, err)
	}
	return string(b)
}

// newTerminal is the case the old script failed silently: a process that
// inherits nothing from the install, reading the profile the way a terminal
// emulator's interactive shell does.
func (s *session) newTerminal(shell string, args ...string) (string, error) {
	cmd := exec.Command(shell, args...)
	cmd.Env = []string{"HOME=" + s.home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}
	out, err := cmd.CombinedOutput()
	return string(out), err
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

// printedCommands are the indented lines the reader is meant to paste. Prose
// sits at column zero and every command is indented, which is what separates
// "run af doctor" as an instruction from the word af inside a sentence.
func printedCommands(out string) []string {
	var cmds []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "  ") && strings.TrimSpace(l) != "" {
			cmds = append(cmds, strings.TrimSpace(l))
		}
	}
	return cmds
}

// The defect this file exists for, as an invariant rather than a string match:
// a bare `af` may only be printed once something earlier in the same output has
// made af resolvable.
func assertEveryPrintedAfIsReachable(t *testing.T, out string, alreadyOnPath bool) {
	t.Helper()
	reachable := alreadyOnPath
	for _, c := range printedCommands(out) {
		if strings.Contains(c, "export PATH=") || strings.HasPrefix(c, "fish_add_path") {
			// Only the pasteable form makes af reachable in this terminal. A
			// line the reader is told to add to a file does not.
			if strings.Contains(c, "&& af ") {
				reachable = true
			}
			continue
		}
		if strings.HasPrefix(c, "af ") && !reachable {
			t.Errorf("printed %q while af is not reachable\n--- output ---\n%s", c, out)
		}
	}
}

// The full path branches print a path with ~ where one applies, because an
// absolute temp path is unreadable and a real home makes it long. Rather than
// match the string, expand it and run it: a printed path that does not execute
// is the same defect as a printed name that does not resolve.
func assertPrintedFullPathsRun(t *testing.T, s *session, out string) {
	t.Helper()
	found := 0
	for _, c := range printedCommands(out) {
		p := strings.Fields(c)[0]
		// The export line names the bin directory too, and is not a command
		// the reader is meant to run on its own.
		if !strings.HasSuffix(p, "/af") {
			continue
		}
		if strings.HasPrefix(p, "~/") {
			p = filepath.Join(s.home, strings.TrimPrefix(p, "~/"))
		}
		got, err := exec.Command(p).CombinedOutput()
		if err != nil {
			t.Errorf("printed %q, which does not run: %v\n%s", c, err, got)
			continue
		}
		if !strings.Contains(string(got), "antifailure 9.9.9") {
			t.Errorf("printed %q, which ran the wrong thing: %s", c, got)
		}
		found++
	}
	if found != 3 {
		t.Errorf("expected the three next steps as full paths, found %d\n--- output ---\n%s", found, out)
	}
}

// A numbered step's commands have to sit under the number that owns them. This
// is not fussiness: next_steps_rest takes its indent as a positional argument,
// and the call site that forgot it printed the two commands flush against the
// left margin under "2. Then:", which reads as a separate list rather than as
// the contents of step 2. The first version of that omission was worse, an
// "unbound variable" from set -u printed where the steps belong.
func assertNumberedStepsAreIndented(t *testing.T, out string) {
	t.Helper()
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, "2. ") {
			continue
		}
		for _, rest := range lines[i+1:] {
			if strings.TrimSpace(rest) == "" || strings.HasPrefix(rest, "   ") {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(rest), "af ") ||
				strings.Contains(rest, "/af ") {
				t.Errorf("a command under %q is indented %d spaces, so it reads as its own list\n--- output ---\n%s",
					l, len(rest)-len(strings.TrimLeft(rest, " ")), out)
			}
			break
		}
	}
}

// zshPath finds the shell these tests need, rather than assuming /bin/zsh.
//
// The installer's whole subject is which profile file a shell reads, so a run
// that never starts a real zsh has not tested the thing. macOS always has one;
// a Linux runner does not unless somebody installed it. Both facts are true, so
// this refuses rather than skips wherever CI is set: a skipped check reads as a
// pass, and this is the check that proves the installer works at all.
func zshPath(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("zsh")
	if err == nil {
		return path
	}
	if os.Getenv("CI") != "" {
		t.Fatalf("zsh is not on PATH and CI is set, so this cannot be skipped: %v.\n"+
			"The workflow has to install it; these tests start a real zsh on purpose.", err)
	}
	t.Skipf("zsh is not on PATH, so the shell this test drives does not exist here: %v", err)
	return ""
}

// The whole point, end to end: install, then open a genuinely new terminal.
func TestTheDefaultInstallMakesANewTerminalWork(t *testing.T) {
	s := newSession(t)
	out := s.install()

	contains(t, out, "Added this to ~/.zshrc")
	contains(t, out, "AF_NO_MODIFY_PATH=1")
	assertEveryPrintedAfIsReachable(t, out, false)

	rc := s.read(".zshrc")
	want := `export PATH="$HOME/.antifailure/bin:$PATH"`
	if !strings.Contains(rc, want) {
		t.Fatalf("~/.zshrc does not export the bin dir:\n%s", rc)
	}
	// The line printed is the line written, or the reader cannot undo it.
	contains(t, out, want)

	got, err := s.newTerminal(zshPath(t), "-ic", "af")
	if err != nil {
		t.Fatalf("a new terminal could not find af: %v\n%s", err, got)
	}
	if !strings.Contains(got, "antifailure 9.9.9") {
		t.Errorf("a new terminal ran something, but not the installed af: %s", got)
	}
}

// The line offered for the terminal the installer ran in is pasted and run
// rather than pattern matched, because a message that looks right and does not
// work is the failure being fixed.
func TestThePastedLineFixesTheCurrentTerminal(t *testing.T) {
	s := newSession(t)
	out := s.install()

	paste := ""
	for _, c := range printedCommands(out) {
		if strings.Contains(c, "&& af doctor") {
			paste = c
		}
	}
	if paste == "" {
		t.Fatalf("no pasteable line for the current terminal\n%s", out)
	}

	// A shell with the installer's PATH and no profile sourced, which is what
	// the terminal that ran curl | sh actually is.
	cmd := exec.Command(zshPath(t), "-c", strings.Replace(paste, "af doctor", "af", 1))
	cmd.Env = []string{"HOME=" + s.home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}
	got, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the pasted line did not make af work here: %v\n%s", err, got)
	}
	if !strings.Contains(string(got), "antifailure 9.9.9") {
		t.Errorf("the pasted line ran the wrong thing: %s", got)
	}
}

// Idempotency is load bearing now that this writes by default. Three runs,
// because two can hide an off by one that a third exposes.
func TestThreeRunsLeaveExactlyOneLine(t *testing.T) {
	s := newSession(t)
	s.install()
	after := s.read(".zshrc")
	s.install()
	s.install()
	final := s.read(".zshrc")

	if final != after {
		t.Errorf("runs two and three changed ~/.zshrc\nafter one:\n%s\nafter three:\n%s", after, final)
	}
	if n := strings.Count(final, ".antifailure/bin"); n != 1 {
		t.Errorf("~/.zshrc names the bin dir %d times after three installs, want 1:\n%s", n, final)
	}
}

// A second install from a terminal that predates the profile line must not
// repeat the "Added this" message, and must still hand back something that
// works here and now.
func TestASecondInstallSaysTheProfileAlreadyHasIt(t *testing.T) {
	s := newSession(t)
	s.install()
	out := s.install()

	contains(t, out, "already puts af on the PATH")
	absent(t, out, "Added this to")
	assertEveryPrintedAfIsReachable(t, out, false)
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
	s.install()
	if got := s.read(".zshrc"); got != line {
		t.Errorf("~/.zshrc was appended to when the line was already there:\n%s", got)
	}
}

func TestAlreadyOnPathWritesNothingExtraAndSaysNothingAboutPath(t *testing.T) {
	s := newSession(t)
	s.install()
	before := s.read(".zshrc")

	// Second install from a terminal that already has it, which is the shape
	// of every install after the first.
	s.path = s.binDir() + ":" + s.path
	out := s.install()

	contains(t, out, "Next:")
	contains(t, out, "af doctor")
	absent(t, out, "start here")
	assertEveryPrintedAfIsReachable(t, out, true)
	if s.read(".zshrc") != before {
		t.Error("a run with the bin dir already on PATH changed the profile")
	}
}

func TestEachShellGetsItsOwnProfileFile(t *testing.T) {
	darwin := runtime.GOOS == "darwin"
	bashFile := ".bashrc"
	if darwin {
		bashFile = ".bash_profile"
	}
	cases := []struct {
		shell string
		file  string
		line  string
		other []string
	}{
		{
			shell: "/bin/zsh", file: ".zshrc",
			line:  `export PATH="$HOME/.antifailure/bin:$PATH"`,
			other: []string{".bashrc", ".bash_profile", ".profile", ".config/fish/config.fish"},
		},
		{
			shell: "/bin/bash", file: bashFile,
			line:  `export PATH="$HOME/.antifailure/bin:$PATH"`,
			other: []string{".zshrc", ".profile", ".config/fish/config.fish"},
		},
		{
			shell: "/usr/local/bin/fish", file: ".config/fish/config.fish",
			line:  `fish_add_path "$HOME/.antifailure/bin"`,
			other: []string{".zshrc", ".bashrc", ".bash_profile", ".profile"},
		},
	}
	for _, c := range cases {
		t.Run(filepath.Base(c.shell), func(t *testing.T) {
			s := newSession(t)
			s.env["SHELL"] = c.shell
			out := s.install()

			body, err := os.ReadFile(filepath.Join(s.home, c.file))
			if err != nil {
				t.Fatalf("%s was not written: %v\n%s", c.file, err, out)
			}
			if !strings.Contains(string(body), c.line) {
				t.Errorf("%s does not contain %q:\n%s", c.file, c.line, body)
			}
			contains(t, out, c.line)
			for _, o := range c.other {
				if _, err := os.Stat(filepath.Join(s.home, o)); err == nil {
					t.Errorf("%s was written for a %s user", o, filepath.Base(c.shell))
				}
			}
			assertEveryPrintedAfIsReachable(t, out, false)
		})
	}
}

// A shell whose startup file this cannot name gets told so and gets commands
// that run, rather than a guessed file and three names that will not resolve.
func TestAnUnrecognisedShellGuessesAtNothing(t *testing.T) {
	s := newSession(t)
	s.env["SHELL"] = "/bin/ksh"
	out := s.install()

	contains(t, out, "login shell is ksh")
	contains(t, out, "~/.profile")
	assertEveryPrintedAfIsReachable(t, out, false)
	assertPrintedFullPathsRun(t, s, out)

	entries, err := os.ReadDir(s.home)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != ".antifailure" {
			t.Errorf("guessed at %s for an unknown shell", e.Name())
		}
	}
}

// An empty SHELL rather than an absent one, because bash in sh mode fills an
// absent one in from the passwd entry, so this branch is only reachable on
// macOS by leaving it set and empty. A getent that fails stands in for a
// machine that does not have one, which is every macOS machine and is the state
// this branch is actually for.
func TestNoShellAtAllGuessesAtNothing(t *testing.T) {
	s := newSession(t)
	s.env["SHELL"] = ""
	if err := os.WriteFile(filepath.Join(s.stubs, "getent"), []byte("#!/bin/sh\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := s.install()

	contains(t, out, "because SHELL is not")
	assertEveryPrintedAfIsReachable(t, out, false)
	assertPrintedFullPathsRun(t, s, out)
	for _, f := range []string{".zshrc", ".bashrc", ".bash_profile", ".profile"} {
		if _, err := os.Stat(filepath.Join(s.home, f)); err == nil {
			t.Errorf("guessed at %s with no shell to go on", f)
		}
	}
}

func TestAfNoModifyPathTouchesNothing(t *testing.T) {
	s := newSession(t)
	s.env["AF_NO_MODIFY_PATH"] = "1"
	out := s.install()

	contains(t, out, "AF_NO_MODIFY_PATH is set")
	contains(t, out, "~/.zshrc was left alone")
	assertEveryPrintedAfIsReachable(t, out, false)
	assertPrintedFullPathsRun(t, s, out)

	entries, err := os.ReadDir(s.home)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != ".antifailure" {
			t.Errorf("AF_NO_MODIFY_PATH was set and %s was written anyway", e.Name())
		}
	}
}

// A profile that cannot be appended to is not a reason to print three commands
// that will not run.
func TestAProfileItCannotWriteIsReportedRatherThanIgnored(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write a read only file, so this cannot be provoked here")
	}
	s := newSession(t)
	rc := filepath.Join(s.home, ".zshrc")
	if err := os.WriteFile(rc, []byte("# mine\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	out := s.install()

	contains(t, out, "could not be written")
	if strings.Contains(out, "Permission denied") {
		t.Errorf("a raw shell error leaked above the message written to explain it:\n%s", out)
	}
	assertEveryPrintedAfIsReachable(t, out, false)
	assertPrintedFullPathsRun(t, s, out)
	if got := s.read(".zshrc"); got != "# mine\n" {
		t.Errorf("a read only profile was modified:\n%s", got)
	}
}

func TestZdotdirIsRespected(t *testing.T) {
	s := newSession(t)
	zdot := filepath.Join(s.home, "cfg", "zsh")
	if err := os.MkdirAll(zdot, 0o755); err != nil {
		t.Fatal(err)
	}
	s.env["ZDOTDIR"] = zdot
	s.install()

	if _, err := os.Stat(filepath.Join(zdot, ".zshrc")); err != nil {
		t.Fatalf("ZDOTDIR was ignored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.home, ".zshrc")); err == nil {
		t.Error("wrote ~/.zshrc while ZDOTDIR named somewhere else")
	}
}

// GitHub Actions gives every step a fresh PATH, so a workflow that installs in
// one step and runs af in the next needs GITHUB_PATH written. The documented
// workflow does exactly that and did not work.
func TestGithubActionsGetsPathForLaterStepsAndKeepsItsProfile(t *testing.T) {
	s := newSession(t)
	gp := filepath.Join(t.TempDir(), "github_path")
	if err := os.WriteFile(gp, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s.env["GITHUB_PATH"] = gp

	out := s.install()
	contains(t, out, "for the rest of this job")
	assertEveryPrintedAfIsReachable(t, out, true)
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
	// GITHUB_PATH is the mechanism a job has; a runner's profile is not the
	// installer's to edit.
	if _, err := os.Stat(filepath.Join(s.home, ".zshrc")); err == nil {
		t.Error("the installer wrote ~/.zshrc in a CI job")
	}
}

// AF_PREFIX with no HOME is an ordinary shape inside a container, and the
// script dereferences HOME in several places that have to survive it.
func TestNoHomeStillInstalls(t *testing.T) {
	s := newSession(t)
	prefix := filepath.Join(t.TempDir(), "opt", "af")
	cmd := exec.Command("/bin/sh", "-c", "cat "+filepath.Join(s.root, "install.sh")+" | sh")
	cmd.Env = []string{
		"PATH=" + s.path,
		"AF_VERSION=" + version,
		"AF_PREFIX=" + prefix,
		"TERM=dumb",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh failed with no HOME: %v\n%s", err, out)
	}
	contains(t, string(out), "HOME is not set")
	contains(t, string(out), filepath.Join(prefix, "bin")+"/af doctor")
	assertEveryPrintedAfIsReachable(t, string(out), false)
	assertPrintedFullPathsRun(t, s, string(out))
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

// A directory outside HOME that the reader has already put on their PATH is
// one they manage. Writing a profile line for something that already works is
// the unrequested change worth not making.
func TestADirectoryOutsideHomeAlreadyOnPathIsLeftAlone(t *testing.T) {
	s := newSession(t)
	prefix := filepath.Join(t.TempDir(), "opt", "af")
	s.env["AF_PREFIX"] = prefix
	s.path = filepath.Join(prefix, "bin") + ":" + s.path
	out := s.install()

	contains(t, out, "Next:")
	assertEveryPrintedAfIsReachable(t, out, true)
	if _, err := os.Stat(filepath.Join(s.home, ".zshrc")); err == nil {
		t.Error("wrote a profile line for a directory already on PATH outside HOME")
	}
}

// The checksum refusal is the one failure path STATUS already claimed was
// tested. It was not.
func TestABadChecksumRefusesToInstall(t *testing.T) {
	s := newSession(t)
	bad := fmt.Sprintf("%s  %s.tar.gz\n", strings.Repeat("0", 64), name())
	if err := os.WriteFile(filepath.Join(s.fixtures, "checksums.txt"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := s.run()
	if err == nil {
		t.Fatalf("a mismatched checksum installed anyway:\n%s", out)
	}
	contains(t, out, "does not match its published checksum")
	if _, err := os.Stat(filepath.Join(s.binDir(), "af")); err == nil {
		t.Error("af was installed despite the checksum mismatch")
	}
	if _, err := os.Stat(filepath.Join(s.home, ".zshrc")); err == nil {
		t.Error("a refused install still edited the profile")
	}
}

// The second command the installer prints has to be one that can succeed.
//
// It could not: install.sh put the runner source at $PREFIX/runner, which is
// where af looks for an INSTALLED runner, and `af runner install` searches
// $PREFIX/bin/runner and $PREFIX/share/antifailure/runner for a SOURCE. So on
// every machine installed with curl | sh it answered AF-AGT-004 and advised
// running itself. Reproduced against the released v0.1.1 binary.
func TestTheRunnerSourceLandsWhereRunnerInstallLooks(t *testing.T) {
	s := newSession(t)
	s.install()

	prefix := filepath.Join(s.home, ".antifailure")
	// The path runnerSource builds from the binary's own directory:
	// dir(af)/../share/antifailure/runner.
	want := filepath.Join(prefix, "share", "antifailure", "runner", "src", "main.ts")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("no runner source where af runner install looks: %v", err)
	}
	// And not at the installed location, which is af runner install's output
	// rather than its input. A source there is a runner with no node_modules
	// that af test finds first and fails on inside node.
	if _, err := os.Stat(filepath.Join(prefix, "runner")); err == nil {
		t.Error("the runner source was left where an installed runner belongs")
	}
}

func TestAHalfInstalledRunnerFromAnOlderInstallerIsCleanedUp(t *testing.T) {
	s := newSession(t)
	stale := filepath.Join(s.home, ".antifailure", "runner", "src")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "main.ts"), []byte("// stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.install()
	if _, err := os.Stat(filepath.Join(s.home, ".antifailure", "runner")); err == nil {
		t.Error("a runner with no node_modules survived, so af test still finds it first")
	}
}

func TestARealInstalledRunnerIsNotDeleted(t *testing.T) {
	s := newSession(t)
	installed := filepath.Join(s.home, ".antifailure", "runner")
	if err := os.MkdirAll(filepath.Join(installed, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(installed, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installed, "src", "main.ts"), []byte("// mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.install()
	if _, err := os.Stat(filepath.Join(installed, "node_modules")); err != nil {
		t.Errorf("an installed runner with dependencies was deleted: %v", err)
	}
}

// A dependency named at the end of a successful install costs a minute. The
// same one discovered three commands later is a bug report.
func TestMissingNodeIsNamedWhileTheReaderIsStillLooking(t *testing.T) {
	s := newSession(t)
	if _, err := exec.LookPath("node"); err == nil {
		// The session PATH is deliberately narrow, but say so rather than
		// silently testing nothing if that ever changes.
		if strings.Contains(s.path, filepath.Dir(mustLookPath(t, "node"))) {
			t.Skip("node is on the session PATH, so its absence cannot be provoked")
		}
	}
	out := s.install()
	contains(t, out, "node was not found")
	contains(t, out, "22.6")
	contains(t, out, "https://nodejs.org")
}

func TestNodePresentSaysNothingAboutNode(t *testing.T) {
	s := newSession(t)
	if err := os.WriteFile(filepath.Join(s.stubs, "node"), []byte("#!/bin/sh\necho v24.0.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := s.install()
	absent(t, out, "node was not found")
}

func mustLookPath(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
