import type { ReactNode } from "react";
import Link from "next/link";
import { WorkloadScene } from "@/components/home/visuals/WorkloadScene";
import { Hairline, MonoLabel, Panel, QueueChip } from "@/components/home/visuals/primitives";
import {
  Callout,
  CodePanel,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  RelatedGrid,
  Split,
} from "@/components/pages/kit";
import { cn } from "@/lib/cn";

const MIX = [
  { key: "Obs", pct: 41, fill: "rgba(0,0,0,0.18)" },
  { key: "Det", pct: 39, fill: "#33BF00" },
  { key: "Exploratory", pct: 20, fill: "#285D49" },
] as const;

const CONCURRENCY = [2, 4, 8, 16] as const;
const ACTIVE_CONC = 16;

const SOURCES = [
  {
    letter: "A",
    accent: "bg-black",
    label: "Observed",
    title: "Production patterns, redacted.",
    body: "Ingress traces, API telemetry, OpenTelemetry, session recordings, or customer samples. Normalized before storage or replay.",
    rows: [
      ["GET", "/settings/billing", "34%"],
      ["POST", "/billing/upgrade", "22%"],
      ["GET", "/api/subscriptions", "18%"],
    ],
    chip: "async capture",
  },
  {
    letter: "B",
    accent: "bg-[#33BF00]",
    label: "Deterministic",
    title: "Versioned journeys at scale.",
    body: "Compiled scenarios run at controlled concurrency. Repeatable, statistically comparable, and enforceable in CI.",
    rows: [
      ["v3", "impatient_upgrade", "×50"],
      ["v2", "checkout_happy", "×20"],
      ["v1", "api_retry_client", "×8"],
    ],
    chip: "no LLM at scale",
  },
  {
    letter: "C",
    accent: "bg-[#285D49]",
    label: "Exploratory",
    title: "Exploratory users that discover.",
    body: "Agents pursue goals, not selectors. They find paths and friction; the runner proves them. They live here beside observed and deterministic traffic.",
    rows: [
      ["goal", "upgrade to Pro", "impatient"],
      ["trait", "double-click, retry", "34%"],
      ["out", "compile to scenario", "v3"],
    ],
    chip: "discover, then compile",
    href: "/product/exploratory-users",
    hrefLabel: "Open exploratory users",
  },
] as const;

export function WorkloadPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Workload Studio"
        title="Exercise the twin the way production actually behaves."
        lead="Three traffic sources on an isolated twin: observed patterns, deterministic journeys, and exploratory users. Capture is asynchronous. Live requests stay on their own path."
        visual={<WorkloadScene />}
      />

      <PageSection>
        <PageHeading
          kicker="Traffic mixer"
          title="<strong>AI discovers. Systems prove.</strong> Agents are not economical load generators."
        />
        <TrafficMixer />
      </PageSection>

      <PageSection tone="white">
        <PageHeading
          kicker="Three sources"
          title="<strong>Observed. Deterministic. Exploratory.</strong> Compile what matters. Scale what repeats."
        />
        <div className="relative mt-14">
          <ul className="grid grid-cols-3 gap-x-16 max-lg:grid-cols-1 max-lg:gap-y-10">
            {SOURCES.map((source) => (
              <li key={source.letter} className="min-w-0">
                <SourceColumn source={source} />
              </li>
            ))}
          </ul>
          <span className="pointer-events-none absolute inset-y-0 left-[calc(33.333%-32px)] w-px bg-black/12 max-lg:hidden" />
          <span className="pointer-events-none absolute inset-y-0 right-[calc(33.333%-32px)] w-px bg-black/12 max-lg:hidden" />
        </div>
      </PageSection>

      <PageSection>
        <Split
          visual={
            <CodePanel label="scenario: impatient_upgrade">{`identity_fixture: returning_pro_user
steps:
  - open: /settings/billing
  - click: upgrade
  - submit: payment_form
  - parallel:
      - retry_submit_after_ms: 300
      - refresh_after_ms: 450
assertions:
  - one_subscription_created
  - at_most_one_payment_attempt
  - confirmation_visible`}
            </CodePanel>
          }
        >
          <PageHeading title="<strong>No LLM at each step.</strong> The deterministic runner executes the compiled journey." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Exploratory users can intervene when the interface changes or an unexplained state is
            encountered. Useful discoveries compile into this IR, then run without a model call.
          </p>
          <Link
            href="/product/exploratory-users"
            className="mt-8 inline-block text-[15px] tracking-extra-tight text-black underline decoration-black/20 underline-offset-4"
          >
            Exploratory users in Workload Studio →
          </Link>
        </Split>
      </PageSection>

      <PageSection tone="sage">
        <Callout label="Never in the hot path">
          A mix or percentage control must not imply that production requests are synchronously diverted.
          Shadowing is asynchronous, redacted, and rate-limited on replay. It must never delay or alter
          production responses.
        </Callout>
      </PageSection>

      <RelatedGrid
        items={[
          { href: "/product/exploratory-users", title: "Exploratory users", description: "Exploratory users inside Workload Studio, beside observed and deterministic traffic." },
          { href: "/product/oracle", title: "Differential Oracle", description: "Same compiled workload against baseline and candidate." },
          { href: "/docs/workload", title: "Workload docs", description: "Scenario IR and traffic controls." },
        ]}
      />
    </PageShell>
  );
}

function TrafficMixer() {
  return (
    <Panel className="mt-14 rounded-[12px] bg-white tabular-nums">
      <header className="flex h-10 items-center justify-between gap-3 px-4">
        <div className="flex min-w-0 items-center gap-2">
          <span className="size-1.5 shrink-0 bg-[#33BF00]" />
          <MonoLabel className="text-black/70">Workload Studio</MonoLabel>
          <span className="text-black/20">·</span>
          <MonoLabel className="tabular-nums">run_08f2</MonoLabel>
        </div>
        <MonoLabel className="shrink-0 tabular-nums text-black/55">06:12 remaining</MonoLabel>
      </header>
      <Hairline />

      <div className="grid grid-cols-4 max-lg:grid-cols-2 max-md:grid-cols-1">
        <MixerCell label="session rate">
          <div className="flex items-baseline gap-1.5">
            <span className="font-mono text-[28px] leading-none tracking-tighter text-black">24</span>
            <MonoLabel>rps</MonoLabel>
          </div>
          <div className="relative mt-4 h-px w-full bg-black/15">
            <div className="absolute inset-y-0 left-0 w-[75%] bg-[#33BF00]" />
            <span className="absolute top-1/2 left-[75%] size-1.5 -translate-x-1/2 -translate-y-1/2 bg-black" />
          </div>
          <div className="mt-1.5 flex justify-between">
            <MonoLabel className="tabular-nums text-[9px]">8</MonoLabel>
            <MonoLabel className="tabular-nums text-[9px]">24</MonoLabel>
            <MonoLabel className="tabular-nums text-[9px]">32</MonoLabel>
          </div>
        </MixerCell>

        <MixerCell label="concurrent sessions" rule>
          <div className="flex items-baseline justify-between gap-2">
            <span className="font-mono text-[28px] leading-none tracking-tighter text-black">{ACTIVE_CONC}</span>
            <MonoLabel>sessions</MonoLabel>
          </div>
          <div className="mt-4 grid grid-cols-4 gap-px bg-black/10">
            {CONCURRENCY.map((n) => (
              <div
                key={n}
                className={cn(
                  "relative flex h-8 items-center justify-center bg-[#f7f7f5] font-mono text-[11px] tabular-nums",
                  n <= ACTIVE_CONC ? "text-black" : "text-black/30",
                )}
              >
                {n <= ACTIVE_CONC ? (
                  <span className="absolute inset-0 bg-[#33BF00]/25" />
                ) : null}
                <span className="relative">{n}</span>
              </div>
            ))}
          </div>
        </MixerCell>

        <MixerCell label="observed | synthetic mix" rule>
          <div className="flex h-2 w-full overflow-hidden border border-black/[0.08]">
            {MIX.map((band) => (
              <span key={band.key} className="h-full" style={{ width: `${band.pct}%`, background: band.fill }} />
            ))}
          </div>
          <div className="mt-4 flex justify-between gap-2">
            {MIX.map((band) => (
              <div key={band.key} className="min-w-0">
                <MonoLabel className="block">{band.key}</MonoLabel>
                <span className="mt-0.5 block font-mono text-[15px] tabular-nums tracking-extra-tight text-black">
                  {band.pct}
                </span>
              </div>
            ))}
          </div>
          <MonoLabel className="mt-3 block text-[9px]">replay on the twin · not live diversion</MonoLabel>
        </MixerCell>

        <MixerCell label="baseline | candidate split" rule>
          <div className="flex items-baseline justify-between gap-2">
            <span className="font-mono text-[28px] leading-none tracking-tighter text-black">50</span>
            <MonoLabel>/ 50</MonoLabel>
          </div>
          <div className="mt-4 flex h-2 overflow-hidden border border-black/[0.08]">
            <span className="h-full w-1/2 bg-black/20" />
            <span className="h-full w-1/2 bg-[#33BF00]/70" />
          </div>
          <div className="mt-1.5 flex justify-between">
            <MonoLabel className="flex items-center gap-1.5">
              <span className="h-px w-3 bg-black/35" />
              baseline
            </MonoLabel>
            <MonoLabel className="flex items-center gap-1.5 text-[#285D49]">
              <span className="h-px w-3 bg-[#33BF00]" />
              candidate
            </MonoLabel>
          </div>
        </MixerCell>
      </div>

      <Hairline />
      <footer className="flex min-h-11 flex-wrap items-center justify-between gap-3 px-4 py-3">
        <MonoLabel className="text-black/55">async capture · never in synchronous prod path</MonoLabel>
        <div className="flex flex-wrap items-center gap-1.5">
          <QueueChip>returning_pro_user</QueueChip>
          <QueueChip>new_customer</QueueChip>
          <QueueChip>adversarial_input</QueueChip>
        </div>
      </footer>
    </Panel>
  );
}

function MixerCell({
  label,
  children,
  rule,
}: {
  label: string;
  children: ReactNode;
  rule?: boolean;
}) {
  return (
    <div
      className={cn(
        "px-4 py-5",
        rule &&
          "border-black/10 max-md:border-t md:max-lg:even:border-l md:max-lg:[&:nth-child(n+3)]:border-t lg:border-l",
      )}
    >
      <MonoLabel className="block">{label}</MonoLabel>
      <div className="mt-4">{children}</div>
    </div>
  );
}

function SourceColumn({ source }: { source: (typeof SOURCES)[number] }) {
  return (
    <div className="flex h-full min-w-0 flex-col">
      <div className="flex items-center gap-2">
        <span className={cn("size-1.5 shrink-0", source.accent)} />
        <MonoLabel className="text-black/55">{source.label}</MonoLabel>
      </div>
      <h3 className="mt-4 text-[18px] leading-snug tracking-extra-tight text-black">{source.title}</h3>
      <p className="mt-2 text-[14px] leading-6 tracking-extra-tight text-gray-new-40">{source.body}</p>
      <ul className="mt-5 space-y-1.5 font-mono text-[11px] tracking-extra-tight">
        {source.rows.map(([k, v, meta]) => (
          <li key={`${k}-${v}`} className="flex items-baseline justify-between gap-3 text-black/70">
            <span className="min-w-0 truncate">
              <span className="text-black/40">{k}</span>
              <span className="ml-2 text-black">{v}</span>
            </span>
            <span className="shrink-0 tabular-nums text-black/45">{meta}</span>
          </li>
        ))}
      </ul>
      {"href" in source && source.href ? (
        <Link
          href={source.href}
          className="mt-auto pt-5 text-[13px] tracking-extra-tight text-black/70 transition-colors hover:text-black"
        >
          {source.hrefLabel} →
        </Link>
      ) : null}
    </div>
  );
}
