// Where the token `af login` receives is kept.
//
// The operating system's credential store when there is one, and a file with
// mode 0600 when there is not. Both are implemented here rather than only the
// first, because `internal/secrets` has a system keyring on darwin and nil
// everywhere else, and a login command that worked on one developer's laptop
// and not on a Linux CI box would be worse than one that is honest about where
// it put the credential.
//
// The fallback is not silent. Store.Location() says which was used, `af login`
// prints it, and `af doctor` can read it. A credential in a file the user does
// not know about is the thing that ends up committed.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// service namespaces entries so two tools on one machine cannot collide.
const service = "antifailure"

// ErrNotSignedIn is returned when there is no credential for a control plane.
var ErrNotSignedIn = errors.New("not signed in")

// Credential is what a successful login leaves behind.
//
// The token and nothing else that is secret. The rest is here so that
// `af whoami` can answer without a round trip when the network is down, and so
// that a stale entry can be recognised rather than merely failing.
type Credential struct {
	// ControlPlane is the origin the token is for. A token is scoped to one,
	// and using it against another would send a credential somewhere it does
	// not belong.
	ControlPlane string    `json:"control_plane"`
	Token        string    `json:"token"`
	Login        string    `json:"login,omitempty"`
	Organization string    `json:"organization,omitempty"`
	Scopes       []string  `json:"scopes,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitzero"`
	StoredAt     time.Time `json:"stored_at"`
}

// Expired reports whether the credential is past its expiry.
//
// A zero expiry never expires, which is how a token minted without one is
// treated. The check is here rather than only on the server so that the CLI can
// say "your session expired, run af login" instead of showing a 401.
func (c Credential) Expired(now time.Time) bool {
	return !c.ExpiresAt.IsZero() && !now.Before(c.ExpiresAt)
}

// Store reads and writes credentials.
type Store struct {
	// Ring is the OS credential store, or nil when the platform has none.
	Ring secrets.Keyring
	// Dir is where the file fallback lives. Empty means ~/.antifailure.
	Dir string
}

// NewStore returns a store using this platform's keyring when it has one.
func NewStore() *Store { return &Store{Ring: secrets.NewSystemKeyring()} }

// Location describes where a credential for this control plane would be kept,
// in the words somebody would use when asked "where is my token".
func (s *Store) Location(controlPlane string) string {
	if s.Ring != nil {
		return fmt.Sprintf("the operating system keyring, under %q", service)
	}
	path, err := s.path(controlPlane)
	if err != nil {
		return "a file under ~/.antifailure"
	}
	return path
}

// UsesKeyring reports whether the OS credential store is doing the work.
//
// Exposed so that `af login` can tell the user which of the two happened. This
// is the difference between a credential the operating system protects and one
// protected only by file permissions, and the user is entitled to know which
// they have.
func (s *Store) UsesKeyring() bool { return s.Ring != nil }

// Save writes a credential, replacing any for the same control plane.
func (s *Store) Save(c Credential) error {
	if c.ControlPlane == "" {
		return errors.New("a credential must name the control plane it is for")
	}
	if c.Token == "" {
		return errors.New("a credential must carry a token")
	}
	c.StoredAt = time.Now().UTC()

	body, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode the credential: %w", err)
	}

	if s.Ring != nil {
		if err := s.Ring.Set(service, account(c.ControlPlane), string(body)); err != nil {
			if !errors.Is(err, secrets.ErrKeyringUnavailable) {
				return fmt.Errorf("write to the keyring: %w", err)
			}
			// A keyring that exists and refuses falls through to the file, and
			// says nothing here: Location() is what reports where it went, and
			// it is read after this returns.
			s.Ring = nil
		} else {
			return nil
		}
	}

	path, err := s.path(c.ControlPlane)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	// Written to a temporary file and renamed, so that an interrupted write
	// cannot leave a truncated credential that reads as corruption. 0600 on
	// both, because the temporary file holds the same secret.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// Load returns the credential for a control plane.
func (s *Store) Load(controlPlane string) (Credential, error) {
	if s.Ring != nil {
		raw, err := s.Ring.Get(service, account(controlPlane))
		switch {
		case err == nil:
			var c Credential
			if err := json.Unmarshal([]byte(raw), &c); err != nil {
				return Credential{}, fmt.Errorf("the stored credential is not readable: %w", err)
			}
			return c, nil
		case errors.Is(err, secrets.ErrNotFound):
			// Fall through to the file. A machine can have both, if a login
			// happened before the keyring worked.
		case errors.Is(err, secrets.ErrKeyringUnavailable):
			// Same.
		default:
			return Credential{}, fmt.Errorf("read from the keyring: %w", err)
		}
	}

	path, err := s.path(controlPlane)
	if err != nil {
		return Credential{}, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Credential{}, ErrNotSignedIn
		}
		return Credential{}, fmt.Errorf("read %s: %w", path, err)
	}
	var c Credential
	if err := json.Unmarshal(body, &c); err != nil {
		return Credential{}, fmt.Errorf("the stored credential is not readable: %w", err)
	}
	return c, nil
}

// Delete removes the credential for a control plane from everywhere it might be.
//
// Both stores are cleared rather than the first that answers, because the whole
// point of `af logout` is that nothing is left. A machine that has a keyring
// entry AND a file, because one login happened before the keyring worked, must
// not keep the file.
func (s *Store) Delete(controlPlane string) (removed bool, err error) {
	if s.Ring != nil {
		switch e := s.Ring.Delete(service, account(controlPlane)); {
		case e == nil:
			removed = true
		case errors.Is(e, secrets.ErrNotFound), errors.Is(e, secrets.ErrKeyringUnavailable):
			// Nothing there, which is not a failure to remove it.
		default:
			err = fmt.Errorf("remove from the keyring: %w", e)
		}
	}

	path, pathErr := s.path(controlPlane)
	if pathErr != nil {
		if err == nil {
			err = pathErr
		}
		return removed, err
	}
	if rmErr := os.Remove(path); rmErr == nil {
		removed = true
	} else if !os.IsNotExist(rmErr) && err == nil {
		err = fmt.Errorf("remove %s: %w", path, rmErr)
	}
	return removed, err
}

// account is the keyring entry name for a control plane.
func account(controlPlane string) string {
	return "cli:" + normalise(controlPlane)
}

func (s *Store) path(controlPlane string) (string, error) {
	dir := s.Dir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find the home directory: %w", err)
		}
		dir = filepath.Join(home, ".antifailure")
	}
	return filepath.Join(dir, "credentials", fileName(controlPlane)+".json"), nil
}

// fileName turns an origin into something safe to put in a path.
//
// Every character outside a small set becomes a dash, so a control plane URL
// cannot escape the credentials directory through a slash or a dot-dot. The
// origin is also kept inside the file, so a collision between two normalised
// names is detectable rather than silent.
func fileName(controlPlane string) string {
	n := normalise(controlPlane)
	var b strings.Builder
	for _, r := range n {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := b.String()
	if out == "" {
		return "default"
	}
	return out
}

// normalise reduces a control plane URL to its origin, lower cased and without
// a trailing slash, so that https://app.dev/ and https://APP.DEV are one entry
// rather than three.
func normalise(controlPlane string) string {
	s := strings.TrimSpace(controlPlane)
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return strings.ToLower(strings.TrimSuffix(s, "/"))
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

// Normalise is the exported form, so that callers store and look up under the
// same key without reimplementing the rule.
func Normalise(controlPlane string) string { return normalise(controlPlane) }
