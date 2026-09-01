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
import { Illustrative } from "@/components/layout/Illustrative";
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
  { label: "Run planning", detail: "Organization policy and which repositories may run." },
  { label: "Reports", detail: "Aggregated evidence and historical comparisons." },
  { label: "Billing", detail: "Plans and the quotas they carry. It takes no money: there is no payment integration behind it." },
];

const DATA_PLANE: PlaneItem[] = [
  { label: "Discovery", detail: "Cloud and database inventory stays local." },
  { label: "Snapshots", detail: "Access, restore, and sanitization in-boundary." },
  { label: "Provisioning", detail: "Twins, networks, and secret replacement." },
  { label: "Egress", detail: "Capture, redaction, and fail-closed enforcement." },
  { label: "Execution", detail: "Workflows, traffic, raw logs, traces, and cleanup." },
];

/** In force today. Each of these is something a run will refuse to start without. */
const ISOLATION_MINIMUMS = [
  "No production write credentials",
  "No production database route",
  "No default internet route",
  "Separate DNS policy",
  "Separate secrets namespace",
] as const;

/**
 * Designed and not built.
 *
 * This list used to sit beside the one above under the same heading, so a
 * reader had no way to tell which five were enforced. Grepping the engine and
 * the control plane for admission, reaper, budget, workload identity and
 * resource expiry finds no implementation of any of them.
 */
const ISOLATION_PLANNED = [
  "Temporary workload identity",
  "Resource tags with an expiry a controller enforces",
  "Hard per-run cost ceiling",
  "Admission policy that rejects unowned resources",
  "An independent cleanup controller",
] as const;

/** What keeps a run cheap today, and what does not exist yet. */
const COST_ROWS: [string, string][] = [
  ["Subset", "A referential subset instead of a full copy. Built, and off until you name a seed table."],
  ["Cache", "Goldens are branched rather than restored per run. Built."],
  ["BYOC", "The engine runs in your own CI on your own compute. Built."],
  ["Sweep", "af env prune removes environments past a cutoff you pass. Built."],
  ["Estimate", "A cost computed before anything is provisioned. Not built."],
  ["Per-run cap", "A hard spending ceiling on every twin. Not built."],
  ["Daily cap", "A per-organization daily budget. Not built."],
  ["Attribution", "Cost by pull request and team. Not built."],
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
        // The trust boundary chip is 103px wide and centred on the divider, so
        // it reaches 52px into each column. Against p-8 it painted its white
        // box over the first word on both sides, and the data plane's node read
        // "rovisioning". Only the divider side needs the air, and only where
        // the divider exists.
        tone === "control" && "bg-white xl:pr-16",
        tone === "data" && "bg-white xl:pl-16",
      )}
    >
      <MonoLabel tone="reader" className="uppercase tracking-[0.14em]">{kicker}</MonoLabel>
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
        <div className="grid grid-cols-2 max-xl:grid-cols-1">
          <PlaneColumn
            kicker="Hosted"
            title="Control plane"
            tone="control"
            items={CONTROL_PLANE}
            footer={
              <Receipt className="flex items-center justify-between gap-3">
                <span>
                  rpt_08f2 · sha256:7c1a…
                  <span className="mt-0.5 block text-black/60">evidence, not records</span>
                </span>
                <StatusPill tone="FAIL" />
              </Receipt>
            }
          />

          <div className="flex items-center gap-3 border-y border-black/10 px-6 py-3 xl:hidden">
            <span className="h-px min-w-0 flex-1 bg-black/12" aria-hidden />
            <MonoLabel tone="reader" className="uppercase tracking-[0.12em]">trust boundary</MonoLabel>
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
                <div className="mt-1.5 text-black/60">short-lived credentials · no inbound hole</div>
              </Receipt>
            }
          />
        </div>

        <div
          className="pointer-events-none absolute inset-y-0 left-1/2 hidden w-px bg-[#c41e1e]/70 xl:block"
          aria-hidden
        />
        {/* Centred on the divider rather than pinned to the top, where it sat
            on top of the data plane's own kicker and rendered it as
            "TOMER-HOSTED". */}
        <div className="pointer-events-none absolute top-1/2 left-1/2 z-[1] hidden -translate-x-1/2 -translate-y-1/2 border border-black/[0.08] bg-white px-2.5 py-1 xl:block">
          <MonoLabel tone="reader" className="uppercase tracking-[0.12em]">trust boundary</MonoLabel>
        </div>
      </div>

      <div className="flex items-center justify-between gap-6 border-t border-black/10 bg-white px-8 py-4 max-md:flex-col max-md:items-start max-md:gap-3 max-md:px-6">
        <CheckRow ok={false}>raw snapshots · secrets · request bodies</CheckRow>
        <MonoLabel tone="reader" className="uppercase tracking-[0.12em]">do not enter the control plane</MonoLabel>
        <CheckRow ok>reports · sha256 · pass or fail</CheckRow>
      </div>
    </Stage>
  );
}

export function ArchitecturePage() {
  return (
    <PageShell>
      <PageHero
        path="/product/architecture"
        eyebrow="Architecture"
        title="Hosted control plane. Customer-hosted data plane."
        lead="Organizations, policy, and reports live in the control plane. Snapshots, secrets, sanitization, provisioning, egress, and cleanup stay in the customer boundary. The agent is outbound-only, authenticated with short-lived mTLS."
        visual={<TrustBoundaryScene />}
        framed={false}
      />

      <PageSection>
        <PageHeading
          kicker="What lives where"
          title="<strong>The control plane never needs a copy of production data.</strong> Evidence crosses the boundary. Records do not."
        />
        <PlaneDiagram />
        <Illustrative>
          The split is architectural and enforced today: masking and verification run in your data
          plane, and the control plane's ingest takes events rather than records. The control plane
          is deployed at app.antifailure.dev and is invitation only, so the boundary now has a live
          consumer on the far side of it rather than a planned one.
        </Illustrative>
      </PageSection>

      <PageSection tone="sage">
        <Split
          visual={
            <Panel className="rounded-[12px] bg-white p-7 max-md:p-5">
              <MonoLabel tone="reader" className="uppercase tracking-[0.14em]">In force today</MonoLabel>
              <ul className="mt-5 grid grid-cols-1 gap-3">
                {ISOLATION_MINIMUMS.map((item) => (
                  <li key={item}>
                    <CheckRow ok>{item}</CheckRow>
                  </li>
                ))}
              </ul>
              <Hairline className="my-6" />
              <MonoLabel tone="reader" className="uppercase tracking-[0.14em]">Designed, not yet built</MonoLabel>
              <ul className="mt-5 grid grid-cols-1 gap-3">
                {ISOLATION_PLANNED.map((item) => (
                  <li key={item}>
                    <CheckRow ok={false} className="text-black/60">
                      {item}
                    </CheckRow>
                  </li>
                ))}
              </ul>
            </Panel>
          }
        >
          <PageHeading title="<strong>Dedicated account, or a strongly isolated network.</strong> When practical, the clone is its own account, subscription, or project." />
          <p className="mt-6 max-w-[520px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            No production write credentials. No production database route. No default internet route.
            Isolation is a ship condition, not a later hardening pass. The list beside this one is
            split on purpose: five of these are enforced today and five are design.
          </p>
          <div className="mt-8">
            <Callout label="Fail closed">
              An unverified golden cannot be branched, and an unresolved secret stops the run. Inside
              the twin the network has no route out and DNS is intercepted, so a client that ignores
              its proxy variables has nowhere to send the packet.
            </Callout>
          </div>
        </Split>
      </PageSection>

      <PageSection tone="white">
        <PageHeading
          kicker="Cost controls"
          title="<strong>What keeps a run cheap,</strong> and what is still only a plan."
        />
        <p className="mt-6 max-w-[560px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          Unlimited free hosted compute is not viable. Today the engine runs on your own compute with
          your own credentials, and the four controls below marked built are what hold the cost down.
          The rest are named here because they are designed, not because they are shipping.
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
              { title: "Evidence", body: "Reports, hashes and the verdict cross. Records do not." },
              { title: "Tear down", body: "The journal is replayed in reverse, and what was removed is counted." },
            ]}
          />
        </div>
        <div className="mt-12">
          <Callout label="Recoverable">
            Every lifecycle transition is idempotent, and a resource is journaled the moment it
            exists rather than after the run succeeds. A run that dies halfway still leaves a list of
            what it made, which is what af down replays.
          </Callout>
        </div>
      </PageSection>

      <RelatedGrid
        items={[
          { href: "/product/firewall", title: "Firewall", description: "How side effects are contained." },
          { href: "/docs", title: "Docs", description: "How a twin run works." },
          { href: "/docs/concepts/journal", title: "Journal docs", description: "Lifecycle and isolation in full." },
        ]}
      />
    </PageShell>
  );
}
