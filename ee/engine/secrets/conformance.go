package secrets

// The contract every enterprise secret source keeps, as a suite rather than a
// paragraph.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// There are four adapters here and there will be more, and the interesting part
// of each is not how it talks to its store. It is whether it keeps the promises
// the rest of the engine has already made on its behalf: that a miss falls
// through and a failure does not, that an unavailable source says why, that a
// lapsed licence turns it off, and that a rejected credential is retried once
// and then reported. Those are the properties a reviewer cannot see by reading
// an adapter, because each one is about the absence of a mistake.
//
// So they are behaviours with names, run against a real store, and the suite
// itself is proved able to fail: conformance_test.go runs a deliberately broken
// backend through it and asserts that every behaviour it breaks is reported. A
// suite nobody has watched fail is a suite that might be asserting nothing,
// which is worse than no suite because it reads as coverage.
//
// A behaviour that a store cannot exhibit is SKIPPED and named, never silently
// passed. A Vault token cannot be refreshed, so the refresh behaviours skip for
// a token backend and say so. The difference between "this store does not do
// that" and "this store was not checked" is the difference between a suite and
// a decoration.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/antifailure/antifailure/ee/engine/feature"
	"github.com/antifailure/antifailure/ee/engine/license"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// T is the part of testing.TB the suite uses.
//
// An interface rather than *testing.T so that the suite can be run against a
// recorder and its own failures asserted on. That is the only way to know a
// suite can fail.
type T interface {
	Errorf(format string, args ...any)
	Logf(format string, args ...any)
	Helper()
}

// Harness is what a store supplies so the suite can put it into each state.
//
// The states are the point. Anybody can test the happy path against a live
// store; what is hard, and what has never been checked in this codebase, is
// what a source does when its credential is wrong and when its store is gone.
// Those cannot be produced generically, because "a wrong credential" is a
// different object for Vault than for Key Vault, so each adapter builds them.
type Harness struct {
	// Name identifies the store in the output.
	Name string

	// Working is a source that can read Present and cannot read Absent.
	//
	// Typed as the interface the engine plugs in rather than as *Source, so
	// that the suite can be pointed at anything shaped like a source. That is
	// not generality for its own sake: it is what lets conformance_test.go run
	// a deliberately ungated implementation through the licence behaviours and
	// watch them fail, which is the only way to know those behaviours assert
	// anything.
	Working extension.SecretSource

	// Present is a variable the store holds, with the value it holds.
	Present      string
	PresentValue string
	// Empty is a variable the store holds with an empty value, or "" when the
	// store cannot represent one. Present-and-empty has to stop the chain, and
	// a store that cannot hold an empty value skips that behaviour explicitly.
	Empty string
	// Absent is a variable the store does not hold.
	Absent string

	// Rejecting is a source whose credential the store will refuse. Nil skips
	// the rejection behaviours, which is only correct for a store that cannot
	// refuse a credential, and there is no such store.
	Rejecting extension.SecretSource
	// Refreshes counts how many times Rejecting has renewed its credential.
	//
	// Supplied by the adapter rather than read out of the backend, because the
	// number is the whole assertion and reaching into a private field to get it
	// would make the suite depend on how each adapter is built. Nil means the
	// store's credential cannot be renewed by us, which is true of an operator
	// supplied Vault token, and the refresh behaviour then skips and says so.
	Refreshes func() int

	// Unreachable is a source pointed at an address nothing answers on. Nil
	// skips the unreachable behaviours.
	Unreachable extension.SecretSource
}

// Result is what the suite found.
type Result struct {
	Passed  []string
	Skipped map[string]string
	Failed  []string
}

// Run exercises every behaviour and returns what happened.
//
// It never stops at the first failure. An adapter that gets three things wrong
// should be told about three things, because they are usually one cause and
// seeing all three is how somebody finds it.
func Run(ctx context.Context, t T, h Harness) Result {
	t.Helper()
	r := Result{Skipped: map[string]string{}}

	// A licence that permits everything, so the behaviours about lookups are
	// about lookups. The licence behaviours build their own contexts.
	licensed := withFeatures(ctx, license.FeatureSecrets)

	behaviours := []struct {
		name string
		run  func(*checker)
	}{
		{"describes where it is", func(c *checker) {
			name := h.Working.Name()
			if strings.TrimSpace(name) == "" {
				c.fail("Name() is empty, so AF-SEC-001 would list a blank line as a place to put the value")
			}
			if len(name) < 4 {
				c.fail("Name() is %q, which does not tell two stores apart", name)
			}
		}},

		{"is available and says nothing when it is usable", func(c *checker) {
			ok, why := h.Working.Available(licensed)
			if !ok {
				c.fail("a working store reports itself unavailable: %s", why)
				return
			}
			if why != "" {
				c.fail("a usable source gave a reason (%q); the reason is for when it cannot be used", why)
			}
		}},

		{"finds a value it holds", func(c *checker) {
			value, found, err := h.Working.Lookup(licensed, h.Present)
			if err != nil {
				c.fail("looking up %s failed: %v", h.Present, err)
				return
			}
			if !found {
				c.fail("%s is in the store and was reported as absent", h.Present)
				return
			}
			if value != h.PresentValue {
				c.fail("%s came back changed: got %d bytes, expected %d",
					h.Present, len(value), len(h.PresentValue))
			}
		}},

		{"reports a name it does not hold as a miss, not a failure", func(c *checker) {
			// The behaviour the whole chain rests on. A miss falls through to
			// the next source; an error stops the environment. A store that
			// reports "no such secret" as an error makes every variable it does
			// not hold fatal, which turns a source somebody added for one
			// credential into a source that breaks the other nineteen.
			_, found, err := h.Working.Lookup(licensed, h.Absent)
			if err != nil {
				c.fail("a name the store does not hold produced an error rather than a miss: %v", err)
				return
			}
			if found {
				c.fail("%s is not in the store and was reported as found", h.Absent)
			}
		}},

		{"reports a value it holds as empty as present", func(c *checker) {
			if h.Empty == "" {
				c.skip("this store cannot hold an empty value")
				return
			}
			// Present and empty has to stop the search, so that a variable
			// somebody deliberately emptied does not fall through to a lower
			// priority source still holding last month's value.
			value, found, err := h.Working.Lookup(licensed, h.Empty)
			if err != nil {
				c.fail("looking up the empty %s failed: %v", h.Empty, err)
				return
			}
			if !found {
				c.fail("%s is stored with an empty value and was reported as absent", h.Empty)
			}
			if value != "" {
				c.fail("%s is stored empty and came back with %d bytes", h.Empty, len(value))
			}
		}},

		{"is unavailable with a reason when the store cannot be reached", func(c *checker) {
			if h.Unreachable == nil {
				c.skip("the harness supplied no unreachable source")
				return
			}
			ok, why := h.Unreachable.Available(licensed)
			if ok {
				c.fail("a source pointed at nothing reports itself usable")
				return
			}
			if strings.TrimSpace(why) == "" {
				c.fail("an unreachable store gave no reason, so AF-SEC-001 would print " +
					"\"not present\" for a store that is present and unreachable")
			}
			c.logf("reports: %s (%s)", h.Unreachable.Name(), why)
		}},

		{"an unreachable store is never mistaken for a miss", func(c *checker) {
			if h.Unreachable == nil {
				c.skip("the harness supplied no unreachable source")
				return
			}
			// The worst available failure. A store that is down reporting every
			// variable as absent makes the chain fall through to nothing and
			// the user is told the variable is not set, in a message that lists
			// the store as a source that was asked and had no answer.
			_, found, err := h.Unreachable.Lookup(licensed, h.Present)
			if found {
				c.fail("an unreachable store answered with a value")
			}
			if err == nil {
				c.fail("an unreachable store reported a miss rather than a failure")
			}
		}},

		{"reports a refused credential as refused, naming itself", func(c *checker) {
			if h.Rejecting == nil {
				c.skip("the harness supplied no source with a bad credential")
				return
			}
			_, found, err := h.Rejecting.Lookup(licensed, h.Present)
			if found {
				c.fail("a store that refuses the credential answered with a value")
			}
			if err == nil {
				c.fail("a refused credential was reported as a miss, which falls through " +
					"to a lower priority source and hands the application a different secret")
				return
			}
			var rejected *extension.CredentialRejectedError
			if !errors.As(err, &rejected) {
				c.fail("a refused credential produced %T rather than a CredentialRejectedError, "+
					"so the engine cannot render AF-SEC-002", err)
				return
			}
			if rejected.Source == "" {
				c.fail("the refusal names no source, so AF-SEC-002 cannot say which credential to rotate")
			}
			if rejected.Detail == "" {
				c.fail("the refusal carries no detail, so the operator is told only that it failed")
			}
			c.logf("reports: %s", rejected.Error())
		}},

		{"refreshes a refused credential once and no more", func(c *checker) {
			if h.Rejecting == nil {
				c.skip("the harness supplied no source with a bad credential")
				return
			}
			if h.Refreshes == nil {
				c.skip("this store's credential cannot be renewed by us, so there is nothing to refresh")
				return
			}
			for range 5 {
				_, _, _ = h.Rejecting.Lookup(licensed, h.Present)
			}
			// Counted from zero rather than as a delta, because the rule is one
			// refresh per process and not one per behaviour. An earlier
			// behaviour in this suite already made a refused lookup against the
			// same source, and a delta would read that as a second allowance
			// and pass a source that renews on every call.
			switch renewals := h.Refreshes(); {
			case renewals == 0:
				c.fail("a renewable credential was refused and never renewed, so a token " +
					"that had merely expired is reported as revoked")
			case renewals > 1:
				c.fail("the credential was renewed %d times; the rule is once per process, "+
					"or twenty declared variables against a revoked credential become twenty "+
					"logins and a rate limit on the store everybody else is using", renewals)
			default:
				c.logf("renewed once across every refused lookup in this suite")
			}
		}},

		{"is off with a reason when there is no licence", func(c *checker) {
			// Degrades to the community behaviour, and says so. An
			// administrator whose licence lapsed has to be told that rather
			// than shown a source that has silently stopped answering.
			ok, why := h.Working.Available(context.Background())
			if ok {
				c.fail("the source is usable with no licence, so the enterprise_secrets feature is free")
				return
			}
			if !strings.Contains(strings.ToLower(why), "licen") {
				c.fail("with no licence the reason is %q, which does not mention a licence", why)
			}
			c.logf("reports: %s (%s)", h.Working.Name(), why)
		}},

		{"is off with a reason when the licence has expired", func(c *checker) {
			expired := withStatus(ctx, expiredStatus())
			ok, why := h.Working.Available(expired)
			if ok {
				c.fail("an expired licence still permits the feature")
				return
			}
			if !strings.Contains(strings.ToLower(why), "expired") {
				c.fail("with an expired licence the reason is %q, which does not say it expired", why)
			}
			c.logf("reports: %s (%s)", h.Working.Name(), why)
		}},

		{"is off with a reason when the licence does not include this feature", func(c *checker) {
			// A licence that buys other things. Worth its own sentence: an
			// administrator can see they have a valid licence and would
			// otherwise assume it covers this.
			other := withFeatures(ctx, license.FeatureSSO)
			ok, why := h.Working.Available(other)
			if ok {
				c.fail("a licence without enterprise_secrets still permits the feature")
				return
			}
			if strings.TrimSpace(why) == "" {
				c.fail("a licence without the feature gave no reason")
			}
			c.logf("reports: %s (%s)", h.Working.Name(), why)
		}},
	}

	for _, b := range behaviours {
		c := &checker{t: t, name: b.name, store: h.Name}
		b.run(c)
		switch {
		case c.failed:
			r.Failed = append(r.Failed, b.name)
		case c.skipped != "":
			r.Skipped[b.name] = c.skipped
			t.Logf("SKIP %s: %s (%s)", h.Name, b.name, c.skipped)
		default:
			r.Passed = append(r.Passed, b.name)
		}
	}

	sort.Strings(r.Passed)
	sort.Strings(r.Failed)
	return r
}

// checker records one behaviour's outcome.
type checker struct {
	t       T
	name    string
	store   string
	failed  bool
	skipped string
}

func (c *checker) fail(format string, args ...any) {
	c.failed = true
	c.t.Helper()
	c.t.Errorf("%s: %s: %s", c.store, c.name, fmt.Sprintf(format, args...))
}

func (c *checker) skip(why string) { c.skipped = why }

func (c *checker) logf(format string, args ...any) {
	c.t.Logf("  %s: %s: %s", c.store, c.name, fmt.Sprintf(format, args...))
}

// withFeatures attaches a licence granting exactly these features.
func withFeatures(ctx context.Context, features ...license.Feature) context.Context {
	return withStatus(ctx, activeStatus(features...))
}

func withStatus(ctx context.Context, s license.Status) context.Context {
	return feature.With(ctx, s)
}

// activeStatus builds an honoured licence, through the verifier so that the
// suite exercises the same evaluation path the product does rather than a
// hand-built Status that could not occur.
func activeStatus(features ...license.Feature) license.Status {
	v := license.NewVerifier(nil)
	return v.Evaluate(license.Claims{
		ID: "conformance", Org: "conformance", Plan: "enterprise",
		Features: features, IssuedAt: time.Now().Add(-time.Hour),
		ExpiresAt: time.Now().Add(365 * 24 * time.Hour),
	}, license.Evaluation{Org: "conformance", Now: time.Now()})
}

func expiredStatus() license.Status {
	v := license.NewVerifier(nil)
	return v.Evaluate(license.Claims{
		ID: "conformance", Org: "conformance", Plan: "enterprise",
		Features:  []license.Feature{license.FeatureSecrets},
		IssuedAt:  time.Now().Add(-400 * 24 * time.Hour),
		ExpiresAt: time.Now().Add(-90 * 24 * time.Hour),
	}, license.Evaluation{Org: "conformance", Now: time.Now()})
}
