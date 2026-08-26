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
// entry point begins with this, and there is a test that every feature has at
// least one call site, because a feature nobody checks is a feature that is
// either free or missing and both are wrong.
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
