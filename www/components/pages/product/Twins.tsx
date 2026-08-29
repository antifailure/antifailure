import {
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  RelatedGrid,
  Split,
} from "@/components/pages/kit";
import { TwinLifecycleScene } from "@/components/home/visuals/TwinLifecycleScene";
import { Hairline, MonoLabel, Node, Panel, StatusPill } from "@/components/home/visuals/primitives";

const LIFECYCLE_STATES = [
  "REQUESTED",
  "PLANNED",
  "PROVISIONING",
  "SANITIZING",
  "DEPLOYING",
  "VERIFYING_CONTAINMENT",
  "READY",
  "BASELINE_RUNNING",
  "CANDIDATE_RUNNING",
  "ANALYZING",
  "REPORTING",
  "DESTROYING",
  "DESTROYED",
] as const;

type LifecycleState = (typeof LIFECYCLE_STATES)[number];

const PHASES = [
  {
    name: "Plan",
    from: "REQUESTED" as const,
    to: "PLANNED" as const,
    note: "Change intelligence assigns fidelity and a validation plan.",
  },
  {
    name: "Provision",
    from: "PROVISIONING" as const,
    to: "VERIFYING_CONTAINMENT" as const,
    note: "Build candidate, restore safe state, replace credentials, verify containment.",
  },
  {
    name: "Run",
    from: "READY" as const,
    to: "ANALYZING" as const,
    note: "Baseline and candidate receive equivalent state and behavior.",
  },
  {
    name: "Close",
    from: "REPORTING" as const,
    to: "DESTROYED" as const,
    note: "Evidence attaches to the pull request. Cleanup is attested.",
  },
] as const;

const ISOLATION = [
  {
    kicker: "credentials",
    title: "No production write credentials",
    body: "Production secrets are replaced. The twin cannot reach live keys.",
    node: "creds replaced",
  },
  {
    kicker: "database",
    title: "No production database route",
    body: "The production path is cut. Twin traffic to prod-db is zero.",
    node: "prod-db cut",
  },
  {
    kicker: "network",
    title: "No default internet route",
    body: "No public egress unless an explicit policy allows a simulator or read-only fixture.",
    node: "egress deny-by-default",
  },
  {
    kicker: "dns",
    title: "Separate DNS policy",
    body: "Clone-local DNS. Production hostnames do not resolve to production.",
    node: "dns clone-local",
  },
  {
    kicker: "secrets",
    title: "Separate secrets namespace",
    body: "Twin-scoped secrets. Unresolved secrets fail closed and block the run.",
    node: "namespace twin-scoped",
  },
  {
    kicker: "identity",
    title: "Temporary workload identity",
    body: "Short-lived identity for the run. Never a standing production role.",
    node: "identity ephemeral",
  },
  {
    kicker: "ownership",
    title: "Resource tags: run ID and expiration",
    body: "Every resource is tagged with the run and TTL. Unowned resources are rejected.",
    node: "run_08f2 · expires",
  },
  {
    kicker: "budget",
    title: "Hard cost ceiling",
    body: "Estimate before provision. Per-run spending cap, not a hope.",
    node: "ceiling $25",
  },
  {
    kicker: "admission",
    title: "Reject unowned resources",
    body: "Admission policy refuses anything the orchestrator did not create.",
    node: "admission enforce",
  },
  {
    kicker: "reaper",
    title: "Independent cleanup controller",
    body: "A TTL reaper operates separately from the main orchestrator.",
    node: "reaper independent",
  },
] as const;

const JOURNAL = [
  { id: "workers-08f2", at: "16:35" },
  { id: "app-08f2", at: "16:44" },
  { id: "sim-stripe", at: "16:53" },
  { id: "dns-clone", at: "17:02" },
  { id: "postgres-sub", at: "17:12" },
  { id: "vpc-iso", at: "17:21" },
  { id: "cert-preview", at: "17:30" },
] as const;

function railLabel(name: LifecycleState): string[] {
  if (name === "VERIFYING_CONTAINMENT") return ["VERIFYING", "CONTAINMENT"];
  if (name === "BASELINE_RUNNING") return ["BASELINE", "RUNNING"];
  if (name === "CANDIDATE_RUNNING") return ["CANDIDATE", "RUNNING"];
  return [name];
}

export function TwinsPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Twin Orchestrator"
        title="A disposable production twin for every risky change."
        lead="Build the candidate, deploy a baseline for comparison, isolate the network, inject clone-specific configuration, replace production credentials, and tear everything down when the report is done."
        visual={<TwinLifecycleScene />}
      />

      <PageSection>
        <PageHeading
          kicker="Lifecycle"
          title="<strong>Every transition is idempotent and recoverable.</strong> A TTL reaper runs independently of the orchestrator."
        />
        <LifecycleRail />
      </PageSection>

      <PageSection tone="white">
        <PageHeading
          title="<strong>Isolation is a spec, not a hope.</strong> Missing containment fails closed and blocks the run."
        />
        <p className="mt-6 max-w-[640px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          For serious customers the clone uses a dedicated account, subscription, project, or a strongly
          isolated network boundary. Convenience must not silently override containment.
        </p>
        <IsolationSpec />
      </PageSection>

      <PageSection tone="sage">
        <Split
          visual={
            <Panel className="rounded-[12px] bg-white p-6">
              <div className="flex items-center justify-between gap-3">
                <MonoLabel className="uppercase tracking-[0.14em]">authenticated preview</MonoLabel>
                <StatusPill tone="PASS">private</StatusPill>
              </div>
              <div className="mt-4 flex items-center gap-2 border border-black/[0.08] bg-white px-3 py-2.5">
                <span className="size-2 rounded-full bg-[#33bf00]" aria-hidden />
                <span className="min-w-0 truncate font-mono text-[13px] tracking-extra-tight text-black tabular-nums">
                  fix-billing-184.preview.company.com
                </span>
              </div>
              <Hairline className="mt-5" />
              <p className="mt-4 font-mono text-[11px] leading-5 tracking-extra-tight text-black/50">
                Protected by short-lived access tokens, an identity-aware proxy, or the customer’s existing
                access system. Credentials are never shared across agent sessions unless they represent an
                intentionally reusable synthetic identity.
              </p>
            </Panel>
          }
        >
          <PageHeading title="<strong>A preview URL is not the product.</strong> The twin exists to answer whether the deployment is safe, then it is destroyed." />
          <p className="mt-6 max-w-[520px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Each twin may receive an authenticated endpoint. Private by default. The output is an
            evidence-backed pass, warning, or block — not a dataset, and not a preview URL alone.
          </p>
          <div className="mt-8 flex flex-wrap items-center gap-x-5 gap-y-2">
            <Node label="Create Wind Tunnel" lit />
            <MonoLabel>adapter and fidelity chosen for you</MonoLabel>
          </div>
        </Split>
      </PageSection>

      <PageSection>
        <Split
          reverse
          visual={
            <Panel className="rounded-[12px] bg-white">
              <div className="flex items-center justify-between gap-3 px-5 py-3">
                <MonoLabel className="uppercase tracking-[0.14em]">cleanup controller</MonoLabel>
                <StatusPill tone="PASS">attested</StatusPill>
              </div>
              <Hairline />
              <div className="flex flex-wrap items-center gap-x-4 gap-y-2 px-5 py-3">
                <Node label="TTL reaper" lit />
                <Node label="independent of orchestrator" lit />
                <MonoLabel className="ml-auto tabular-nums">cost $6.14 / $25.00</MonoLabel>
              </div>
              <Hairline />
              <ul className="px-5 py-3">
                {JOURNAL.map((row) => (
                  <li
                    key={row.id}
                    className="flex items-baseline justify-between gap-3 border-b border-black/[0.06] py-1.5 last:border-0"
                  >
                    <span className="font-mono text-[11px] tracking-extra-tight text-black/70">
                      destroyed · {row.id}
                    </span>
                    <MonoLabel className="tabular-nums">t+{row.at}</MonoLabel>
                  </li>
                ))}
              </ul>
              <Hairline />
              <div className="flex flex-wrap items-center gap-x-4 gap-y-1 px-5 py-3">
                <MonoLabel className="tabular-nums text-black/70">14/14 destroyed</MonoLabel>
                <MonoLabel className="tabular-nums">0 orphans</MonoLabel>
                <MonoLabel>production untouched</MonoLabel>
              </div>
            </Panel>
          }
        >
          <PageHeading title="<strong>Cleanup is a first-class safety property.</strong> Resource deletion is not a background convenience." />
          <p className="mt-6 max-w-[520px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Every environment has a TTL, a cost ceiling, an independent cleanup path, and a verifiable
            destruction record. Incomplete cleanup policies fail closed. Nothing outlives the run.
          </p>
          <div className="mt-8 grid grid-cols-2 gap-x-8 gap-y-5 max-sm:grid-cols-1">
            <div>
              <MonoLabel className="uppercase tracking-[0.14em]">ttl</MonoLabel>
              <p className="mt-1.5 text-[15px] leading-6 tracking-extra-tight text-black">
                Inactivity expiration. A separate reaper, not the orchestrator.
              </p>
            </div>
            <div>
              <MonoLabel className="uppercase tracking-[0.14em]">proof</MonoLabel>
              <p className="mt-1.5 text-[15px] leading-6 tracking-extra-tight text-black">
                Journaled destruction. Zero orphans. Production never in the path.
              </p>
            </div>
          </div>
        </Split>
      </PageSection>

      <RelatedGrid
        items={[
          { href: "/product/safe-state", title: "Safe State", description: "What gets restored into the twin." },
          { href: "/product/architecture", title: "Architecture", description: "Customer-hosted data plane." },
          { href: "/product/fidelity", title: "Fidelity Graph", description: "What the twin actually reproduced." },
        ]}
      />
    </PageShell>
  );
}

function LifecycleRail() {
  return (
    <Panel className="mt-14 rounded-[12px] bg-white max-md:mt-10">
      <div className="flex flex-wrap items-center justify-between gap-3 px-5 py-3">
        <MonoLabel className="uppercase tracking-[0.14em]">environment lifecycle</MonoLabel>
        <div className="flex flex-wrap items-center gap-4">
          <Node label="13 states" lit />
          <Node label="idempotent" lit />
          <StatusPill tone="PASS">DESTROYED</StatusPill>
        </div>
      </div>
      <Hairline />
      <div className="relative overflow-x-auto">
        <div className="pointer-events-none absolute inset-y-0 right-0 z-10 w-8 bg-gradient-to-l from-white md:hidden" />
        <ol className="flex min-w-[920px] px-3 pt-6 pb-4">
          {LIFECYCLE_STATES.map((name, i) => {
            const terminal = name === "DESTROYED";
            return (
              <li key={name} className="flex min-w-0 flex-1 flex-col px-0.5">
                <div className="relative h-1 w-full bg-black/[0.08]">
                  <div
                    className="absolute inset-y-0 left-0 w-full"
                    style={{ background: terminal ? "#33bf00" : "#CAE6D9" }}
                  />
                </div>
                <div
                  className={`mt-2 text-center font-mono text-[8px] leading-[10px] tracking-extra-tight uppercase ${
                    terminal ? "text-black" : "text-black/50"
                  }`}
                >
                  {railLabel(name).map((line) => (
                    <div key={line}>{line}</div>
                  ))}
                </div>
                {terminal ? (
                  <div className="mx-auto mt-1.5 size-1.5 rounded-full bg-[#33bf00]" />
                ) : (
                  <div className="mx-auto mt-1.5 size-1 rounded-full bg-black/20" />
                )}
                <span className="sr-only">
                  {i + 1} of {LIFECYCLE_STATES.length}
                </span>
              </li>
            );
          })}
        </ol>
      </div>
      <Hairline />
      <div className="grid grid-cols-4 max-lg:grid-cols-2 max-md:grid-cols-1">
        {PHASES.map((phase, i) => (
          <div key={phase.name} className="relative px-5 py-5">
            {i < PHASES.length - 1 ? (
              <Hairline vertical className="absolute top-5 right-0 bottom-5 hidden h-auto lg:block" />
            ) : null}
            <MonoLabel className="uppercase tracking-[0.14em]">{phase.name}</MonoLabel>
            <div className="mt-2 font-mono text-[11px] tracking-extra-tight text-black tabular-nums">
              {phase.from}
              <span className="text-black/30"> → </span>
              {phase.to}
            </div>
            <p className="mt-2 max-w-[280px] text-[13px] leading-5 tracking-extra-tight text-gray-new-40">
              {phase.note}
            </p>
          </div>
        ))}
      </div>
    </Panel>
  );
}

function IsolationSpec() {
  return (
    <Panel className="mt-14 rounded-[12px] bg-white max-md:mt-10">
      <div className="flex flex-wrap items-center justify-between gap-3 px-5 py-3">
        <div className="flex items-center gap-4">
          <MonoLabel className="uppercase tracking-[0.14em]">isolation model</MonoLabel>
          <Node label="run_08f2" lit />
        </div>
        <StatusPill tone="BLOCK">fail closed</StatusPill>
      </div>
      <Hairline />
      <div className="grid grid-cols-5 max-xl:grid-cols-3 max-md:grid-cols-1">
        {ISOLATION.map((item, i) => {
          const lastInRowXl = (i + 1) % 5 === 0 || i === ISOLATION.length - 1;
          const lastInRowLg = (i + 1) % 3 === 0 || i === ISOLATION.length - 1;
          return (
            <div
              key={item.title}
              className="relative px-5 py-6 max-xl:border-black/10 max-md:border-t md:max-xl:[&:nth-child(n+4)]:border-t xl:[&:nth-child(n+6)]:border-t xl:border-black/10"
            >
              {!lastInRowXl ? (
                <Hairline vertical className="absolute top-5 right-0 bottom-5 hidden h-auto xl:block" />
              ) : null}
              {!lastInRowLg ? (
                <Hairline vertical className="absolute top-5 right-0 bottom-5 hidden h-auto max-xl:block max-md:hidden" />
              ) : null}
              <MonoLabel className="uppercase tracking-[0.14em]">{item.kicker}</MonoLabel>
              <h3 className="mt-3 text-[16px] leading-snug tracking-extra-tight text-black">{item.title}</h3>
              <p className="mt-2 max-w-[240px] text-[13px] leading-5 tracking-extra-tight text-gray-new-40">
                {item.body}
              </p>
              <div className="mt-4">
                <Node label={item.node} lit />
              </div>
            </div>
          );
        })}
      </div>
    </Panel>
  );
}
