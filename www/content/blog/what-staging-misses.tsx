import type { Post } from "@/lib/blog";

export const MIGRATION_LOCKS: Post = {
  slug: "what-staging-misses-about-migrations",
  title: "Why migration timing changes with your data",
  dek: "A migration can pass on staging and still hold a long lock in production. Rehearse it at realistic row counts before you deploy.",
  summary: "How data volume, query plans, and lock duration affect a Postgres migration.",
  published: "2026-08-29",
  updated: "2026-09-26",
  tags: ["Postgres", "Migrations", "Testing"],
  body: (
    <>
      <p>A schema change can be valid SQL, pass review, and work on staging while still holding a costly lock in production. The difference often lies in the data the statement has to read or rewrite.</p>
      <p>A small test database helps check correctness. To understand migration cost, you also need realistic row counts, data distributions, and observations from the database while the statement runs.</p>

      <h2>Lock mode and lock duration answer different questions</h2>
      <p>A migration linter can identify statements that request strong locks. The time those locks remain held depends on the work Postgres performs, the available resources, and other activity in the database.</p>
      <p>That distinction matters for a type change that rewrites a table. The SQL may be a single line, but Postgres has to process the existing rows while holding the required lock.</p>

      <h2>What to carry into a rehearsal</h2>
      <ul>
        <li><strong>Row counts.</strong> A rewrite on a large table can take much longer than the same change on a fixture database.</li>
        <li><strong>Data distribution.</strong> Skew, nulls, and large accounts affect planner choices. Uniform seed data may produce different query plans.</li>
        <li><strong>Concurrent activity.</strong> Other sessions can wait for a migration or delay its lock acquisition. A quiet branch alone does not reproduce production contention.</li>
        <li><strong>Environment differences.</strong> Hardware, indexes, and storage conditions affect timing. Treat rehearsal measurements as observations of that environment.</li>
      </ul>

      <h2>What Antifailure measures</h2>
      <p>Antifailure applies pending migrations to a disposable branch of a masked database. It records statement durations, samples locks from another connection, observes table rewrites, and compares query plans.</p>
      <p>Lock sampling records the strongest mode held per table, a lower bound on its duration, and whether another session was observed waiting. The report also includes migration lint findings and suggested changes.</p>
      <p>In a recorded orders-app demo, changing <code>total_cents</code> to <code>bigint</code> rewrote the orders table. The report recorded an <code>AccessExclusiveLock</code> on that table for at least 10.5 seconds. That is a measurement from the demo branch, not a forecast for another database.</p>

      <h2>Plan the path back</h2>
      <p>Reverting application code does not undo every schema or data change. Dropping a column removes data; a backfill may create values an older release does not expect.</p>
      <p>For a column type change, consider an expand-and-contract sequence: add a new column, backfill in batches, move reads and writes, and remove the old column in a later migration. Rehearse the sequence and check compatibility with the previous release.</p>

      <h2>Put the findings in the review</h2>
      <p>A useful report names the statement, the affected table, the observed lock, and the conditions of the test. Your team can then decide whether to change the migration, adjust its timeout, or gather more evidence before deployment.</p>
      <p><a href="/docs/concepts/insights">Read the migration rehearsal guide</a> to set up the checks for your repository.</p>
    </>
  ),
};
