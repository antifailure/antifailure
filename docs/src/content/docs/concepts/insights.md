---
title: Insights
description: What Postgres itself can tell you about a change, before anybody clicks anything.
sidebar:
  order: 11
---

A branch is a real database with production's shape in it, which makes some
questions answerable without running the application at all.

Every check is on unless the manifest turns it off, so a project that has said
nothing about insights gets them. The block below is the defaults written out:

```yaml
insights:
  enabled: true
  migration_rehearsal: true
  query_regression: true
  plan_diff: true
  regression_factor: 1.5
  regression_min_ms: 5
  large_table_rows: 100000
```

```
af insights --save baseline.json      on main
af insights --baseline baseline.json  on the branch
```

Every check here also runs inside `af ci`, so what it finds reaches the pull
request comment rather than only a terminal somebody chose to open. `af ci`
takes the same two flags, spelled `--save-baseline` and `--baseline`. What each
finding does to the check is the manifest's
[policy block](/docs/concepts/verdicts/): a lock held past two seconds fails by
default, a rewrite and a lint finding warn.

The rehearsal runs on every change, including one with no migrations in it.
There is no cheaper way to know: `af up` applies the branch's migrations to the
environment's own database, so asking that database what is pending returns
nothing on exactly the pull requests that have migrations. Finding out costs a
branch of the golden, which is the branch the rehearsal needs anyway, so the
check runs and a change with nothing pending gets one line saying so. Set
`insights.migration_rehearsal: false` to skip it.

## Migration rehearsal

The pending migrations run against a branch made for the rehearsal and thrown
away afterwards, never against the environment's own database. Migrations are
not required to be idempotent and most are not, so a rehearsal against a
database they have already touched measures nothing.

Which migrations are pending is decided from the database, not from a diff
against the base branch. A branch of a golden carries production's own history
table, so what is pending against the branch is exactly what is pending against
production. A diff gets that wrong the moment somebody applies a migration out
of band, which is the case where a rehearsal matters most.

A migration that fails here is a migration that would have failed in
production, found before merge instead of during a deploy window.

```
AF-DB-030 Migrations failed on the branch: relation "users_email_key" already
exists
```

`af insights` exits non-zero when that happens, and inside `af ci` it is a
`migration_failed` finding, which fails the check by default. Either way a
pull request check fails rather than printing a note nobody reads.

### Every statement is timed on its own

The timing matters as much as the outcome. A migration that takes four seconds
on an empty test database and ninety on a branch with production's row counts
is a migration that will lock a table in production, and the branch is where
that becomes visible.

Every tool reports one number for a migration file. The number somebody needs
is which statement inside it took the ninety seconds, so each statement is run
and timed separately:

```
Migrations rehearsed: 2 pending, 1m34s in total.
      12ms  ALTER TABLE orders ADD COLUMN currency text
    94.1s   UPDATE orders SET currency = 'usd'
```

### Rewrites come from Postgres, not from reading the SQL

An `ALTER TABLE` that rewrites a table copies every row under a lock nothing
can read through. Whether a given statement rewrites is not something the
statement says: `ALTER COLUMN ... TYPE` rewrites or does not depending on the
type it is coming from, and `ADD COLUMN` depends on the server version and on
whether the default is volatile.

So the rehearsal asks the server. An event trigger on `table_rewrite` fires
immediately before Postgres copies a table, and names the table it is about to
copy. That needs a superuser, which is true on a local branch and often not on
a hosted one; where it is refused, the report says so rather than reporting no
rewrites.

### Locks are sampled while the migrations run

`pg_locks` and `pg_stat_activity` are sampled every 250 milliseconds from a
second connection, because a lock held by a statement in flight is invisible to
the session holding it until that statement returns, which is exactly when the
interesting part is over.

```
Locks held while the migrations ran:
  orders   AccessExclusiveLock for at least 1.2s, with another session waiting on it
```

The figures are sampled, so each one is a lower bound rather than a
measurement, and the report says so.

### The lint rules

Each rule fires on the statement and reports the row count of the table it
touches, because every one of these is harmless on an empty table. Each finding
carries what will happen and what to write instead: a lint that says "unsafe"
and stops is a lint people turn off.

| Rule | Why it matters | What to do instead |
| --- | --- | --- |
| **No `lock_timeout`** | A lock request that is not granted immediately queues, and every query arriving after it queues behind the request rather than behind the table. A four millisecond `ALTER TABLE` blocked behind one long transaction stops all traffic on that table for as long as that transaction runs. | `SET lock_timeout = '3s'` before the first statement, and have the deploy retry. The statement gives up instead of queueing, which turns a stalled table into a failed migration somebody runs again. |
| **NOT NULL column added with no default** | Refused outright on a table with any rows, because every existing row would violate it. | Add it nullable, backfill in batches, then add the constraint `NOT VALID` and validate separately. A constant `DEFAULT` also works from Postgres 11 and does not rewrite. |
| **NOT NULL set on a column that already exists** | `SET NOT NULL` reads every row to prove none is null, under an `ACCESS EXCLUSIVE` lock held for the whole scan. | Add `CHECK (col IS NOT NULL) NOT VALID`, `VALIDATE CONSTRAINT` it separately, then `SET NOT NULL`. From Postgres 12 the validated `CHECK` is proof enough and the scan is skipped. |
| **Column type change that rewrites the table** | Copies every row under an `ACCESS EXCLUSIVE` lock, so nothing can read it either. `int` to `bigint` is the common one and looks like a widening. | Add a new column, backfill, switch reads and writes over, drop the old one. |
| **Index built without `CONCURRENTLY`** | Takes a `SHARE` lock, blocking every insert, update and delete until the index is built. | `CREATE INDEX CONCURRENTLY`. It cannot run inside a transaction, and a failed build leaves an invalid index to drop and retry. |
| **Index dropped without `CONCURRENTLY`** | `DROP INDEX` takes an `ACCESS EXCLUSIVE` lock on the table, not on the index alone. The drop itself is instant; the wait for the lock is the whole cost. | `DROP INDEX CONCURRENTLY`, which takes a `SHARE UPDATE EXCLUSIVE` lock. Like the concurrent build it cannot run inside a transaction. |
| **Index rebuilt without `CONCURRENTLY`** | `REINDEX` takes an `ACCESS EXCLUSIVE` lock on the index and a `SHARE` lock on the table, so writes wait for the whole rebuild. | `REINDEX CONCURRENTLY`, from Postgres 12. A failed run leaves an invalid index with a `_ccnew` suffix to drop before retrying. |
| **Foreign key added without `NOT VALID`** | Scans every existing row to validate, holding a `SHARE ROW EXCLUSIVE` lock on both tables, so writes to the referenced table block too. | `ADD CONSTRAINT ... NOT VALID`, then `VALIDATE CONSTRAINT` separately. New rows are checked from the moment the constraint exists either way. |
| **CHECK constraint added without `NOT VALID`** | Reads every existing row to validate it, under an `ACCESS EXCLUSIVE` lock held for the whole scan. | `ADD CONSTRAINT ... CHECK (...) NOT VALID`, then `VALIDATE CONSTRAINT` in a second migration under a lock reads and writes pass through. |
| **Unique constraint that builds its index in place** | A unique constraint is an index with a catalogue entry, and `ADD CONSTRAINT` builds that index without `CONCURRENTLY`, under `ACCESS EXCLUSIVE` for the whole build. | `CREATE UNIQUE INDEX CONCURRENTLY`, then `ADD CONSTRAINT ... UNIQUE USING INDEX`. Name the index what the constraint should be called: `USING INDEX` renames it. |
| **Rows changed in the same transaction as the schema** | Every tool here applies one migration file in one transaction, so the lock the `ALTER` took is held until the file commits: for the length of the backfill, not the length of the schema change. | Put the row change in its own migration after the schema one, and run it in batches with a commit between them. |
| **Column renamed while something still reads it** | Not backward compatible. Between the migration and the last old instance shutting down, the running application asks for a column that no longer exists, and a rolling deploy guarantees that window. | Add the new column, write to both, migrate readers, drop the old one. |
| **Column dropped while a view still selects it** | Postgres refuses without `CASCADE`, and with `CASCADE` it drops the view too, silently. | Change or drop the view first, in its own migration. |
| **`VACUUM FULL`** | Copies the table into a new file and rebuilds every index, under `ACCESS EXCLUSIVE` for the whole copy. It also needs as much free disk as the table and its indexes already occupy. | Plain `VACUUM` makes the dead space reusable without a rewrite and without blocking anything. Where the file itself has to shrink, `pg_repack` holds the strong lock only at the start and the end. |
| **`CLUSTER`** | Rewrites the table in index order under `ACCESS EXCLUSIVE`, and the ordering is not maintained afterwards, so the benefit decays and somebody schedules the outage again. | `pg_repack --order-by`, or an index that covers the query, which is usually cheaper than an ordering that has to be re-established. |
| **Table dropped** | The rows are gone at commit, and a rolling deploy means old instances are still reading the table until the last one stops. | Stop the application reading it and deploy that first. Rename the table out of the way next, so a rollback is a rename back, and drop it a release later. |
| **Table truncated** | `ACCESS EXCLUSIVE`, every row at once, and unlike `DELETE` there is nothing to recover from except rolling back the transaction. | Decide whether the rows are meant to be gone in production, because a migration reaches production too. Where the table is being reloaded, truncate and reload in one transaction. |

The `lock_timeout` rule fires once for the whole migration rather than once per
statement, because the fix is one line for the whole migration. It reads
`current_setting('lock_timeout')` from the branch before it fires, so a project
that sets the timeout on the role or on the database rather than in the file is
not told it has none. When the rehearsal saw the lock, the finding also carries
how long it was really held on a table with production's row counts, which is
how long production's queries would have been queued behind it.

A change is only reported when it is genuinely unsafe. `varchar` widened to
`text` shares an on disk representation and does not rewrite, and it is not
reported, because a false alarm on the exact change somebody made to avoid a
rewrite is how a check loses its reader.

`large_table_rows` decides which findings read as urgent. It does not decide
whether a rule fires: a rewrite of a small table is still a rewrite, and the
row count is on the finding so a reader can judge it.

## Query regression

The statements come from `pg_stat_statements` on the environment's own
database, so the set is what this application actually ran rather than a list
somebody maintains by hand. A hand maintained list goes stale exactly when a
new query is added, which is the change most likely to be the problem.

Save a report on the base branch and compare against it:

```
af insights --save baseline.json
af insights --baseline baseline.json
```

A statement that runs `regression_factor` times more often, or whose mean time
grew by that factor, is reported. So is a statement this branch runs and the
baseline did not.

`regression_min_ms`, five milliseconds by default, is the floor on the absolute change. A query going from
0.1 ms to 0.3 ms is three times slower and means nothing. Without a floor the
report is all noise and people stop reading it. The floor applies to time per
call and never to call counts, so the four hundred calls of a fast query that
make up an N+1 are still reported however high the floor is set.

## Plan diff

`EXPLAIN (FORMAT JSON)` on the branch before the migrations, against the same
statements after them. The two captures are of the same branch holding the same
rows, so the only thing that changed is the migrations. There is nothing else
to hold equal, which is the part a plan comparison usually gets wrong.

Three things are reported: a table now read end to end that was not before, an
index the plan used before and does not now, and a cost estimate that grew by
more than `regression_factor`. Structural findings come first, because a cost
estimate is a number the planner made up from statistics and moves for reasons
nobody changed, while a sequential scan appearing where an index scan was is a
decision somebody can act on.

```
Query plans that changed:
  a table is now read end to end
    orders is now read end to end. It was reached by index before, and it holds
    about 40000000 rows. The index it used to use is orders_user_id_idx.
    SELECT id, status FROM orders WHERE user_id = $1
```

Two details make this work rather than merely run:

`ANALYZE` runs on both sides before the capture. A freshly created branch has
no statistics until something gathers them, and a planner with no statistics
guesses. Comparing a guess to a measurement produces a report full of findings
that mean nothing.

The plans are generic. `pg_stat_statements` normalises every literal to `$1`,
and before Postgres 16 there was no way to explain a statement with an unbound
parameter. `GENERIC_PLAN` asks for the plan the server caches for a prepared
statement, which is also the plan production runs for a parameterised query, so
it is the right thing to compare as well as the only thing available.

A sequential scan on a table below `large_table_rows` is not reported. On a
small table it is the right plan and flagging it is the noise somebody learns
to ignore.

## Where the migrations are found

The migration tool is recognised from the repository, and the rehearsal replays
the SQL it finds:

| Tool | Migrations read from | Version matched against |
| --- | --- | --- |
| Prisma | `prisma/migrations/<name>/migration.sql` | `_prisma_migrations.migration_name` |
| Supabase CLI | `supabase/migrations/*.sql` | `supabase_migrations.schema_migrations.version` |
| Drizzle | the directory beside `meta/_journal.json` | the file stem |
| Flyway | `db/migration`, `sql`, or `src/main/resources/db/migration` | `flyway_schema_history.version` |
| A plain SQL directory | `migrations`, `db/migrations`, `sql/migrations` | the file stem |
| Rails, Django, Alembic, Knex | not read: the tool runs in the service's image | the tool's own history table |

Flyway is ordered by version rather than by filename, because it compares
versions component by component and numerically: `V1.1` comes after `V1`, while
the filenames sort the other way round.

Rails, Django, Alembic and Knex write their migrations as Ruby, Python or
JavaScript, and only those tools know what SQL they become. So they are not
replayed: the project's own migrate command runs inside the service's own
image, against the rehearsal branch.

It has to be the image rather than the workstation. What a Rails migration
becomes depends on the gems in the image, and what a Django one becomes depends
on its installed packages. Running the tool here would rehearse something the
deploy does not do, which is worse than not rehearsing, because it produces a
result somebody would believe.

The migrate command comes from the service that declares one, and the
connection string is handed to it as `database.url_env`, because not every
framework reads `DATABASE_URL`. If the tool fails, its own output is the
finding: a migration tool explains itself far better than an exit code does.

**That container has no route off the machine.** It runs on a network created
`internal`, holding the branch's database and nothing else. This container has
a connection to a copy of production's data, and the product's premise is that
an environment has nowhere to send a packet, so a rehearsal quietly running
with the internet attached would be the one hole in it. There is a test that
tries to resolve a public name and open a socket from inside, and requires both
to fail while the database still answers.

Since the tool is opaque, per-statement timing comes from the server instead.
Two event triggers around every DDL command record what was sent and how long
it took, so a Rails migration is still reported statement by statement:

```
Migrations rehearsed: 3 pending, 1m12s in total.
      71.4s  ALTER TABLE orders ALTER COLUMN total_cents TYPE bigint
             rewrote orders, which copies every row under a lock nothing can read through
```

Where the event triggers cannot be installed, because they need a superuser and
a hosted provider often will not give one, the report says so.

## Turning things off

Every check is a `*bool`, so `false` is distinguishable from unset. Setting
`plan_diff: false` turns off that check and nothing else, and a block that sets
only `regression_factor` leaves all three checks on.

A check that is off is named in the report:

```
Turned off in the manifest: the plan diff, because insights.plan_diff is false
```

So is anything that could not be measured, and for the same reason. A report
that silently omits a check reads exactly like a check that found nothing,
which is the difference between a clean bill of health and no examination.

```
Not measured: query statistics need the pg_stat_statements extension, which is
not available here
```

Related: [verdicts](/docs/concepts/verdicts/),
[goldens](/docs/concepts/goldens/), [load](/docs/concepts/load/),
[invariants](/docs/guides/invariants/).
