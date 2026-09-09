// Package feature is the one question enterprise code asks before doing
// anything: is this permitted here, right now.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// It is a package rather than a method on a license so that the check has one
// shape at every call site, and so that the answer travels in a context rather
// than being threaded through every signature. Enterprise entry points are deep
// in request handlers, and a license argument on every function between the
// handler and the check is a change that never gets made and a check that
// therefore never happens.
//
// The default answer is no. A context with no license carries no features, so
// code that forgets to attach one degrades to the community behaviour rather
// than granting everything. That is the direction the mistake has to fail in.
package feature

import (
	"context"
	"sync"

	"github.com/antifailure/antifailure/ee/engine/license"
)

type contextKey struct{}

// With attaches a license status to a context.
func With(ctx context.Context, status license.Status) context.Context {
	return context.WithValue(ctx, contextKey{}, status)
}

// StatusFrom reads the status, or the no-license status.
func StatusFrom(ctx context.Context) license.Status {
	if s, ok := ctx.Value(contextKey{}).(license.Status); ok {
		return s
	}
	return license.None()
}

// Enabled reports whether a feature may be used.
//
// The whole public surface for the rest of the enterprise code. Every enterprise
// entry point that gates on a licence begins with this.
//
// THE SENTENCE THAT USED TO BE HERE WAS FALSE, and it is worth saying so rather
// than quietly deleting it, because it is the same defect as the one this
// package exists to catch. It read "there is a test that every feature has at
// least one call site, because a feature nobody checks is a feature that is
// either free or missing and both are wrong". There was no such test. What
// existed was TestDeclaredSitesAreRecordedForTheDeadCodeCheck, which declared a
// site naming ee/web/auth.Handler, a package that has never existed, from a
// test binary that links none of the packages doing the enforcing, and then
// asserted that Sites(FeatureCompliance) was EMPTY. It was empty because
// nothing in that binary could have filled it, so the assertion could not fail
// for any reason to do with the product. A comment claiming a check, sitting
// above a check that cannot say no, is exactly what a licensed feature that
// enforces nothing looks like from the inside.
//
// The claim was also wrong on its own terms. Four of the fourteen features have
// no call site and that is the measured, published state of this product rather
// than a bug to be asserted away; see catalogue.go. What is actually true, and
// what is actually tested:
//
//   - Every feature a licence can carry has a catalogue entry saying what
//     happens without it. TestEveryLicensedFeatureIsInTheCatalogue.
//   - A catalogue entry that CLAIMS enforcement names a file that contains a
//     literal Enabled call for that exact feature.
//     TestAGatedEntryNamesAFileThatChecksThatExactFeature.
//   - Every Declare corresponds to a catalogue entry marked gated, and every
//     gated entry to a Declare, checked from ee/engine/cmd/af, which is the
//     only package where every enterprise init has run and this registry is
//     therefore populated at all.
//   - Each gated feature's real entry point behaves differently with the
//     entitlement and without it. TestTheEntitlementIsWhatDecides.
func Enabled(ctx context.Context, f license.Feature) bool {
	return StatusFrom(ctx).Enabled(f)
}

// Registry records where each feature is checked.
//
// Populated by init functions in the packages that implement each feature. Its
// purpose is a test: a feature declared in the license and checked nowhere is a
// feature that is silently free, and a call site for a feature that no license
// can grant is dead code. Both are invisible without a list.
type Registry struct {
	mu    sync.Mutex
	sites map[license.Feature][]string
}

var global = &Registry{sites: map[license.Feature][]string{}}

// Declare records that a feature is enforced at a named site.
func Declare(f license.Feature, site string) {
	global.mu.Lock()
	defer global.mu.Unlock()
	global.sites[f] = append(global.sites[f], site)
}

// Sites returns everywhere a feature is enforced.
func Sites(f license.Feature) []string {
	global.mu.Lock()
	defer global.mu.Unlock()
	out := make([]string, len(global.sites[f]))
	copy(out, global.sites[f])
	return out
}

// Declared returns every feature with at least one enforcement site.
func Declared() []license.Feature {
	global.mu.Lock()
	defer global.mu.Unlock()
	out := make([]license.Feature, 0, len(global.sites))
	for f := range global.sites {
		out = append(out, f)
	}
	return out
}
