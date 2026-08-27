// Package personas creates the accounts an agent signs in as.
//
// A workflow runs as somebody. That somebody has to exist before the browser
// opens, and making them exist is harder than it sounds, because every
// application disagrees about where a user lives. Some own a users table and a
// password column. Some hand the whole question to Supabase, whose auth schema
// has its own shape and its own hashing. Some hand it to Clerk or Auth0, which
// will not accept a row written directly at all and only create users through
// an API.
//
// The adapter is the seam. Everything above it works in terms of a persona
// from the manifest and an account that came back; everything below it knows
// one authentication scheme and nothing else.
//
// Two properties are worth stating because the rest of the package is built
// around them.
//
// Provisioning is idempotent, and reconciles rather than duplicates. The
// golden is a masked copy of production, so a persona's address may already be
// in it as a real user who has since been masked. Creating a second row for
// that address gives the application two users with one email, which is a bug
// in the fixture rather than in the application, and the sort that takes a day
// to find. Running twice is therefore safe by construction, which is also what
// lets a persona be provisioned into the golden once and reconciled on a
// branch without anybody tracking which already happened.
//
// Credentials are derived, never stored. The password and the TOTP secret are
// a function of the environment id and the persona name, so the adapter that
// writes the hash and the runner that types the password arrive at the same
// value without either one writing it down or sending it anywhere. That is
// what makes them per environment: two branches of the same repository have
// different passwords for the same persona, and neither is a secret that
// outlives its branch.
package personas

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Account is a persona that exists, and what is needed to sign in as it.
//
// It is what an adapter returns and what the runner is told. The secret fields
// are secrets.Value so that a report, a log line or a marshalled job document
// cannot carry them by accident; reaching the plaintext takes a Reveal call
// that is easy to grep for.
type Account struct {
	// Name is the persona's name in the manifest.
	Name string
	// Email is the address it signs in with.
	Email string
	// Phone is the number an sms code is sent to, empty unless the persona
	// signs in that way.
	Phone string
	// Role is whatever the application calls its roles, uninterpreted here.
	Role string
	// Login is how this persona signs in.
	Login schema.LoginStrategy
	// Password is set when the strategy needs one.
	Password secrets.Value
	// TOTPSecret is set when the persona enrolled a second factor. It is the
	// raw secret, base32 encoded, which is the form an authenticator app and
	// the runner both expect.
	TOTPSecret secrets.Value
	// Subject is the identifier the authentication system knows this account
	// by: a uuid in auth.users, a user id at Clerk. Recorded so that a second
	// run reconciles the same account rather than matching on email again.
	Subject string
	// Adapter names what created it, for the report and for the metadata.
	Adapter string
	// Reconciled is true when an existing account was updated rather than a
	// new one created. Worth surfacing: it means the address was already in
	// the golden, which is usually a masked real user and occasionally a sign
	// that the persona list collided with production data.
	Reconciled bool
}

// Adapter creates or reconciles personas in one authentication scheme.
//
// Deliberately small. An adapter is handed a persona and the credentials it
// should end up with, and answers with the account. Everything it needs to
// reach its system, a database connection or an API token, it was constructed
// with, so this interface says nothing about where users live.
type Adapter interface {
	// Name identifies the adapter in reports and in the golden's metadata.
	Name() string
	// Provision creates the persona, or updates it if the address is already
	// there, and returns the account either way. It must be safe to call
	// twice with the same arguments.
	Provision(ctx context.Context, p schema.Persona, want Credentials) (*Account, error)
}

// Credentials are what a persona should end up signing in with.
//
// Passed in rather than chosen by the adapter, because the runner has to
// arrive at the same values independently and a value the adapter invented
// would have to be transmitted back.
type Credentials struct {
	// Password is the plaintext the persona signs in with. Empty when the
	// strategy does not use one.
	Password secrets.Value
	// TOTPSecret is the base32 secret to enrol, empty when no second factor
	// is wanted.
	TOTPSecret secrets.Value
}

// Deriver produces a persona's credentials from the environment.
//
// The same environment id and persona name always give the same password and
// the same secret, and a different environment gives different ones. That is
// the whole mechanism: nothing is stored, nothing is sent, and both sides
// compute it.
type Deriver struct {
	envID string
	// policy shapes the generated password, for an application whose rules
	// are stricter than the default.
	policy PasswordPolicy
}

// NewDeriver returns a Deriver for an environment.
func NewDeriver(envID string, policy PasswordPolicy) *Deriver {
	return &Deriver{envID: envID, policy: policy}
}

// For returns the credentials a persona should have.
//
// A TOTP secret is derived for every persona whether or not it is enrolled,
// because deriving one costs nothing and the alternative is a second code
// path that only runs when mfa is set. Whether it is enrolled is the adapter's
// decision, taken from the persona.
func (d *Deriver) For(p schema.Persona) Credentials {
	return Credentials{
		Password:   secrets.NewFrom(d.password(p.Name), "derived"),
		TOTPSecret: secrets.NewFrom(d.totpSecret(p.Name), "derived"),
	}
}

// password derives the persona's password.
//
// The shape satisfies the common password rules without being configurable by
// accident: upper, lower, digit and a symbol, over twenty characters. An
// application with stricter rules gets them through the policy.
func (d *Deriver) password(persona string) string {
	sum := derive("antifailure/persona/v1", d.envID, persona)
	return d.policy.apply("Af-" + hex.EncodeToString(sum[:8]) + "!1")
}

// totpSecret derives the persona's second factor.
//
// Twenty bytes, which is the length RFC 4226 recommends for HMAC-SHA1, base32
// encoded without padding because that is the form every authenticator app
// and every otpauth URL uses.
func (d *Deriver) totpSecret(persona string) string {
	sum := derive("antifailure/persona/totp/v1", d.envID, persona)
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:20])
}

// derive is the one place the derivation is defined, so a change to it is a
// change to one function rather than to several that had drifted.
func derive(domain, envID, persona string) [32]byte {
	return sha256.Sum256([]byte(domain + "\x00" + envID + "\x00" + persona))
}

// PasswordPolicy adjusts the generated password for an application whose rules
// the default does not satisfy.
//
// The spec's case is an application with a policy stricter than the generator.
// The failure it prevents is quiet: the adapter writes a hash, the application
// refuses the password at sign in, and the run reports a login failure that
// looks like the application's fault.
type PasswordPolicy struct {
	// MinLength pads the password when the default is too short.
	MinLength int
	// Symbols, when set, replaces the default symbol set. An application that
	// rejects "!" needs to say so somewhere, and this is where.
	Symbols string
	// Forbid lists characters the application will not accept.
	Forbid string
}

// apply shapes a generated password to the policy.
//
// Shaping rather than refusing, because an application with stricter rules
// than the default should still work. What it cannot do is invent a password
// out of a policy that forbids everything, and that case is caught by
// Validate rather than papered over here.
func (p PasswordPolicy) apply(base string) string {
	out := base
	if p.Forbid != "" {
		filler := p.filler()
		for _, r := range p.Forbid {
			// Replaced rather than dropped, so the length and the character
			// classes survive the substitution.
			out = strings.ReplaceAll(out, string(r), filler)
		}
		if p.Symbols == "" && strings.ContainsAny(p.Forbid, "!-") {
			// The default symbols were just removed, so the password no
			// longer has one and a policy requiring one would reject it.
			for _, candidate := range "#$%*?@" {
				if !strings.ContainsRune(p.Forbid, candidate) {
					out += string(candidate)
					break
				}
			}
		}
	}
	if p.Symbols != "" {
		replacement := string(p.Symbols[0])
		out = strings.NewReplacer("!", replacement, "-", replacement).Replace(out)
	}
	// Padded with a character the policy allows. Padding with a fixed "x"
	// while "x" is forbidden produces a password that fails the very rule the
	// padding was applied to satisfy, which is a bug that only shows up for
	// somebody whose policy happens to forbid that one letter.
	if filler := p.filler(); filler != "" {
		for len(out) < p.MinLength {
			out += filler
		}
	}
	return out
}

// filler returns a character the policy permits, for padding and substitution.
//
// Empty when the policy forbids every candidate, which leaves the password
// short and lets Validate say so rather than producing one that cannot work.
func (p PasswordPolicy) filler() string {
	for _, candidate := range "xqzmkwvnbjhgfdsrpltcy" {
		if !strings.ContainsRune(p.Forbid, candidate) {
			return string(candidate)
		}
	}
	return ""
}

// Validate reports whether a password satisfies the policy.
//
// Run against what the generator produced, so a policy that the generator
// cannot satisfy fails here, loudly, rather than at sign in where it would
// look like the application refusing a correct password.
func (p PasswordPolicy) Validate(password string) error {
	if p.MinLength > 0 && len(password) < p.MinLength {
		return fmt.Errorf("the generated password is %d characters and the policy needs %d",
			len(password), p.MinLength)
	}
	for _, r := range p.Forbid {
		if strings.ContainsRune(password, r) {
			return fmt.Errorf("the generated password contains %q, which the policy forbids", r)
		}
	}
	return nil
}

// Result is what a provisioning run produced.
type Result struct {
	// Accounts are the personas that now exist, in manifest order.
	Accounts []*Account
	// Adapter names what provisioned them.
	Adapter string
}

// Account returns the named account, and whether it was provisioned.
func (r *Result) Account(name string) (*Account, bool) {
	for _, a := range r.Accounts {
		if a.Name == name {
			return a, true
		}
	}
	return nil, false
}

// Provision creates every persona through the adapter.
//
// Ordered by manifest position rather than by map iteration, so two runs
// produce the same report and a diff of two runs is about what changed rather
// than about ordering.
func Provision(
	ctx context.Context, a Adapter, d *Deriver, list []schema.Persona,
) (*Result, error) {
	out := &Result{Adapter: a.Name()}
	for _, p := range list {
		want := d.For(p)

		// Checked here rather than at the sign in, which is the whole point of
		// checking it. An application whose rules are stricter than the
		// generator refuses the password when the agent types it, and that
		// arrives as the application rejecting a correct password: a finding
		// against the application for a manifest problem. Failing now names
		// the real cause.
		if needsPassword(p.Login) || p.Login == "" {
			if err := d.policy.Validate(want.Password.Reveal()); err != nil {
				return nil, fmt.Errorf(
					"the password generated for persona %q does not satisfy auth.password: %w",
					p.Name, err)
			}
		}

		account, err := a.Provision(ctx, p, want)
		if err != nil {
			return nil, fmt.Errorf("provisioning persona %q: %w", p.Name, err)
		}
		out.Accounts = append(out.Accounts, account)
	}
	return out, nil
}

// SortedAttributes returns a persona's attributes in a stable order.
//
// Map iteration order in Go is deliberately random, and an adapter that builds
// a column list from it produces a different statement every run. That is
// invisible until somebody compares two runs, or until a test that asserts on
// the statement starts failing one time in six.
func SortedAttributes(attrs map[string]string) []string {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
