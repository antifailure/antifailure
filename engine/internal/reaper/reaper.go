// Package reaper decides which environments have outlived their stated
// lifetime, and removes them.
//
// It exists because an environment nobody tore down is not a tidiness problem.
// Every one of them holds a database branch, a network and a container per
// service, and the customer who forgets about six of them for a fortnight pays
// for six of them for a fortnight. runtime.ttl has been in the manifest,
// validated, defaulted and printed by af explain since the manifest existed,
// and until this package nothing read it: every environment lived until
// somebody remembered it.
//
// Two rules shape everything here, and both exist because the thing this code
// does is irreversible.
//
// An environment is expired only when it says so itself. The expiry is stamped
// on the resources at creation, by whichever engine created them, and read back
// off them here. It is never derived from the manifest of whoever happens to be
// running the sweep. That is what makes it safe to run in a repository whose
// manifest has a two hour ttl on a machine that is also holding another
// project's week long environment: the sweep reads the other project's stated
// expiry, not this one's.
//
// Nothing without a stated expiry is ever destroyed. A resource created by an
// older release carries no expiry label, and treating "no lifetime stated" as
// "lifetime already over" would turn an upgrade into a machine wipe. Those are
// what `af env prune --older-than` is for, where a person names the cutoff.
package reaper

import (
	"context"
	"sort"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// Expired is one environment whose lifetime has passed.
type Expired struct {
	// EnvID is the environment identifier every resource is labelled with.
	EnvID string
	// ExpiresAt is when its lifetime ended.
	ExpiresAt time.Time
	// Overdue is how far past that the sweep found it.
	Overdue time.Duration
	// Resources is how many things the inventory attributes to it.
	Resources int
	// Extended reports that the expiry came from a lease somebody took out
	// with af env extend rather than from the resources themselves.
	Extended bool
}

// Plan decides which of an inventory's environments are expired.
//
// `leases` maps an environment id to an expiry that overrides whatever its
// resources carry. It is how af env extend works: a container's labels cannot
// be changed after it is created, so an extension is recorded beside the
// journal and consulted here. A lease wins in both directions, later and
// earlier, because it is the more recent deliberate statement about the
// environment and the whole point of taking one out is to be believed.
//
// Within one environment the LATEST stated expiry wins. An environment is a
// set of resources created over a span of time, and the conservative reading
// of a set that disagrees with itself is the one that destroys later: a sweep
// that removed an environment because its oldest network had expired would
// take the containers somebody started ten minutes ago with it.
//
// The result is sorted by environment id, so two runs over the same inventory
// produce the same plan in the same order.
func Plan(resources []provider.Resource, leases map[string]time.Time, now time.Time) []Expired {
	type acc struct {
		expires time.Time
		stated  bool
		count   int
	}
	byEnv := map[string]*acc{}
	for _, res := range resources {
		if res.EnvID == "" {
			// Machine scoped: the sidecar image and the forwarder image, which
			// every environment on this daemon shares. No environment's
			// lifetime may destroy them.
			continue
		}
		a, ok := byEnv[res.EnvID]
		if !ok {
			a = &acc{}
			byEnv[res.EnvID] = a
		}
		a.count++
		at, ok := expiryOf(res)
		if !ok {
			continue
		}
		if !a.stated || at.After(a.expires) {
			a.expires, a.stated = at, true
		}
	}

	// A lease for an environment the inventory has never heard of is not an
	// environment: it is a stale record for something already gone, and
	// planning a teardown for it would report destroying something that does
	// not exist.
	for envID, at := range leases {
		a, ok := byEnv[envID]
		if !ok {
			continue
		}
		a.expires, a.stated = at, true
	}

	var out []Expired
	for envID, a := range byEnv {
		if !a.stated || !a.expires.Before(now) {
			continue
		}
		_, leased := leases[envID]
		out = append(out, Expired{
			EnvID:     envID,
			ExpiresAt: a.expires,
			Overdue:   now.Sub(a.expires),
			Resources: a.count,
			Extended:  leased,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EnvID < out[j].EnvID })
	return out
}

// expiryOf reads the expiry an inventory resource carries.
//
// The runtimes put it in the display label map rather than making the reaper
// speak Docker and Kubernetes, so this reads one shape for both. Absent, empty
// and unparseable all mean the same thing: this resource states no lifetime.
func expiryOf(res provider.Resource) (time.Time, bool) {
	raw := res.Labels["expires"]
	if raw == "" {
		return time.Time{}, false
	}
	secs, err := parseUnix(raw)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(secs, 0).UTC(), true
}

// Destroyer removes one environment completely.
//
// An interface rather than the orchestrator itself, so that the sweep can be
// tested against something that records what it was asked to destroy without
// a Docker daemon in the loop. The count returned is resources removed.
type Destroyer interface {
	Destroy(ctx context.Context, envID string) (removed int, err error)
}

// Outcome is what the sweep did to one environment.
type Outcome struct {
	Expired
	// Removed is how many resources went.
	Removed int
	// Err is why it did not go, or nil.
	Err error
}

// Result is one sweep.
type Result struct {
	// Scanned is how many environments the inventory held, expired or not.
	Scanned int
	// Outcomes is one entry per environment the plan named, in plan order.
	Outcomes []Outcome
}

// Removed totals the resources this sweep destroyed.
func (r Result) Removed() int {
	n := 0
	for _, o := range r.Outcomes {
		n += o.Removed
	}
	return n
}

// Failed counts the environments the sweep could not remove.
func (r Result) Failed() int {
	n := 0
	for _, o := range r.Outcomes {
		if o.Err != nil {
			n++
		}
	}
	return n
}

// Sweep destroys every environment the plan names.
//
// It never stops at the first failure, for the same reason teardown does not:
// one unreachable provider must not strand the other five environments. Every
// failure is reported against the environment it belongs to and the sweep
// carries on.
//
// A cancelled context stops the sweep between environments rather than in the
// middle of one, so an interrupt leaves environments either untouched or fully
// removed and never half of each.
func Sweep(
	ctx context.Context,
	resources []provider.Resource,
	leases map[string]time.Time,
	now time.Time,
	d Destroyer,
) Result {
	seen := map[string]bool{}
	for _, res := range resources {
		if res.EnvID != "" {
			seen[res.EnvID] = true
		}
	}
	result := Result{Scanned: len(seen)}
	for _, e := range Plan(resources, leases, now) {
		if err := ctx.Err(); err != nil {
			result.Outcomes = append(result.Outcomes, Outcome{Expired: e, Err: err})
			return result
		}
		removed, err := d.Destroy(ctx, e.EnvID)
		result.Outcomes = append(result.Outcomes, Outcome{Expired: e, Removed: removed, Err: err})
	}
	return result
}
