package docker

// THE TWO CALLERS OF ONE LISTING, AND WHY THEY MUST NOT GET THE SAME ANSWER.
//
// `rebased` answers "which copies of goldens onto other builds exist". Two things
// ask, and they want opposite readings of a daemon that would not answer.
//
// The INVENTORY is describing what it can see, for a leak detector. An empty
// answer from a failed listing is the honest reading there: nothing is decided on
// it, and a listing that raised would turn `af status` red because a daemon
// hiccuped.
//
// DESTROY ACTS on the answer, and it is the LAST moment anything can act. The
// copies are labelled with a golden version, and the version is about to stop
// existing, so reading a failed listing as "there are none" removes the golden and
// leaves a full sized image per build that ever opened it with nothing left to
// find it by. That is a transient failure turned into a permanent leak.
//
// WHY THIS IS AN INTERNAL TEST AND WHY IT STOPS WHERE IT DOES. The provider holds
// a concrete *client.Client, so there is no seam to put a failing fake behind, and
// a cancelled context is the only way to make a real listing fail for a real
// reason. That reaches the two functions directly and it does NOT reach
// DestroyGolden, because a cancelled context fails its container listing first,
// several steps earlier: a test that asserted the golden survived a cancelled
// destroy would pass whether or not this change existed, which makes it no test at
// all. So this proves the mechanism that was changed, the whole of it, and the
// pull request says plainly that the end to end path through DestroyGolden is
// covered by reading rather than by a test, and what a test would need.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
)

func TestOnlyTheCallerThatActsOnTheListingIsToldItFailed(t *testing.T) {
	p, err := New(Options{Version: 17, PortFrom: 49960, Clock: clock.New()})
	if err != nil {
		t.Skipf("no Docker daemon to ask: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })

	// Proved reachable first, against a daemon that IS answering, so that the
	// failure below is the cancel rather than a provider that cannot list at all.
	live, cancelLive := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelLive()
	if _, err := p.rebasedOrError(live, ""); err != nil {
		t.Skipf("the daemon would not list images even without a cancel: %v", err)
	}

	dead, stop := context.WithCancel(context.Background())
	stop()

	// The caller that ACTS is told.
	_, err = p.rebasedOrError(dead, "gv_nothing_at_all")
	require.Error(t, err,
		"destroy acts on this answer and removes a golden, so a listing that could not run "+
			"has to say so rather than read as no copies")
	require.ErrorIs(t, err, context.Canceled,
		"and the cause has to survive, or the caller cannot tell a wedged daemon from an "+
			"empty daemon")

	// The caller that only DESCRIBES is not.
	require.Nil(t, p.rebased(dead, "gv_nothing_at_all"),
		"the inventory describes what it could see, and a listing it could not run is "+
			"nothing to describe rather than a reason to fail af status")

}
