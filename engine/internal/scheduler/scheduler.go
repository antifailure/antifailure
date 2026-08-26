// Package scheduler decides which queued run starts next.
//
// It exists because capacity is finite and demand is bursty. A repository that
// merges twenty pull requests in an afternoon must not be able to take every
// database branch an organization is allowed, and a nightly load run must not
// sit behind it either.
//
// Four properties, and each one is a failure somebody has actually had:
//
// Limits are never exceeded. A provider that allows ten branches means ten, and
// dispatching an eleventh produces an error at the worst possible moment, after
// the user has been told their environment is starting.
//
// Nothing starves. Fair sharing across repositories stops one busy repository
// from filling the queue, and aging raises a low-priority run's effective
// priority the longer it waits, so a nightly job behind a steady stream of pull
// requests still eventually runs. Fair sharing alone does not give that: it
// balances between repositories at the same priority and says nothing about
// lanes.
//
// Decisions are deterministic. The same queue and the same capacity produce the
// same answer every time, so a scheduling decision can be replayed from the
// event log and explained. Anything that depends on map iteration order or on
// the wall clock's exact value is a decision nobody can reproduce when they
// need to know why their run did not start.
//
// Backpressure is visible. A run that is queued knows its position, so the pull
// request comment says "third in the queue" rather than nothing at all. Silence
// is what makes people press the button again.
//
// There is no I/O here and no goroutine. It is a function from a queue and a
// capacity report to a list of decisions, which is what makes the simulation
// tests possible: ten thousand runs across fifty organizations, checked for
// fairness and for limits, in milliseconds.
package scheduler

import (
	"fmt"
	"sort"
	"time"
)

// Lane is what a run is for, which decides how urgent it is.
//
// Ordered from most to least urgent. The order is a property of the lane rather
// than a number a caller passes, because a caller that can choose its own
// priority is a caller that always chooses the highest.
type Lane int

const (
	// LaneInteractive is a pull request run. Somebody is waiting for it.
	LaneInteractive Lane = iota
	// LaneScheduled is a nightly or a cron run. Nobody is watching, but it
	// still has to happen before the next one is due.
	LaneScheduled
	// LaneBatch is a load run or a corpus sweep. It can wait.
	LaneBatch
)

// String names the lane in decisions and in the queue view.
func (l Lane) String() string {
	switch l {
	case LaneInteractive:
		return "interactive"
	case LaneScheduled:
		return "scheduled"
	case LaneBatch:
		return "batch"
	}
	return fmt.Sprintf("lane(%d)", int(l))
}

// Run is one unit of work waiting for capacity.
type Run struct {
	// ID identifies the run and breaks every remaining tie, so that two runs
	// alike in every other way still order deterministically.
	ID string
	// Org is the tenant. Limits and fairness are per organization.
	Org string
	// Repo is the repository, which is the unit fair sharing works over.
	Repo string
	// Branch is what the run is for. A newer run on the same branch supersedes
	// an older one.
	Branch string
	Lane   Lane
	// QueuedAt is when it arrived. Aging is measured from here.
	QueuedAt time.Time
	// Requires are placement requirements, matched against a runtime's tags.
	// Empty means anywhere.
	Requires map[string]string
	// Cost is how much capacity the run occupies, in environments. Almost
	// always one; a load run that needs several is the reason it is a number.
	Cost int
	// Attempts counts how many times a runtime accepted this run and then
	// failed to start it.
	Attempts int
}

// Limits bound how much of the pool one tenant may hold.
type Limits struct {
	// PerOrg is the most environments one organization may hold at once. Zero
	// means no limit.
	PerOrg int
	// PerRepo is the most one repository may hold. Zero means no limit.
	PerRepo int
	// MaxAttempts is how many times a run may be dispatched and fail to start
	// before it is given up on. A run that is retried forever is a run that
	// fills the queue and never finishes.
	MaxAttempts int
}

// Runtime is somewhere a run can be placed.
type Runtime struct {
	Name string
	// Tags describe the runtime, and a run's Requires must be a subset.
	Tags map[string]string
	// Free is how many environments it can still take. Reported by the runtime
	// and treated as advisory: it may be stale by the time it is used, which is
	// why dispatch is optimistic and a rejection returns the run to the queue.
	Free int
	// Healthy runtimes take new placements. An unhealthy one keeps what it has.
	Healthy bool
}

// Held is what a tenant currently occupies.
type Held struct {
	ByOrg  map[string]int
	ByRepo map[string]int
}

// Decision is what the scheduler decided about one run.
type Decision struct {
	Run Run
	// Runtime is where it goes, empty when it is not being dispatched.
	Runtime string
	// Dispatched reports whether it starts now.
	Dispatched bool
	// Position is its place in the queue when it is not dispatched, counting
	// from one, so a pull request comment can say where it stands.
	Position int
	// Reason is one sentence explaining the decision, in the words somebody
	// reading a pull request comment would want.
	Reason string
	// Superseded reports that a newer run on the same branch replaced it.
	Superseded bool
	// GivenUp reports that the run exhausted its retry budget.
	GivenUp bool
}

// Config is the scheduler's tuning.
type Config struct {
	Limits Limits
	// AgeStep is how much waiting is worth. A run's effective lane improves by
	// one step for every AgeStep it has waited, which is what stops a batch run
	// from waiting behind an endless stream of pull requests.
	//
	// Zero disables aging, which is a decision a test can make and an operator
	// should not: with it off, the lowest lane starves whenever the highest is
	// never empty.
	AgeStep time.Duration
}

// DefaultAgeStep is how long a run waits before it counts as one lane more
// urgent. Chosen so that a batch run promotes past interactive after about half
// an hour, which is long enough that it does not fight with a burst of pull
// requests and short enough that a nightly job still finishes overnight.
const DefaultAgeStep = 15 * time.Minute

// Plan decides what happens to every queued run.
//
// It returns a decision for each, dispatched or not, in the order the scheduler
// considered them. Nothing here mutates its inputs, so the same call twice
// gives the same answer, which is what makes a decision replayable.
func Plan(queue []Run, runtimes []Runtime, held Held, cfg Config, now time.Time) []Decision {
	ordered := order(queue, cfg, now)

	// Copies, because a plan must not change the caller's view of the world.
	// The scheduler is called speculatively, to answer "what would happen", as
	// often as it is called to act.
	byOrg := copyCounts(held.ByOrg)
	byRepo := copyCounts(held.ByRepo)
	free := make([]int, len(runtimes))
	for i, rt := range runtimes {
		free[i] = rt.Free
	}

	// The newest run on a branch wins. An older one is superseded rather than
	// dispatched, because starting an environment for a commit that has already
	// been replaced spends capacity to test something nobody will look at.
	newestOnBranch := map[string]string{}
	for _, r := range ordered {
		key := r.Org + "\x00" + r.Repo + "\x00" + r.Branch
		if cur, ok := newestOnBranch[key]; !ok || newer(r, findRun(ordered, cur)) {
			newestOnBranch[key] = r.ID
		}
	}

	decisions := make([]Decision, 0, len(ordered))
	position := 0

	for _, run := range ordered {
		key := run.Org + "\x00" + run.Repo + "\x00" + run.Branch
		if run.Branch != "" && newestOnBranch[key] != run.ID {
			decisions = append(decisions, Decision{
				Run: run, Superseded: true,
				Reason: fmt.Sprintf(
					"A newer run for %s replaced this one, so it will not start.", run.Branch),
			})
			continue
		}

		if cfg.Limits.MaxAttempts > 0 && run.Attempts >= cfg.Limits.MaxAttempts {
			decisions = append(decisions, Decision{
				Run: run, GivenUp: true,
				Reason: fmt.Sprintf(
					"A runtime accepted this run %d times and failed to start it. Giving up rather than queueing it again.",
					run.Attempts),
			})
			continue
		}

		cost := run.Cost
		if cost <= 0 {
			cost = 1
		}

		// Limits before capacity, because a limit is the tenant's own ceiling
		// and reporting "waiting for capacity" when the answer is "you are at
		// your limit" sends somebody to ask the wrong question.
		if cfg.Limits.PerOrg > 0 && byOrg[run.Org]+cost > cfg.Limits.PerOrg {
			position++
			decisions = append(decisions, Decision{
				Run: run, Position: position,
				Reason: fmt.Sprintf(
					"The organization is holding %d of %d environments. This starts when one is freed.",
					byOrg[run.Org], cfg.Limits.PerOrg),
			})
			continue
		}
		if cfg.Limits.PerRepo > 0 && byRepo[run.Repo]+cost > cfg.Limits.PerRepo {
			position++
			decisions = append(decisions, Decision{
				Run: run, Position: position,
				Reason: fmt.Sprintf(
					"%s is holding %d of %d environments. This starts when one is freed.",
					run.Repo, byRepo[run.Repo], cfg.Limits.PerRepo),
			})
			continue
		}

		index, why := place(run, runtimes, free, cost)
		if index < 0 {
			position++
			decisions = append(decisions, Decision{Run: run, Position: position, Reason: why})
			continue
		}

		free[index] -= cost
		byOrg[run.Org] += cost
		byRepo[run.Repo] += cost
		decisions = append(decisions, Decision{
			Run: run, Runtime: runtimes[index].Name, Dispatched: true,
			Reason: fmt.Sprintf("Starting on %s.", runtimes[index].Name),
		})
	}

	return decisions
}

// place picks a runtime, or explains why none will do.
//
// The explanation names the unmet requirement rather than saying no runtime is
// available, because those are different problems: one is capacity and the
// other is a placement rule nobody can satisfy, and they are fixed by different
// people.
func place(run Run, runtimes []Runtime, free []int, cost int) (int, string) {
	var eligible, unhealthy, tooFull int
	var unmet string

	best := -1
	for i, rt := range runtimes {
		if missing, ok := matches(run.Requires, rt.Tags); !ok {
			if unmet == "" {
				unmet = missing
			}
			continue
		}
		eligible++
		if !rt.Healthy {
			unhealthy++
			continue
		}
		if free[i] < cost {
			tooFull++
			continue
		}
		// Most free capacity wins, so load spreads rather than filling one
		// runtime and then spilling. Ties break on the earlier runtime, which
		// is the caller's order, so the choice is reproducible.
		if best < 0 || free[i] > free[best] {
			best = i
		}
	}

	switch {
	case best >= 0:
		return best, ""
	case eligible == 0 && unmet != "":
		return -1, fmt.Sprintf(
			"No runtime satisfies %s. Register one that does, or relax the requirement.", unmet)
	case eligible == 0:
		return -1, "No runtime is registered."
	case unhealthy == eligible:
		return -1, "Every runtime that could take this is unhealthy. It starts when one recovers."
	default:
		return -1, "Every runtime is full. This starts when capacity frees up."
	}
}

// matches reports whether a runtime's tags satisfy a run's requirements, and
// names the first one that does not.
func matches(requires, tags map[string]string) (string, bool) {
	// Sorted, so the requirement reported is the same every time rather than
	// whichever one map iteration happened to reach first.
	keys := make([]string, 0, len(requires))
	for k := range requires {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if tags[k] != requires[k] {
			return fmt.Sprintf("%s=%s", k, requires[k]), false
		}
	}
	return "", true
}

// order sorts the queue into the sequence the scheduler considers it in.
//
// Fair sharing is implemented as a round: each repository's first waiting run
// is considered before any repository's second. Without it, a repository that
// queued fifty runs would be considered fifty times before a repository that
// queued one, and the second repository would wait for the first to drain.
//
// Within a round, the effective lane decides, then how long the run has waited,
// then the identifier. Every tiebreak is a total order on values that are
// already in hand, so the result does not depend on the input's order.
func order(queue []Run, cfg Config, now time.Time) []Run {
	type keyed struct {
		run       Run
		lane      Lane
		round     int
		queuedAt  time.Time
		effective int
	}

	// Group by repository within organization to compute the round each run
	// falls in, after sorting each group so that "first waiting run" means the
	// same thing every time.
	groups := map[string][]Run{}
	for _, r := range queue {
		key := r.Org + "\x00" + r.Repo
		groups[key] = append(groups[key], r)
	}
	groupKeys := make([]string, 0, len(groups))
	for k := range groups {
		groupKeys = append(groupKeys, k)
	}
	sort.Strings(groupKeys)

	items := make([]keyed, 0, len(queue))
	for _, k := range groupKeys {
		g := groups[k]
		sort.Slice(g, func(a, b int) bool {
			la, lb := effectiveLane(g[a], cfg, now), effectiveLane(g[b], cfg, now)
			if la != lb {
				return la < lb
			}
			if !g[a].QueuedAt.Equal(g[b].QueuedAt) {
				return g[a].QueuedAt.Before(g[b].QueuedAt)
			}
			return g[a].ID < g[b].ID
		})
		for i, r := range g {
			items = append(items, keyed{
				run: r, lane: effectiveLane(r, cfg, now), round: i, queuedAt: r.QueuedAt,
			})
		}
	}

	sort.Slice(items, func(a, b int) bool {
		// Lane first. An interactive run that arrived a moment ago beats a batch
		// run that has been waiting, until aging promotes the batch run, which
		// is what the effective lane already accounts for.
		if items[a].lane != items[b].lane {
			return items[a].lane < items[b].lane
		}
		// Then the round, which is the fair share: everybody's first before
		// anybody's second.
		if items[a].round != items[b].round {
			return items[a].round < items[b].round
		}
		if !items[a].queuedAt.Equal(items[b].queuedAt) {
			return items[a].queuedAt.Before(items[b].queuedAt)
		}
		return items[a].run.ID < items[b].run.ID
	})

	out := make([]Run, len(items))
	for i, it := range items {
		out[i] = it.run
	}
	return out
}

// effectiveLane is a run's lane after aging.
//
// Waiting makes a run more urgent, one lane per AgeStep, never past
// interactive. Without this the lowest lane starves for as long as the highest
// is non-empty, and "eventually" is not a property a nightly job can rely on.
func effectiveLane(r Run, cfg Config, now time.Time) Lane {
	if cfg.AgeStep <= 0 {
		return r.Lane
	}
	waited := now.Sub(r.QueuedAt)
	if waited <= 0 {
		return r.Lane
	}
	steps := int(waited / cfg.AgeStep)
	lane := int(r.Lane) - steps
	if lane < int(LaneInteractive) {
		lane = int(LaneInteractive)
	}
	return Lane(lane)
}

func newer(a, b Run) bool {
	if b.ID == "" {
		return true
	}
	if !a.QueuedAt.Equal(b.QueuedAt) {
		return a.QueuedAt.After(b.QueuedAt)
	}
	// A total order even when two runs arrived in the same instant, so that
	// "the newest" is never ambiguous.
	return a.ID > b.ID
}

func findRun(runs []Run, id string) Run {
	for _, r := range runs {
		if r.ID == id {
			return r
		}
	}
	return Run{}
}

func copyCounts(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Dispatched returns just the runs that start, for a caller that does not care
// why the others did not.
func Dispatched(decisions []Decision) []Decision {
	out := make([]Decision, 0, len(decisions))
	for _, d := range decisions {
		if d.Dispatched {
			out = append(out, d)
		}
	}
	return out
}

// PositionOf reports where a run stands, or zero if it is not waiting.
func PositionOf(decisions []Decision, runID string) int {
	for _, d := range decisions {
		if d.Run.ID == runID {
			return d.Position
		}
	}
	return 0
}
