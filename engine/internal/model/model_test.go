package model_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/model"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// fakeRing is an in-memory credential store.
//
// Every test here uses one. The real implementation on macOS runs the security
// command against the login keychain, so a test that reached the system ring
// would write a secret into the developer's own keychain and leave it there.
type fakeRing struct {
	values map[string]string
	// unavailable models a machine with no credential store, which is a Linux
	// server without libsecret, a container, and every platform this has no
	// implementation for. It is a case worth having a test for rather than one
	// that only exists on somebody else's laptop.
	unavailable bool
}

func newRing() *fakeRing { return &fakeRing{values: map[string]string{}} }

func (r *fakeRing) Get(service, name string) (string, error) {
	if r.unavailable {
		return "", secrets.ErrKeyringUnavailable
	}
	v, ok := r.values[service+"/"+name]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}

func (r *fakeRing) Set(service, name, value string) error {
	if r.unavailable {
		return secrets.ErrKeyringUnavailable
	}
	r.values[service+"/"+name] = value
	return nil
}

func (r *fakeRing) Delete(service, name string) error {
	if r.unavailable {
		return secrets.ErrKeyringUnavailable
	}
	key := service + "/" + name
	if _, ok := r.values[key]; !ok {
		return secrets.ErrNotFound
	}
	delete(r.values, key)
	return nil
}

func chainFor(root string, env map[string]string, ring secrets.Keyring) *secrets.Chain {
	return secrets.LocalChain(root, func(k string) string { return env[k] }, nil, ring)
}

// No key is the normal case and must not be an error, because the deterministic
// planner handles it and every caller would otherwise treat a supported mode as
// a failure.
func TestResolve_NoKeyIsNotAnError(t *testing.T) {
	t.Parallel()
	cfg, err := model.Resolve(context.Background(),
		chainFor(t.TempDir(), nil, newRing()))
	require.NoError(t, err)
	require.Nil(t, cfg)
}

func TestResolve_ReadsTheEnvironment(t *testing.T) {
	t.Parallel()
	cfg, err := model.Resolve(context.Background(), chainFor(t.TempDir(),
		map[string]string{"ANTHROPIC_API_KEY": "sk-ant-one"}, newRing()))
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, "anthropic", cfg.Provider.Name)
	require.Equal(t, "claude-sonnet-5", cfg.Model)
	require.Equal(t, "https://api.anthropic.com", cfg.BaseURL)
	require.Equal(t, "this shell's environment", cfg.Source)
	require.False(t, cfg.Custom())
}

// The precedence rule, in both orders. A key set by an export and a key set by
// 'af model set' can both be present, one of them has to win, and which one is
// not something a person should have to discover from a run.
func TestResolve_PrecedenceBothOrders(t *testing.T) {
	t.Parallel()

	t.Run("the environment beats the keyring", func(t *testing.T) {
		t.Parallel()
		ring := newRing()
		require.NoError(t, ring.Set(secrets.DefaultKeyringService, "ANTHROPIC_API_KEY", "sk-from-ring"))

		cfg, err := model.Resolve(context.Background(), chainFor(t.TempDir(),
			map[string]string{"ANTHROPIC_API_KEY": "sk-from-shell"}, ring))
		require.NoError(t, err)
		require.Equal(t, "sk-from-shell", cfg.Key.Reveal())
		require.Equal(t, "this shell's environment", cfg.Source)
	})

	t.Run("the keyring answers when the environment does not", func(t *testing.T) {
		t.Parallel()
		ring := newRing()
		require.NoError(t, ring.Set(secrets.DefaultKeyringService, "ANTHROPIC_API_KEY", "sk-from-ring"))

		cfg, err := model.Resolve(context.Background(), chainFor(t.TempDir(), nil, ring))
		require.NoError(t, err)
		require.Equal(t, "sk-from-ring", cfg.Key.Reveal())
		require.Equal(t, "the system keyring", cfg.Source)
	})

	t.Run("a dotenv file sits between them", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"),
			[]byte("ANTHROPIC_API_KEY=sk-from-dotenv\n"), 0o600))
		ring := newRing()
		require.NoError(t, ring.Set(secrets.DefaultKeyringService, "ANTHROPIC_API_KEY", "sk-from-ring"))

		cfg, err := model.Resolve(context.Background(), chainFor(dir, nil, ring))
		require.NoError(t, err)
		require.Equal(t, "sk-from-dotenv", cfg.Key.Reveal())

		// And the export still beats the file.
		cfg, err = model.Resolve(context.Background(), chainFor(dir,
			map[string]string{"ANTHROPIC_API_KEY": "sk-from-shell"}, ring))
		require.NoError(t, err)
		require.Equal(t, "sk-from-shell", cfg.Key.Reveal())
	})
}

// With both providers set one has to win, and it is Anthropic, in whichever
// source answers. The runner has always preferred Anthropic and the sidecar
// picks in the same order, so this is the ordering the rest of the product
// already implements rather than a new opinion.
func TestResolve_AnthropicWinsAcrossProviders(t *testing.T) {
	t.Parallel()
	ring := newRing()
	require.NoError(t, ring.Set(secrets.DefaultKeyringService, "ANTHROPIC_API_KEY", "sk-ant"))

	// OpenAI in the highest priority source, Anthropic in the lowest. The
	// provider order wins, not the source order, because a rule that mixed the
	// two dimensions would be impossible to predict.
	cfg, err := model.Resolve(context.Background(), chainFor(t.TempDir(),
		map[string]string{"OPENAI_API_KEY": "sk-oai"}, ring))
	require.NoError(t, err)
	require.Equal(t, "anthropic", cfg.Provider.Name)
	require.Equal(t, "the system keyring", cfg.Source)
}

// An empty key is treated as absent. A variable exported as the empty string is
// a common way to "unset" one in CI, and reporting a key that cannot
// authenticate anything would be worse than falling through.
func TestResolve_EmptyKeyFallsThrough(t *testing.T) {
	t.Parallel()
	cfg, err := model.Resolve(context.Background(), chainFor(t.TempDir(),
		map[string]string{"ANTHROPIC_API_KEY": "  ", "OPENAI_API_KEY": "sk-oai"}, newRing()))
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, "openai", cfg.Provider.Name)
}

func TestResolve_ModelAndBaseURLOverride(t *testing.T) {
	t.Parallel()
	cfg, err := model.Resolve(context.Background(), chainFor(t.TempDir(), map[string]string{
		"ANTHROPIC_API_KEY":  "sk-ant",
		"AF_MODEL":           "claude-opus-5",
		"ANTHROPIC_BASE_URL": "http://127.0.0.1:11434",
	}, newRing()))
	require.NoError(t, err)
	require.Equal(t, "claude-opus-5", cfg.Model)
	require.Equal(t, "http://127.0.0.1:11434", cfg.BaseURL)
	require.True(t, cfg.Custom())
	require.Equal(t, "http://127.0.0.1:11434/v1/messages", cfg.Endpoint())
}

// The base URL is passed to a subprocess only when it is not the provider's
// own. Passing the default would be harmless today and would silently pin the
// endpoint against a release that changes it.
func TestEnvironment_OmitsTheDefaultBaseURL(t *testing.T) {
	t.Parallel()

	plain, err := model.Resolve(context.Background(), chainFor(t.TempDir(),
		map[string]string{"ANTHROPIC_API_KEY": "sk-ant"}, newRing()))
	require.NoError(t, err)
	require.Equal(t,
		[]string{"ANTHROPIC_API_KEY=sk-ant", "AF_MODEL=claude-sonnet-5"},
		plain.Environment())

	custom, err := model.Resolve(context.Background(), chainFor(t.TempDir(), map[string]string{
		"ANTHROPIC_API_KEY":  "sk-ant",
		"ANTHROPIC_BASE_URL": "http://127.0.0.1:8080",
	}, newRing()))
	require.NoError(t, err)
	require.Contains(t, custom.Environment(), "ANTHROPIC_BASE_URL=http://127.0.0.1:8080")
}

// ---------------------------------------------------------------------------
// Storing and removing
// ---------------------------------------------------------------------------

func TestStore_PrefersTheKeyring(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ring := newRing()

	where, err := model.Store(dir, func(string) string { return "" },
		ring, model.Providers[0], "sk-stored")
	require.NoError(t, err)
	require.Equal(t, "the system keyring", where)

	cfg, err := model.Resolve(context.Background(), chainFor(dir, nil, ring))
	require.NoError(t, err)
	require.Equal(t, "sk-stored", cfg.Key.Reveal())
}

// The fallback for a Linux server without libsecret, a container, and every
// platform with no credential store. It needs a passphrase and deliberately has
// no default, so with neither there is nowhere to write and that is reported
// rather than written to a file that only looks encrypted.
func TestStore_FallsBackToTheEncryptedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ring := &fakeRing{unavailable: true}
	env := map[string]string{"AF_SECRET_PASSPHRASE": "correct horse battery staple"}
	getenv := func(k string) string { return env[k] }

	where, err := model.Store(dir, getenv, ring, model.Providers[0], "sk-in-a-file")
	require.NoError(t, err)
	require.Equal(t, "the encrypted local store", where)

	cfg, err := model.Resolve(context.Background(), chainFor(dir, env, ring))
	require.NoError(t, err)
	require.Equal(t, "sk-in-a-file", cfg.Key.Reveal())
	require.Equal(t, "the encrypted local store", cfg.Source)

	// The bytes on disk must not contain the key. This is the whole claim the
	// encrypted store makes and it is cheap to check.
	raw, err := os.ReadFile(filepath.Join(dir, ".antifailure", "secrets.enc"))
	require.NoError(t, err)
	require.NotContains(t, string(raw), "sk-in-a-file")
}

func TestStore_NowhereToWriteIsReported(t *testing.T) {
	t.Parallel()
	_, err := model.Store(t.TempDir(), func(string) string { return "" },
		&fakeRing{unavailable: true}, model.Providers[0], "sk-nowhere")
	require.ErrorIs(t, err, model.ErrNoStore)
}

// Remove clears every place this can write, not the first that answers. A key
// left in the encrypted store after the keyring entry was removed is a key the
// next run silently uses, which is the exact failure somebody typing this is
// trying to prevent.
func TestRemove_ClearsBothStores(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ring := newRing()
	env := map[string]string{"AF_SECRET_PASSPHRASE": "a passphrase"}
	getenv := func(k string) string { return env[k] }

	require.NoError(t, ring.Set(secrets.DefaultKeyringService, "ANTHROPIC_API_KEY", "sk-ring"))
	require.NoError(t, secrets.NewFileStore(
		filepath.Join(dir, ".antifailure", "secrets.enc"), "a passphrase").
		Set("ANTHROPIC_API_KEY", "sk-file"))

	removed, err := model.Remove(dir, getenv, ring, model.Providers[0])
	require.NoError(t, err)
	require.ElementsMatch(t,
		[]string{"the system keyring", "the encrypted local store"}, removed)

	cfg, err := model.Resolve(context.Background(), chainFor(dir, env, ring))
	require.NoError(t, err)
	require.Nil(t, cfg, "a key survived removal in one of the two stores")
}

// A retry after a timeout must not report failure for reaching the state the
// caller asked for.
func TestRemove_MissingIsNotAnError(t *testing.T) {
	t.Parallel()
	removed, err := model.Remove(t.TempDir(), func(string) string { return "" },
		newRing(), model.Providers[0])
	require.NoError(t, err)
	require.Empty(t, removed)
}

// ---------------------------------------------------------------------------
// The verification record
// ---------------------------------------------------------------------------

// A record is about one key. Rotating the key must discard it, or the previous
// key's success stays on screen beside the new one, in exactly the situation
// where somebody is looking at that screen to find out whether a rotation
// worked.
func TestRecord_IgnoredWhenTheKeyChanged(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

	cfg, err := model.Resolve(context.Background(), chainFor(dir,
		map[string]string{"ANTHROPIC_API_KEY": "sk-first"}, newRing()))
	require.NoError(t, err)
	require.NoError(t, model.WriteRecord(dir, *cfg, at))

	require.NotNil(t, model.ReadRecord(dir, cfg.Fingerprint))
	require.Equal(t, at, model.ReadRecord(dir, cfg.Fingerprint).VerifiedAt)

	rotated, err := model.Resolve(context.Background(), chainFor(dir,
		map[string]string{"ANTHROPIC_API_KEY": "sk-second"}, newRing()))
	require.NoError(t, err)
	require.NotEqual(t, cfg.Fingerprint, rotated.Fingerprint)
	require.Nil(t, model.ReadRecord(dir, rotated.Fingerprint),
		"the previous key's verification was reported for a different key")
}

// The record is a note to a person, not state anything depends on. Every way it
// can be broken has to read as "not verified" rather than as an error, because
// there is no state this file can be in that should stop somebody finding out
// what their key is.
func TestRecord_UnreadableReadsAsNotVerified(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".antifailure"), 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, ".antifailure", "model-verified.json"),
		[]byte("{ this is not json"), 0o600))
	require.Nil(t, model.ReadRecord(dir, "anything"))
}

// The record holds a fingerprint and never a key, because it is a file that
// ends up in a support bundle and on somebody's screen.
func TestRecord_HoldsNoKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg, err := model.Resolve(context.Background(), chainFor(dir,
		map[string]string{"ANTHROPIC_API_KEY": "sk-ant-secret-value"}, newRing()))
	require.NoError(t, err)
	require.NoError(t, model.WriteRecord(dir, *cfg, time.Unix(0, 0)))

	raw, err := os.ReadFile(filepath.Join(dir, ".antifailure", "model-verified.json"))
	require.NoError(t, err)
	require.NotContains(t, string(raw), "sk-ant-secret-value")
	require.Contains(t, string(raw), cfg.Fingerprint)
}

func TestLookup_RejectsAnUnknownProvider(t *testing.T) {
	t.Parallel()
	_, ok := model.Lookup("gemini")
	require.False(t, ok)

	p, ok := model.Lookup("  ANTHROPIC ")
	require.True(t, ok, "the provider name is matched case insensitively and trimmed")
	require.Equal(t, "anthropic", p.Name)
	require.Equal(t, []string{"anthropic", "openai"}, model.Names())
	require.False(t, strings.Contains(strings.Join(model.Names(), ","), " "))
}
