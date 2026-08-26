package secrets

// Where a value comes from, and the order sources are asked in.
//
// The order is the whole design. A developer running af up on a laptop has a
// keyring, a .env file the application already reads, and a shell they may have
// exported something into. A CI runner has none of those and has variables the
// platform injected. Both have to work with the same manifest and no flags, and
// somebody debugging has to be able to find out which source answered.
//
// So: sources are asked in a fixed order, the first that answers wins, and what
// answered is recorded by name. Not by value. The record exists to be printed
// in an audit event and in af explain, and a record that carried the value
// would be a secret in a log the moment either was shown.
//
// The order is most specific first. An explicit shell export beats a file,
// because somebody who typed it meant it and is usually debugging. A file beats
// the keyring, because a repository's .env is checked out with the branch and
// is the thing that changes per project. The keyring is last because it is the
// long-lived default.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Source is somewhere values can be read from.
//
// Lookup returns whether the name was found, separately from the value, because
// a variable that exists and is empty is a different thing from one that does
// not exist and only one of them should stop the search. A CI platform that
// injects empty strings for unset variables would otherwise mask every later
// source.
type Source interface {
	// Name identifies the source in audit records and error messages, in the
	// words somebody would use: "this shell's environment", not "envsource".
	Name() string
	// Lookup returns the value for a name.
	Lookup(ctx context.Context, name string) (Value, bool, error)
	// Available reports whether the source can be used at all, and why not when
	// it cannot. A missing keyring is not an error; it is a source that is
	// skipped and named in the message when nothing else answers either.
	Available(ctx context.Context) (bool, string)
}

// Resolution records where one variable came from.
//
// The value is deliberately absent. This is what gets written to an audit
// event, printed by af explain, and included in a support bundle.
type Resolution struct {
	Name   string
	Source string
	// Fingerprint is a short, non-reversible tag, so that two environments can
	// be compared without either value being shown. Empty when the value was
	// not found.
	Fingerprint string
}

// Missing is a variable that no source could supply.
type Missing struct {
	Name string
	// Searched names every source that was asked, so the message can say where
	// to put it rather than only that it is absent.
	Searched []string
}

// Chain asks several sources in order.
type Chain struct {
	sources []Source
}

// NewChain builds a chain. Sources are asked in the order given.
func NewChain(sources ...Source) *Chain {
	return &Chain{sources: sources}
}

// Sources returns the names in the order they are asked, for the error message
// that lists where a variable could have been.
func (c *Chain) Sources(ctx context.Context) []string {
	out := make([]string, 0, len(c.sources))
	for _, s := range c.sources {
		if ok, _ := s.Available(ctx); ok {
			out = append(out, s.Name())
		}
	}
	return out
}

// Considered returns every source with its state, for the message somebody
// reads when a variable is missing.
//
// It includes sources that are not present, and that is the point: the place to
// put a value is very often the .env file that does not exist yet, and a list
// of only the sources that happen to be usable would never mention it. A source
// that is unusable for a fixable reason says so, because "the keyring is
// locked" is the sentence somebody needs and without it the variable simply
// looks absent.
func (c *Chain) Considered(ctx context.Context) []string {
	out := make([]string, 0, len(c.sources))
	for _, s := range c.sources {
		ok, why := s.Available(ctx)
		switch {
		case ok:
			out = append(out, s.Name())
		case why != "":
			out = append(out, fmt.Sprintf("%s (%s)", s.Name(), why))
		default:
			out = append(out, fmt.Sprintf("%s (not present)", s.Name()))
		}
	}
	return out
}

// Unavailable returns the sources that could not be used, with the reason.
//
// Reported rather than hidden. "STRIPE_SECRET_KEY was not found" is much less
// useful than the same sentence followed by "the keyring is locked", and the
// second one is what somebody needs at the moment they hit it.
func (c *Chain) Unavailable(ctx context.Context) []string {
	var out []string
	for _, s := range c.sources {
		if ok, why := s.Available(ctx); !ok && why != "" {
			out = append(out, fmt.Sprintf("%s (%s)", s.Name(), why))
		}
	}
	return out
}

// Lookup asks each source in turn and returns the first answer.
func (c *Chain) Lookup(ctx context.Context, name string) (Value, Resolution, bool, error) {
	for _, s := range c.sources {
		if ok, _ := s.Available(ctx); !ok {
			continue
		}
		value, found, err := s.Lookup(ctx, name)
		if err != nil {
			// A source that fails is reported rather than skipped. Skipping it
			// would silently fall through to a lower-priority source and hand
			// the application a different value than it would have got
			// yesterday, which is the worst possible way for this to break.
			return Value{}, Resolution{}, false, fmt.Errorf("%s: %w", s.Name(), err)
		}
		if !found {
			continue
		}
		return value, Resolution{
			Name: name, Source: s.Name(), Fingerprint: value.Fingerprint(),
		}, true, nil
	}
	return Value{}, Resolution{}, false, nil
}

// ---------------------------------------------------------------------------
// The process environment
// ---------------------------------------------------------------------------

// EnvSource reads the environment af is running in.
type EnvSource struct {
	// Getenv is injected so that tests and af explain can both run without
	// touching the real process environment.
	Getenv func(string) (string, bool)
	// Label distinguishes a developer's shell from a CI runner's injected
	// variables in the audit record, since they are the same mechanism and
	// mean quite different things.
	Label string
}

// NewEnvSource reads the real process environment.
func NewEnvSource() *EnvSource {
	return &EnvSource{Getenv: os.LookupEnv, Label: "this shell's environment"}
}

// NewCISource is the same mechanism with a different name, because a value
// injected by a CI platform and one exported by a person are worth telling
// apart when somebody is reading an audit trail.
func NewCISource(getenv func(string) (string, bool)) *EnvSource {
	return &EnvSource{Getenv: getenv, Label: "the CI runner's variables"}
}

func (e *EnvSource) Name() string {
	if e.Label != "" {
		return e.Label
	}
	return "the process environment"
}

func (e *EnvSource) Available(context.Context) (bool, string) { return e.Getenv != nil, "" }

func (e *EnvSource) Lookup(_ context.Context, name string) (Value, bool, error) {
	raw, ok := e.Getenv(name)
	if !ok {
		return Value{}, false, nil
	}
	// Present and empty counts as present. A variable somebody deliberately set
	// to nothing should not fall through to a file with a stale value in it.
	return NewFrom(raw, e.Name()), true, nil
}

// ---------------------------------------------------------------------------
// A dotenv file
// ---------------------------------------------------------------------------

// DotEnvSource reads a .env file the application already uses.
//
// Read rather than required. Most repositories that need secrets already have
// one, and asking somebody to duplicate it into a keyring before they can try
// the product is how a first run fails.
type DotEnvSource struct {
	Path string
	// values is parsed once, on first use, so that a file with a thousand lines
	// is not re-read per variable.
	values map[string]string
	err    error
	loaded bool
}

// NewDotEnvSource reads a file, which may not exist.
func NewDotEnvSource(path string) *DotEnvSource { return &DotEnvSource{Path: path} }

func (d *DotEnvSource) Name() string { return filepath.Base(d.Path) }

func (d *DotEnvSource) Available(context.Context) (bool, string) {
	d.load()
	if d.err != nil {
		return false, d.err.Error()
	}
	return d.values != nil, ""
}

func (d *DotEnvSource) Lookup(_ context.Context, name string) (Value, bool, error) {
	d.load()
	if d.err != nil {
		return Value{}, false, d.err
	}
	raw, ok := d.values[name]
	if !ok {
		return Value{}, false, nil
	}
	return NewFrom(raw, d.Name()), true, nil
}

func (d *DotEnvSource) load() {
	if d.loaded {
		return
	}
	d.loaded = true

	f, err := os.Open(d.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Not an error. Most repositories have no .env and that is fine.
			return
		}
		d.err = err
		return
	}
	defer func() { _ = f.Close() }()

	values, err := ParseDotEnv(f)
	if err != nil {
		d.err = err
		return
	}
	d.values = values
}

// ParseDotEnv reads the dotenv format.
//
// Implemented here rather than taken as a dependency, for the same reason
// .dockerignore is: the format is twenty lines of rules and a dependency that
// parses secrets is a dependency with a great deal of access.
//
// What is supported is what these files actually contain: comments, blank
// lines, an optional export prefix, and single or double quoted values.
// Interpolation is deliberately not supported. A file where one value depends
// on another is a file where reading a variable can produce a different result
// depending on order, and that is not a property a secret should have.
func ParseDotEnv(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	scanner := bufio.NewScanner(r)
	// A generous line limit: a private key in a .env file is one long line and
	// the default 64 KiB is not always enough.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		text = strings.TrimPrefix(text, "export ")

		eq := strings.IndexByte(text, '=')
		if eq <= 0 {
			// Named rather than skipped. A line nobody can parse in a file of
			// secrets is worth stopping for: silently ignoring it is how a
			// variable is "set" and absent at the same time.
			return nil, fmt.Errorf("line %d is not NAME=value: %q", line, truncate(text))
		}

		name := strings.TrimSpace(text[:eq])
		if !validName(name) {
			return nil, fmt.Errorf("line %d has %q as a name, which is not a variable name", line, truncate(name))
		}

		value := strings.TrimSpace(text[eq+1:])
		switch {
		case len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"':
			value = unescape(value[1 : len(value)-1])
		case len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'':
			// Single quotes are literal, which is why a value containing a
			// backslash belongs in them.
			value = value[1 : len(value)-1]
		default:
			// An unquoted value ends at the first unescaped comment marker, but
			// only when the marker is preceded by whitespace: a token like
			// sk_test_a#b is one value, not a value and a comment.
			if i := strings.Index(value, " #"); i >= 0 {
				value = strings.TrimSpace(value[:i])
			}
		}
		out[name] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func validName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// truncate keeps an error message from quoting an entire secret.
//
// The line that failed to parse might be a private key, and an error message is
// the one place a value is guaranteed to be printed.
func truncate(s string) string {
	const limit = 24
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}

// ---------------------------------------------------------------------------
// A keyring
// ---------------------------------------------------------------------------

// Keyring is the operating system's credential store.
//
// An interface rather than a direct dependency, because the implementations are
// platform specific, because two of the three prompt the user, and because a
// test that touches a real keychain is a test that either prompts a developer
// or fails on a headless runner.
type Keyring interface {
	Get(service, name string) (string, error)
	Set(service, name, value string) error
	Delete(service, name string) error
}

// ErrKeyringUnavailable reports that no credential store could be reached.
var ErrKeyringUnavailable = errors.New("no keyring is available")

// ErrNotFound reports that a keyring has no such entry.
var ErrNotFound = errors.New("not found")

// KeyringSource reads from a credential store.
type KeyringSource struct {
	Ring Keyring
	// Service namespaces the entries, so that two projects on one machine do
	// not overwrite each other's values.
	Service string
	// unavailable caches the reason, so a locked keyring is reported once
	// rather than probed for every variable.
	unavailable string
	probed      bool
}

// NewSystemKeyring returns the platform's credential store, or nil where there
// is not one yet. Nil is a valid ring: KeyringSource reports it as not
// configured and the chain skips it.
func NewSystemKeyring() Keyring { return newSystemKeyring() }

// PassphraseKey is the entry a stored passphrase is kept under, so that the
// encrypted file store can be unlocked without an environment variable.
const PassphraseKey = "secret-store-passphrase"

// PassphraseFromKeyring reads the file store's passphrase from the system
// keyring, returning an empty string when there is none. Errors are not
// reported: a missing or locked keyring means the caller falls back to the
// environment, which is a normal path and not a failure.
func PassphraseFromKeyring(service string) string {
	ring := newSystemKeyring()
	if ring == nil {
		return ""
	}
	value, err := ring.Get(service, PassphraseKey)
	if err != nil {
		return ""
	}
	return value
}

// StorePassphrase finds the passphrase for the encrypted file store.
//
// The environment first, so that CI and a one off override work without a
// keyring at all. Then the system keyring, which is where 'af secret set' puts
// it on a machine that has one, so a workstation does not need the passphrase
// exported in every shell.
func StorePassphrase(getenv func(string) string) string {
	if v := getenv("AF_SECRET_PASSPHRASE"); v != "" {
		return v
	}
	return PassphraseFromKeyring(DefaultKeyringService)
}

// DefaultKeyringService namespaces entries so that two tools on one machine do
// not overwrite each other's.
const DefaultKeyringService = "antifailure"

// NewKeyringSource wraps a credential store.
func NewKeyringSource(ring Keyring, service string) *KeyringSource {
	if service == "" {
		service = "antifailure"
	}
	return &KeyringSource{Ring: ring, Service: service}
}

func (k *KeyringSource) Name() string { return "the system keyring" }

func (k *KeyringSource) Available(context.Context) (bool, string) {
	if k.Ring == nil {
		return false, "not configured"
	}
	if !k.probed {
		k.probed = true
		// A probe rather than a capability flag. A keyring can be present and
		// locked, and the only way to find out is to ask it for something.
		if _, err := k.Ring.Get(k.Service, "antifailure-probe"); err != nil &&
			!errors.Is(err, ErrNotFound) {
			k.unavailable = err.Error()
		}
	}
	return k.unavailable == "", k.unavailable
}

func (k *KeyringSource) Lookup(_ context.Context, name string) (Value, bool, error) {
	raw, err := k.Ring.Get(k.Service, name)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Value{}, false, nil
		}
		return Value{}, false, err
	}
	return NewFrom(raw, k.Name()), true, nil
}

// SortResolutions orders by name, so an audit event is comparable between runs.
func SortResolutions(rs []Resolution) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].Name < rs[j].Name })
}
