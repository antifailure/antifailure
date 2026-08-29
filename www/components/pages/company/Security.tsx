import {
  Callout,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  RelatedGrid,
  Split,
  Stage,
} from "@/components/pages/kit";
import { TrustBoundaryScene } from "@/components/home/visuals/TrustBoundaryScene";
import {
  CheckRow,
  Hairline,
  MonoLabel,
  Node,
  Panel,
  Receipt,
  StatusPill,
  Timestamp,
  Ticker,
} from "@/components/home/visuals/primitives";

const CONTROL_PLANE = ["orgs", "GitHub", "policy", "reports", "billing"] as const;
const DATA_PLANE = [
  "snapshot",
  "sanitize",
  "provision",
  "secrets",
  "egress",
  "workload",
  "logs",
  "cleanup",
] as const;

const DENIES: { condition: string; detail: string }[] = [
  {
    condition: "Unknown outbound destination",
    detail: "No default public egress. Unknown destinations are blocked and written to the attempted-effect ledger.",
  },
  {
    condition: "Unresolved secrets",
    detail: "Missing or production-scoped credentials cannot be replaced with twin identity. The run does not start.",
  },
  {
    condition: "Incomplete cleanup policy",
    detail: "No TTL, no cost ceiling, or no independent teardown path. Convenience does not override containment.",
  },
  {
    condition: "Missing isolation",
    detail: "No dedicated account, project, or strongly isolated network when the risk requires it. The run is blocked.",
  },
  {
    condition: "Direct-IP or production hostname",
    detail: "Bypass attempts and production API hosts are denied by default and flagged as critical.",
  },
  {
    condition: "Failed cleanup",
    detail: "If destruction cannot be proven, the run fails. Orphans are not a background inconvenience.",
  },
];

export function SecurityPage() {
  return (
    <PageShell>
      <PageHero
        path="/security"
        eyebrow="Security"
        title="Fail closed. Data stays in your boundary."
        lead="The twin runs where production data already lives. Unknown egress is blocked. Cleanup is proven. Open source does not bypass compliance."
        visual={
          <Stage className="min-h-[320px]">
            <TrustBoundaryScene />
          </Stage>
        }
      />

      <PageSection tone="sage">
        <PageHeading
          kicker="Trust boundary"
          title="<strong>Hosted control plane. Customer-hosted data plane.</strong> Raw snapshots, secrets, and captured bodies never cross."
        />
        <div className="mt-16 grid grid-cols-2 gap-x-16 max-lg:grid-cols-1 max-lg:gap-y-12">
          <div className="border-l border-gray-new-50 pl-8 max-sm:border-none max-sm:pl-0">
            <div className="mb-5 size-8 rounded-full bg-black max-lg:mb-3.5 max-lg:size-7" />
            <h3 className="text-4xl leading-dense tracking-tighter max-xl:text-[36px] max-lg:text-[28px] max-md:text-[24px]">
              Control plane
            </h3>
            <p className="mt-1.5 max-w-[420px] tracking-extra-tight text-gray-new-40 max-xl:text-sm max-xl:leading-snug">
              Organizations, GitHub, CI, run planning, policy, fleet status, aggregated reports, and
              billing. Evidence, not records.
            </p>
            <Panel className="mt-8 rounded-[12px] p-5">
              <MonoLabel>HOSTED CONTROL PLANE</MonoLabel>
              <Hairline className="my-3" />
              <div className="flex flex-col gap-2">
                {CONTROL_PLANE.map((label) => (
                  <Node key={label} label={label} lit={label === "reports"} />
                ))}
              </div>
              <div className="mt-4">
                <CheckRow ok>no inbound hole</CheckRow>
              </div>
            </Panel>
          </div>
          <div className="border-l border-gray-new-50 pl-8 max-sm:border-none max-sm:pl-0">
            <div className="mb-5 size-8 rounded-full bg-black max-lg:mb-3.5 max-lg:size-7" />
            <h3 className="text-4xl leading-dense tracking-tighter max-xl:text-[36px] max-lg:text-[28px] max-md:text-[24px]">
              Data plane
            </h3>
            <p className="mt-1.5 max-w-[420px] tracking-extra-tight text-gray-new-40 max-xl:text-sm max-xl:leading-snug">
              Discovery, snapshots, sanitization, provisioning, secret replacement, egress, workloads,
              logs, and cleanup. Production-derived state stays here.
            </p>
            <Panel className="mt-8 rounded-[12px] p-5">
              <MonoLabel>CUSTOMER-HOSTED DATA PLANE</MonoLabel>
              <Hairline className="my-3" />
              <div className="flex flex-col gap-2">
                {DATA_PLANE.map((label) => (
                  <Node key={label} label={label} lit={label === "cleanup"} />
                ))}
              </div>
              <div className="mt-4">
                <CheckRow ok>outbound-only mTLS</CheckRow>
              </div>
            </Panel>
          </div>
        </div>
        <div className="mt-8 flex items-center gap-4">
          <div className="min-w-0 flex-1">
            <Hairline />
          </div>
          <MonoLabel>trust boundary · short-lived mTLS · control plane receives evidence</MonoLabel>
          <div className="min-w-0 flex-1 max-md:hidden">
            <Hairline />
          </div>
        </div>
      </PageSection>

      <PageSection>
        <PageHeading
          kicker="Fail closed"
          title="<strong>Designed denies.</strong> Unknown, unresolved, or uncontained is a block — not a warning you can click through."
        />
        <ul className="mt-14 overflow-hidden rounded-[12px] ring-1 ring-black/10">
          {DENIES.map((row) => (
            <li
              key={row.condition}
              className="flex items-start justify-between gap-8 border-b border-black/8 px-6 py-5 last:border-0 max-md:flex-col max-md:gap-3 max-md:px-5"
            >
              <div className="min-w-0">
                <h3 className="text-[18px] leading-snug tracking-extra-tight text-black">{row.condition}</h3>
                <p className="mt-1.5 max-w-[640px] text-[14px] leading-6 tracking-extra-tight text-gray-new-40">
                  {row.detail}
                </p>
              </div>
              <StatusPill tone="BLOCK" className="mt-1 shrink-0" />
            </li>
          ))}
        </ul>
      </PageSection>

      <PageSection tone="sage">
        <Split
          visual={
            <Panel className="rounded-[12px] p-6">
              <div className="flex items-center justify-between gap-4">
                <MonoLabel>cleanup proof · run_08f2</MonoLabel>
                <Timestamp value="00:00:00" />
              </div>
              <Hairline className="my-4" />
              <Receipt>
                <div className="flex items-center justify-between gap-3">
                  <span>destroyed</span>
                  <Ticker value="14/14" className="text-[12px]" />
                </div>
                <div className="mt-1 flex items-center justify-between gap-3">
                  <span>orphans</span>
                  <span>0</span>
                </div>
              </Receipt>
              <div className="mt-5 flex flex-col gap-2">
                <CheckRow ok>TTL expired independently of the orchestrator</CheckRow>
                <CheckRow ok>cost ceiling held</CheckRow>
                <CheckRow ok>unowned resources rejected at admission</CheckRow>
                <CheckRow ok>destruction record signed</CheckRow>
              </div>
            </Panel>
          }
        >
          <PageHeading title="<strong>Cleanup is a safety property,</strong> not a best-effort script." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Every environment has a TTL, a hard cost ceiling, an independent reaper, and a verifiable
            destruction record. Resource tags carry the run ID and expiration. Failed teardown fails the
            run.
          </p>
        </Split>
      </PageSection>

      <PageSection>
        <Callout label="What we will not claim">
          Open source does not bypass compliance. Inspectability of the data plane is not a substitute
          for customer-managed keys, residency, audit, or a published threat model. Safety is not an
          enterprise add-on — the open product must still be safe.
        </Callout>
      </PageSection>

      <RelatedGrid
        items={[
          { href: "/product/architecture", title: "Architecture", description: "Control plane vs data plane." },
          { href: "/product/firewall", title: "Firewall", description: "How side effects are contained." },
          { href: "/privacy", title: "Privacy", description: "What we collect and what we never take." },
        ]}
      />
    </PageShell>
  );
}
