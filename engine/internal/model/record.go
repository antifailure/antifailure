package model

// What was proven to work, and when.
//
// "Is my key configured" and "does my key work" are different questions and
// only the second one is worth much. A key can be present, well formed, from
// the right provider, and revoked; a base URL can be set to a local model that
// is not running. So `af model test` makes one real call and writes down that
// it succeeded, and `af model show` reports it.
//
// The record is checked against the key it claims to be about. It carries the
// fingerprint of the key that was verified, and a record whose fingerprint does
// not match the key resolved now is ignored rather than shown. Otherwise
// rotating a key would leave the previous key's success on screen beside the
// new one, which is a lie in exactly the situation where somebody is looking at
// this screen to find out whether a rotation worked.
//
// It is not a secret and it is not a cache: nothing reads it to decide whether
// to make a call. It is a note to the person.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Record is what `af model test` last proved.
type Record struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"base_url"`
	// Fingerprint identifies the key that was verified, never the key.
	Fingerprint string    `json:"fingerprint"`
	VerifiedAt  time.Time `json:"verified_at"`
}

func storePath(root string) string {
	return filepath.Join(root, ".antifailure", "secrets.enc")
}

func recordPath(root string) string {
	return filepath.Join(root, ".antifailure", "model-verified.json")
}

// ReadRecord returns the last verification for a key, or nothing.
//
// A missing, unreadable or mismatched record all return nothing rather than an
// error. There is no state this file can be in that should stop somebody
// finding out what their key is: the worst case is that `af model show` says
// the key has not been verified, which is exactly what an unreadable record
// means.
func ReadRecord(root, fingerprint string) *Record {
	raw, err := os.ReadFile(recordPath(root))
	if err != nil {
		return nil
	}
	var rec Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil
	}
	if rec.Fingerprint == "" || rec.Fingerprint != fingerprint {
		return nil
	}
	return &rec
}

// WriteRecord notes that a configuration was proven to work.
//
// The directory is created because this is very often the first thing written
// into it: somebody who has cloned a repository and pasted a key has not
// necessarily run `af init` yet, and failing here would turn a successful model
// call into a reported failure.
func WriteRecord(root string, cfg Config, at time.Time) error {
	dir := filepath.Dir(recordPath(root))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	raw, err := json.MarshalIndent(Record{
		Provider:    cfg.Provider.Name,
		Model:       cfg.Model,
		BaseURL:     cfg.BaseURL,
		Fingerprint: cfg.Fingerprint,
		VerifiedAt:  at.UTC(),
	}, "", "  ")
	if err != nil {
		return err
	}
	// 0600 rather than 0644, even though this holds no secret. The file sits
	// beside the encrypted store, it names an endpoint somebody chose, and a
	// permission that has to be argued about every time somebody reads the
	// directory listing is worse than one that is uniformly tight.
	if err := os.WriteFile(recordPath(root), append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return nil
}

// ForgetRecord removes the verification note, if there is one.
func ForgetRecord(root string) error {
	err := os.Remove(recordPath(root))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
