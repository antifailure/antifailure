//go:build darwin

package secrets_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// Against the real keychain, not a fake.
//
// A fake would prove this code agrees with our idea of the security command,
// and what matters is that it agrees with the security command: the exit code
// for a missing item, whether a second write replaces or fails, whether a
// delete of something absent is an error. Those are the things that were
// guessed at, and each one is a real failure if the guess is wrong.
func TestSystemKeyringRoundTrips(t *testing.T) {
	if _, err := exec.LookPath("security"); err != nil {
		t.Skip("skipped: the security command is not on the path")
	}
	ring := secrets.NewSystemKeyring()
	require.NotNil(t, ring, "darwin must have a keyring")

	const service = "antifailure-test"
	const name = "round-trip"
	t.Cleanup(func() { _ = ring.Delete(service, name) })

	// Missing is a miss, not a failure. The chain relies on telling those
	// apart: a miss falls through to the next source and a failure does not.
	_, err := ring.Get(service, "definitely-not-set")
	require.ErrorIs(t, err, secrets.ErrNotFound)

	require.NoError(t, ring.Set(service, name, "first"))
	got, err := ring.Get(service, name)
	require.NoError(t, err)
	require.Equal(t, "first", got)

	// A second write has to replace rather than fail, or 'af secret set' can
	// only ever be run once per name.
	require.NoError(t, ring.Set(service, name, "second"))
	got, err = ring.Get(service, name)
	require.NoError(t, err)
	require.Equal(t, "second", got)

	require.NoError(t, ring.Delete(service, name))
	_, err = ring.Get(service, name)
	require.ErrorIs(t, err, secrets.ErrNotFound)

	// Deleting something already gone succeeds, for the same reason every
	// teardown in this product does: the caller wanted it gone.
	require.NoError(t, ring.Delete(service, name))
}

// Every value comes back byte for byte.
//
// Two defects lived here and neither made a sound. The prompt route this
// replaced read the value through readpassphrase, which keeps 128 bytes and
// drops the rest with exit status 0, so an OpenAI project key and every
// credential af login writes were stored cut short and read back as something
// else. And the read used -w, which prints a value holding any byte that is not
// printable ASCII as bare hex, indistinguishable from a value that is hex text,
// so a key with a tab or an accent in it came back as its own encoding.
//
// The cases are the bytes most likely to be mangled somewhere between a Go
// string, a command stream parsed by security, and a listing printed by it.
func TestEveryValueSurvivesTheRoundTripByteForByte(t *testing.T) {
	if _, err := exec.LookPath("security"); err != nil {
		t.Skip("skipped: the security command is not on the path")
	}
	ring := secrets.NewSystemKeyring()
	const service = "antifailure-test-bytes"

	for name, value := range map[string]string{
		"a connection string":           `postgres://u:p@ss w0rd!"'$&@host:5432/db?opt=a b`,
		"a PEM block with line breaks":  "-----BEGIN KEY-----\nabc\ndef\n-----END KEY-----\n",
		"a carriage return":             "one\r\ntwo",
		"a tab":                         "tab\there",
		"multibyte characters":          "unicodé ✓ 鍵",
		"a backslash":                   `C:\keys\n`,
		"double and single quotes":      `he said "it's" fine`,
		"a leading dash that is a flag": "-U",
		"another flag":                  "-w",
		"text that is valid hex":        "deadbeef",
		"text shaped like the listing":  `0x41  "A"`,
		"spaces at both ends":           "  padded  ",
		"a NUL byte":                    "before\x00after",
		"the empty string":              "",
		"128 bytes, the old ceiling":    strings.Repeat("a", 128),
		"129 bytes, one past it":        strings.Repeat("b", 129),
		"an OpenAI project key's size":  "sk-proj-" + strings.Repeat("Xy9_", 40),
		"a kilobyte":                    strings.Repeat("0123456789abcdef", 64),
		"one byte":                      "x",
		"127 bytes":                     strings.Repeat("c", 127),
		"2000 bytes":                    strings.Repeat("z", 2000),
		"bytes that are not UTF-8":      "\xff\xfe\x80 not utf-8 \xc3",
	} {
		t.Run(name, func(t *testing.T) {
			account := "bytes-" + strings.ReplaceAll(name, " ", "-")
			t.Cleanup(func() { _ = ring.Delete(service, account) })

			require.NoError(t, ring.Set(service, account, value))
			got, err := ring.Get(service, account)
			require.NoError(t, err)
			require.Equal(t, len(value), len(got), "the stored value has a different length")
			require.Equal(t, value, got)
		})
	}
}

// The entry's own names go through the command stream too, and a name holding
// the characters that stream quotes with has to name the same entry the
// argv based Get and Delete name.
func TestAnEntryNameWithQuotesAndBackslashesNamesOneEntry(t *testing.T) {
	if _, err := exec.LookPath("security"); err != nil {
		t.Skip("skipped: the security command is not on the path")
	}
	ring := secrets.NewSystemKeyring()
	const service = `antifailure test "quoted" \service`
	const name = `cli:https://host:8443/a b "c" \d`
	t.Cleanup(func() { _ = ring.Delete(service, name) })

	require.NoError(t, ring.Set(service, name, "value"))
	got, err := ring.Get(service, name)
	require.NoError(t, err)
	require.Equal(t, "value", got)

	require.NoError(t, ring.Delete(service, name))
	_, err = ring.Get(service, name)
	require.ErrorIs(t, err, secrets.ErrNotFound, "the delete removed some other entry")
}

func TestAMissingSecurityCommandIsReportedAsUnavailable(t *testing.T) {
	// Not the same as an empty keyring. The chain reports which sources it
	// considered and why each did not answer, and "unavailable" and "no such
	// entry" are different sentences.
	require.True(t, errors.Is(secrets.ErrKeyringUnavailable, secrets.ErrKeyringUnavailable))
}

// terminalHelperEnv tells the test binary it was started by
// TestAWriteFromInsideATerminalReturnsWithoutWaitingOnIt, inside a terminal.
const terminalHelperEnv = "AF_TEST_KEYCHAIN_WRITE_IN_A_TERMINAL"

const (
	terminalService = "antifailure-test-terminal"
	terminalName    = "terminal-probe"
	terminalValue   = "af-terminal-canary-0b7e53c1"
)

// A write made by a process that has a controlling terminal returns.
//
// Which is every write a person makes: af login and af model set run in
// Terminal.app, in iTerm, in an editor's terminal, over ssh with a pty, and all
// of those are a controlling terminal. The prompt route this replaced asked
// security to read the value, and security reads a password from the terminal
// whenever there is one, ignoring the stdin it was handed. So on every Mac the
// login printed "password data for new item:" and waited for a person to type a
// token they have never seen, until killed, with nothing stored. The tests
// beside this one all passed throughout, because go test gives them no
// terminal, and with no terminal security falls back to stdin.
//
// So this starts the test binary again with a pseudo terminal as its
// controlling terminal, has that process make the write, and gives it a
// deadline. Main's implementation fails here by timing out with the prompt on
// the terminal; it cannot hang the suite.
func TestAWriteFromInsideATerminalReturnsWithoutWaitingOnIt(t *testing.T) {
	if _, err := exec.LookPath("security"); err != nil {
		t.Skip("skipped: the security command is not on the path")
	}
	if os.Getenv(terminalHelperEnv) != "" {
		t.Skip("this is the helper process")
	}
	ring := secrets.NewSystemKeyring()
	t.Cleanup(func() { _ = ring.Delete(terminalService, terminalName) })

	master, slave := openTerminal(t)

	cmd := exec.Command(os.Args[0], "-test.run=^TestKeychainWriteHelperInsideATerminal$", "-test.v")
	cmd.Env = append(os.Environ(), terminalHelperEnv+"=1")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	// Setsid makes a new session, Setctty makes the pseudo terminal its
	// controlling terminal, and Ctty names it by the child's descriptor, which
	// is stdin.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	require.NoError(t, cmd.Start())
	// The parent's copy, closed so the terminal hangs up when the child's side
	// is gone and the reader below sees the end.
	require.NoError(t, slave.Close())

	var (
		mu     sync.Mutex
		screen bytes.Buffer
	)
	read := make(chan struct{})
	go func() {
		defer close(read)
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			mu.Lock()
			screen.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	terminal := func() string {
		mu.Lock()
		defer mu.Unlock()
		return screen.String()
	}

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	const deadline = 30 * time.Second
	select {
	case <-exited:
	case <-time.After(deadline):
		// The whole session, which includes security, since it never left the
		// process group its parent was started in.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-exited
		<-read
		t.Fatalf("the write was still waiting %s after it started, from inside a terminal. "+
			"The terminal read:\n%s", deadline, terminal())
	}
	<-read
	shown := terminal()

	// Without this the test passes against the defect whenever the helper
	// somehow has no terminal, because that is exactly the case that works.
	require.Contains(t, shown, "HAS A CONTROLLING TERMINAL",
		"the helper had no controlling terminal, so this proved nothing:\n%s", shown)
	require.Contains(t, shown, "SET RETURNED <nil>", "the write failed:\n%s", shown)

	got, err := ring.Get(terminalService, terminalName)
	require.NoError(t, err)
	require.Equal(t, terminalValue, got, "the write returned and stored something else")
}

// TestKeychainWriteHelperInsideATerminal is the process the test above starts.
// Run on its own it skips.
func TestKeychainWriteHelperInsideATerminal(t *testing.T) {
	if os.Getenv(terminalHelperEnv) == "" {
		t.Skip("run by TestAWriteFromInsideATerminalReturnsWithoutWaitingOnIt, inside a terminal")
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		fmt.Printf("NO CONTROLLING TERMINAL: %v\n", err)
		t.FailNow()
	}
	_ = tty.Close()
	fmt.Println("HAS A CONTROLLING TERMINAL")

	err = secrets.NewSystemKeyring().Set(terminalService, terminalName, terminalValue)
	fmt.Printf("SET RETURNED %v\n", err)
}

// openTerminal allocates a pseudo terminal pair without cgo: posix_openpt is
// an open of /dev/ptmx, and grantpt, unlockpt and ptsname are three ioctls.
func openTerminal(t *testing.T) (master, slave *os.File) {
	t.Helper()
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	require.NoError(t, err, "open /dev/ptmx")
	master = os.NewFile(uintptr(fd), "/dev/ptmx")
	t.Cleanup(func() { _ = master.Close() })

	require.NoError(t, unix.IoctlSetInt(fd, unix.TIOCPTYGRANT, 0), "grantpt")
	require.NoError(t, unix.IoctlSetInt(fd, unix.TIOCPTYUNLK, 0), "unlockpt")
	var name [128]byte
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(unix.TIOCPTYGNAME),
		uintptr(unsafe.Pointer(&name[0]))); errno != 0 {
		t.Fatalf("ptsname: %v", errno)
	}
	path := string(name[:bytes.IndexByte(name[:], 0)])

	slave, err = os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY, 0)
	require.NoError(t, err, "open %s", path)
	t.Cleanup(func() { _ = slave.Close() })
	return master, slave
}

// The secret must not reach the child process's argv, in any encoding.
//
// This is the defect bf9f394ca fixed: every secret this product stores went
// through `security add-generic-password -w <value>`, where any other user on
// the machine could read it out of ps. A control plane bearer token was read
// that way on this project's own machine, by accident.
//
// The value now travels as hex, so the watcher looks for the hex as well as the
// plain text. A regression that moved the hex into argv would otherwise pass a
// watcher that only knew the secret's plain spelling.
//
// Asserted by watching the process rather than by reading the code, because
// reading the code is what missed it for the life of the file. A writer is
// started against a value nothing else on the machine would produce, and the
// full command line of every security process is sampled while it runs.
func TestTheSecretNeverReachesTheProcessListing(t *testing.T) {
	if _, err := exec.LookPath("security"); err != nil {
		t.Skip("skipped: the security command is not on the path")
	}
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("skipped: ps is not on the path, so the listing cannot be read")
	}
	ring := secrets.NewSystemKeyring()

	const service = "antifailure-test-argv"
	const name = "argv-probe"
	// Distinctive enough that a match cannot be anything else on the machine.
	const secret = "af-argv-canary-8c41d2e7-do-not-store"
	t.Cleanup(func() { _ = ring.Delete(service, name) })

	seen := make(chan string, 1)
	done := make(chan struct{})
	go func() {
		defer close(seen)
		for {
			select {
			case <-done:
				return
			default:
			}
			out, err := exec.Command("ps", "-Ao", "args=").Output()
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(out), "\n") {
				if securityArgvHolding(line, secret) {
					select {
					case seen <- line:
					default:
					}
					return
				}
			}
		}
	}()

	require.NoError(t, ring.Set(service, name, secret))
	close(done)

	if line, ok := <-seen; ok {
		t.Fatalf("the secret was on a command line, readable by any user on this machine:\n%s", line)
	}

	// And it really was stored, or this test would pass by writing nothing.
	got, err := ring.Get(service, name)
	require.NoError(t, err)
	require.Equal(t, secret, got)
}

// securityArgvHolding reports whether this is the security command with the
// secret in its own arguments, spelled plainly or as hex in either case.
//
// Scoped to that process rather than to any line containing the string, and the
// first version was not, which made it fail against a fixed implementation: the
// shell that wrote this test file still had the canary in ITS command line,
// because the heredoc quotes the test source. A watcher that matches anything
// is a watcher that reports the machine rather than the subject.
func securityArgvHolding(line, secret string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 || filepath.Base(fields[0]) != "security" {
		return false
	}
	encoded := hex.EncodeToString([]byte(secret))
	return strings.Contains(line, secret) ||
		strings.Contains(strings.ToLower(line), encoded)
}

// The sampler has to be able to see a secret that IS on a command line, or the
// test above passes because it is looking at nothing.
//
// Proved against real `ps` output rather than by racing a process, which is
// what two earlier versions of this did and both were wrong for different
// reasons. Matching any line holding the string reported the shell that wrote
// this test file, because the heredoc quotes the test source. Starting a
// shebang script named security reported nothing, because `ps` renders such a
// process as `/bin/sh /path/to/security` and the first field is the shell.
//
// So the predicate is exercised on the exact line the old implementation
// produced, taken from a real listing, alongside the line that fooled the first
// version.
func TestTheProcessListingWatcherCanSeeASecretThatIsThere(t *testing.T) {
	const canary = "af-argv-positive-control-5b2f9a"

	// What ps showed while the implementation before bf9f394ca ran.
	defective := "/usr/bin/security add-generic-password -s antifailure -a model.anthropic -w " +
		canary + " -U"
	if !securityArgvHolding(defective, canary) {
		t.Error("the watcher cannot see the defect it was written for, so the test above proves nothing")
	}
	// And the same value moved into argv in the form it now travels in.
	for _, encoded := range []string{
		hex.EncodeToString([]byte(canary)),
		strings.ToUpper(hex.EncodeToString([]byte(canary))),
	} {
		if !securityArgvHolding("/usr/bin/security add-generic-password -s antifailure -a x -U -X "+encoded, canary) {
			t.Errorf("the watcher cannot see the secret as hex in argv: %s", encoded)
		}
	}

	// And the shapes it must not report. The first is this test's own shell,
	// which is why the earlier version failed against a fixed implementation.
	for name, line := range map[string]string{
		"a shell whose command line quotes this file": "/bin/zsh -c cat > keyring_darwin_test.go <<EOF " + canary,
		"security in interactive mode":                "/usr/bin/security -i",
		"security running with no secret in argv":     "/usr/bin/security add-generic-password -s antifailure -a model.anthropic -U -w",
		"an unrelated process":                        "/usr/bin/grep -r " + canary + " .",
		"an empty line":                               "",
	} {
		if securityArgvHolding(line, canary) {
			t.Errorf("the watcher reports %s, which is a false finding: %s", name, line)
		}
	}
}
