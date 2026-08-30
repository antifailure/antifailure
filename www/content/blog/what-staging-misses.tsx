import type { Post } from "@/lib/blog";

/**
 * Every factual claim about Antifailure here is one this repository already
 * makes: the per-statement timing and strongest-lock-per-table rehearsal, the
 * pg_stat_statements diff, and the plan comparison are all in the README under
 * "Database review, automatically". The Postgres behaviour described is
 * standard and checkable against the Postgres documentation, which is linked
 * rather than paraphrased.
 */
export const MIGRATION_LOCKS: Post = {
  slug: "what-staging-misses-about-migrations",
  title: "Staging cannot tell you how long a lock is held",
  dek: "A migration that runs instantly against a seeded table can hold an exclusive lock for minutes against a real one. The difference is row count, and staging does not have it.",
  summary:
    "Why migrations that pass on staging take production down: lock duration scales with data, and staging has no data.",
  published: "2026-08-29",
  author: { name: "Antifailure", url: "https://antifailure.dev/company" },
  tags: ["Postgres", "Migrations", "Testing"],
  body: (
    <>
      <p>
        Almost every migration incident has the same shape. The change was
        reviewed. It ran on staging in under a second. It ran in CI. Then it
        reached production and something held a lock long enough that requests
        queued behind it, connections filled, and the application stopped
        answering.
      </p>
      <p>
        Nothing about the review was careless. The problem is that the property
        that matters is not visible in any environment that lacks production
        data, and the artifact everyone inspects, the SQL, does not contain it.
      </p>

      <h2>The property that does not fit in a diff</h2>
      <p>
        A lock has two independent characteristics. Which lock mode a statement
        takes is a static fact about the statement, and a linter can tell you.
        How long it is held is a function of how much data it has to touch, and
        nothing static can tell you.
      </p>
      <p>
        Those two are frequently confused because on a seeded table they look
        identical. An <code>ACCESS EXCLUSIVE</code> lock held for four
        milliseconds against ten thousand rows and the same lock held for
        twenty-seven seconds against ninety million are the same line of SQL.
        Only one of them is an outage.
      </p>
      <p>
        This is why static migration linters, which are useful, are not
        sufficient. They correctly tell you that a statement takes a strong
        lock. They cannot tell you that it will hold it past your connection
        pool&apos;s patience, because that answer depends on the table.
      </p>

      <h2>Why the staging copy does not stand in</h2>
      <p>Four things are usually different, and each one hides a distinct failure.</p>
      <ul>
        <li>
          <strong>Row count.</strong> Rewrite and scan times scale with it.
          Staging is typically several orders of magnitude smaller, so every
          duration measured there is meaningless.
        </li>
        <li>
          <strong>Distribution.</strong> Planner choices depend on statistics.
          Uniform seeded data produces different plans from real data with its
          skew, its nulls and its handful of enormous accounts.
        </li>
        <li>
          <strong>Concurrency.</strong> A lock is only a problem when something
          else wants the table. A quiet staging box has no queue to form behind
          it, so the same lock is invisible.
        </li>
        <li>
          <strong>Index and bloat state.</strong> A table that has been written
          to for two years does not behave like one created by a fixture script
          this morning.
        </li>
      </ul>
      <p>
        The uncomfortable consequence is that a green staging run on a schema
        change is close to no evidence at all. It proves the SQL parses and the
        application still boots. It does not address the question anybody
        actually has, which is whether this is safe to run at 2pm on a Tuesday.
      </p>

      <h2>What has to be measured instead</h2>
      <p>
        The question is not &ldquo;is this migration valid&rdquo; but
        &ldquo;what does this migration do to a database shaped like ours,
        while it is being used.&rdquo; That requires executing it against
        production-shaped data and watching, which is what Antifailure rehearses
        on a fresh branch. Four measurements come out of it:
      </p>
      <ul>
        <li>
          <strong>Per-statement timing.</strong> Not the total, which averages
          away the one statement that matters, but each statement separately.
        </li>
        <li>
          <strong>The strongest lock held per table.</strong> Per table, because
          a migration touching six tables has six different blast radii and
          reporting the maximum tells you nothing about which one to fix.
        </li>
        <li>
          <strong>Query plan comparison.</strong> Plans before and after,
          compared, which is how you catch the index you stopped using rather
          than the one you forgot to add.
        </li>
        <li>
          <strong>A <code>pg_stat_statements</code> diff</strong> between main
          and the branch, which is how an N+1 introduced by an ORM change shows
          up as a number instead of as a support ticket next week.
        </li>
      </ul>

      <h2>Rollback is a separate question, and it expires</h2>
      <p>
        &ldquo;We can roll back&rdquo; is usually said about the deployment
        rather than the schema, and the two come apart quickly. Once a column is
        dropped the data is gone. Once a backfill has run, reverting the code
        leaves rows the old version never expected. Once a type has changed in
        place, going back is another rewrite holding another lock.
      </p>
      <p>
        So rollback feasibility is a property to check while rehearsing, not a
        reassurance to offer during an incident. It is frequently true at the
        moment of deploy and false twenty minutes later, and knowing which of
        those you are in changes what you do next.
      </p>

      <h2>What this looks like in practice</h2>
      <p>
        The output that is worth having is not a pass or a fail. It is a
        statement specific enough to argue with:{" "}
        <em>
          this statement holds ACCESS EXCLUSIVE on <code>orders</code> for
          twenty-seven seconds at your row count, eighty-four statements queue
          behind it, and the table is rewritten in full.
        </em>
      </p>
      <p>
        That is a decision somebody can make. &ldquo;It passed on staging&rdquo;
        is not, and it never was. It only looked like one because the thing it
        failed to measure is invisible until the day it is not.
      </p>
    </>
  ),
};
