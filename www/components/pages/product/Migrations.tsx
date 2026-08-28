"use client";

import { useEffect, useRef, useState } from "react";
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
import { MigrationScene, type MigrationBar } from "@/components/home/visuals/MigrationScene";
import { cn } from "@/lib/cn";

const TABS = ["Catch exclusive locks", "Safer expand-and-contract"] as const;
const VEIL_EASE = "cubic-bezier(0.16, 1, 0.3, 1)";
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
  const [veil, setVeil] = useState(0);
  const [bar, setBar] = useState<MigrationBar>({
    verdict: "BLOCK",
    slam: false,
    decided: false,
    passGlow: false,
  });
  const busy = useRef(false);
  const timers = useRef<number[]>([]);

  useEffect(() => () => timers.current.forEach((id) => window.clearTimeout(id)), []);

  function cutTo(index: 0 | 1) {
    if (busy.current || index === active) return;
    busy.current = true;
    setVeil(1);
    const cut = window.setTimeout(() => {
      setActive(index);
      setPlayId((n) => n + 1);
      setBar({
        verdict: index === 0 ? "BLOCK" : "PASS",
        slam: false,
        decided: false,
        passGlow: false,
      });
    }, 80);
    const clear = window.setTimeout(() => {
      setVeil(0);
      busy.current = false;
    }, 160);
    timers.current = [cut, clear];
  }

  return (
    <div className="relative z-10 w-full min-w-0">
      <div className="group relative z-20 w-fit">
        {TABS.map((item, index) => (
          <button
            className={cn(
              "relative h-11 min-w-[134px] px-4 py-3 whitespace-nowrap transition-colors duration-200",
              "font-medium leading-none tracking-extra-tight",
              "border border-gray-new-10 even:border-l-0",
              "max-xl:h-10 max-xl:min-w-[130px] max-lg:h-9 max-lg:min-w-[124px] max-lg:px-3 max-lg:py-2.5 max-md:text-[14px]",
              index === active
                ? "bg-white text-gray-new-10"
                : "bg-[#E4F1EB] text-gray-new-10/80 hover:bg-white/70",
            )}
            key={item}
            type="button"
            onClick={() => cutTo(index === 0 ? 0 : 1)}
          >
            {item}
          </button>
        ))}
      </div>
      <div className="relative mt-8 w-full min-w-0 max-lg:mt-6">
        <MigrationScene tab={active} playId={playId} onBar={setBar} />
        <div className="relative z-20 border-x border-b border-gray-new-10 bg-[#CAE6D9] px-5 py-3 max-md:px-4">
          <p className="font-mono text-[13px] leading-5 tracking-extra-tight text-pretty text-[#285D49] max-xl:text-[12px] max-md:text-[12px] max-md:leading-5">
            <span
              className={cn(
                "font-semibold uppercase tabular-nums",
                bar.verdict === "BLOCK" && bar.decided && "text-red-600",
                bar.verdict === "PASS" && bar.passGlow && "text-green-45",
              )}
              style={{
                letterSpacing: bar.slam ? "0.04em" : "0em",
                transition: bar.slam ? "none" : `letter-spacing 200ms ${VEIL_EASE}`,
              }}
            >
              {bar.verdict}
            </span>
            <span className="ml-2 font-medium normal-case">
              {bar.verdict === "BLOCK"
                ? "ACCESS EXCLUSIVE 27.4s · checkout p99 820ms→6.9s · 11.8% upgrade timeouts · rollback unsafe"
                : "expand-and-contract · lock 0.4s · blocked 0 · p99 834ms · rollback feasible"}
            </span>
          </p>
        </div>
        <div
          className="pointer-events-none absolute inset-0 z-30 bg-[#E4F1EB]"
          style={{
            opacity: veil,
            transition: `opacity 80ms ${VEIL_EASE}`,
          }}
          aria-hidden
        />
      </div>
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
      />

      <PageSection tone="sage">
        <PageHeading
          kicker="On the twin"
          title="<strong>Migration safety first.</strong> Measure locks, plans, pool pressure, and rollback feasibility on a production-shaped twin before the change ships."
        />
        <div className="mt-12 max-xl:mt-10 max-md:mt-8">
          <MigrationStudio />
        </div>
      </PageSection>

      <PageSection>
        <PageHeading
          kicker="The finding"
          title="<strong>A 27-second lock is a block.</strong> Not a warning you can ignore."
        />
        <ul className="mt-16 grid grid-cols-4 gap-x-10 gap-y-10 border-t border-black/10 pt-12 max-lg:grid-cols-2 max-md:mt-10 max-md:grid-cols-1 max-md:pt-8">
          {FINDINGS.map((item) => (
            <li key={item.label} className="min-w-0">
              <div className="font-mono text-[11px] font-medium tracking-[0.14em] text-gray-new-50 uppercase">
                {item.label}
              </div>
              <div
                className={cn(
                  "mt-3 font-title text-[52px] leading-none tracking-tighter tabular-nums max-xl:text-[40px] max-md:text-[36px]",
                  item.danger ? "text-red-700" : "text-black",
                )}
              >
                {item.value}
              </div>
              <p className="mt-3 max-w-[220px] text-[14px] leading-5 tracking-extra-tight text-gray-new-40">
                {item.hint}
              </p>
            </li>
          ))}
        </ul>
        <div className="mt-16 overflow-hidden ring-1 ring-black/10 max-md:mt-10">
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

      <PageSection tone="sage">
        <Split
          reverse
          visual={
            <div className="overflow-hidden ring-1 ring-black/10">
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
          { href: "/docs/concepts/insights/", title: "Migration docs", description: "The subscriptions demo in full." },
        ]}
      />
    </PageShell>
  );
}
