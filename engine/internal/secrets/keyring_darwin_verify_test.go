//go:build darwin

package secrets

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// truncatingStore behaves the way the keychain did through the prompt route:
// it accepts any write and keeps the first 128 bytes.
type truncatingStore struct {
	held    map[string]string
	deleted []string
}

func (s *truncatingStore) write(service, name, value string) {
	if len(value) > 128 {
		value = value[:128]
	}
	s.held[service+"/"+name] = value
}

func (s *truncatingStore) Get(service, name string) (string, error) {
	v, ok := s.held[service+"/"+name]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (s *truncatingStore) Delete(service, name string) error {
	delete(s.held, service+"/"+name)
	s.deleted = append(s.deleted, service+"/"+name)
	return nil
}

// A write that the store shortened is an error, however the store reported it.
//
// This is the check that would have caught the prompt route on the day it
// shipped: security exited 0, and only reading the entry back shows that 128
// bytes of a 300 byte credential are all that arrived.
func TestAWriteTheStoreShortenedIsAnErrorThatNamesTheEntryAndNotTheValue(t *testing.T) {
	store := &truncatingStore{held: map[string]string{}}
	const service, name = "antifailure", "cli:https://app.antifailure.dev"
	value := `{"control_plane":"https://app.antifailure.dev","token":"afu_` +
		strings.Repeat("Q", 43) + `","login":"someone","scopes":["runs.read"],` +
		`"stored_at":"2026-09-13T16:00:00Z","padding":"` + strings.Repeat("p", 150) + `"}`

	store.write(service, name, value)
	err := verifyStored(store, service, name, value)

	require.Error(t, err, "a truncated write was accepted")
	require.Contains(t, err.Error(), service, "the error does not say which store")
	require.Contains(t, err.Error(), name, "the error does not say which entry")
	require.NotContains(t, err.Error(), "afu_", "the error printed the secret")
	require.Equal(t, []string{service + "/" + name}, store.deleted,
		"the wrong value was left in the keychain, where every later read finds it first")
}

// And a faithful write passes, or the check would refuse every login.
func TestAWriteTheStoreKeptWholeIsAccepted(t *testing.T) {
	store := &truncatingStore{held: map[string]string{}}
	value := strings.Repeat("k", 128)
	store.write("antifailure", "model", value)
	require.NoError(t, verifyStored(store, "antifailure", "model", value))
	require.Empty(t, store.deleted)
}
