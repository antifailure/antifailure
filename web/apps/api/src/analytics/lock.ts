// The one lock every analytics writer takes.
//
// ONE WRITER AT A TIME ACROSS THE WHOLE DEPLOYMENT.
//
// The rollup rides the maintenance pass, every replica runs that pass, and
// startMaintenance runs it once immediately on start. Production is configured
// for two replicas, so two rollups begin within milliseconds of each other on
// every deploy.
//
// Every writer here is a DELETE followed by an INSERT in one transaction, which
// is right for one writer and is a race for two: the second insert lands on
// rows the first has already written and fails on the primary key. That aborts
// the whole maintenance pass on that replica, so the failure is not a wrong
// number, it is a dashboard that silently stops updating while a line goes into
// a log nobody reads. Found by running three rollups at once, which no test did
// until one asked what happens in that order.
//
// TRANSACTION SCOPED, and taken inside each write rather than held across the
// whole run. A session lock would need a reserved connection, and maintenance.ts
// opens its admin pool with `max: 1`, so reserving from it takes the only
// connection there is and every statement after it waits for a connection the
// rollup is itself holding. That is a maintenance pass that never returns,
// which is worse than the race it was fixing, and it is what happened: a
// control plane job cancelled at its timeout rather than failing. Postgres
// gives a transaction scoped lock back at COMMIT whatever happens, so nothing
// here can leak it.
//
// The cost is that a second replica repeats work rather than skipping it. A
// duplicated scan once a day against a deadlock is the right way round.
//
// Its own module rather than rollup.ts, because insights.ts takes it too and
// rollup.ts already imports insights.ts.

/** The advisory lock key, as SQL. Spliced into a statement rather than passed
 *  as a parameter because `hashtext` of a literal is what makes the key the
 *  same on every replica without a number anybody has to keep in step. */
export const ROLLUP_LOCK = "hashtext('antifailure.analytics.rollup')"

/** The statement that takes it. One spelling, so two writers cannot take two
 *  different locks and both believe they are alone. */
export const TAKE_ROLLUP_LOCK = `SELECT pg_advisory_xact_lock(${ROLLUP_LOCK})`
