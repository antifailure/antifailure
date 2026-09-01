import {
  Callout,
  FeatureGrid,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  Split,
  Steps,
} from "@/components/pages/kit";
import { Illustrative } from "@/components/layout/Illustrative";
import { PMG01, PMG02, PMG03, PMG04 } from "@/components/pages/figures/product";
import { cn } from "@/lib/cn";

const CAPTIONS = [
  "An exclusive lock on subscriptions holds for 27 seconds. The rehearsal reports it before it ships.",
  "Expand-and-contract holds the same lock for 0.4s, and no other session is left waiting on it.",
] as const;

/**
 * What the rehearsal measures.
 *
 * These four rows used to be a lock, a checkout p99, a timeout rate and a
 * rollback verdict. Three of the four were not things this engine can produce:
 * nothing sends traffic at a migration while it applies, and there is no
 * old-binary coexistence check in the product under any name. What is here is
 * what internal/insights returns.
 *
 * The second row was then a count, "84 blocked statements", which is the same
 * mistake one level down. insights.LockHold carries a single boolean,
 * Blocking, "whether another session was ever seen waiting on it". There is no
 * count of waiters and no list of their statements anywhere in the engine, and
 * a rehearsal branch has no application traffic on it, so the honest reading
 * of a yes here is that something was blocked even on a branch nothing else
 * was using.
 */
const FINDINGS = [
  { value: "27.4s", label: "ACCESS EXCLUSIVE", hint: "the strongest mode held on subscriptions, sampled every 250ms", danger: true },
  { value: "Yes", label: "Blocked another session", hint: "a second session was seen waiting on the lock while it was held", danger: true },
  { value: "Yes", label: "Table rewrite", hint: "reported by Postgres, not inferred from the statement", danger: true },
  { value: "Seq Scan", label: "Plan change", hint: "EXPLAIN before and after, on production's own shape", danger: true },
] as const;

const INSIGHTS = `20260824_widen_plan_id

lock        ACCESS EXCLUSIVE  subscriptions  27.4s
blocked     another session was seen waiting on it
rewrite     subscriptions rewritten in full
plan        events: Index Scan -> Seq Scan  12ms -> 410ms
lint        changing plan_id to bigint rewrites the whole table`;

export function MigrationsPage() {
  return (
    <PageShell>
      <PageHero
        path="/product/migrations"
        eyebrow="Migration Safety Engine"
        title="Catch exclusive locks before they take checkout down."
        lead="The flagship module. A fresh branch carrying production's shape applies the pending migrations while a second connection samples what is locked, then reports the strongest mode held per table, how long it was held, whether another session was left waiting on it, which tables were rewritten, and how the query plans moved."
        framed={false}
        visual={<PMG01 captions={CAPTIONS} />}
      />

      <PageSection tone="sage">
        <Split visual={<PMG02 />}>
          <PageHeading
            kicker="The finding"
            title="<strong>A 27-second lock is a finding.</strong> Not a line in a log nobody reads."
          />
          <ul className="mt-16 divide-y divide-black/[0.08] border-y border-black/[0.08] max-md:mt-10">
            {FINDINGS.map((item) => (
              <li key={item.label} className="flex items-baseline justify-between gap-8 py-4 max-sm:flex-col max-sm:gap-2">
                <div className="min-w-0">
                  <div className="font-mono text-[11px] font-medium tracking-[0.14em] text-gray-new-50 uppercase">
                    {item.label}
                  </div>
                  <p className="mt-1 max-w-[420px] text-[14px] leading-5 tracking-extra-tight text-gray-new-40">
                    {item.hint}
                  </p>
                </div>
                <div
                  className={cn(
                    "shrink-0 font-mono text-[18px] leading-none tracking-extra-tight tabular-nums",
                    item.danger ? "text-red-700" : "text-black",
                  )}
                >
                  {item.value}
                </div>
              </li>
            ))}
          </ul>
        </Split>
        <Illustrative label="Example finding">
          A rehearsal of one migration, with the numbers chosen. The measurements are the ones{" "}
          <code className="font-mono text-[12px] text-black/70">af insights</code> takes: lock mode
          and hold time from pg_locks, rewrites from Postgres, plans from EXPLAIN.
        </Illustrative>
      </PageSection>

      <PageSection tone="white">
        <Split visual={<PMG03 source={INSIGHTS} />}>
          <PageHeading title="<strong>Measured, not inferred.</strong> The lock comes from pg_locks and the rewrite from Postgres itself." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Staging with a handful of rows will not show an exclusive lock or a table rewrite. A
            sampler on its own connection watches pg_locks and pg_stat_activity while the migration
            runs, because the session running it cannot see its own lock until the statement returns,
            which is exactly when the interesting part is over.
          </p>
          <div className="mt-8">
            <Callout label="Suggested remediation">
              Add a second column of the new type, backfill it in batches, deploy code that reads
              both, then drop the old column in a later migration.
            </Callout>
          </div>
        </Split>
      </PageSection>

      <PageSection>
        <PageHeading title="<strong>Failures conventional tests miss.</strong> The engine measures what staging cannot." />
        <FeatureGrid
          items={[
            { title: "Locks", body: "The strongest mode held per table, how long, and whether another session was left waiting on it." },
            { title: "Rewrites", body: "Full table rewrites, reported by Postgres rather than guessed from the SQL." },
            { title: "Plans", body: "EXPLAIN before and against the migrated branch, on production's own shape." },
            { title: "Statements", body: "Per-statement duration, so the slow one in a batch is named." },
            {
              title: "Lint",
              body: "Missing lock timeouts, constraints added without NOT VALID, index builds that are not concurrent, backfills sharing a transaction with the schema change, and the rewrites and offline table operations. Each finding reaches the pull request under its own rule name with the fix attached, because a finding called migration_lint tells nobody what to change.",
            },
            { title: "Comparison", body: "A saved report from an earlier run, compared against this one." },
          ]}
        />
      </PageSection>

      <PageSection>
        <Split visual={<PMG04 />}>
          <PageHeading title="<strong>Safer pattern: expand-and-contract.</strong> The lint rule carries the fix, not only the complaint." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Switch the film to expand-and-contract. The strongest lock drops to 0.4s, nothing is
            left waiting on it, and the table is not rewritten.
          </p>
          <div className="mt-8">
            <Callout label="What the lint rule says">
              Changing a column to bigint rewrites the whole table under an ACCESS EXCLUSIVE lock,
              so nothing can read it either. Add a new column of the new type, backfill it, switch
              reads and writes over, then drop the old one.
            </Callout>
          </div>
        </Split>
        <div className="mt-16 max-md:mt-10">
          <Steps
            items={[
              {
                title: "Expand",
                body: "Add a second column of the new type, nullable. Old rows stay readable by both binaries.",
              },
              {
                title: "Backfill",
                body: "Copy values across in batches so checkout never waits on ACCESS EXCLUSIVE.",
              },
              {
                title: "Dual-read",
                body: "Deploy code that reads both shapes before you tighten the schema.",
              },
              {
                title: "Contract",
                body: "Drop the old column in a later migration, once nothing reads it any more.",
              },
            ]}
          />
        </div>
      </PageSection>

    </PageShell>
  );
}
