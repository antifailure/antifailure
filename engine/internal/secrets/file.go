package secrets

// An encrypted file, for machines with no keyring.
//
// Linux servers, containers, and headless CI runners have no credential store,
// and the alternative to this is telling people to export secrets in a shell
// profile, which is a plaintext file with worse permissions and no audit trail.
//
// What this is not: a secret manager. It is a local fallback with one job,
// which is to be less bad than a plaintext file. The passphrase comes from the
// keyring when there is one and from the environment when there is not, and
// when it comes from the environment the honest description is "obfuscated at
// rest", which the docs say rather than implying otherwise.
//
// The construction is standard on purpose. Argon2id for the passphrase, because
// a passphrase is low entropy and a fast hash would make the file a dictionary
// attack. AES-256-GCM for the contents, so a modified file fails to open rather
// than decrypting to something plausible. A random salt and nonce per write,
// stored beside the ciphertext, because reusing a nonce with GCM loses
// everything at once.

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"golang.org/x/crypto/argon2"
)

const (
	fileMagic   = "AFSEC1"
	saltSize    = 16
	nonceSize   = 12
	keySize     = 32
	argonTime   = 3
	argonMemory = 64 * 1024 // 64 MiB
	argonLanes  = 4
)

// ErrWrongPassphrase reports that a file could not be opened.
//
// Deliberately one error for a wrong passphrase and for a corrupted file. They
// are indistinguishable to the caller by construction, because authenticated
// encryption cannot tell them apart, and pretending otherwise would mean
// guessing.
var ErrWrongPassphrase = errors.New("the passphrase is wrong, or the file has been altered")

// FileStore is an encrypted map of names to values.
type FileStore struct {
	path       string
	passphrase []byte
}

// NewFileStore opens a store at a path.
func NewFileStore(path string, passphrase string) *FileStore {
	return &FileStore{path: path, passphrase: []byte(passphrase)}
}

// Name identifies the store in audit records.
func (f *FileStore) Name() string { return "the encrypted local store" }

// Available reports whether the file can be used.
func (f *FileStore) Available(ctx context.Context) (bool, string) {
	if len(f.passphrase) == 0 {
		return false, "no passphrase is set"
	}
	info, err := os.Stat(f.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Not an error: there is simply nothing stored yet.
			return false, ""
		}
		return false, err.Error()
	}
	// A stat succeeds on a directory, so checking only the error reports a
	// directory as a usable store and then fails on the first read.
	if !info.Mode().IsRegular() {
		return false, fmt.Sprintf("%s is not a file", f.path)
	}
	return true, ""
}

// Lookup reads one value.
func (f *FileStore) Lookup(_ context.Context, name string) (Value, bool, error) {
	values, err := f.Load()
	if err != nil {
		return Value{}, false, err
	}
	raw, ok := values[name]
	if !ok {
		return Value{}, false, nil
	}
	return NewFrom(raw, f.Name()), true, nil
}

// Load decrypts the whole file.
func (f *FileStore) Load() (map[string]string, error) {
	blob, err := os.ReadFile(f.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, err
	}

	header := len(fileMagic) + saltSize + nonceSize
	if len(blob) < header || string(blob[:len(fileMagic)]) != fileMagic {
		return nil, fmt.Errorf("%s is not an antifailure secret store", f.path)
	}
	salt := blob[len(fileMagic) : len(fileMagic)+saltSize]
	nonce := blob[len(fileMagic)+saltSize : header]
	ciphertext := blob[header:]

	gcm, err := f.cipher(salt)
	if err != nil {
		return nil, err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(fileMagic))
	if err != nil {
		return nil, ErrWrongPassphrase
	}

	var values map[string]string
	if err := json.Unmarshal(plaintext, &values); err != nil {
		return nil, fmt.Errorf("the store decrypted but is not readable: %w", err)
	}
	return values, nil
}

// Save writes the whole file.
//
// Written to a temporary file and renamed, so that an interrupted write leaves
// the previous store intact rather than a truncated one. A half-written secret
// store is unrecoverable, and there is no second copy.
func (f *FileStore) Save(values map[string]string) error {
	plaintext, err := json.Marshal(values)
	if err != nil {
		return err
	}

	salt := make([]byte, saltSize)
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	if _, err := rand.Read(nonce); err != nil {
		return err
	}

	gcm, err := f.cipher(salt)
	if err != nil {
		return err
	}
	// The magic is authenticated as additional data, so a file cannot be
	// truncated to its header and still open.
	ciphertext := gcm.Seal(nil, nonce, plaintext, []byte(fileMagic))

	blob := make([]byte, 0, len(fileMagic)+saltSize+nonceSize+len(ciphertext))
	blob = append(blob, fileMagic...)
	blob = append(blob, salt...)
	blob = append(blob, nonce...)
	blob = append(blob, ciphertext...)

	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	// 0600 before anything is written to it, not after. Creating a file
	// readable and then narrowing it is a window, and it is the only window
	// that matters here.
	if err := os.WriteFile(tmp, blob, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, f.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Set stores one value.
func (f *FileStore) Set(name, value string) error {
	values, err := f.Load()
	if err != nil {
		return err
	}
	values[name] = value
	return f.Save(values)
}

// Delete removes one value.
func (f *FileStore) Delete(name string) error {
	values, err := f.Load()
	if err != nil {
		return err
	}
	delete(values, name)
	return f.Save(values)
}

// Names lists what is stored, sorted. Names only, which is the whole point.
func (f *FileStore) Names() ([]string, error) {
	values, err := f.Load()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(values))
	for name := range values {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

func (f *FileStore) cipher(salt []byte) (cipher.AEAD, error) {
	key := argon2.IDKey(f.passphrase, salt, argonTime, argonMemory, argonLanes, keySize)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
