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
import { LockChart, LockChartMobile } from "@/components/home/media/LockChart";
import { MigrationScene } from "@/components/home/visuals/MigrationScene";
import { cn } from "@/lib/cn";

const CAPTIONS = [
  "An exclusive lock on subscriptions stalls checkout. The twin reports BLOCK before it ships.",
  "Expand-and-contract keeps checkout live. Lock 0.4s, rollback feasible, PASS.",
] as const;

const FINDINGS = [
  { value: "27.4s", label: "ACCESS EXCLUSIVE", hint: "held on subscriptions", danger: true },
  { value: "6.9s", label: "Checkout p99", hint: "from 820ms under equivalent traffic", danger: true },
  { value: "11.8%", label: "Upgrade timeouts", hint: "of attempts failed during the lock", danger: true },
  { value: "Unsafe", label: "Rolling rollback", hint: "old app cannot read candidate writes", danger: true },
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
    <PageShell>
      <PageHero
        eyebrow="Migration Safety Engine"
        title="Catch exclusive locks before they take checkout down."
        lead="The flagship module. A disposable production twin applies the proposed schema change under production-shaped traffic, then returns an evidence-backed pass, warning, or block — with the lock, the p99, and a safer pattern."
        framed={false}
        visual={<MigrationStudio />}
      />

      <PageSection tone="sage">
        <PageHeading
          kicker="The finding"
          title="<strong>A 27-second lock is a block.</strong> Not a warning you can ignore."
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
      </PageSection>

      <PageSection tone="white">
        <Split
          visual={
            <CodePanel label="BLOCKED: unsafe schema migration">{`Migration 20260824_add_billing_status held an
ACCESS EXCLUSIVE lock on subscriptions for 27.4s.

checkout p99          820ms → 6.9s
upgrade timeouts      11.8%
old app cannot deserialize candidate writes
rolling rollback is unsafe`}
            </CodePanel>
          }
        >
          <PageHeading title="<strong>The report is causal.</strong> Change, lock, workflow, evidence, then the decision." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Staging with a handful of rows will not show an exclusive lock, a table rewrite, or the
            moment old binaries fail to read new rows. The twin runs the migration the way production
            would — then attaches the finding to the pull request.
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
            { title: "Locks", body: "Acquisition, duration, blocked statements, and cumulative blocked time." },
            { title: "Rewrites", body: "Full table rewrites, index builds, constraint failures on rare rows." },
            { title: "Plans", body: "Query-plan changes and latency distributions under production-shaped data." },
            { title: "Pools", body: "Connection-pool pressure, CPU, memory, IOPS, disk, and WAL growth." },
            { title: "Coexistence", body: "Old and new application versions during a rolling deploy." },
            { title: "Rollback", body: "Whether candidate writes make rolling rollback unsafe." },
          ]}
        />
      </PageSection>

      <PageSection>
        <Split
          reverse
          visual={
            <div className="overflow-hidden rounded-[12px] border border-black/[0.08] bg-white">
              <LockChart state={1} />
              <LockChartMobile state={1} />
            </div>
          }
        >
          <PageHeading title="<strong>Safer pattern: expand-and-contract.</strong> Evidence, then a recommended path that keeps checkout live." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Switch the film to expand-and-contract. Lock drops to 0.4s, blocked statements stay at
            zero, checkout p99 holds near 830ms, and rollback stays feasible.
          </p>
          <div className="mt-8">
            <Callout label="Expand-and-contract">
              Dual-read compatibility. Constraint later. The engine generates both the evidence and
              the recommended safer migration pattern.
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
                body: "Enforce the constraint in a later migration. Rolling rollback stays feasible.",
              },
            ]}
          />
        </div>
      </PageSection>

      <RelatedGrid
        items={[
          { href: "/solutions/migrations", title: "Schema migrations", description: "Why this is the starting wedge." },
          { href: "/product/report", title: "Safety Report", description: "How the lock becomes a GitHub check." },
          { href: "/docs/guides/invariants", title: "Invariants docs", description: "The subscriptions demo in full." },
        ]}
      />
    </PageShell>
  );
}
