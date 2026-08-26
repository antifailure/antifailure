package secrets_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func envSource(label string, values map[string]string) *secrets.EnvSource {
	return &secrets.EnvSource{
		Label: label,
		Getenv: func(name string) (string, bool) {
			v, ok := values[name]
			return v, ok
		},
	}
}

// ---------------------------------------------------------------------------
// Precedence
// ---------------------------------------------------------------------------

func TestChain_TheFirstSourceThatAnswersWins(t *testing.T) {
	t.Parallel()
	chain := secrets.NewChain(
		envSource("shell", map[string]string{"TOKEN": "from-shell"}),
		envSource("file", map[string]string{"TOKEN": "from-file", "OTHER": "only-file"}),
	)

	value, res, found, err := chain.Lookup(t.Context(), "TOKEN")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "from-shell", value.Reveal())
	require.Equal(t, "shell", res.Source)

	value, res, found, err = chain.Lookup(t.Context(), "OTHER")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "only-file", value.Reveal())
	require.Equal(t, "file", res.Source)
}

func TestChain_AnEmptyValueCountsAsAnAnswer(t *testing.T) {
	t.Parallel()
	// A CI platform that injects empty strings for unset variables would
	// otherwise mask every later source, and the application would silently get
	// a stale value from a file instead of the empty one somebody set.
	chain := secrets.NewChain(
		envSource("shell", map[string]string{"TOKEN": ""}),
		envSource("file", map[string]string{"TOKEN": "stale"}),
	)
	value, res, found, err := chain.Lookup(t.Context(), "TOKEN")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "", value.Reveal())
	require.Equal(t, "shell", res.Source)
}

type failingSource struct{ err error }

func (f failingSource) Name() string                             { return "a broken source" }
func (f failingSource) Available(context.Context) (bool, string) { return true, "" }
func (f failingSource) Lookup(context.Context, string) (secrets.Value, bool, error) {
	return secrets.Value{}, false, f.err
}

func TestChain_ASourceThatFailsStopsTheSearch(t *testing.T) {
	t.Parallel()
	// Falling through to a lower-priority source would hand the application a
	// different value than it got yesterday, which is the worst way for this to
	// break: it works, and it works differently.
	boom := errors.New("the keyring is locked")
	chain := secrets.NewChain(
		failingSource{err: boom},
		envSource("file", map[string]string{"TOKEN": "would-be-wrong"}),
	)
	_, _, _, err := chain.Lookup(t.Context(), "TOKEN")
	require.ErrorIs(t, err, boom)
	require.Contains(t, err.Error(), "a broken source")
}

type unavailableSource struct{ why string }

func (u unavailableSource) Name() string { return "an absent source" }
func (u unavailableSource) Available(context.Context) (bool, string) {
	return false, u.why
}
func (u unavailableSource) Lookup(context.Context, string) (secrets.Value, bool, error) {
	panic("an unavailable source must never be asked")
}

func TestChain_SkipsUnavailableSourcesAndNamesThem(t *testing.T) {
	t.Parallel()
	chain := secrets.NewChain(
		unavailableSource{why: "the keyring is locked"},
		envSource("shell", map[string]string{"TOKEN": "ok"}),
	)
	_, _, found, err := chain.Lookup(t.Context(), "TOKEN")
	require.NoError(t, err)
	require.True(t, found)

	require.Equal(t, []string{"shell"}, chain.Sources(t.Context()))
	// "TOKEN was not found" is much less useful than the same sentence followed
	// by "the keyring is locked".
	require.Equal(t, []string{"an absent source (the keyring is locked)"},
		chain.Unavailable(t.Context()))
}

func TestChain_ASourceThatIsAbsentWithNoReasonIsNotReported(t *testing.T) {
	t.Parallel()
	// A missing .env is the ordinary case and not worth a line of output.
	chain := secrets.NewChain(unavailableSource{why: ""})
	require.Empty(t, chain.Unavailable(t.Context()))
}

// ---------------------------------------------------------------------------
// dotenv
// ---------------------------------------------------------------------------

func TestParseDotEnv_HandlesWhatTheseFilesActuallyContain(t *testing.T) {
	t.Parallel()
	values, err := secrets.ParseDotEnv(strings.NewReader(`
# A comment
  # An indented comment

DATABASE_URL=postgres://localhost/app
export EXPORTED=yes
QUOTED="a value with spaces"
SINGLE='literal \n stays'
ESCAPED="line\nbreak"
TRAILING=value # with a comment
HASH_INSIDE=sk_test_a#b
EMPTY=
EQUALS=a=b=c
`))
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"DATABASE_URL": "postgres://localhost/app",
		"EXPORTED":     "yes",
		"QUOTED":       "a value with spaces",
		"SINGLE":       `literal \n stays`,
		"ESCAPED":      "line\nbreak",
		"TRAILING":     "value",
		// A token containing a hash is one value, not a value and a comment.
		"HASH_INSIDE": "sk_test_a#b",
		"EMPTY":       "",
		"EQUALS":      "a=b=c",
	}, values)
}

func TestParseDotEnv_RefusesALineItCannotParse(t *testing.T) {
	t.Parallel()
	// Silently ignoring it is how a variable is "set" and absent at the same
	// time, which is a very long debugging session.
	_, err := secrets.ParseDotEnv(strings.NewReader("VALID=1\nthis is not a variable\n"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "line 2")
}

func TestParseDotEnv_RefusesAnImpossibleName(t *testing.T) {
	t.Parallel()
	_, err := secrets.ParseDotEnv(strings.NewReader("not a name=1\n"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "line 1")
}

func TestParseDotEnv_DoesNotQuoteAWholeSecretInAnError(t *testing.T) {
	t.Parallel()
	// The line that failed to parse might be a private key, and an error
	// message is the one place a value is guaranteed to be printed.
	secret := strings.Repeat("s3cr3t", 40)
	_, err := secrets.ParseDotEnv(strings.NewReader(secret + "\n"))
	require.Error(t, err)
	require.NotContains(t, err.Error(), secret)
	require.Less(t, len(err.Error()), 120)
}

func TestDotEnvSource_AMissingFileIsNotAnError(t *testing.T) {
	t.Parallel()
	// Most repositories have no .env, and a first run must not fail on it.
	src := secrets.NewDotEnvSource(filepath.Join(t.TempDir(), "nothing-here"))
	ok, why := src.Available(t.Context())
	require.False(t, ok)
	require.Empty(t, why)
}

func TestDotEnvSource_ReadsAFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(path, []byte("STRIPE_SECRET_KEY=sk_test_abc\n"), 0o600))

	src := secrets.NewDotEnvSource(path)
	ok, _ := src.Available(t.Context())
	require.True(t, ok)
	require.Equal(t, ".env", src.Name())

	value, found, err := src.Lookup(t.Context(), "STRIPE_SECRET_KEY")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "sk_test_abc", value.Reveal())

	_, found, err = src.Lookup(t.Context(), "ABSENT")
	require.NoError(t, err)
	require.False(t, found)
}

func TestDotEnvSource_ReportsAFileItCannotParse(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(path, []byte("garbage line\n"), 0o600))

	src := secrets.NewDotEnvSource(path)
	ok, why := src.Available(t.Context())
	require.False(t, ok)
	require.Contains(t, why, "line 1")
}

// ---------------------------------------------------------------------------
// The keyring
// ---------------------------------------------------------------------------

type fakeKeyring struct {
	entries map[string]string
	err     error
}

func (f *fakeKeyring) Get(service, name string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	v, ok := f.entries[service+"/"+name]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}
func (f *fakeKeyring) Set(service, name, value string) error {
	if f.entries == nil {
		f.entries = map[string]string{}
	}
	f.entries[service+"/"+name] = value
	return nil
}
func (f *fakeKeyring) Delete(service, name string) error {
	delete(f.entries, service+"/"+name)
	return nil
}

func TestKeyringSource_ReadsAndNamespacesByService(t *testing.T) {
	t.Parallel()
	ring := &fakeKeyring{entries: map[string]string{
		"antifailure/TOKEN": "kept-safe",
		"other/TOKEN":       "somebody-else",
	}}
	src := secrets.NewKeyringSource(ring, "")
	ok, _ := src.Available(t.Context())
	require.True(t, ok)

	value, found, err := src.Lookup(t.Context(), "TOKEN")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "kept-safe", value.Reveal())
}

func TestKeyringSource_ALockedKeyringIsUnavailableRatherThanFatal(t *testing.T) {
	t.Parallel()
	// A keyring can be present and locked, and the only way to find out is to
	// ask it for something.
	src := secrets.NewKeyringSource(&fakeKeyring{err: errors.New("the keychain is locked")}, "af")
	ok, why := src.Available(t.Context())
	require.False(t, ok)
	require.Contains(t, why, "locked")
}

func TestKeyringSource_ProbesOnceRatherThanPerVariable(t *testing.T) {
	t.Parallel()
	ring := &countingKeyring{fakeKeyring: fakeKeyring{entries: map[string]string{}}}
	src := secrets.NewKeyringSource(ring, "af")
	for range 5 {
		src.Available(t.Context())
	}
	require.Equal(t, 1, ring.gets, "the keyring was probed more than once")
}

type countingKeyring struct {
	fakeKeyring
	gets int
}

func (c *countingKeyring) Get(service, name string) (string, error) {
	c.gets++
	return c.fakeKeyring.Get(service, name)
}

func TestKeyringSource_WithNoRingIsSimplyAbsent(t *testing.T) {
	t.Parallel()
	src := secrets.NewKeyringSource(nil, "af")
	ok, why := src.Available(t.Context())
	require.False(t, ok)
	require.Equal(t, "not configured", why)
}

// ---------------------------------------------------------------------------
// The encrypted file
// ---------------------------------------------------------------------------

func TestFileStore_RoundTrips(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "secrets.enc")
	store := secrets.NewFileStore(path, "correct horse battery staple")

	require.NoError(t, store.Set("STRIPE_SECRET_KEY", "sk_test_abc"))
	require.NoError(t, store.Set("RESEND_API_KEY", "re_test_def"))

	names, err := store.Names()
	require.NoError(t, err)
	require.Equal(t, []string{"RESEND_API_KEY", "STRIPE_SECRET_KEY"}, names)

	value, found, err := store.Lookup(t.Context(), "STRIPE_SECRET_KEY")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "sk_test_abc", value.Reveal())
}

func TestFileStore_TheValuesAreNotOnDiskInTheClear(t *testing.T) {
	t.Parallel()
	// The entire reason this exists rather than a plaintext file.
	path := filepath.Join(t.TempDir(), "secrets.enc")
	store := secrets.NewFileStore(path, "a passphrase")
	require.NoError(t, store.Set("TOKEN", "a-very-distinctive-secret-value"))

	blob, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(blob), "a-very-distinctive-secret-value")
	require.NotContains(t, string(blob), "TOKEN")
}

func TestFileStore_IsOnlyReadableByItsOwner(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "secrets.enc")
	require.NoError(t, secrets.NewFileStore(path, "p").Set("TOKEN", "v"))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"a secret store readable by anybody else on the machine is not a fallback, it is a leak")
}

func TestFileStore_TheWrongPassphraseFails(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "secrets.enc")
	require.NoError(t, secrets.NewFileStore(path, "right").Set("TOKEN", "v"))

	_, err := secrets.NewFileStore(path, "wrong").Load()
	require.ErrorIs(t, err, secrets.ErrWrongPassphrase)
}

func TestFileStore_AnAlteredFileFailsRatherThanDecryptingToSomethingPlausible(t *testing.T) {
	t.Parallel()
	// Authenticated encryption, so a flipped bit is detected rather than
	// producing a value that looks real.
	path := filepath.Join(t.TempDir(), "secrets.enc")
	store := secrets.NewFileStore(path, "p")
	require.NoError(t, store.Set("TOKEN", "v"))

	blob, err := os.ReadFile(path)
	require.NoError(t, err)
	for i := range blob {
		if i < 6 {
			continue // leave the magic, so this tests the ciphertext
		}
		altered := append([]byte(nil), blob...)
		altered[i] ^= 0x01
		require.NoError(t, os.WriteFile(path, altered, 0o600))

		_, err := store.Load()
		require.Errorf(t, err, "flipping a bit at offset %d was not detected", i)
		if i > 40 {
			break // enough of the salt, nonce, and ciphertext to be convincing
		}
	}
}

func TestFileStore_TruncatingToTheHeaderIsDetected(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "secrets.enc")
	require.NoError(t, secrets.NewFileStore(path, "p").Set("TOKEN", "v"))
	require.NoError(t, os.WriteFile(path, []byte("AFSEC1"), 0o600))

	_, err := secrets.NewFileStore(path, "p").Load()
	require.Error(t, err)
}

func TestFileStore_AFileThatIsNotAStoreSaysSo(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "notes.txt")
	require.NoError(t, os.WriteFile(path, []byte("just some notes"), 0o600))

	_, err := secrets.NewFileStore(path, "p").Load()
	require.Error(t, err)
	require.Contains(t, err.Error(), "not an antifailure secret store")
}

func TestFileStore_TwoWritesOfTheSameValueProduceDifferentBytes(t *testing.T) {
	t.Parallel()
	// A fresh salt and nonce per write. Reusing a nonce with GCM loses
	// everything at once, and identical ciphertext would also leak that the
	// value did not change.
	dir := t.TempDir()
	a := filepath.Join(dir, "a.enc")
	b := filepath.Join(dir, "b.enc")
	require.NoError(t, secrets.NewFileStore(a, "p").Set("TOKEN", "same"))
	require.NoError(t, secrets.NewFileStore(b, "p").Set("TOKEN", "same"))

	first, err := os.ReadFile(a)
	require.NoError(t, err)
	second, err := os.ReadFile(b)
	require.NoError(t, err)
	require.NotEqual(t, first, second)
}

func TestFileStore_DeleteRemovesOneValue(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "secrets.enc")
	store := secrets.NewFileStore(path, "p")
	require.NoError(t, store.Set("A", "1"))
	require.NoError(t, store.Set("B", "2"))
	require.NoError(t, store.Delete("A"))

	names, err := store.Names()
	require.NoError(t, err)
	require.Equal(t, []string{"B"}, names)
}

func TestFileStore_AnAbsentFileIsAnEmptyStore(t *testing.T) {
	t.Parallel()
	store := secrets.NewFileStore(filepath.Join(t.TempDir(), "none.enc"), "p")
	values, err := store.Load()
	require.NoError(t, err)
	require.Empty(t, values)

	ok, why := store.Available(t.Context())
	require.False(t, ok)
	require.Empty(t, why, "no store yet is not a problem worth reporting")
}

func TestFileStore_WithNoPassphraseIsUnavailable(t *testing.T) {
	t.Parallel()
	store := secrets.NewFileStore(filepath.Join(t.TempDir(), "s.enc"), "")
	ok, why := store.Available(t.Context())
	require.False(t, ok)
	require.Contains(t, why, "passphrase")
}

// ---------------------------------------------------------------------------
// Manifest helpers
// ---------------------------------------------------------------------------

func TestDeclaredAndSandboxNames(t *testing.T) {
	t.Parallel()
	m := &schema.Manifest{
		Services: []schema.Service{
			{Name: "web", Env: []schema.EnvVar{{Name: "DATABASE_URL"}, {Name: "STRIPE_SECRET_KEY"}}},
			{Name: "worker", Env: []schema.EnvVar{{Name: "DATABASE_URL"}}},
		},
		Egress: &schema.Egress{Rules: []schema.EgressRule{
			{Host: "api.stripe.com", Mode: schema.ModeSandbox, Credential: "STRIPE_SECRET_KEY"},
			{Host: "files.stripe.com", Mode: schema.ModeSandbox, Credential: "STRIPE_SECRET_KEY"},
			{Host: "api.resend.com", Mode: schema.ModeCapture},
		}},
	}

	declared := secrets.DeclaredVars(m)
	require.Len(t, declared, 3, "every declaration is kept; deduplication happens at resolution")

	// One name even though two rules use it, because it is one credential.
	require.Equal(t, []string{"STRIPE_SECRET_KEY"}, secrets.SandboxNames(m))
	require.Nil(t, secrets.SandboxNames(nil))
	require.Nil(t, secrets.DeclaredVars(nil))
}

// ---------------------------------------------------------------------------
// The paths that only appear when something is wrong
// ---------------------------------------------------------------------------

func TestNewEnvSource_ReadsTheRealProcessEnvironment(t *testing.T) {
	// Not parallel: it sets a variable on the process.
	t.Setenv("AF_TEST_ENV_SOURCE", "present")
	src := secrets.NewEnvSource()
	require.Equal(t, "this shell's environment", src.Name())

	value, found, err := src.Lookup(t.Context(), "AF_TEST_ENV_SOURCE")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "present", value.Reveal())
}

func TestNewCISource_IsTheSameMechanismUnderADifferentName(t *testing.T) {
	t.Parallel()
	// A value injected by a CI platform and one exported by a person are worth
	// telling apart when somebody is reading an audit trail.
	src := secrets.NewCISource(func(string) (string, bool) { return "v", true })
	require.Equal(t, "the CI runner's variables", src.Name())

	value, found, err := src.Lookup(t.Context(), "ANY")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "v", value.Reveal())
}

func TestEnvSource_WithNoLabelStillNamesItself(t *testing.T) {
	t.Parallel()
	src := &secrets.EnvSource{Getenv: func(string) (string, bool) { return "", false }}
	require.Equal(t, "the process environment", src.Name())

	ok, _ := src.Available(t.Context())
	require.True(t, ok)
}

func TestEnvSource_WithNoLookupIsUnavailable(t *testing.T) {
	t.Parallel()
	ok, _ := (&secrets.EnvSource{}).Available(t.Context())
	require.False(t, ok)
}

func TestDotEnvSource_ReportsAFileItCannotOpen(t *testing.T) {
	t.Parallel()
	// A directory where a file was expected. Not "absent", which would be
	// silently ignored, because somebody who put a path in a configuration
	// wants to know it is wrong.
	dir := t.TempDir()
	src := secrets.NewDotEnvSource(dir)
	ok, why := src.Available(t.Context())
	require.False(t, ok)
	require.NotEmpty(t, why)

	_, _, err := src.Lookup(t.Context(), "ANY")
	require.Error(t, err)
}

func TestDotEnvSource_ReadsTheFileOnceHoweverManyLookups(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(path, []byte("A=1\nB=2\n"), 0o600))

	src := secrets.NewDotEnvSource(path)
	_, _, err := src.Lookup(t.Context(), "A")
	require.NoError(t, err)

	// Removed after the first read. A second lookup must still answer, which
	// proves it is not re-reading per variable.
	require.NoError(t, os.Remove(path))
	value, found, err := src.Lookup(t.Context(), "B")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "2", value.Reveal())
}

func TestParseDotEnv_UnescapesOnlyWhatItShould(t *testing.T) {
	t.Parallel()
	values, err := secrets.ParseDotEnv(strings.NewReader(
		`A="tab\there"` + "\n" +
			`B="return\rhere"` + "\n" +
			`C="unknown\qescape"` + "\n" +
			`D="trailing backslash\"` + "\n"))
	require.NoError(t, err)
	require.Equal(t, "tab\there", values["A"])
	require.Equal(t, "return\rhere", values["B"])
	// An escape nobody defined keeps the character rather than the backslash,
	// which is what every other implementation of this format does.
	require.Equal(t, "unknownqescape", values["C"])
	require.Equal(t, `trailing backslash\`, values["D"])
}

func TestParseDotEnv_RefusesANameStartingWithADigit(t *testing.T) {
	t.Parallel()
	_, err := secrets.ParseDotEnv(strings.NewReader("1INVALID=x\n"))
	require.Error(t, err)
}

func TestParseDotEnv_ReadsAVeryLongLine(t *testing.T) {
	t.Parallel()
	// A private key in a .env file is one long line, and the scanner's default
	// buffer is not always enough.
	long := strings.Repeat("k", 200_000)
	values, err := secrets.ParseDotEnv(strings.NewReader("KEY=" + long + "\n"))
	require.NoError(t, err)
	require.Equal(t, long, values["KEY"])
}

func TestKeyringSource_ReportsAnErrorThatIsNotAMissingEntry(t *testing.T) {
	t.Parallel()
	// Available probes once and caches, so a ring that starts working still has
	// to surface a per-lookup failure.
	ring := &erroringOnGet{}
	src := secrets.NewKeyringSource(ring, "af")
	ok, _ := src.Available(t.Context())
	require.True(t, ok, "the probe should have succeeded")

	ring.fail = true
	_, _, err := src.Lookup(t.Context(), "TOKEN")
	require.Error(t, err)
}

type erroringOnGet struct {
	fail bool
}

func (e *erroringOnGet) Get(string, string) (string, error) {
	if e.fail {
		return "", errors.New("the keychain went away")
	}
	return "", secrets.ErrNotFound
}
func (e *erroringOnGet) Set(string, string, string) error { return nil }
func (e *erroringOnGet) Delete(string, string) error      { return nil }

func TestKeyringSource_AMissingEntryIsNotFoundRatherThanAnError(t *testing.T) {
	t.Parallel()
	src := secrets.NewKeyringSource(&fakeKeyring{entries: map[string]string{}}, "af")
	_, found, err := src.Lookup(t.Context(), "ABSENT")
	require.NoError(t, err)
	require.False(t, found)
}

func TestFileStore_ReportsAPathItCannotWrite(t *testing.T) {
	t.Parallel()
	// A directory where the file should be, so the rename fails rather than
	// the write.
	dir := t.TempDir()
	blocked := filepath.Join(dir, "secrets.enc")
	require.NoError(t, os.MkdirAll(blocked, 0o700))

	require.Error(t, secrets.NewFileStore(blocked, "p").Set("A", "1"))
}

func TestFileStore_ReportsAPathItCannotRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "asdir"), 0o700))

	store := secrets.NewFileStore(filepath.Join(dir, "asdir"), "p")
	_, err := store.Load()
	require.Error(t, err)

	ok, why := store.Available(t.Context())
	require.False(t, ok)
	require.NotEmpty(t, why)

	_, err = store.Names()
	require.Error(t, err)
	require.Error(t, store.Delete("A"))
	_, _, err = store.Lookup(t.Context(), "A")
	require.Error(t, err)
}
