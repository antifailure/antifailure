import type { ReactNode } from "react";
import {
  Callout,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  RelatedGrid,
  SpecTable,
  Split,
  Stage,
  Steps,
} from "@/components/pages/kit";
import { TrustBoundaryScene } from "@/components/home/visuals/TrustBoundaryScene";
import {
  CheckRow,
  Hairline,
  MonoLabel,
  Node,
  Panel,
  QueueChip,
  Receipt,
  StatusPill,
} from "@/components/home/visuals/primitives";
import { cn } from "@/lib/cn";

type PlaneItem = {
  label: string;
  detail: string;
};

const CONTROL_PLANE: PlaneItem[] = [
  { label: "Organizations", detail: "Projects, entitlements, and fleet status." },
  { label: "GitHub and CI", detail: "Integrations that plan the run and attach the gate." },
  { label: "Run planning", detail: "Policy, fidelity, and the validation plan." },
  { label: "Reports", detail: "Aggregated evidence and historical comparisons." },
  { label: "Billing", detail: "Credits, caps, and cost attribution." },
];

const DATA_PLANE: PlaneItem[] = [
  { label: "Discovery", detail: "Cloud and database inventory stays local." },
  { label: "Snapshots", detail: "Access, restore, and sanitization in-boundary." },
  { label: "Provisioning", detail: "Twins, networks, and secret replacement." },
  { label: "Egress", detail: "Capture, redaction, and fail-closed enforcement." },
  { label: "Execution", detail: "Workloads, raw logs, traces, and cleanup." },
];

const ISOLATION_MINIMUMS = [
  "No production write credentials",
  "No production database route",
  "No default internet route",
  "Separate DNS policy",
  "Separate secrets namespace",
  "Temporary workload identity",
  "Resource tags with run ID and expiration",
  "Hard cost ceiling",
  "Admission policy that rejects unowned resources",
  "Independent cleanup controller",
] as const;

const COST_ROWS: [string, string][] = [
  ["Estimate", "Cost is computed before anything is provisioned."],
  ["Per-run cap", "A hard spending ceiling on every twin."],
  ["Daily cap", "Per-organization daily budget."],
  ["Concurrency", "Maximum agent and deterministic concurrency."],
  ["Downscale", "Automatic downscaling when statistical confidence is reached."],
  ["Cache", "Snapshot and build caching across runs."],
  ["Subset", "Referential copies instead of full cloning by default."],
  ["BYOC", "Customer-cloud execution for expensive enterprise workloads."],
  ["TTL", "Inactivity expiration with attested destruction."],
  ["Attribution", "Cost by pull request and team."],
];

function PlaneColumn({
  kicker,
  title,
  tone,
  items,
  footer,
}: {
  kicker: string;
  title: string;
  tone: "control" | "data";
  items: PlaneItem[];
  footer: ReactNode;
}) {
  return (
    <div
      className={cn(
        "flex min-w-0 flex-col p-8 max-md:p-6",
        tone === "control" && "bg-white",
        tone === "data" && "bg-[#E4F1EB]/55",
      )}
    >
      <MonoLabel className="uppercase tracking-[0.14em]">{kicker}</MonoLabel>
      <h3 className="mt-3 text-[22px] leading-snug tracking-extra-tight text-black max-md:text-[18px]">
        {title}
      </h3>
      <Hairline className="my-6" />
      <ul className="flex flex-1 flex-col gap-4">
        {items.map((item) => (
          <li key={item.label} className="min-w-0">
            <Node label={item.label} lit={item.label === "Reports" || item.label === "Snapshots"} />
            <p className="mt-1 pl-3.5 text-[13px] leading-5 tracking-extra-tight text-gray-new-40">
              {item.detail}
            </p>
          </li>
        ))}
      </ul>
      <div className="mt-8">{footer}</div>
    </div>
  );
}

function PlaneDiagram() {
  return (
    <Stage className="mt-14">
      <div className="relative">
        <div className="grid grid-cols-2 max-lg:grid-cols-1">
          <PlaneColumn
            kicker="Hosted"
            title="Control plane"
            tone="control"
            items={CONTROL_PLANE}
            footer={
              <Receipt className="flex items-center justify-between gap-3">
                <span>
                  rpt_08f2 · sha256:7c1a…
                  <span className="mt-0.5 block text-black/45">evidence, not records</span>
                </span>
                <StatusPill tone="BLOCK" />
              </Receipt>
            }
          />

          <div className="flex items-center gap-3 border-y border-black/10 px-6 py-3 lg:hidden">
            <span className="h-px min-w-0 flex-1 bg-black/12" aria-hidden />
            <MonoLabel className="uppercase tracking-[0.12em]">trust boundary</MonoLabel>
            <span className="h-px min-w-0 flex-1 bg-black/12" aria-hidden />
          </div>

          <PlaneColumn
            kicker="Customer-hosted"
            title="Data plane"
            tone="data"
            items={DATA_PLANE}
            footer={
              <Receipt>
                <div className="flex flex-wrap items-center gap-1.5">
                  <QueueChip>customer agent</QueueChip>
                  <MonoLabel>UP / OUT</MonoLabel>
                  <QueueChip>mTLS</QueueChip>
                </div>
                <div className="mt-1.5 text-black/45">short-lived credentials · no inbound hole</div>
              </Receipt>
            }
          />
        </div>

        <div
          className="pointer-events-none absolute inset-y-0 left-1/2 hidden w-px bg-[#c41e1e]/70 lg:block"
          aria-hidden
        />
        <div className="pointer-events-none absolute top-6 left-1/2 z-[1] hidden -translate-x-1/2 bg-white px-2.5 py-1 ring-1 ring-black/10 lg:block">
          <MonoLabel className="uppercase tracking-[0.12em]">trust boundary</MonoLabel>
        </div>
      </div>

      <div className="flex items-center justify-between gap-6 border-t border-black/10 bg-white px-8 py-4 max-md:flex-col max-md:items-start max-md:gap-3 max-md:px-6">
        <CheckRow ok={false}>raw snapshots · secrets · request bodies</CheckRow>
        <MonoLabel className="uppercase tracking-[0.12em]">do not enter the control plane</MonoLabel>
        <CheckRow ok>reports · sha256 · pass / warn / block</CheckRow>
      </div>
    </Stage>
  );
}

export function ArchitecturePage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Architecture"
        title="Hosted control plane. Customer-hosted data plane."
        lead="Organizations, policy, and reports live in the control plane. Snapshots, secrets, sanitization, provisioning, egress, and cleanup stay in the customer boundary. The agent is outbound-only, authenticated with short-lived mTLS."
        visual={
          <Stage className="min-h-[360px]">
            <TrustBoundaryScene />
          </Stage>
        }
      />

      <PageSection>
        <PageHeading
          kicker="What lives where"
          title="<strong>The control plane never needs a copy of production data.</strong> Evidence crosses the boundary. Records do not."
        />
        <PlaneDiagram />
      </PageSection>

      <PageSection tone="sage">
        <Split
          visual={
            <Panel className="rounded-[12px] bg-white p-7 ring-1 ring-black/10 max-md:p-5">
              <MonoLabel className="uppercase tracking-[0.14em]">Isolation minimums</MonoLabel>
              <ul className="mt-5 grid grid-cols-1 gap-3">
                {ISOLATION_MINIMUMS.map((item) => (
                  <li key={item}>
                    <CheckRow ok>{item}</CheckRow>
                  </li>
                ))}
              </ul>
            </Panel>
          }
        >
          <PageHeading title="<strong>Dedicated account, or a strongly isolated network.</strong> When practical, the clone is its own account, subscription, or project." />
          <p className="mt-6 max-w-[520px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            No production write credentials. No production database route. No default internet route.
            Isolation is a ship condition, not a later hardening pass.
          </p>
          <div className="mt-8">
            <Callout label="Fail closed">
              Unknown destinations, unresolved secrets, incomplete cleanup, or missing isolation block
              the run. Convenience must not silently override containment.
            </Callout>
          </div>
        </Split>
      </PageSection>

      <PageSection tone="white">
        <PageHeading
          kicker="Cost controls"
          title="<strong>Estimate before you provision.</strong> Hard ceilings, not hope."
        />
        <p className="mt-6 max-w-[560px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          Unlimited free hosted compute is not viable. Free usage has strict credits, or it runs in the
          customer’s own cloud with their model credentials.
        </p>
        <div className="mt-12">
          <SpecTable rows={COST_ROWS} />
        </div>
      </PageSection>

      <PageSection>
        <PageHeading title="<strong>Outbound-only from the agent</strong> where possible, with short-lived mTLS." />
        <p className="mt-6 max-w-[560px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          The customer agent dials out. There is no inbound hole into the data plane. Raw production
          data stays inside the customer boundary by default. The control plane receives evidence, not
          records.
        </p>
        <div className="mt-14">
          <Steps
            items={[
              { title: "Dial out", body: "The agent initiates. The control plane does not open a path in." },
              { title: "Handshake", body: "Short-lived mTLS credentials authenticate the session." },
              { title: "Evidence", body: "Reports, hashes, and the pass / warning / block cross. Records do not." },
              { title: "Reap", body: "A TTL reaper runs independently of the orchestrator and attests destroy." },
            ]}
          />
        </div>
        <div className="mt-12">
          <Callout label="Recoverable">
            Every lifecycle transition is idempotent. Stuck resources retry, then the independent reaper
            destroys them. Cleanup is a first-class safety property.
          </Callout>
        </div>
      </PageSection>

      <RelatedGrid
        items={[
          { href: "/security", title: "Security", description: "Fail closed. Data stays in your boundary." },
          { href: "/open-source", title: "Open source", description: "The inspectable surface inside the boundary." },
          { href: "/docs/guides/local-runtime/", title: "Architecture docs", description: "Lifecycle and isolation in full." },
        ]}
      />
    </PageShell>
  );
}
