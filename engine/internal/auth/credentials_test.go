package auth_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/auth"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// fakeRing is an in-memory keyring, so these tests never touch a real one.
//
// A test that writes to the developer's keychain either prompts them or fails
// on a headless runner, and one that does it in CI leaves a credential behind
// on a shared machine.
type fakeRing struct {
	items     map[string]string
	failWrite bool
	absent    bool
}

func newFakeRing() *fakeRing { return &fakeRing{items: map[string]string{}} }

func (f *fakeRing) Get(service, name string) (string, error) {
	if f.absent {
		return "", secrets.ErrKeyringUnavailable
	}
	v, ok := f.items[service+"/"+name]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}

func (f *fakeRing) Set(service, name, value string) error {
	if f.failWrite {
		return secrets.ErrKeyringUnavailable
	}
	f.items[service+"/"+name] = value
	return nil
}

func (f *fakeRing) Delete(service, name string) error {
	key := service + "/" + name
	if _, ok := f.items[key]; !ok {
		return secrets.ErrNotFound
	}
	delete(f.items, key)
	return nil
}

func TestRoundTripsThroughTheKeyring(t *testing.T) {
	ring := newFakeRing()
	store := &auth.Store{Ring: ring, Dir: t.TempDir()}

	want := auth.Credential{
		ControlPlane: "https://app.dev.antifailure.dev",
		Token:        "afu_abcdefghijklmnop",
		Login:        "somebody",
		Organization: "antifailure",
		Scopes:       []string{"environments.view"},
	}
	require.NoError(t, store.Save(want))

	got, err := store.Load(want.ControlPlane)
	require.NoError(t, err)
	require.Equal(t, want.Token, got.Token)
	require.Equal(t, want.Login, got.Login)
	require.True(t, store.UsesKeyring())

	// And nothing was written to disk, because the keyring took it.
	entries, err := os.ReadDir(store.Dir)
	if err == nil {
		require.Empty(t, entries, "the keyring accepted the credential and a file was written anyway")
	}
}

// The fallback matters more than the happy path: internal/secrets has a system
// keyring on darwin and nil everywhere else, so on Linux and Windows this file
// IS the credential store today.
func TestFallsBackToAFileWhenThereIsNoKeyring(t *testing.T) {
	dir := t.TempDir()
	store := &auth.Store{Ring: nil, Dir: dir}

	require.False(t, store.UsesKeyring())
	require.NoError(t, store.Save(auth.Credential{
		ControlPlane: "https://app.dev.antifailure.dev",
		Token:        "afu_fallback",
	}))

	got, err := store.Load("https://app.dev.antifailure.dev")
	require.NoError(t, err)
	require.Equal(t, "afu_fallback", got.Token)
}

func TestTheFallbackFileIsNotReadableByAnybodyElse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes do not mean the same thing on Windows")
	}
	dir := t.TempDir()
	store := &auth.Store{Ring: nil, Dir: dir}
	require.NoError(t, store.Save(auth.Credential{
		ControlPlane: "https://app.dev.antifailure.dev",
		Token:        "afu_private",
	}))

	var found string
	require.NoError(t, filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(p) == ".json" {
			found = p
			// 0600 and nothing wider. This file is the whole credential on
			// every platform without a keyring, so group or world readable
			// here is the credential being readable by every other account on
			// the machine.
			require.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
				"the credential file is readable by somebody other than its owner")
		}
		return nil
	}))
	require.NotEmpty(t, found, "no credential file was written")

	// The directory too. A world-readable directory does not expose the file's
	// contents, but it does expose which control planes somebody uses.
	info, err := os.Stat(filepath.Dir(found))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

// A keyring that exists and refuses is a real state: a locked keychain, a
// headless session with no D-Bus. The credential must still be stored rather
// than the login failing at the last step, after the person already approved it
// in a browser.
func TestAKeyringThatRefusesFallsBackRatherThanFailing(t *testing.T) {
	ring := newFakeRing()
	ring.failWrite = true
	store := &auth.Store{Ring: ring, Dir: t.TempDir()}

	require.NoError(t, store.Save(auth.Credential{
		ControlPlane: "https://app.dev.antifailure.dev",
		Token:        "afu_refused",
	}))
	got, err := store.Load("https://app.dev.antifailure.dev")
	require.NoError(t, err)
	require.Equal(t, "afu_refused", got.Token)
	// And it now reports the file, so af login tells the truth about where the
	// token went.
	require.False(t, store.UsesKeyring())
}

// af logout has to leave nothing. A machine can hold both a keyring entry and a
// file, if one login happened before the keyring worked, and clearing only the
// first would leave a working credential behind on exactly the machine somebody
// is trying to clean.
func TestLogoutClearsBothStores(t *testing.T) {
	ring := newFakeRing()
	dir := t.TempDir()
	const origin = "https://app.dev.antifailure.dev"

	// A file, written while there was no keyring.
	fileOnly := &auth.Store{Ring: nil, Dir: dir}
	require.NoError(t, fileOnly.Save(auth.Credential{ControlPlane: origin, Token: "afu_old"}))

	// And a keyring entry, written afterwards.
	both := &auth.Store{Ring: ring, Dir: dir}
	require.NoError(t, both.Save(auth.Credential{ControlPlane: origin, Token: "afu_new"}))

	removed, err := both.Delete(origin)
	require.NoError(t, err)
	require.True(t, removed)

	_, err = both.Load(origin)
	require.ErrorIs(t, err, auth.ErrNotSignedIn, "a credential survived af logout")

	// Explicitly: the file is gone too, not merely shadowed.
	_, err = fileOnly.Load(origin)
	require.ErrorIs(t, err, auth.ErrNotSignedIn)
}

func TestNotSignedInIsDistinctFromAnError(t *testing.T) {
	store := &auth.Store{Ring: newFakeRing(), Dir: t.TempDir()}
	_, err := store.Load("https://nothing.stored.here")
	require.ErrorIs(t, err, auth.ErrNotSignedIn)
}

// One entry per control plane, so signing in to staging does not sign you out
// of production.
func TestControlPlanesAreSeparateEntries(t *testing.T) {
	store := &auth.Store{Ring: newFakeRing(), Dir: t.TempDir()}
	require.NoError(t, store.Save(auth.Credential{ControlPlane: "https://a.example", Token: "afu_a"}))
	require.NoError(t, store.Save(auth.Credential{ControlPlane: "https://b.example", Token: "afu_b"}))

	a, err := store.Load("https://a.example")
	require.NoError(t, err)
	b, err := store.Load("https://b.example")
	require.NoError(t, err)
	require.Equal(t, "afu_a", a.Token)
	require.Equal(t, "afu_b", b.Token)

	_, err = store.Delete("https://a.example")
	require.NoError(t, err)
	_, err = store.Load("https://a.example")
	require.ErrorIs(t, err, auth.ErrNotSignedIn)
	// b survives.
	b, err = store.Load("https://b.example")
	require.NoError(t, err)
	require.Equal(t, "afu_b", b.Token)
}

// The same control plane written three ways is one entry, or somebody signs in
// and af whoami says they are not signed in.
func TestOriginsAreNormalised(t *testing.T) {
	require.Equal(t, "https://app.dev.antifailure.dev", auth.Normalise("https://app.dev.antifailure.dev/"))
	require.Equal(t, "https://app.dev.antifailure.dev", auth.Normalise("https://APP.DEV.ANTIFAILURE.DEV"))
	require.Equal(t, "https://app.dev.antifailure.dev", auth.Normalise("  https://app.dev.antifailure.dev/  "))
	// A path is not part of the origin: a token is for a host, not for a page.
	require.Equal(t, "https://app.dev.antifailure.dev", auth.Normalise("https://app.dev.antifailure.dev/device?code=X"))

	store := &auth.Store{Ring: newFakeRing(), Dir: t.TempDir()}
	require.NoError(t, store.Save(auth.Credential{
		ControlPlane: auth.Normalise("https://app.dev.antifailure.dev/"),
		Token:        "afu_norm",
	}))
	got, err := store.Load("https://APP.dev.antifailure.dev")
	require.NoError(t, err)
	require.Equal(t, "afu_norm", got.Token)
}

// A control plane name cannot escape the credentials directory. It arrives from
// a flag or an environment variable, so it is attacker-influenced on a shared
// machine.
func TestAHostileControlPlaneNameCannotEscapeTheDirectory(t *testing.T) {
	dir := t.TempDir()
	store := &auth.Store{Ring: nil, Dir: dir}
	require.NoError(t, store.Save(auth.Credential{
		ControlPlane: "https://../../../../etc/passwd",
		Token:        "afu_escape",
	}))

	// Everything written stays under dir.
	var outside []string
	require.NoError(t, filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil || len(rel) > 1 && rel[0] == '.' && rel[1] == '.' {
			outside = append(outside, p)
		}
		return nil
	}))
	require.Empty(t, outside)
}

func TestExpiry(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)

	// A zero expiry never expires. That is how a token minted without one is
	// treated, and treating it as already expired would make such a token
	// unusable rather than permanent.
	require.False(t, auth.Credential{}.Expired(now))

	require.True(t, auth.Credential{ExpiresAt: now.Add(-time.Second)}.Expired(now))
	require.False(t, auth.Credential{ExpiresAt: now.Add(time.Second)}.Expired(now))
	// Exactly at the boundary is expired, not valid.
	require.True(t, auth.Credential{ExpiresAt: now}.Expired(now))
}

func TestSaveRefusesAnIncompleteCredential(t *testing.T) {
	store := &auth.Store{Ring: newFakeRing(), Dir: t.TempDir()}
	require.Error(t, store.Save(auth.Credential{Token: "afu_x"}), "saved a credential with no control plane")
	require.Error(t, store.Save(auth.Credential{ControlPlane: "https://a.example"}), "saved a credential with no token")
}

func TestLocationNamesWhereTheTokenActuallyIs(t *testing.T) {
	// The user is told which of the two happened, because the difference is
	// whether the operating system is protecting the credential or only the
	// file mode is.
	withRing := &auth.Store{Ring: newFakeRing(), Dir: t.TempDir()}
	require.Contains(t, withRing.Location("https://a.example"), "keyring")

	dir := t.TempDir()
	withoutRing := &auth.Store{Ring: nil, Dir: dir}
	require.Contains(t, withoutRing.Location("https://a.example"), dir)
}

var _ = errors.Is
