package scheduler_test

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/scheduler"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func run(id, org, repo string, lane scheduler.Lane, queuedAt time.Time) scheduler.Run {
	return scheduler.Run{ID: id, Org: org, Repo: repo, Branch: id, Lane: lane, QueuedAt: queuedAt, Cost: 1}
}

func runtime(name string, free int, tags map[string]string) scheduler.Runtime {
	return scheduler.Runtime{Name: name, Free: free, Healthy: true, Tags: tags}
}

func nothingHeld() scheduler.Held {
	return scheduler.Held{ByOrg: map[string]int{}, ByRepo: map[string]int{}}
}

// ---------------------------------------------------------------------------
// The properties. Each one is a failure somebody has had.
// ---------------------------------------------------------------------------

func TestNoOrganizationExceedsItsLimit(t *testing.T) {
	t.Parallel()
	// A limit that is exceeded produces an error after the user has been told
	// their environment is starting, which is the worst moment to find out.
	seed := rand.NewPCG(1, 2)
	rng := rand.New(seed)

	for trial := range 200 {
		limit := 1 + rng.IntN(8)
		var queue []scheduler.Run
		held := nothingHeld()
		for i := range 1 + rng.IntN(40) {
			org := fmt.Sprintf("org-%d", rng.IntN(4))
			queue = append(queue, run(
				fmt.Sprintf("r%d-%d", trial, i), org,
				fmt.Sprintf("%s/repo-%d", org, rng.IntN(3)),
				scheduler.Lane(rng.IntN(3)),
				epoch.Add(time.Duration(rng.IntN(120))*time.Minute),
			))
		}
		for o := range 4 {
			org := fmt.Sprintf("org-%d", o)
			held.ByOrg[org] = rng.IntN(limit + 1)
		}

		cfg := scheduler.Config{
			Limits:  scheduler.Limits{PerOrg: limit},
			AgeStep: scheduler.DefaultAgeStep,
		}
		decisions := scheduler.Plan(queue, []scheduler.Runtime{runtime("a", 1000, nil)}, held, cfg,
			epoch.Add(2*time.Hour))

		after := map[string]int{}
		for k, v := range held.ByOrg {
			after[k] = v
		}
		for _, d := range scheduler.Dispatched(decisions) {
			after[d.Run.Org]++
		}
		for org, n := range after {
			require.LessOrEqualf(t, n, limit,
				"organization %s would hold %d with a limit of %d", org, n, limit)
		}
	}
}

func TestNoRepositoryExceedsItsLimit(t *testing.T) {
	t.Parallel()
	held := nothingHeld()
	held.ByRepo["acme/app"] = 2

	var queue []scheduler.Run
	for i := range 10 {
		queue = append(queue, run(fmt.Sprintf("r%d", i), "acme", "acme/app", scheduler.LaneInteractive, epoch))
	}
	cfg := scheduler.Config{Limits: scheduler.Limits{PerRepo: 3}}
	decisions := scheduler.Plan(queue, []scheduler.Runtime{runtime("a", 100, nil)}, held, cfg, epoch)

	require.Len(t, scheduler.Dispatched(decisions), 1,
		"the repository holds 2 of 3, so exactly one more may start")
}

func TestALimitSaysWhoseLimitItIsRatherThanWaitingForCapacity(t *testing.T) {
	t.Parallel()
	held := nothingHeld()
	held.ByOrg["acme"] = 5

	decisions := scheduler.Plan(
		[]scheduler.Run{run("a", "acme", "acme/app", scheduler.LaneInteractive, epoch)},
		[]scheduler.Runtime{runtime("big", 1000, nil)},
		held,
		scheduler.Config{Limits: scheduler.Limits{PerOrg: 5}},
		epoch,
	)
	require.False(t, decisions[0].Dispatched)
	// There is plenty of capacity. Saying "waiting for capacity" would send
	// somebody to ask the wrong person for the wrong thing.
	require.Contains(t, decisions[0].Reason, "organization is holding 5 of 5")
	require.NotContains(t, decisions[0].Reason, "capacity")
}

func TestEveryQueuedRunEventuallyDispatchesUnderFiniteLoad(t *testing.T) {
	t.Parallel()
	// Fair sharing balances between repositories at the same priority. It says
	// nothing about lanes, so without aging the lowest lane starves for as long
	// as the highest is non-empty. This is that guarantee.
	//
	// The capacity is one, and a fresh pull request arrives every minute, so
	// there is never a spare slot for the batch run to fall into. It starts only
	// when aging has promoted it past the stream, which is the property under
	// test. An earlier version of this gave the runtime two free slots and the
	// batch run started on the first tick, proving nothing at all.
	cfg := scheduler.Config{AgeStep: scheduler.DefaultAgeStep}
	rt := []scheduler.Runtime{runtime("a", 1, nil)}

	queue := []scheduler.Run{
		run("batch", "acme", "acme/loadtest", scheduler.LaneBatch, epoch),
	}

	var startedAfter time.Duration
	dispatched := false
	now := epoch
	for tick := range 300 {
		queue = append(queue,
			run(fmt.Sprintf("pr-%03d", tick), "acme", "acme/app", scheduler.LaneInteractive, now))

		decisions := scheduler.Plan(queue, rt, nothingHeld(), cfg, now)

		// At most one run starts per tick, and while the batch run is starved it
		// is always a pull request.
		started := scheduler.Dispatched(decisions)
		require.LessOrEqual(t, len(started), 1)

		var remaining []scheduler.Run
		for _, d := range decisions {
			if d.Dispatched {
				if d.Run.ID == "batch" {
					dispatched = true
					startedAfter = now.Sub(epoch)
				}
				continue
			}
			if d.Superseded || d.GivenUp {
				continue
			}
			remaining = append(remaining, d.Run)
		}
		if dispatched {
			break
		}
		queue = remaining
		now = now.Add(time.Minute)
	}

	require.True(t, dispatched,
		"a batch run starved behind a steady stream of pull requests, which is what aging exists to prevent")
	// It has to actually have waited. Starting immediately would mean the test
	// never created the contention it claims to.
	require.GreaterOrEqual(t, startedAfter, 2*scheduler.DefaultAgeStep,
		"the batch run started after %s, before aging could have promoted it, so this test is not testing starvation",
		startedAfter)
	t.Logf("the batch run started after %s of waiting", startedAfter)
}

func TestWithoutAgingTheLowestLaneStarves(t *testing.T) {
	t.Parallel()
	// The negative control for the test above. With aging off, the batch run
	// never starts, however long the stream runs. If this ever passes, the
	// starvation test above is passing for some reason other than aging.
	cfg := scheduler.Config{} // AgeStep zero
	rt := []scheduler.Runtime{runtime("a", 1, nil)}
	queue := []scheduler.Run{run("batch", "acme", "acme/loadtest", scheduler.LaneBatch, epoch)}

	now := epoch
	for tick := range 300 {
		queue = append(queue,
			run(fmt.Sprintf("pr-%03d", tick), "acme", "acme/app", scheduler.LaneInteractive, now))
		decisions := scheduler.Plan(queue, rt, nothingHeld(), cfg, now)

		var remaining []scheduler.Run
		for _, d := range decisions {
			if d.Dispatched {
				require.NotEqual(t, "batch", d.Run.ID,
					"the batch run started with aging disabled, so aging is not what promotes it")
				continue
			}
			remaining = append(remaining, d.Run)
		}
		queue = remaining
		now = now.Add(time.Minute)
	}
}

func TestAgingNeverPromotesPastInteractive(t *testing.T) {
	t.Parallel()
	// Otherwise a long-waiting batch run would eventually outrank a pull request
	// somebody is watching, which inverts the whole point of the lanes.
	cfg := scheduler.Config{AgeStep: time.Minute}
	queue := []scheduler.Run{
		run("ancient", "acme", "acme/loadtest", scheduler.LaneBatch, epoch),
		run("fresh", "acme", "acme/app", scheduler.LaneInteractive, epoch.Add(100*time.Hour)),
	}
	decisions := scheduler.Plan(queue, []scheduler.Runtime{runtime("a", 1, nil)}, nothingHeld(), cfg,
		epoch.Add(100*time.Hour))

	// They tie on lane, and the tie breaks on how long each waited, so the
	// ancient one goes first. What must not happen is a lane above interactive.
	dispatched := scheduler.Dispatched(decisions)
	require.Len(t, dispatched, 1)
	require.Equal(t, "ancient", dispatched[0].Run.ID)
}

func TestDecisionsAreDeterministic(t *testing.T) {
	t.Parallel()
	// A decision that cannot be reproduced cannot be explained, and the question
	// people ask is always "why did mine not start".
	rng := rand.New(rand.NewPCG(7, 11))
	var queue []scheduler.Run
	for i := range 300 {
		org := fmt.Sprintf("org-%d", rng.IntN(6))
		queue = append(queue, run(
			fmt.Sprintf("r%03d", i), org,
			fmt.Sprintf("%s/repo-%d", org, rng.IntN(4)),
			scheduler.Lane(rng.IntN(3)),
			epoch.Add(time.Duration(rng.IntN(600))*time.Minute),
		))
	}
	runtimes := []scheduler.Runtime{runtime("a", 20, nil), runtime("b", 15, nil)}
	cfg := scheduler.Config{Limits: scheduler.Limits{PerOrg: 5, PerRepo: 3}, AgeStep: scheduler.DefaultAgeStep}
	now := epoch.Add(11 * time.Hour)

	want := scheduler.Plan(queue, runtimes, nothingHeld(), cfg, now)
	for range 20 {
		// Shuffled, because the answer must depend on the queue's contents and
		// not on the order they happened to be read out of the database.
		shuffled := append([]scheduler.Run(nil), queue...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

		got := scheduler.Plan(shuffled, runtimes, nothingHeld(), cfg, now)
		require.Equal(t, summarize(want), summarize(got),
			"the same queue in a different order produced a different plan")
	}
}

func summarize(decisions []scheduler.Decision) []string {
	out := make([]string, 0, len(decisions))
	for _, d := range decisions {
		out = append(out, fmt.Sprintf("%s->%s dispatched=%t pos=%d",
			d.Run.ID, d.Runtime, d.Dispatched, d.Position))
	}
	sort.Strings(out)
	return out
}

func TestPlanDoesNotMutateItsInputs(t *testing.T) {
	t.Parallel()
	// It is called speculatively, to answer "what would happen if", as often as
	// it is called to act. A speculative call that changed the world would be a
	// scheduler that dispatches by being asked a question.
	held := scheduler.Held{ByOrg: map[string]int{"acme": 1}, ByRepo: map[string]int{"acme/app": 1}}
	queue := []scheduler.Run{run("a", "acme", "acme/app", scheduler.LaneInteractive, epoch)}
	runtimes := []scheduler.Runtime{runtime("a", 5, nil)}

	scheduler.Plan(queue, runtimes, held, scheduler.Config{}, epoch)

	require.Equal(t, 1, held.ByOrg["acme"], "Plan changed the held counts")
	require.Equal(t, 1, held.ByRepo["acme/app"])
	require.Equal(t, 5, runtimes[0].Free, "Plan changed a runtime's free capacity")
	require.Len(t, queue, 1)
}

// ---------------------------------------------------------------------------
// Fairness
// ---------------------------------------------------------------------------

func TestOneBusyRepositoryDoesNotStarveAQuietOne(t *testing.T) {
	t.Parallel()
	var queue []scheduler.Run
	for i := range 50 {
		queue = append(queue, run(fmt.Sprintf("busy-%02d", i), "acme", "acme/monorepo",
			scheduler.LaneInteractive, epoch))
	}
	queue = append(queue, run("quiet", "acme", "acme/small", scheduler.LaneInteractive, epoch))

	decisions := scheduler.Plan(queue, []scheduler.Runtime{runtime("a", 2, nil)}, nothingHeld(),
		scheduler.Config{}, epoch)

	dispatched := scheduler.Dispatched(decisions)
	require.Len(t, dispatched, 2)

	var ids []string
	for _, d := range dispatched {
		ids = append(ids, d.Run.ID)
	}
	// Without fair sharing the busy repository is considered fifty times before
	// the quiet one is considered once, and the quiet repository waits for a
	// monorepo to drain.
	require.Contains(t, ids, "quiet",
		"a repository with one run waited behind a repository with fifty")
}

func TestSimulation_TenThousandRunsAcrossFiftyOrganizations(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(42, 43))

	const orgs, runsTotal = 50, 10_000
	var queue []scheduler.Run
	for i := range runsTotal {
		org := fmt.Sprintf("org-%02d", rng.IntN(orgs))
		queue = append(queue, run(
			fmt.Sprintf("run-%05d", i), org,
			fmt.Sprintf("%s/repo-%d", org, rng.IntN(5)),
			scheduler.Lane(rng.IntN(3)),
			epoch.Add(-time.Duration(rng.IntN(240))*time.Minute),
		))
	}

	runtimes := []scheduler.Runtime{
		runtime("scus-1", 200, map[string]string{"region": "scus"}),
		runtime("scus-2", 200, map[string]string{"region": "scus"}),
		runtime("weu-1", 100, map[string]string{"region": "weu"}),
	}
	cfg := scheduler.Config{
		Limits:  scheduler.Limits{PerOrg: 10, PerRepo: 4},
		AgeStep: scheduler.DefaultAgeStep,
	}

	start := time.Now()
	decisions := scheduler.Plan(queue, runtimes, nothingHeld(), cfg, epoch)
	elapsed := time.Since(start)

	// The exit criterion is a decision in under a second at ten thousand queued
	// runs. Measured on the whole plan rather than on one decision, which is the
	// harder reading of it.
	require.Lessf(t, elapsed, time.Second,
		"planning %d runs took %s, and the budget is one second", runsTotal, elapsed)
	t.Logf("planned %d runs in %s", runsTotal, elapsed)

	byOrg := map[string]int{}
	byRepo := map[string]int{}
	for _, d := range scheduler.Dispatched(decisions) {
		byOrg[d.Run.Org]++
		byRepo[d.Run.Repo]++
	}
	for org, n := range byOrg {
		require.LessOrEqualf(t, n, 10, "%s got %d, above its limit of 10", org, n)
	}
	for repo, n := range byRepo {
		require.LessOrEqualf(t, n, 4, "%s got %d, above its limit of 4", repo, n)
	}

	// Fairness: with every organization holding far more demand than its limit,
	// every one of them should get some capacity. One organization getting
	// nothing while another sits at its limit is the failure this is for.
	require.Len(t, byOrg, orgs,
		"only %d of %d organizations got any capacity", len(byOrg), orgs)

	// And every queued run has an answer, dispatched or with a position.
	for _, d := range decisions {
		if d.Dispatched || d.Superseded || d.GivenUp {
			continue
		}
		require.NotZerof(t, d.Position, "%s is queued with no position to report", d.Run.ID)
	}
}

func TestQueuePositionsCountFromOneAndDoNotRepeat(t *testing.T) {
	t.Parallel()
	// The position goes in a pull request comment. A wrong one is worse than
	// none, because somebody watches it and it does not move.
	var queue []scheduler.Run
	for i := range 20 {
		queue = append(queue, run(fmt.Sprintf("r%02d", i), "acme",
			fmt.Sprintf("acme/repo-%d", i%4), scheduler.LaneInteractive, epoch))
	}
	decisions := scheduler.Plan(queue, []scheduler.Runtime{runtime("a", 3, nil)}, nothingHeld(),
		scheduler.Config{}, epoch)

	seen := map[int]bool{}
	next := 1
	for _, d := range decisions {
		if d.Dispatched {
			require.Zero(t, d.Position, "a dispatched run reported a queue position")
			continue
		}
		require.Equal(t, next, d.Position, "positions must be consecutive from one")
		require.False(t, seen[d.Position], "position %d was reported twice", d.Position)
		seen[d.Position] = true
		next++
	}
}

// ---------------------------------------------------------------------------
// Placement
// ---------------------------------------------------------------------------

func TestPlacementRespectsRequirements(t *testing.T) {
	t.Parallel()
	r := run("a", "acme", "acme/app", scheduler.LaneInteractive, epoch)
	r.Requires = map[string]string{"region": "weu"}

	decisions := scheduler.Plan([]scheduler.Run{r}, []scheduler.Runtime{
		runtime("scus", 10, map[string]string{"region": "scus"}),
		runtime("weu", 1, map[string]string{"region": "weu"}),
	}, nothingHeld(), scheduler.Config{}, epoch)

	require.True(t, decisions[0].Dispatched)
	// Not the one with more room. A residency requirement is not a preference.
	require.Equal(t, "weu", decisions[0].Runtime)
}

func TestUnsatisfiablePlacementNamesTheRequirement(t *testing.T) {
	t.Parallel()
	// "No runtime available" and "no runtime matches region=weu" are different
	// problems fixed by different people. Saying the first when it is the second
	// sends somebody to add capacity that will not help.
	r := run("a", "acme", "acme/app", scheduler.LaneInteractive, epoch)
	r.Requires = map[string]string{"region": "weu", "residency": "eu"}

	decisions := scheduler.Plan([]scheduler.Run{r},
		[]scheduler.Runtime{runtime("scus", 100, map[string]string{"region": "scus"})},
		nothingHeld(), scheduler.Config{}, epoch)

	require.False(t, decisions[0].Dispatched)
	require.Contains(t, decisions[0].Reason, "region=weu")
	require.NotContains(t, decisions[0].Reason, "full")
}

func TestAnUnsatisfiableRequirementIsReportedTheSameWayEveryTime(t *testing.T) {
	t.Parallel()
	// Two unmet requirements, and the one reported must not depend on map
	// iteration order, or the message changes between identical runs.
	r := run("a", "acme", "acme/app", scheduler.LaneInteractive, epoch)
	r.Requires = map[string]string{"region": "weu", "class": "gpu"}

	first := scheduler.Plan([]scheduler.Run{r},
		[]scheduler.Runtime{runtime("scus", 100, nil)}, nothingHeld(), scheduler.Config{}, epoch)
	for range 50 {
		again := scheduler.Plan([]scheduler.Run{r},
			[]scheduler.Runtime{runtime("scus", 100, nil)}, nothingHeld(), scheduler.Config{}, epoch)
		require.Equal(t, first[0].Reason, again[0].Reason)
	}
	require.Contains(t, first[0].Reason, "class=gpu", "requirements should report in sorted order")
}

func TestAnUnhealthyRuntimeTakesNothingNewAndSaysSo(t *testing.T) {
	t.Parallel()
	sick := runtime("a", 100, nil)
	sick.Healthy = false

	decisions := scheduler.Plan(
		[]scheduler.Run{run("a", "acme", "acme/app", scheduler.LaneInteractive, epoch)},
		[]scheduler.Runtime{sick}, nothingHeld(), scheduler.Config{}, epoch)

	require.False(t, decisions[0].Dispatched)
	require.Contains(t, decisions[0].Reason, "unhealthy")
}

func TestLoadSpreadsRatherThanFillingOneRuntime(t *testing.T) {
	t.Parallel()
	var queue []scheduler.Run
	for i := range 6 {
		queue = append(queue, run(fmt.Sprintf("r%d", i), "acme",
			fmt.Sprintf("acme/repo-%d", i), scheduler.LaneInteractive, epoch))
	}
	decisions := scheduler.Plan(queue,
		[]scheduler.Runtime{runtime("a", 10, nil), runtime("b", 10, nil)},
		nothingHeld(), scheduler.Config{}, epoch)

	counts := map[string]int{}
	for _, d := range scheduler.Dispatched(decisions) {
		counts[d.Runtime]++
	}
	require.Equal(t, 3, counts["a"])
	require.Equal(t, 3, counts["b"])
}

func TestNoRuntimeRegisteredIsItsOwnMessage(t *testing.T) {
	t.Parallel()
	decisions := scheduler.Plan(
		[]scheduler.Run{run("a", "acme", "acme/app", scheduler.LaneInteractive, epoch)},
		nil, nothingHeld(), scheduler.Config{}, epoch)
	require.Contains(t, decisions[0].Reason, "No runtime is registered")
}

// ---------------------------------------------------------------------------
// Supersession and retries
// ---------------------------------------------------------------------------

func TestANewerRunOnTheSameBranchSupersedesAnOlderOne(t *testing.T) {
	t.Parallel()
	// Starting an environment for a commit that has already been replaced spends
	// capacity to test something nobody will look at.
	old := scheduler.Run{ID: "old", Org: "acme", Repo: "acme/app", Branch: "feature/x",
		Lane: scheduler.LaneInteractive, QueuedAt: epoch, Cost: 1}
	fresh := scheduler.Run{ID: "new", Org: "acme", Repo: "acme/app", Branch: "feature/x",
		Lane: scheduler.LaneInteractive, QueuedAt: epoch.Add(time.Minute), Cost: 1}

	decisions := scheduler.Plan([]scheduler.Run{old, fresh},
		[]scheduler.Runtime{runtime("a", 10, nil)}, nothingHeld(), scheduler.Config{}, epoch)

	byID := map[string]scheduler.Decision{}
	for _, d := range decisions {
		byID[d.Run.ID] = d
	}
	require.True(t, byID["old"].Superseded)
	require.False(t, byID["old"].Dispatched)
	require.True(t, byID["new"].Dispatched)
	require.Contains(t, byID["old"].Reason, "feature/x")
}

func TestSupersessionIsUnambiguousWhenTwoRunsShareAnInstant(t *testing.T) {
	t.Parallel()
	// Two runs queued in the same millisecond on the same branch. Exactly one
	// must win, and it must be the same one every time.
	a := scheduler.Run{ID: "a", Org: "o", Repo: "o/r", Branch: "b", QueuedAt: epoch, Cost: 1}
	b := scheduler.Run{ID: "b", Org: "o", Repo: "o/r", Branch: "b", QueuedAt: epoch, Cost: 1}

	for range 30 {
		decisions := scheduler.Plan([]scheduler.Run{a, b},
			[]scheduler.Runtime{runtime("rt", 10, nil)}, nothingHeld(), scheduler.Config{}, epoch)
		dispatched := scheduler.Dispatched(decisions)
		require.Len(t, dispatched, 1, "both runs on one branch were dispatched")
		require.Equal(t, "b", dispatched[0].Run.ID)
	}
}

func TestARunWithNoBranchIsNeverSuperseded(t *testing.T) {
	t.Parallel()
	// A load run or a corpus sweep is not tied to a branch, and two of them are
	// two pieces of work rather than one replacing the other.
	a := scheduler.Run{ID: "a", Org: "o", Repo: "o/r", Lane: scheduler.LaneBatch, QueuedAt: epoch, Cost: 1}
	b := scheduler.Run{ID: "b", Org: "o", Repo: "o/r", Lane: scheduler.LaneBatch, QueuedAt: epoch, Cost: 1}

	decisions := scheduler.Plan([]scheduler.Run{a, b},
		[]scheduler.Runtime{runtime("rt", 10, nil)}, nothingHeld(), scheduler.Config{}, epoch)
	require.Len(t, scheduler.Dispatched(decisions), 2)
}

func TestARunThatKeepsFailingToStartIsGivenUpOn(t *testing.T) {
	t.Parallel()
	// A runtime that accepts a placement and then fails to start it returns the
	// run to the queue. Without a budget that is an infinite loop that fills the
	// queue and never finishes.
	r := run("a", "acme", "acme/app", scheduler.LaneInteractive, epoch)
	r.Attempts = 3

	decisions := scheduler.Plan([]scheduler.Run{r},
		[]scheduler.Runtime{runtime("rt", 10, nil)}, nothingHeld(),
		scheduler.Config{Limits: scheduler.Limits{MaxAttempts: 3}}, epoch)

	require.True(t, decisions[0].GivenUp)
	require.False(t, decisions[0].Dispatched)
	require.Contains(t, decisions[0].Reason, "3 times")
}

func TestARunWithAttemptsLeftIsStillDispatched(t *testing.T) {
	t.Parallel()
	r := run("a", "acme", "acme/app", scheduler.LaneInteractive, epoch)
	r.Attempts = 2

	decisions := scheduler.Plan([]scheduler.Run{r},
		[]scheduler.Runtime{runtime("rt", 10, nil)}, nothingHeld(),
		scheduler.Config{Limits: scheduler.Limits{MaxAttempts: 3}}, epoch)
	require.True(t, decisions[0].Dispatched)
}

// ---------------------------------------------------------------------------
// Cost
// ---------------------------------------------------------------------------

func TestARunThatCostsSeveralEnvironmentsIsCountedAsSeveral(t *testing.T) {
	t.Parallel()
	big := run("load", "acme", "acme/app", scheduler.LaneBatch, epoch)
	big.Cost = 4

	decisions := scheduler.Plan([]scheduler.Run{big},
		[]scheduler.Runtime{runtime("rt", 3, nil)}, nothingHeld(), scheduler.Config{}, epoch)
	require.False(t, decisions[0].Dispatched, "a run costing 4 was placed on a runtime with 3 free")

	decisions = scheduler.Plan([]scheduler.Run{big},
		[]scheduler.Runtime{runtime("rt", 4, nil)}, nothingHeld(), scheduler.Config{}, epoch)
	require.True(t, decisions[0].Dispatched)
}

func TestACostOfZeroIsTreatedAsOne(t *testing.T) {
	t.Parallel()
	// A zero cost would let unlimited runs through every limit. Defaulting it
	// rather than trusting the caller, because the caller is a database row.
	r := scheduler.Run{ID: "a", Org: "o", Repo: "o/r", Branch: "b", QueuedAt: epoch}
	decisions := scheduler.Plan([]scheduler.Run{r},
		[]scheduler.Runtime{runtime("rt", 1, nil)}, nothingHeld(),
		scheduler.Config{Limits: scheduler.Limits{PerOrg: 1}}, epoch)
	require.True(t, decisions[0].Dispatched)

	held := scheduler.Held{ByOrg: map[string]int{"o": 1}, ByRepo: map[string]int{}}
	decisions = scheduler.Plan([]scheduler.Run{r},
		[]scheduler.Runtime{runtime("rt", 1, nil)}, held,
		scheduler.Config{Limits: scheduler.Limits{PerOrg: 1}}, epoch)
	require.False(t, decisions[0].Dispatched, "a zero cost slipped past the limit")
}

// ---------------------------------------------------------------------------
// Reporting
// ---------------------------------------------------------------------------

func TestEveryDecisionCarriesAReasonSomebodyCanRead(t *testing.T) {
	t.Parallel()
	held := scheduler.Held{ByOrg: map[string]int{"full": 2}, ByRepo: map[string]int{}}
	queue := []scheduler.Run{
		run("dispatched", "acme", "acme/app", scheduler.LaneInteractive, epoch),
		run("limited", "full", "full/app", scheduler.LaneInteractive, epoch),
	}
	decisions := scheduler.Plan(queue, []scheduler.Runtime{runtime("rt", 1, nil)}, held,
		scheduler.Config{Limits: scheduler.Limits{PerOrg: 2}}, epoch)

	for _, d := range decisions {
		require.NotEmpty(t, d.Reason, "%s has no reason", d.Run.ID)
		require.True(t, strings.HasSuffix(d.Reason, "."), "%q is not a sentence", d.Reason)
		require.Equal(t, strings.ToUpper(d.Reason[:1]), d.Reason[:1],
			"%q does not start with a capital", d.Reason)
	}
}

func TestPositionOfFindsAQueuedRun(t *testing.T) {
	t.Parallel()
	queue := []scheduler.Run{
		run("a", "o", "o/r1", scheduler.LaneInteractive, epoch),
		run("b", "o", "o/r2", scheduler.LaneInteractive, epoch),
	}
	decisions := scheduler.Plan(queue, []scheduler.Runtime{runtime("rt", 1, nil)}, nothingHeld(),
		scheduler.Config{}, epoch)
	require.Zero(t, scheduler.PositionOf(decisions, "a"))
	require.Equal(t, 1, scheduler.PositionOf(decisions, "b"))
	require.Zero(t, scheduler.PositionOf(decisions, "missing"))
}

func TestLaneNamesItself(t *testing.T) {
	t.Parallel()
	require.Equal(t, "interactive", scheduler.LaneInteractive.String())
	require.Equal(t, "scheduled", scheduler.LaneScheduled.String())
	require.Equal(t, "batch", scheduler.LaneBatch.String())
	require.Contains(t, scheduler.Lane(9).String(), "9")
}

func TestAnEmptyQueuePlansNothing(t *testing.T) {
	t.Parallel()
	require.Empty(t, scheduler.Plan(nil, []scheduler.Runtime{runtime("rt", 5, nil)},
		nothingHeld(), scheduler.Config{}, epoch))
}
