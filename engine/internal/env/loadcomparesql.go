package env

import (
	"context"
	"fmt"
	"time"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/internal/sqlload"
)

// Sending the CONCURRENT SQL WORKLOAD at both builds instead of the HTTP mix.
//
// WHY THIS HALF WAS MISSING. `af load compare` brought a second environment up
// from the base revision, pinned it to this build's golden and sent both sides
// the same traffic. It could send exactly one kind of traffic: the weighted
// HTTP mix. So the promise, the same workload on two builds over the same rows
// at the same concurrency, was true of API traffic and false of the workload
// that reaches the database directly, which is the one somebody changing an
// index, a lock, a storage parameter or a storage engine is asking about. They
// could measure their own build with `af load sql` and had nothing to measure
// it AGAINST.
//
// EVERYTHING ABOVE THIS FILE IS SHARED, and that is the point. The base
// revision is resolved, this build comes up first so that its golden is the
// one both sides branch, the base environment is stamped ephemeral before it
// comes up and torn down by a defer registered before the first error is
// checked, and the rounds are interleaved in the same Thue Morse order under
// the same per round seed. Only the sending differs, which is this file.
//
// WHAT IS PINNED, AND WHY EVERY ONE OF THEM IS PINNED EXPLICITLY. The mix, the
// client count, the duration, the transaction bound and the think time are all
// resolved ONCE, here, from this build's manifest and this build's database,
// and handed to both sides as settled values. Left unresolved they would each
// be settled again per side, and three of them have a zero that is a real
// choice, so "the caller said none" and "the caller did not say" would be the
// same value and the base side would fall back to a manifest.
//
// The mix is the sharpest case and it is not a manifest problem at all. A
// derived mix is read from pg_stat_statements ON THE DATABASE IT IS ABOUT TO
// RUN AGAINST, so two environments produce two mixes, each weighted by
// whatever that environment's own startup happened to execute. Two different
// workloads differenced against each other is the one thing a comparison must
// never do, and it would report as a regression rather than as an error.

// compareSQL resolves the workload once, sends it at both sides in
// interleaved rounds, and pools each side's rounds into one result.
func (o *Orchestrator) compareSQL(
	ctx context.Context, result *LoadCompareResult, baseline *Orchestrator,
	opts LoadCompareOptions, progress func(string),
) error {
	// Built against THIS build, before anything is sent at either side. It
	// also establishes that the database is reachable at all, so a comparison
	// that could never have run says so before it spends the warm-up.
	plan, err := o.sqlLoadMix(ctx, SQLLoadOptions{
		Clients: opts.Clients, Duration: opts.Duration,
		Transactions: opts.Transactions, ThinkTime: opts.ThinkTime,
	})
	if err != nil {
		return err
	}
	result.SQLSource, result.SQLDescription = plan.Mix.Source, plan.Description
	result.Clients, result.ThinkTime = plan.Clients, plan.ThinkTime

	sched := sqlComparePlanFor(opts, plan)
	result.Rounds, result.RoundDuration = sched.rounds, sched.perRound
	result.Warmup, result.RoundTransactions = sched.warmup, sched.perRoundTransactions

	if sched.perRound <= 0 && sched.perRoundTransactions <= 0 {
		// Both bounds empty is a round that would never end, and sqlload.Run
		// refuses it one round at a time. Refusing here says so once, and says
		// it about the comparison rather than about the first round.
		return aferrors.Coded(aferrors.AFLOD017,
			"detail", "this workload is bounded neither by a duration nor by a transaction "+
				"count, so a round of it would never finish; set load.sql.duration or "+
				"load.sql.transactions")
	}

	sides := map[compareSide]*Orchestrator{sideBase: baseline, sideCandidate: o}
	// The refusal list every send returns is empty, and that is a statement
	// rather than an omission. interleaved records per side what the mix
	// declined to send as unsafe, because an HTTP route list is read from each
	// side's own manifest and the two can differ. A SQL mix refuses statements
	// while it is BUILT, the refusals ride on the mix itself, and this
	// comparison builds ONE mix for both sides, so the two sides cannot
	// differ. Returning the same list twice would invite a reader to compare
	// two copies of one list and read agreement into it.
	send := func(ctx context.Context, side compareSide, d time.Duration, seed int64) (
		*sqlload.Result, []load.Route, error,
	) {
		res, _, err := sides[side].SQLLoad(ctx, sqlSendOptions(plan.Mix, sched, d, seed))
		if err != nil {
			return nil, nil, fmt.Errorf("the workload against %s did not complete: %w", side, err)
		}
		return res, nil, nil
	}

	got, err := interleaved(ctx, sched.comparePlan, opts.Seed, send, sqlload.Merge, progress)
	if err != nil {
		return err
	}
	result.BaselineSQL, result.CandidateSQL = got.base, got.cand
	result.BaselineSQLRounds, result.CandidateSQLRounds = got.baseRounds, got.candRounds
	result.Notes = loadCompareNotes(result)
	return nil
}

// sqlCompareNotes says what a SQL comparison could not control.
//
// WRITTEN FOR THIS WORKLOAD RATHER THAN COPIED FROM THE HTTP ONE, because
// three of the HTTP sentences are wrong here and the two that are missing are
// the two that matter most to somebody comparing two database builds.
//
// The HTTP notes say the seed made the REQUEST SEQUENCE the same. Here it made
// the transaction sequence and the parameter values the same, drawn from one
// stream per client, which is a stronger claim and a different one.
//
// The HTTP notes say both sides branched one golden so they answered queries
// over the same rows. That is true of this comparison at the moment it starts
// and it stops being true the instant a write commits, because these clients
// run whole transactions against the database on purpose. Repeating the HTTP
// sentence unchanged would tell a person running a write mix the opposite of
// what is happening to their data.
//
// And two things nothing in the HTTP wording covers at all: a branch is copy
// on write, so the first write to a page pays for the copy on whichever side
// touched it first; and autovacuum, the checkpointer and the background writer
// run on the SERVER's schedule rather than this comparison's, so a round can
// land on a checkpoint that the paired round on the other side did not.
func sqlCompareNotes(r *LoadCompareResult) []string {
	var notes []string
	switch {
	case r.Rounds <= 1:
		notes = append(notes, "the two sides were measured once each, the base branch first "+
			"and this build second, so the second met a host and a shared buffer cache the "+
			"first had just warmed, and that lands on this build every time rather than on "+
			"either side by chance")
	case r.Rounds%2 == 1:
		notes = append(notes, fmt.Sprintf("each side ran %d rounds of %s, interleaved so that "+
			"neither side always went first; with an odd number of rounds the two sides' slots "+
			"cannot be balanced, so a steady drift across the comparison is reduced rather "+
			"than cancelled", r.Rounds, sqlRoundSize(r)))
	default:
		notes = append(notes, fmt.Sprintf("each side ran %d rounds of %s, interleaved in a "+
			"balanced order, so a host warming or cooling steadily across the comparison lands "+
			"on both sides equally; it does not cancel a neighbour on the host, or a checkpoint "+
			"on the server, that fell during one round, and the rounds are sequential rather "+
			"than simultaneous because two databases running transactions at once would "+
			"contend with each other", r.Rounds, sqlRoundSize(r)))
	}
	if r.Rounds >= 2 {
		notes = append(notes, "each unit's change is measured round against round: the latency "+
			"columns are each side's per round p95s averaged on a log scale, and the interval "+
			"around the change comes from how much the rounds disagreed with each other, which "+
			"includes the host's own noise between rounds; a unit that too few rounds ran on "+
			"both sides is left unresolved rather than judged")
	} else {
		notes = append(notes, "with one round each, the only resolution available is the single "+
			"run band, which models how far a p95 could land from itself inside one run and "+
			"cannot see the noise between two runs; on the host this was measured on, that band "+
			"labelled a build that differed by a comment as regressed")
	}
	if r.Warmup > 0 {
		notes = append(notes, fmt.Sprintf("each side first ran the same mix for %s and that was "+
			"discarded, so neither side's numbers include the first execution of a statement, "+
			"which is the one that plans it and reads its pages off disk", r.Warmup))
	} else {
		notes = append(notes, "no warm-up was run, so each side's numbers include the first "+
			"execution of every statement, which is the one that plans it, opens the "+
			"connection and reads its pages off disk rather than out of the shared buffers")
	}
	notes = append(notes, fmt.Sprintf("both sides ran the same mix, built once on this build "+
		"from %s, at %s and %s, so neither side chose its own workload or its own "+
		"concurrency; round for round the seed made the transaction sequence and the "+
		"generated parameter values the same as well",
		sqlSourceWords(r.SQLSource), plural(r.Clients, "client", "clients"),
		sqlThinkTimeWords(r.ThinkTime)))
	notes = append(notes, "both sides branched the same golden "+short(r.Golden)+", so they "+
		"started over identical rows; a mix that WRITES changes them from there, so the two "+
		"databases diverge as the comparison runs and each side's later rounds meet a table, "+
		"an index and a dead tuple count that its own earlier rounds produced")
	notes = append(notes, "a branch is copy on write, so the first write to a page pays for "+
		"copying it and a later write to the same page does not; a write heavy round therefore "+
		"measures the branching as well as the build, and it measures it on whichever side "+
		"reached that page first")
	notes = append(notes, "autovacuum, the checkpointer and the background writer run on the "+
		"server's own schedule rather than this comparison's, so a checkpoint can fall inside "+
		"one round and not inside the round it is paired with; that is noise the interval "+
		"between rounds can see and a single pass cannot")
	if r.RoundTransactions > 0 {
		notes = append(notes, fmt.Sprintf("each round was bounded at %s per client, so each "+
			"side ran up to %s per client in total, and a round that hit that bound before its "+
			"time was up measured a fixed amount of work rather than a fixed amount of time",
			plural(r.RoundTransactions, "transaction", "transactions"),
			plural(r.RoundTransactions*r.Rounds, "transaction", "transactions")))
	}
	return notes
}

// sqlRoundSize is what one round was bounded by, in the words of whichever
// bound was actually set. A round sized in transactions and reported in
// seconds would be a report of a number nobody chose.
func sqlRoundSize(r *LoadCompareResult) string {
	switch {
	case r.RoundDuration > 0 && r.RoundTransactions > 0:
		return fmt.Sprintf("%s or %s per client, whichever came first",
			r.RoundDuration, plural(r.RoundTransactions, "transaction", "transactions"))
	case r.RoundDuration > 0:
		return r.RoundDuration.String()
	case r.RoundTransactions > 0:
		return plural(r.RoundTransactions, "transaction", "transactions") + " per client"
	}
	return "no bound at all"
}

func sqlSourceWords(source string) string {
	if source == sqlload.SourceStatementStatistics {
		return "pg_stat_statements on this build's own database"
	}
	return "the workload document in the repository"
}

func sqlThinkTimeWords(think time.Duration) string {
	if think <= 0 {
		return "no think time between transactions, so each client started its next " +
			"transaction the moment the last one committed"
	}
	return fmt.Sprintf("%s of think time between transactions", think)
}

// sqlSendOptions is what one round is sent with, on either side.
//
// IT TAKES NO SIDE, and that is the point rather than an omission. Both sides
// are handed the same values because there is no argument this function could
// vary them by, which is a stronger guarantee than two call sites that agree
// today. A per side knob here would let a comparison run eight clients against
// sixteen, or one mix against another, and report the difference as a
// regression; every number in that report would still be a number.
//
// Resolved is what stops each side settling the knobs again against its own
// manifest, and it matters because three of the four have a zero that is a
// real choice. The mix is handed over whole for a sharper reason still: a
// derived mix is read from pg_stat_statements on the database it is about to
// run against, so a side left to build its own would weight the statements by
// whatever that environment's own startup happened to execute.
func sqlSendOptions(
	mix *sqlload.Mix, sched sqlComparePlan, d time.Duration, seed int64,
) SQLLoadOptions {
	return SQLLoadOptions{
		Mix: mix, Resolved: true,
		Clients: sched.clients, Duration: d,
		Transactions: sched.perRoundTransactions, ThinkTime: sched.thinkTime,
		Seed: seed,
	}
}

// sqlComparePlan is the schedule plus the knobs both sides are pinned to.
type sqlComparePlan struct {
	comparePlan
	clients              int
	thinkTime            time.Duration
	perRoundTransactions int
}

// sqlComparePlanFor turns the resolved workload into a schedule.
//
// The TOTAL is what the workload asked for and the rounds divide it, so a
// comparison sends the amount of work its manifest declared rather than that
// amount times the number of rounds. Both bounds divide, because a workload
// may declare both and be stopped by whichever arrives first.
//
// A transaction bound smaller than the number of rounds cannot be divided, and
// one transaction per round is the closest thing to what was asked for. It
// means the comparison runs MORE work than the manifest declared, so the
// result says what one round carried and the notes say what that came to. The
// remainder of an uneven division is dropped rather than spread over the first
// few rounds: rounds that ran different amounts of work have p95s drawn from
// different distributions, and the round against round interval rests on them
// being drawn from one.
func sqlComparePlanFor(opts LoadCompareOptions, plan *SQLLoadPlan) sqlComparePlan {
	base := comparePlanFor(opts, plan.Duration)
	out := sqlComparePlan{
		comparePlan: base, clients: plan.Clients, thinkTime: plan.ThinkTime,
	}
	if plan.Transactions > 0 {
		out.perRoundTransactions = plan.Transactions / base.rounds
		if out.perRoundTransactions < 1 {
			out.perRoundTransactions = 1
		}
	}
	// comparePlanFor divides a zero duration into a zero per round duration,
	// which is what a workload sized in transactions alone asked for and must
	// keep: a default dropped in here would cap a run its author sized in work
	// and the report would say it ran fewer transactions than it asked for
	// with nothing saying why.
	return out
}
