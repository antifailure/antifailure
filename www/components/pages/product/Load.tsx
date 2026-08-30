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
} from "@/components/pages/kit";
import { Illustrative } from "@/components/layout/Illustrative";
import { Hairline, MonoLabel, Panel, StatusPill } from "@/components/home/visuals/primitives";
import { cn } from "@/lib/cn";

const MANIFEST = `load:
  enabled: true
  source: access_log
  source_config:
    path: ops/access.log
  scale: 0.05
  duration: 2m
  safe_routes: ["GET /**", "POST /api/search"]
  unsafe_routes: ["POST /api/payments/**", "DELETE /**"]
  thresholds:
    p95_increase: 0.25
    error_rate: 0.01`;

/** A shaped result, in the order the runner sorts one: worst regression first. */
const ROUTES: { route: string; share: string; p95: string; base: string | null; delta: number | null }[] = [
  { route: "GET /api/subscriptions", share: "18%", p95: "412ms", base: "180ms", delta: 1.29 },
  { route: "GET /settings/billing", share: "34%", p95: "168ms", base: "150ms", delta: 0.12 },
  { route: "GET /", share: "27%", p95: "44ms", base: "41ms", delta: 0.07 },
  { route: "POST /api/search", share: "9%", p95: "228ms", base: null, delta: null },
];

const REFUSED = ["POST /billing/upgrade", "POST /api/payments/intent", "DELETE /api/seats/*"];

const PROPERTIES = [
  {
    title: "The mix, not one endpoint",
    body: "Routes are weighted by the share of requests production actually served them. A flat mix proves the endpoint you already trusted is fast.",
  },
  {
    title: "Poisson arrivals",
    body: "Requests arrive in clumps, the way real ones do. Evenly spaced arrivals hide the queueing that the change is about to make worse.",
  },
  {
    title: "Deterministic per seed",
    body: "The same seed sends the same requests in the same order, so two runs are comparable and a difference belongs to the change.",
  },
  {
    title: "The achieved rate, reported",
    body: "The report carries the rate the generator managed, not the rate it was asked for. Reporting the target is how a load test says everything was fine while the queue grew.",
  },
  {
    title: "Unsafe until named",
    body: "No route is sent until the manifest names it safe. With no allowlist the default is read-only GETs under the root.",
  },
  {
    title: "Compared, not scored",
    body: "Each route is measured against the p95 production serves it in. The answer is a delta, never an absolute capacity claim.",
  },
];

function TrafficResult() {
  return (
    <Panel className="rounded-[12px] bg-white tabular-nums">
      <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 px-5 py-3">
        <div className="flex min-w-0 items-center gap-3">
          <MonoLabel className="uppercase tracking-[0.14em]">af load</MonoLabel>
          <MonoLabel className="text-black/45">source access_log</MonoLabel>
        </div>
        <div className="flex flex-wrap items-center gap-x-5 gap-y-1 font-mono text-[11px] tracking-extra-tight text-black/55">
          <span>
            sent <span className="text-black">2,140</span>
          </span>
          <span>
            rate <span className="text-black">17.8/s</span>
          </span>
          <span>
            errors <span className="text-black">0.2%</span>
          </span>
          <StatusPill tone="FAIL">FAIL</StatusPill>
        </div>
      </div>
      <Hairline />

      <div className="flex items-center gap-4 px-5 py-2">
        <MonoLabel className="w-[44%] shrink-0 uppercase tracking-[0.14em]">route</MonoLabel>
        <MonoLabel className="w-[14%] shrink-0 text-right uppercase tracking-[0.14em]">share</MonoLabel>
        <MonoLabel className="w-[20%] shrink-0 text-right uppercase tracking-[0.14em]">p95</MonoLabel>
        <MonoLabel className="min-w-0 flex-1 text-right uppercase tracking-[0.14em]">
          vs production
        </MonoLabel>
      </div>
      <Hairline />

      <ul>
        {ROUTES.map((row) => {
          const breach = row.delta !== null && row.delta > 0.25;
          return (
            <li
              key={row.route}
              className="flex items-baseline gap-4 border-b border-black/[0.06] px-5 py-3 last:border-0 max-md:flex-wrap max-md:gap-y-1"
            >
              <span className="w-[44%] min-w-0 shrink-0 truncate font-mono text-[12px] tracking-extra-tight text-black max-md:w-full">
                {row.route}
              </span>
              <span className="w-[14%] shrink-0 text-right font-mono text-[11px] tracking-extra-tight text-black/45 max-md:w-auto max-md:text-left">
                {row.share}
              </span>
              <span className="w-[20%] shrink-0 text-right font-mono text-[12px] tracking-extra-tight text-black/70 max-md:w-auto">
                {row.p95}
              </span>
              <span
                className={cn(
                  "min-w-0 flex-1 text-right font-mono text-[11px] tracking-extra-tight max-md:flex-none",
                  breach ? "text-red-700" : "text-black/45",
                )}
              >
                {row.delta === null
                  ? "no baseline"
                  : `${row.base} baseline, ${Math.round(row.delta * 100)}% slower`}
              </span>
            </li>
          );
        })}
      </ul>

      <Hairline />
      <div className="px-5 py-3">
        <MonoLabel className="mb-2 block uppercase tracking-[0.14em]">not sent</MonoLabel>
        <div className="flex flex-wrap gap-1.5">
          {REFUSED.map((route) => (
            <span
              key={route}
              className="border border-black/[0.08] px-2 py-1 font-mono text-[10px] tracking-extra-tight text-black/45"
            >
              {route}
            </span>
          ))}
        </div>
      </div>
    </Panel>
  );
}

export function LoadPage() {
  return (
    <PageShell inset>
      <PageHero
        eyebrow="Load"
        title="Traffic shaped like production's, sent at the twin."
        lead="The engine reads your production access log, keeps the mix of routes it actually served, and sends that mix at the twin. Every route is unsafe until the manifest names it, and every answer is a comparison against the p95 production serves that route in."
        framed={false}
        visual={
          <div>
            <TrafficResult />
            <Illustrative>
              A shaped run, to show the format. The routes, latencies and shares are written. The
              columns, the sort order and the thresholds are the ones{" "}
              <code className="font-mono text-[12px] text-black/70">af load</code> produces.
            </Illustrative>
          </div>
        }
      />

      <PageSection>
        <PageHeading
          kicker="Why the shape"
          title="<strong>The mix is the point.</strong> A load test that hammers one endpoint proves the endpoint is fast, which nobody doubted."
        />
        <p className="mt-6 max-w-[560px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          What breaks under real traffic is the mix: the page nobody thinks about that is nine
          percent of requests, and the endpoint that is fine alone and holds a lock the hot path
          wants. So the traffic is a weighted mix read from what production actually served.
        </p>
        <FeatureGrid items={PROPERTIES} />
      </PageSection>

      <PageSection tone="white">
        <Split visual={<CodePanel label="antifailure.yaml">{MANIFEST}</CodePanel>}>
          <PageHeading title="<strong>One source is connected.</strong> The others are in the schema and refused out loud." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            A combined-format access log is the only shape source this build reads. The manifest also
            accepts Datadog, New Relic and OpenTelemetry, and every one of them is refused at runtime
            with a message naming what to use instead.
          </p>
          <div className="mt-8">
            <Callout label="AF-LOD-010">
              otel is not connected in this build; use access_log or none.
            </Callout>
          </div>
        </Split>
      </PageSection>

      <PageSection tone="sage">
        <PageHeading title="<strong>A route with no baseline is never a breach.</strong> Comparing against nothing and calling the answer a regression is how a check becomes noise." />
        <p className="mt-8 max-w-[560px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          Thresholds are deltas against what production serves, never absolute numbers: an absolute
          limit fails on a slow runner and says nothing about the change. A new route the log has
          never seen is listed with its latency and no verdict, because there is nothing yet to
          compare it to.
        </p>
        <div className="mt-12 max-w-[720px]">
          <Callout label="What load does not do">
            It does not run traffic against a migration while the migration applies, and it does not
            deploy a second version of the application to compare against. The baseline is the p95 in
            your own access log.
          </Callout>
        </div>
      </PageSection>

      <RelatedGrid
        items={[
          {
            href: "/product/report",
            title: "Safety Report",
            description: "Where a regressed route lands on the pull request.",
          },
          {
            href: "/product/migrations",
            title: "Migration Safety",
            description: "Locks, rewrites and query plans on a branch with production's shape.",
          },
          { href: "/docs/concepts/load", title: "Load docs", description: "Sources, routes, and thresholds." },
        ]}
      />
    </PageShell>
  );
}
