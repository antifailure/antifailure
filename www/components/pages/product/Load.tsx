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
    title: "Production route mix",
    body: "Weight each route by its share of production requests.",
  },
  {
    title: "Poisson arrivals",
    body: "Vary request arrival times to exercise queues under uneven load.",
  },
  {
    title: "Deterministic per seed",
    body: "Reuse a seed to send the same request sequence across runs.",
  },
  {
    title: "Actual throughput",
    body: "See the request rate achieved during the run alongside latency and errors.",
  },
  {
    title: "Choose which routes to test",
    body: "Configure a route allowlist. The default permits read-only GET requests.",
  },
  {
    title: "Latency comparison",
    body: "Compare each eligible route with its production p95 from the trace export.",
  },
];

export function LoadPage() {
  return (
    <PageShell>
      <PageHero
        path="/product/load"
        eyebrow="Load"
        title="Test your new build with your real traffic mix."
        lead="Import an access log or OpenTelemetry trace export. Antifailure sends the recorded route mix to your twin and measures latency and errors. Trace exports also provide a production baseline for comparison."
        framed={false}
        visual={
          <div>
            <PLD01 />
            <Illustrative>
            Example load report with sample routes and measurements.
          </Illustrative>
          </div>
        }
      />

      <PageSection>
        <PageHeading
          kicker="Why the shape"
          title="<strong>Exercise the routes together, as your users do.</strong>"
        />
        <p className="mt-8 max-w-[560px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          Routes share database connections, locks, and workers. Testing the production mix helps reveal slowdowns that isolated endpoint tests can miss.
        </p>
        <FeatureGrid items={PROPERTIES} />
      </PageSection>

      <PageSection tone="ruled">
        <Split visual={<PLD02 source={MANIFEST} />}>
          <PageHeading title="<strong>Start with access logs or traces.</strong>" />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Access logs provide route weights and arrival rates. OpenTelemetry traces
            also include request durations, so Antifailure can compare the new build
            with production latency. Point the manifest at your export and choose
            which routes to exercise.
          </p>
          {/*
            Both refusals under one label because both really are AF-MAN-002:
            the validator reports a path and a message, and the code carries
            them as its detail. Giving the second one a label of its own read
            as a second error code to anybody who had just read the first.
          */}
          <div className="mt-8">
            <Callout label="Configure your first load test">
              <a href="/docs/concepts/load" className="underline decoration-black/25 underline-offset-4">
                Read the guide to sources, route policies, and thresholds.
              </a>
            </Callout>
          </div>
        </Split>
      </PageSection>

      <PageSection tone="panel">
        <Split
          visual={
            <Callout label="Baseline requirements">
              Use an OpenTelemetry trace export for p95 comparisons. Access logs provide the route mix and arrival rate.
            </Callout>
          }
        >
          <PageHeading title="<strong>Set thresholds against your production baseline.</strong>" />
          <p className="mt-8 max-w-[560px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Set an acceptable p95 increase and error rate in the manifest. Routes with fewer than twenty baseline samples show their measured latency without a regression verdict. The report identifies checks that lacked enough data.
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
