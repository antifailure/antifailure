package golden_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/antifailure/antifailure/engine/internal/golden"
)

func versions(n int, base time.Time) []golden.Version {
	out := make([]golden.Version, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, golden.Version{
			ID:        fmt.Sprintf("gv_%02d", i),
			CreatedAt: base.Add(time.Duration(i) * time.Hour),
			Verified:  true,
		})
	}
	return out
}

func removed(decisions []golden.Decision) []string {
	var out []string
	for _, d := range decisions {
		if d.Remove {
			out = append(out, d.Version.ID)
		}
	}
	return out
}

func TestSweep_KeepsTheNewestAndRemovesTheRest(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	decisions := golden.Sweep(versions(6, base), 3)

	require.Len(t, decisions, 6)
	require.Equal(t, []string{"gv_02", "gv_01", "gv_00"}, removed(decisions),
		"the three oldest go, newest first in the report")
	for _, d := range decisions[:3] {
		require.False(t, d.Remove)
		require.NotEmpty(t, d.Reason, "why it stayed is as often asked as why it went")
	}
}

func TestSweep_NeverRemovesTheNewestVerifiedWhateverTheCountSays(t *testing.T) {
	t.Parallel()
	// A retention setting is about disk. A project with nothing left to branch
	// cannot bring an environment up at all, which is worse than the disk it
	// saved, so the count is overruled rather than obeyed.
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	for _, keep := range []int{0, -1, 1} {
		decisions := golden.Sweep(versions(3, base), keep)
		require.NotContains(t, removed(decisions), "gv_02",
			"keep=%d still leaves something to branch", keep)
		require.Len(t, decisions, 3)
	}
}

func TestSweep_KeepsTheNewestVerifiedRatherThanTheNewest(t *testing.T) {
	t.Parallel()
	// An unverified version was never publishable, so it is neither what an
	// environment branches nor what makes a project fresh. Protecting the
	// newest version rather than the newest VERIFIED one would protect the
	// wrong artifact and collect the only usable golden.
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	vs := []golden.Version{
		{ID: "gv_old", CreatedAt: base, Verified: true},
		{ID: "gv_new", CreatedAt: base.Add(time.Hour), Verified: false},
	}
	decisions := golden.Sweep(vs, 1)
	require.Equal(t, []string{"gv_new"}, removed(decisions),
		"the unverified one goes, and it never counts against the budget: "+
			"counting it would let something unbranchable push out the only usable golden")
	for _, d := range decisions {
		if d.Version.ID == "gv_new" {
			require.Contains(t, d.Reason, "not verified")
		}
	}

	kept, ok := golden.Newest(vs)
	require.True(t, ok)
	require.Equal(t, "gv_old", kept.ID)
}

func TestSweep_IsDeterministicWhenTwoWereMadeInTheSameSecond(t *testing.T) {
	t.Parallel()
	// Two runners can finish a refresh in the same second. Ordering by time
	// alone would leave the tiebreak to whatever order the provider listed
	// them in, and a garbage collector that removes a different one on each
	// run is one nobody trusts.
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	vs := []golden.Version{
		{ID: "gv_a", CreatedAt: base, Verified: true},
		{ID: "gv_b", CreatedAt: base, Verified: true},
		{ID: "gv_c", CreatedAt: base, Verified: true},
	}
	first := removed(golden.Sweep(vs, 2))
	for i := 0; i < 10; i++ {
		require.Equal(t, first, removed(golden.Sweep(vs, 2)))
	}
	// And reversing the input does not change the answer.
	reversed := []golden.Version{vs[2], vs[1], vs[0]}
	require.Equal(t, first, removed(golden.Sweep(reversed, 2)))
}

func TestSweep_AlwaysLeavesSomethingBranchable(t *testing.T) {
	// The property, over random inputs, because the counterexample that
	// matters is the one nobody thought to write down: any set of versions,
	// any count, and a caller that removes everything Sweep names must still
	// have a verified golden afterwards.
	rapid.Check(t, func(t *rapid.T) {
		base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
		n := rapid.IntRange(0, 12).Draw(t, "count")
		keep := rapid.IntRange(-2, 15).Draw(t, "keep")

		var vs []golden.Version
		anyVerified := false
		for i := 0; i < n; i++ {
			verified := rapid.Bool().Draw(t, fmt.Sprintf("verified%d", i))
			anyVerified = anyVerified || verified
			vs = append(vs, golden.Version{
				ID: fmt.Sprintf("gv_%02d", i),
				// Deliberately lumpy: some share a timestamp, which is the
				// case the tiebreak exists for.
				CreatedAt: base.Add(time.Duration(i/2) * time.Hour),
				Verified:  verified,
			})
		}

		decisions := golden.Sweep(vs, keep)
		if len(decisions) != len(vs) {
			t.Fatalf("every version needs a decision: %d in, %d out", len(vs), len(decisions))
		}

		gone := map[string]bool{}
		for _, d := range decisions {
			if d.Reason == "" {
				t.Fatalf("%s has no reason", d.Version.ID)
			}
			if d.Remove {
				gone[d.Version.ID] = true
			}
		}
		var survivors []golden.Version
		for _, v := range vs {
			if !gone[v.ID] {
				survivors = append(survivors, v)
			}
		}
		if _, ok := golden.Newest(survivors); anyVerified && !ok {
			t.Fatalf("a verified golden existed and none survived: keep=%d", keep)
		}
		// And it never keeps more than it was asked to, once the one
		// protected version is accounted for.
		want := keep
		if want < 1 {
			want = 1
		}
		if len(survivors) > want+1 {
			t.Fatalf("kept %d with keep=%d", len(survivors), keep)
		}
	})
}

func TestStale_IsAboutDriftAndNotAboutAge(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)

	require.True(t, golden.Stale(now.Add(-25*time.Hour), 24*time.Hour, now))
	require.False(t, golden.Stale(now.Add(-23*time.Hour), 24*time.Hour, now))

	require.True(t, golden.Stale(time.Time{}, 24*time.Hour, now),
		"no golden at all is the most stale a project can be")
	require.False(t, golden.Stale(time.Time{}, 0, now),
		"a project that configured no maximum age is never interrupted by a refresh it did not ask for")
	require.False(t, golden.Stale(now.Add(-1000*time.Hour), 0, now))
}
