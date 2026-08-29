package golden

import (
	"fmt"
	"sort"
	"time"
)

// Version is one published golden, as retention sees it.
type Version struct {
	ID        string
	CreatedAt time.Time
	Verified  bool
}

// Decision is what should happen to one version, and why.
//
// A reason on every version rather than only on the ones being removed,
// because "why is this still here" is asked as often as "why did that go", and
// a garbage collector that answers only one of them is one somebody turns off.
type Decision struct {
	Version Version
	Remove  bool
	Reason  string
}

// Sweep decides which versions to remove, newest first.
//
// It never proposes removing the newest verified version, whatever the count
// says. A retention setting is about disk, and a project with no branchable
// golden left cannot bring an environment up at all, which is a worse outcome
// than the disk it saved. A count below one is read as one for the same reason.
//
// It never decides anything about whether a version is referenced. That is the
// provider's refusal, because the provider is the only thing that knows
// whether a branch is still running; a second count kept here could disagree
// with it, and the disagreement would be resolved in favour of deleting the
// copy an environment is using.
func Sweep(versions []Version, keep int) []Decision {
	if keep < 1 {
		keep = 1
	}
	sorted := append([]Version(nil), versions...)
	// Newest first, with the identifier as the tiebreak so that two versions
	// created in the same second are ordered the same way on every run.
	sort.Slice(sorted, func(i, j int) bool {
		if !sorted[i].CreatedAt.Equal(sorted[j].CreatedAt) {
			return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
		}
		return sorted[i].ID > sorted[j].ID
	})

	newestVerified := ""
	for _, v := range sorted {
		if v.Verified {
			newestVerified = v.ID
			break
		}
	}

	out := make([]Decision, 0, len(sorted))
	kept := 0
	for _, v := range sorted {
		switch {
		case v.ID == newestVerified:
			kept++
			out = append(out, Decision{Version: v, Reason: "it is the newest verified golden, and removing it would leave nothing to branch"})
		case !v.Verified:
			// Never counted against the budget and always collected. A version
			// that is not verified cannot be branched, so keeping it holds
			// disk for something no environment can ever use, and counting it
			// would let it push out a golden that can. A provider never
			// publishes one, so this only fires on a version that arrived some
			// other way.
			out = append(out, Decision{
				Version: v, Remove: true,
				Reason: "it is not verified, so nothing can branch from it",
			})
		case kept < keep:
			kept++
			out = append(out, Decision{
				Version: v,
				Reason:  fmt.Sprintf("it is within the %d this project retains", keep),
			})
		default:
			out = append(out, Decision{
				Version: v, Remove: true,
				Reason: fmt.Sprintf("this project retains %d and there are newer ones", keep),
			})
		}
	}
	return out
}

// Stale reports whether the newest golden is old enough that branching it is
// testing data production has moved on from.
//
// A zero creation time is stale: there is no golden, which is the most stale a
// project can be. A maximum age of zero is not a maximum age at all and never
// makes anything stale, so a project that has not configured one is never
// interrupted by a refresh it did not ask for.
func Stale(newest time.Time, maxAge time.Duration, now time.Time) bool {
	if maxAge <= 0 {
		return false
	}
	if newest.IsZero() {
		return true
	}
	return now.Sub(newest) > maxAge
}

// Newest returns the most recent verified version, and false when there is
// none. An unverified version is not a candidate: it was never publishable, so
// it is neither what an environment branches nor what makes a project fresh.
func Newest(versions []Version) (Version, bool) {
	var best Version
	found := false
	for _, v := range versions {
		if !v.Verified {
			continue
		}
		if !found || v.CreatedAt.After(best.CreatedAt) {
			best, found = v, true
		}
	}
	return best, found
}
