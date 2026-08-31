// Package model is the key that drives the agents, on this machine.
//
// The agents can read a page and decide what a person would do next, and that
// takes a model. The key is the user's. Nothing here ships a credential,
// nothing here stores one anywhere the user did not put it, and with no key at
// all the deterministic planner runs instead, which is a supported mode rather
// than a broken one.
//
// There are two ways to bring a key to this product and they are not the same
// thing. `af provider` stores one on the control plane, sealed, with a monthly
// cap, and runs reach the model through it. That is the arrangement in which a
// cap is a cap, and it needs a control plane. This package is the other one:
// the key stays on this machine, the call goes straight to the provider, and
// nothing hosted is involved. It is what a hobbyist and a self-hoster get, and
// it has to be as good as the hosted path rather than the fallback nobody
// finished.
//
// The key is resolved through the same chain every other secret uses, in the
// same order, so there is one precedence rule in this product rather than two.
// That matters more than it sounds: the alternative is a user who knows that an
// export beats a keyring for DATABASE_URL and has to learn it again, possibly
// differently, for ANTHROPIC_API_KEY.
package model

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// Provider is one model provider and the names its settings live under.
//
// A table rather than a switch, because every one of these names appears in a
// command's output, in the doctor check, in the environment handed to the
// runner and in the documentation, and a provider added in five places is a
// provider that is added wrong in one of them.
type Provider struct {
	// Name is what a user types: "anthropic".
	Name string
	// KeyVar is the variable the key is looked up under, and the name it is
	// stored under. It is the provider's own conventional name rather than
	// something of ours, so that a machine already set up for the provider's
	// SDK needs no new variable at all.
	KeyVar string
	// BaseURLVar overrides the endpoint, for a local model or a gateway.
	BaseURLVar string
	// DefaultBaseURL is the provider's own API.
	DefaultBaseURL string
	// DefaultModel is used when AF_MODEL says nothing.
	DefaultModel string
	// Path is the completion endpoint under the base URL.
	Path string
}

// Providers are the providers the agents can use, in the order they are asked.
//
// Anthropic first, and that is a real decision rather than alphabetical order:
// with both keys present one has to win, the runner has always preferred
// Anthropic, and changing which provider an existing setup uses in the release
// that adds a way to inspect it would be a surprise nobody asked for.
var Providers = []Provider{
	{
		Name:           "anthropic",
		KeyVar:         "ANTHROPIC_API_KEY",
		BaseURLVar:     "ANTHROPIC_BASE_URL",
		DefaultBaseURL: "https://api.anthropic.com",
		DefaultModel:   "claude-sonnet-5",
		Path:           "/v1/messages",
	},
	{
		Name:           "openai",
		KeyVar:         "OPENAI_API_KEY",
		BaseURLVar:     "OPENAI_BASE_URL",
		DefaultBaseURL: "https://api.openai.com",
		DefaultModel:   "gpt-4.1",
		Path:           "/v1/chat/completions",
	},
}

// ModelVar names the model, for either provider.
const ModelVar = "AF_MODEL"

// Lookup finds a provider by name.
func Lookup(name string) (Provider, bool) {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, p := range Providers {
		if p.Name == want {
			return p, true
		}
	}
	return Provider{}, false
}

// Names lists the providers, for an error message that has to say what is valid.
func Names() []string {
	out := make([]string, 0, len(Providers))
	for _, p := range Providers {
		out = append(out, p.Name)
	}
	sort.Strings(out)
	return out
}

// Config is a resolved model configuration.
//
// Key is a secrets.Value rather than a string, which is the whole point of that
// type: it cannot be printed by an fmt verb, marshalled into JSON or YAML, or
// attached to a log record without somebody having written Reveal. There is
// exactly one Reveal in this package's callers, in the function that puts the
// key into a request.
type Config struct {
	Provider Provider
	Key      secrets.Value
	Model    string
	BaseURL  string
	// Source names where the key was found, in the words a person would use:
	// "this shell's environment", "the system keyring".
	Source string
	// Fingerprint is short and non-reversible, so two machines can be compared
	// without either key being read out, and so a stored verification record
	// can be checked against the key actually in use.
	Fingerprint string
}

// Endpoint is the full URL one completion is sent to.
func (c Config) Endpoint() string {
	return strings.TrimRight(c.BaseURL, "/") + c.Provider.Path
}

// Custom reports whether the endpoint is somewhere other than the provider's.
func (c Config) Custom() bool {
	return strings.TrimRight(c.BaseURL, "/") != c.Provider.DefaultBaseURL
}

// Resolve finds a key for the first provider that has one.
//
// Nothing found is not an error. It is the normal case for somebody who has not
// set a key up, the deterministic planner handles it, and returning an error
// would make every caller treat a supported mode as a failure.
//
// The order this asks in is Providers' order, not the chain's. A machine with
// both keys set gets Anthropic, from whichever source answers for it first,
// rather than getting whichever provider happens to have a key in the
// highest-priority source; a rule that mixed the two dimensions would be
// impossible to predict and impossible to document.
func Resolve(ctx context.Context, chain *secrets.Chain) (*Config, error) {
	for _, p := range Providers {
		key, resolution, found, err := chain.Lookup(ctx, p.KeyVar)
		if err != nil {
			return nil, err
		}
		if !found || strings.TrimSpace(key.Reveal()) == "" {
			// Present and empty is treated as absent here, unlike in the chain
			// itself. The chain is right that an empty export should stop the
			// search for a declared variable; a key that is the empty string
			// cannot authenticate anything, and falling through to the other
			// provider is more useful than reporting a key that cannot work.
			continue
		}

		cfg := &Config{
			Provider:    p,
			Key:         key,
			Model:       p.DefaultModel,
			BaseURL:     p.DefaultBaseURL,
			Source:      resolution.Source,
			Fingerprint: resolution.Fingerprint,
		}
		if v, _, ok, err := chain.Lookup(ctx, ModelVar); err == nil && ok {
			if name := strings.TrimSpace(v.Reveal()); name != "" {
				cfg.Model = name
			}
		}
		if v, _, ok, err := chain.Lookup(ctx, p.BaseURLVar); err == nil && ok {
			if url := strings.TrimSpace(v.Reveal()); url != "" {
				cfg.BaseURL = url
			}
		}
		return cfg, nil
	}
	return nil, nil
}

// Environment renders a resolved configuration as variables for a subprocess.
//
// The runner and the egress sidecar both read the provider's own variable
// names, so a key that came from a keyring arrives looking exactly like one
// somebody exported. That is deliberate: the alternative is a second
// configuration path inside the runner that only the engine can produce, which
// would have to be kept in step with the one a person uses by hand.
//
// Every value returned is registered with the redactor by the caller, which is
// why this returns the pairs rather than writing them anywhere itself.
func (c Config) Environment() []string {
	out := []string{
		c.Provider.KeyVar + "=" + c.Key.Reveal(),
		ModelVar + "=" + c.Model,
	}
	// The base URL is passed only when it is not the provider's own. Passing
	// the default would be harmless today and would silently pin the endpoint
	// against a future release that changes it.
	if c.Custom() {
		out = append(out, c.Provider.BaseURLVar+"="+c.BaseURL)
	}
	return out
}

// ErrNoStore reports that a key cannot be written anywhere on this machine.
var ErrNoStore = errors.New("no keyring and no passphrase for the encrypted store")

// Store writes a key where Resolve will find it.
//
// The keyring first, where the platform has one, because it is the only place
// on a workstation where a secret is protected by something other than file
// permissions: macOS gates it on the login keychain, Linux on the session
// keyring daemon, Windows on the user's credentials.
//
// Where there is no keyring, which is a Linux server without libsecret, a
// container, and every platform this does not have an implementation for, it
// falls back to the encrypted file store beside the repository. That store
// needs a passphrase and deliberately has no default, so this reports
// ErrNoStore rather than writing a file that only looks encrypted.
//
// It returns where the value went, because the user has to be told: those two
// places have very different properties and a command that said "stored" for
// either would be hiding the difference that matters.
func Store(
	root string, getenv func(string) string, ring secrets.Keyring, p Provider, key string,
) (string, error) {
	if ring != nil {
		err := ring.Set(secrets.DefaultKeyringService, p.KeyVar, key)
		if err == nil {
			return "the system keyring", nil
		}
		if !errors.Is(err, secrets.ErrKeyringUnavailable) {
			return "", fmt.Errorf("writing to the system keyring: %w", err)
		}
	}
	if secrets.StorePassphrase(getenv) == "" {
		return "", ErrNoStore
	}
	if err := fileStore(root, getenv).Set(p.KeyVar, key); err != nil {
		return "", err
	}
	return "the encrypted local store", nil
}

// Remove deletes a stored key from everywhere this package can write.
//
// Both places, not the first that answers. A key left in the file store after a
// keyring entry was removed is a key the next run silently uses, which is the
// exact failure somebody is trying to prevent when they type this.
//
// Removing something that is not there succeeds, for the same reason every
// teardown does: the caller wanted it gone. It returns the places it actually
// removed from, so the command can say whether anything was there.
func Remove(
	root string, getenv func(string) string, ring secrets.Keyring, p Provider,
) ([]string, error) {
	var removed []string
	if ring != nil {
		err := ring.Delete(secrets.DefaultKeyringService, p.KeyVar)
		switch {
		case err == nil:
			removed = append(removed, "the system keyring")
		case errors.Is(err, secrets.ErrNotFound), errors.Is(err, secrets.ErrKeyringUnavailable):
			// Nothing there, or nowhere to look. Neither is a failure.
		default:
			return removed, fmt.Errorf("removing from the system keyring: %w", err)
		}
	}
	if secrets.StorePassphrase(getenv) != "" {
		store := fileStore(root, getenv)
		names, err := store.Names()
		if err == nil {
			for _, name := range names {
				if name != p.KeyVar {
					continue
				}
				if err := store.Delete(p.KeyVar); err != nil {
					return removed, err
				}
				removed = append(removed, "the encrypted local store")
			}
		}
	}
	return removed, nil
}

func fileStore(root string, getenv func(string) string) *secrets.FileStore {
	return secrets.NewFileStore(
		storePath(root), secrets.StorePassphrase(getenv))
}
