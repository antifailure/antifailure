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

	// The slave's name without ptsname. ptsname is the TIOCPTYGNAME ioctl, which
	// fills a buffer, and x/sys has no darwin wrapper for such an ioctl: its raw
	// SYS_IOCTL is deprecated there, and the standard library's syscall.Syscall
	// is a raw trap too. The master's device minor is the pair's number, so the
	// name comes from Fstat, which x/sys routes through libSystem, and the pairing
	// is then proved rather than assumed.
	var st unix.Stat_t
	require.NoError(t, unix.Fstat(fd, &st), "fstat the terminal master")
	path := fmt.Sprintf("/dev/ttys%03d", unix.Minor(uint64(st.Rdev)))

	slave, err = os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY, 0)
	require.NoError(t, err, "open %s", path)
	t.Cleanup(func() { _ = slave.Close() })

	// A line written to this master arrives on this slave, or the name was
	// wrong and the test would be driving somebody else's terminal.
	const probe = "af-pty-pair-probe\n"
	_, err = master.Write([]byte(probe))
	require.NoError(t, err, "write to the terminal master")
	arrived := make(chan string, 1)
	go func() {
		buf := make([]byte, len(probe))
		n, _ := slave.Read(buf)
		arrived <- string(buf[:n])
	}()
	select {
	case got := <-arrived:
		require.Equal(t, probe, got, "%s is not the other end of this terminal", path)
	case <-time.After(5 * time.Second):
		t.Fatalf("nothing written to the terminal master arrived on %s, so it is not its pair", path)
	}
	return master, slave
}

// The secret is in none of the arguments security is started with, and it
// reaches security on stdin.
//
// This replaced a test that sampled `ps -Ao args=` in a loop while Set ran. A
// mutation that moved the value's hex into security's own arguments passed it in
// 0.09s: security lives for milliseconds, and a sampler that misses the process
// reports a clean machine. A check that can miss the thing it looks for cannot
// say no, so this one does not sample.
//
// A shim named security is put first on PATH. It records every argument vector
// it is started with, one argument per line, and everything it is given on
// stdin, then execs the real command with those arguments and that input, so
// the write still happens and is read back. The recorded vectors are the whole
// of what any other user on the machine could have read in ps.
//
// Set's error is held until the recordings have been checked, so that each
// assertion below can be the one that fails. Each was proved to fail on a break
// of its own.
func TestTheSecretIsInNoArgumentAndArrivesOnStdin(t *testing.T) {
	real, err := exec.LookPath("security")
	if err != nil {
		t.Skip("skipped: the security command is not on the path")
	}

	dir := t.TempDir()
	argvLog := filepath.Join(dir, "argv.log")
	stdinLog := filepath.Join(dir, "stdin.log")
	script := "#!/bin/sh\n" +
		"{ echo START; for a in \"$@\"; do printf '%s\\n' \"$a\"; done; } >> \"$AF_TEST_SECURITY_ARGV_LOG\"\n" +
		"input=$(mktemp \"$AF_TEST_SECURITY_DIR/stdin.XXXXXX\")\n" +
		"cat > \"$input\"\n" +
		"cat \"$input\" >> \"$AF_TEST_SECURITY_STDIN_LOG\"\n" +
		"exec " + real + " \"$@\" < \"$input\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "security"), []byte(script), 0o700))
	t.Setenv("AF_TEST_SECURITY_DIR", dir)
	t.Setenv("AF_TEST_SECURITY_ARGV_LOG", argvLog)
	t.Setenv("AF_TEST_SECURITY_STDIN_LOG", stdinLog)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ring := secrets.NewSystemKeyring()
	const service = "antifailure-test-argv-vectors"
	const name = "argv-vectors"
	// Distinctive enough that a match cannot be anything else on the machine.
	const secret = "af-argv-vector-canary-3e9b17c4-do-not-store"
	t.Cleanup(func() { _ = ring.Delete(service, name) })

	setErr := ring.Set(service, name, secret)

	vectors := recording(t, argvLog)
	encoded := hex.EncodeToString([]byte(secret))
	for _, arg := range strings.Split(vectors, "\n") {
		require.NotContains(t, arg, secret, "the secret was an argument to security")
	}
	for _, arg := range strings.Split(vectors, "\n") {
		require.NotContains(t, strings.ToLower(arg), encoded, "the secret's hex was an argument to security")
	}

	// Without this, a change that stopped resolving security through PATH would
	// leave nothing recorded, and the two loops above would pass about a command
	// that was never watched.
	require.Contains(t, vectors, "START\n-i\n",
		"the write did not go through `security -i` on the watched path:\n%s", vectors)

	require.Contains(t, strings.ToLower(recording(t, stdinLog)), encoded,
		"the value did not reach security on stdin")

	require.NoError(t, setErr)

	// And the write really happened. Not separately breakable: Set already reads
	// the entry back through this same Get and fails on any difference.
	got, err := ring.Get(service, name)
	require.NoError(t, err)
	require.Equal(t, secret, got)
}

// recording is what the security shim wrote, or nothing when it never ran.
func recording(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	require.NoError(t, err, "read %s", path)
	return string(body)
}
