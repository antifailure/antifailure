"use client";

import { useState } from "react";
import {
  Callout,
  CodePanel,
  FeatureGrid,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  RelatedGrid,
  Split,
  Steps,
} from "@/components/pages/kit";
import { Illustrative } from "@/components/layout/Illustrative";
import { LockChart, LockChartMobile } from "@/components/home/media/LockChart";
import { MigrationScene } from "@/components/home/visuals/MigrationScene";
import { cn } from "@/lib/cn";

const CAPTIONS = [
  "An exclusive lock on subscriptions holds for 27 seconds. The rehearsal reports it before it ships.",
  "Expand-and-contract holds the same lock for 0.4s, and nothing queues behind it.",
] as const;

/**
 * What the rehearsal measures.
 *
 * These four rows used to be a lock, a checkout p99, a timeout rate and a
 * rollback verdict. Three of the four were not things this engine can produce:
 * nothing sends traffic at a migration while it applies, and there is no
 * old-binary coexistence check in the product under any name. What is here is
 * what internal/insights returns.
 */
const FINDINGS = [
  { value: "27.4s", label: "ACCESS EXCLUSIVE", hint: "the strongest mode held on subscriptions, sampled every 250ms", danger: true },
  { value: "84", label: "Blocked statements", hint: "queued behind the lock while it was held", danger: true },
  { value: "Yes", label: "Table rewrite", hint: "reported by Postgres, not inferred from the statement", danger: true },
  { value: "Seq Scan", label: "Plan change", hint: "EXPLAIN before and after, on production's own shape", danger: true },
] as const;

function MigrationStudio() {
  const [active, setActive] = useState<0 | 1>(0);
  const [playId, setPlayId] = useState(0);

  function cutTo(index: 0 | 1) {
    if (index === active) return;
    setActive(index);
    setPlayId((n) => n + 1);
  }

  return (
    <div className="relative z-10 w-full min-w-0">
      <MigrationScene tab={active} playId={playId} onTab={cutTo} />
      <p className="relative z-20 mt-10 max-w-[640px] text-[18px] leading-normal tracking-extra-tight text-black max-xl:mt-8 max-md:mt-7 max-md:text-[15px]">
        {CAPTIONS[active]}
      </p>
    </div>
  );
}

export function MigrationsPage() {
  return (
    <PageShell inset>
      <PageHero
        path="/product/migrations"
        eyebrow="Migration Safety Engine"
        title="Catch exclusive locks before they take checkout down."
        lead="The flagship module. A fresh branch carrying production's shape applies the pending migrations while a second connection samples what is locked, then reports the strongest mode held per table, how long it was held, what queued behind it, which tables were rewritten, and how the query plans moved."
        framed={false}
        visual={<MigrationStudio />}
      />

      <PageSection tone="sage">
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
        <div className="mt-16 overflow-hidden rounded-[12px] border border-black/[0.08] bg-white max-md:mt-10">
          <LockChart state={0} />
          <LockChartMobile state={0} />
        </div>
        <Illustrative label="Example finding">
          A rehearsal of one migration, with the numbers chosen. The measurements are the ones{" "}
          <code className="font-mono text-[12px] text-black/70">af insights</code> takes: lock mode
          and hold time from pg_locks, rewrites from Postgres, plans from EXPLAIN.
        </Illustrative>
      </PageSection>

      <PageSection tone="white">
        <Split
          visual={
            <CodePanel label="af insights">{`20260824_add_billing_status

lock        ACCESS EXCLUSIVE  subscriptions  27.4s
blocked     84 statements queued behind it
rewrite     subscriptions rewritten in full
plan        events: Index Scan -> Seq Scan  12ms -> 410ms
lint        adding a column with a default rewrites the table`}
            </CodePanel>
          }
        >
          <PageHeading title="<strong>Measured, not inferred.</strong> The lock comes from pg_locks and the rewrite from Postgres itself." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Staging with a handful of rows will not show an exclusive lock or a table rewrite. A
            sampler on its own connection watches pg_locks and pg_stat_activity while the migration
            runs, because the session running it cannot see its own lock until the statement returns,
            which is exactly when the interesting part is over.
          </p>
          <div className="mt-8">
            <Callout label="Suggested remediation">
              Add the nullable column without a default, backfill in batches, deploy dual-read
              compatibility, then enforce the constraint in a later migration.
            </Callout>
          </div>
        </Split>
      </PageSection>

      <PageSection>
        <PageHeading title="<strong>Failures conventional tests miss.</strong> The engine measures what staging cannot." />
        <FeatureGrid
          items={[
            { title: "Locks", body: "The strongest mode held per table, how long, and what queued behind it." },
            { title: "Rewrites", body: "Full table rewrites, reported by Postgres rather than guessed from the SQL." },
            { title: "Plans", body: "EXPLAIN before and against the migrated branch, on production's own shape." },
            { title: "Statements", body: "Per-statement duration, so the slow one in a batch is named." },
            { title: "Lint", body: "Six rules, each carrying the fix rather than only the complaint." },
            { title: "Comparison", body: "A saved report from an earlier run, compared against this one." },
          ]}
        />
      </PageSection>

      <PageSection>
        <Split
          visual={
            <div className="overflow-hidden rounded-[12px] border border-black/[0.08] bg-white">
              <LockChart state={1} />
              <LockChartMobile state={1} />
            </div>
          }
        >
          <PageHeading title="<strong>Safer pattern: expand-and-contract.</strong> The lint rule carries the fix, not only the complaint." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Switch the film to expand-and-contract. The strongest lock drops to 0.4s, no statement
            queues behind it, and the table is not rewritten.
          </p>
          <div className="mt-8">
            <Callout label="What the lint rule says">
              Adding a column with a default rewrites the table. Add it nullable with no default,
              backfill in batches, then enforce the constraint in a later migration.
            </Callout>
          </div>
        </Split>
        <div className="mt-16 max-md:mt-10">
          <Steps
            items={[
              {
                title: "Expand",
                body: "Add a nullable column with no default. Old rows stay readable by both binaries.",
              },
              {
                title: "Backfill",
                body: "Fill values in batches so checkout never waits on ACCESS EXCLUSIVE.",
              },
              {
                title: "Dual-read",
                body: "Deploy code that reads both shapes before you tighten the schema.",
              },
              {
                title: "Contract",
                body: "Enforce the constraint in a later migration, when every row already satisfies it.",
              },
            ]}
          />
        </div>
      </PageSection>

      <RelatedGrid
        items={[
          { href: "/solutions", title: "Solutions", description: "The teams who feel this first." },
          { href: "/product/report", title: "Safety Report", description: "How the lock becomes a GitHub check." },
          { href: "/docs/guides/invariants", title: "Invariants docs", description: "The subscriptions demo in full." },
        ]}
      />
    </PageShell>
  );
}
