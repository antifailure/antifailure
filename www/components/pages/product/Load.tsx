import { Callout, FeatureGrid, PageHeading, PageHero, PageSection, PageShell, RelatedGrid, Split } from "@/components/pages/kit";
import { Illustrative } from "@/components/layout/Illustrative";
import { PLD01, PLD02 } from "@/components/pages/figures/product-load";

const MANIFEST = `load:
  enabled: true
  source: otel
  source_config:
    path: traffic/production.otlp.json
  scale: 0.05
  duration: 2m
  safe_routes: ["GET /**", "POST /api/search"]
  unsafe_routes: ["POST /api/payments/**", "DELETE /**"]
  thresholds:
    p95_increase: 0.25
    error_rate: 0.01`;

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

export function LoadPage() {
  return (
    <PageShell>
      <PageHero
        path="/product/load"
        eyebrow="Load"
        title="Traffic shaped like production's, sent at the twin."
        lead="The engine reads your own production traffic, keeps the mix of routes it actually served, and sends that mix at the twin. Every route is unsafe until the manifest names it, and a trace export arrives carrying production's own p95 per route, so the answer is a comparison rather than a score."
        framed={false}
        visual={
          <div>
            <PLD01 />
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
        <p className="mt-8 max-w-[560px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          What breaks under real traffic is the mix: the page nobody thinks about that is nine
          percent of requests, and the endpoint that is fine alone and holds a lock the hot path
          wants. So the traffic is a weighted mix read from what production actually served.
        </p>
        <FeatureGrid items={PROPERTIES} />
      </PageSection>

      <PageSection tone="ruled">
        <Split visual={<PLD02 source={MANIFEST} />}>
          <PageHeading title="<strong>Two sources, and only one of them carries a baseline.</strong> A trace export carries a latency. A log line does not." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            <code className="font-mono text-[15px] text-black/70">otel</code> reads an OpenTelemetry
            trace export in OTLP/JSON, the file a collector&apos;s file exporter writes.{" "}
            <code className="font-mono text-[15px] text-black/70">access_log</code> reads a combined
            format log. Both are read out of the repository, so no credential and no outbound call
            decides what traffic gets sent. A span has a start and an end, so a shape read from
            traces arrives with production&apos;s p95 for each route in it, which is what{" "}
            <code className="font-mono text-[15px] text-black/70">p95_increase</code> compares
            against. A combined format line carries no duration, so an access log gives the mix, the
            weights and the arrival rate, and no baseline to measure a regression against. Setting{" "}
            <code className="font-mono text-[15px] text-black/70">p95_increase</code> under the log
            is refused when the manifest is read, rather than accepted and quietly skipped.
          </p>
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            There were four sources once. Two of them existed only in the schema and were refused
            when a run reached them, which is worse than not offering them: a key you can set that
            cannot work reads as a broken product rather than an unfinished one. They are gone, and
            anything unrecognised is refused when the manifest is read, before anything is built.
          </p>
          {/*
            Both refusals under one label because both really are AF-MAN-002:
            the validator reports a path and a message, and the code carries
            them as its detail. Giving the second one a label of its own read
            as a second error code to anybody who had just read the first.
          */}
          <div className="mt-8">
            <Callout label="AF-MAN-002">
              <p>
                load.source: There is no load source called &quot;datadog&quot;. The sources that
                read traffic are otel, an OpenTelemetry trace export, and access_log, a combined
                format log. Both take source_config.path.
              </p>
              <p className="mt-4">
                load.thresholds.p95_increase: The load source is access_log and p95_increase is
                set. A combined format log line carries no duration, so every route read from one
                arrives with no baseline and this threshold can never fire.
              </p>
            </Callout>
          </div>
        </Split>
      </PageSection>

      <PageSection tone="panel">
        <Split
          visual={
            <Callout label="What load does not do">
              It does not run traffic against a migration while the migration applies, and it does not
              deploy a second version of the application to compare against. The baseline is
              production&rsquo;s own p95, read out of the trace export you point it at.
            </Callout>
          }
        >
          <PageHeading title="<strong>A route with no baseline is never a breach.</strong> Comparing against nothing and calling the answer a regression is how a check becomes noise." />
          <p className="mt-8 max-w-[560px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Thresholds are deltas against what production serves, never absolute numbers: an absolute
            limit fails on a slow runner and says nothing about the change. A route the export saw
            fewer than twenty times is listed with its latency and no verdict, because a percentile
            made of three numbers is noise. When no route in a run has a baseline, the threshold
            measured nothing and the run says so instead of reporting a clean p95.
          </p>
        </Split>
      </PageSection>

      <RelatedGrid
        items={[
          {
            href: "/product/twins",
            title: "Isolated Twin",
            description: "Where the traffic is sent.",
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
