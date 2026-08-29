// Package secrets adds the stores an organization already keeps its
// credentials in to the engine's lookup chain.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// The community engine looks in four places, all of them local: this shell,
// .env, an encrypted file beside it, and the workstation's keyring. That is the
// right set for one person on one laptop and the wrong set for a company, where
// the credential already exists in Vault or in a cloud secret manager and the
// thing nobody wants is fifty developers copying it onto fifty machines so that
// a preview environment can start.
//
// The adapters are the small part. What matters, and what this file is, is the
// contract they all keep.
//
// A source that cannot answer has to say why. AF-SEC-001 is the message
// somebody sees when a variable is not found, and it lists every source that
// was considered along with the reason each one did not answer, because "it was
// not found" leaves you guessing which of five places to put it. A source that
// returns "unavailable" with nothing to say turns "the token expired at 09:14"
// into "not present", and the person then goes looking in the four places the
// value is not. So the reason is not decoration and it is not optional, and
// there is a conformance suite in this package that fails an adapter which
// omits it.
//
// A licence that lapses has to turn the source off. The check is per call
// rather than at registration, for the same reason the policy hook's is: a
// licence can expire while the process is running, and a feature that checked
// its entitlement once at startup keeps working for an organization that
// stopped paying and cannot be turned off without a restart. When it lapses the
// source degrades to "not licensed" with those words, which is a sentence an
// administrator can act on, rather than to silence.
//
// A credential that is rejected gets exactly one refresh. Every cloud store
// here authenticates with a bearer token that expires, and a long lived process
// will hold one past its expiry. One refresh covers that. A second rejection is
// not an expiry, it is a credential that has been revoked or was never right,
// and retrying it is how a deployment turns a configuration mistake into a rate
// limit. That second rejection is AF-SEC-002.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/antifailure/antifailure/ee/engine/feature"
	"github.com/antifailure/antifailure/ee/engine/license"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

func init() {
	// Recorded so that a feature which is sold and never checked shows up as
	// such. See ee/engine/feature.
	feature.Declare(license.FeatureSecrets, "ee/engine/secrets.Source")
}

// Backend is one store, reduced to the two things a lookup needs.
//
// Deliberately smaller than extension.SecretSource. Everything the contract
// requires and every mistake it is written to prevent lives in Source, so an
// adapter is a description, a reachability check, and a fetch, and an adapter
// author has no opportunity to get the licence gate, the reason reporting, or
// the refresh rule subtly different from the other three.
type Backend interface {
	// Describe names the store in the words somebody would use, including
	// enough of where it is to tell two of them apart: "HashiCorp Vault at
	// https://vault.internal (secret/antifailure)", not "vault". This is what
	// AF-SEC-001 prints and what an audit record records as the source.
	Describe() string

	// Reach reports whether the store can be used, returning nil when it can.
	//
	// How much it costs to answer differs per store and each adapter documents
	// what it actually checked. Where there is a cheap call that proves
	// reachability, it makes it. Where there is not, it verifies that it is
	// configured and says so, because burning a signed, billed API call on
	// every run to find out something the first real lookup will discover
	// anyway is not a trade worth making.
	Reach(ctx context.Context) error

	// Fetch returns a value.
	//
	// Found is separate from the value: a secret that exists and is empty is a
	// different thing from one that does not exist, and only one of them should
	// stop the chain. An error means the store could not answer, which is
	// different again, and must never be reported as a miss.
	Fetch(ctx context.Context, name string) (value string, found bool, err error)
}

// Refresher is a Backend whose credential can be renewed.
//
// Optional, because not every store has one: a Vault token supplied by an
// operator cannot be refreshed by us, and a cloud token minted from a service
// account can. A backend that does not implement this gets no refresh, which is
// correct rather than a limitation; refreshing something that cannot be
// refreshed just doubles the rejections.
type Refresher interface {
	// Refresh renews the credential once. It is called at most once per lookup
	// and only after a rejection.
	Refresh(ctx context.Context) error
}

// ErrRejected is what a Backend returns when the store refused the credential
// rather than refusing the request.
//
// The distinction is the whole point. A 404 is a miss, a 500 is a store having
// a bad day, and a 401 is the one case where trying again with the same
// credential is pointless and trying again with a new one might work.
var ErrRejected = errors.New("the store rejected the credential")

// ErrNotConfigured reports a backend that was asked for but never given what it
// needs. Carried separately so that Available can say "no address is set"
// rather than reporting a connection failure to an empty host.
var ErrNotConfigured = errors.New("not configured")

// Source is a Backend seen as something the engine can plug in.
//
// It is the only implementation of extension.SecretSource in this package, and
// every adapter goes through it. That is what makes the contract a property of
// the package rather than a paragraph each adapter is trusted to have read.
type Source struct {
	backend Backend

	// probe caches the reachability answer. A CLI run resolves a handful of
	// variables and the chain asks Available for every one of them plus twice
	// more to build the message, so probing per call would turn one lookup into
	// six network round trips. Cached for the life of the process, like the
	// keyring source's probe, because a store that is unreachable at the start
	// of an af up is not going to be reached before it finishes.
	//
	// The licence is deliberately NOT cached with it. That answer can change
	// underneath a running process and has to be asked every time.
	probe     sync.Once
	probeErr  error
	refreshed bool
	mu        sync.Mutex
}

// New wraps a backend.
func New(b Backend) *Source { return &Source{backend: b} }

// Name identifies the source. It is the backend's description, unchanged: the
// backend is the only thing that knows which Vault this is.
func (s *Source) Name() string { return s.backend.Describe() }

// Available reports whether the source can be used, and why not when it cannot.
//
// The order of the checks is the order of the answers somebody needs. A licence
// that has lapsed is reported as a licence, not as a connection problem, even
// when the store is also down, because an administrator whose licence expired
// wants to be told that and not sent to look at a firewall.
func (s *Source) Available(ctx context.Context) (bool, string) {
	if !feature.Enabled(ctx, license.FeatureSecrets) {
		status := feature.StatusFrom(ctx)
		return false, licenceReason(status)
	}

	s.probe.Do(func() { s.probeErr = s.backend.Reach(ctx) })
	if s.probeErr != nil {
		// The backend's own words, whatever kind of failure it was. An earlier
		// version of this collapsed a configuration problem to the two words
		// "not configured", which threw away the only useful part: which
		// variable is missing, or which of four credential mechanisms was
		// looked for. That is the same silence the whole package exists to
		// prevent, committed by the thing enforcing the rule.
		return false, reason(s.probeErr)
	}
	return true, ""
}

// Lookup returns a value, refreshing the credential once if it is rejected.
func (s *Source) Lookup(ctx context.Context, name string) (string, bool, error) {
	value, found, err := s.backend.Fetch(ctx, name)
	if err == nil || !errors.Is(err, ErrRejected) {
		return value, found, err
	}

	refresher, ok := s.backend.(Refresher)
	if !ok {
		// Nothing to refresh, so the first rejection is the final one. Reported
		// as rejected rather than as a miss: falling through to a lower
		// priority source here would hand the application a different secret
		// than it got yesterday, with nothing said about it.
		return "", false, s.rejected(err)
	}

	// Once. Per process, not per lookup: twenty variables against a revoked
	// token would otherwise be twenty refresh attempts and twenty rejections,
	// which is how a configuration mistake becomes a rate limit.
	if !s.markRefreshed() {
		return "", false, s.rejected(err)
	}
	if refreshErr := refresher.Refresh(ctx); refreshErr != nil {
		return "", false, s.rejected(refreshErr)
	}

	value, found, err = s.backend.Fetch(ctx, name)
	if err != nil && errors.Is(err, ErrRejected) {
		return "", false, s.rejected(err)
	}
	return value, found, err
}

// markRefreshed reports whether this process still has its one refresh left.
func (s *Source) markRefreshed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refreshed {
		return false
	}
	s.refreshed = true
	return true
}

// rejected turns a refusal into the error the engine renders as AF-SEC-002.
func (s *Source) rejected(cause error) error {
	// The sentinel's own words are trimmed off the detail. Without this the
	// message reads "Vault at x rejected the credential: the store rejected the
	// credential: 403 permission denied", which says the same thing twice and
	// buries the only part that is specific.
	detail := strings.TrimPrefix(reason(cause), ErrRejected.Error()+": ")
	return &extension.CredentialRejectedError{
		Source: s.backend.Describe(),
		Detail: detail,
		Err:    cause,
	}
}

// licenceReason says which of the licence states turned the source off.
//
// Named states rather than one message, because "the licence expired on 4 June"
// and "this licence belongs to another organization" lead to entirely different
// next actions and the second one is otherwise indistinguishable from a bug.
func licenceReason(status license.Status) string {
	switch status.State {
	case license.StateNone:
		return "the enterprise_secrets feature needs a licence and none is installed"
	case license.StateExpired:
		return "the licence expired on " +
			status.Claims.ExpiresAt.UTC().Format("2 January 2006") +
			" and its grace period has ended"
	case license.StateRevoked:
		return "the licence has been revoked"
	case license.StateWrongOrg:
		return "the installed licence was issued to " + status.Claims.Org
	case license.StateClockRollback:
		return "this machine's clock reads earlier than the licence was last checked at"
	default:
		// Honoured but without this feature: a licence that buys other things
		// and not this one. Worth saying plainly, because the administrator can
		// see they have a licence and would otherwise assume it covers this.
		return "this licence does not include the enterprise_secrets feature"
	}
}

// reason renders an error as the sentence AF-SEC-001 prints beside the source.
//
// Trimmed and flattened to one line, because it is printed inside a
// parenthesised list and a multi-line reason breaks that message apart.
func reason(err error) string {
	if err == nil {
		return ""
	}
	text := strings.TrimSpace(err.Error())
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = strings.TrimSpace(text[:i]) + " ..."
	}
	if text == "" {
		// A backend that failed and said nothing still has to produce a
		// sentence, because the alternative renders in AF-SEC-001 as
		// "the store ()" and reads as a bug in the message rather than a
		// silence in the adapter.
		return "unavailable"
	}
	return text
}

// Register plugs sources into a registry, in order.
//
// Order is the order given, and registered sources are asked after every local
// one, so an organization running both Vault and a cloud secret manager gets a
// deterministic answer. An empty list registers nothing, which leaves the
// engine's chain exactly as the community edition builds it.
func Register(reg *extension.Registry, sources ...*Source) {
	if reg == nil {
		reg = extension.Default
	}
	for _, s := range sources {
		if s == nil {
			continue
		}
		reg.AddSecretSource(s)
	}
}

// wrap is the shape every adapter uses to report a store's refusal, so that the
// distinction between a rejected credential and an unreachable store is made in
// one place rather than four.
func wrap(kind error, format string, args ...any) error {
	return fmt.Errorf("%w: %s", kind, fmt.Sprintf(format, args...))
}
