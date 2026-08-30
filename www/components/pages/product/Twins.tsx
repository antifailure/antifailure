import {
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  RelatedGrid,
  Split,
} from "@/components/pages/kit";
import { Illustrative } from "@/components/layout/Illustrative";
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
    note: "Read the manifest, take the environment lock, write the plan.",
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
    note: "Agents drive the declared workflows. Invariants are asked of the data.",
  },
  {
    name: "Close",
    from: "REPORTING" as const,
    to: "DESTROYED" as const,
    note: "Evidence attaches to the pull request. The journal is replayed in reverse.",
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
    kicker: "ownership",
    title: "One label scheme, on everything",
    body: "Every container, network and volume carries the run that made it.",
    node: "run_08f2",
  },
  {
    kicker: "teardown",
    title: "Refuses what it did not create",
    body: "Teardown will not destroy a namespace this run did not provision. It errors instead.",
    node: "AF-RUN-045",
  },
] as const;

const JOURNAL = [
  { id: "workers-08f2", at: "16:35" },
  { id: "app-08f2", at: "16:44" },
  { id: "sim-stripe", at: "16:53" },
  { id: "dns-clone", at: "17:02" },
  { id: "postgres-sub", at: "17:12" },
  { id: "vpc-iso", at: "17:21" },
  { id: "proxy-08f2", at: "17:30" },
] as const;

function railLabel(name: LifecycleState): string[] {
  if (name === "VERIFYING_CONTAINMENT") return ["VERIFYING", "CONTAINMENT"];
  if (name === "BASELINE_RUNNING") return ["BASELINE", "RUNNING"];
  if (name === "CANDIDATE_RUNNING") return ["CANDIDATE", "RUNNING"];
  return [name];
}

export function TwinsPage() {
  return (
    <PageShell inset>
      <PageHero
        eyebrow="Twin Orchestrator"
        title="A disposable production twin for every risky change."
        lead="Build the change, branch a sanitized database, isolate the network, replace production credentials, journal every resource as it comes up, and tear all of it down when the report is done."
        visual={<TwinLifecycleScene />}
      />

      <PageSection>
        <PageHeading
          kicker="Lifecycle"
          title="<strong>Every transition is idempotent and recoverable.</strong> A resource is journaled the moment it exists, not after the run succeeds."
        />
        <LifecycleRail />
        <Illustrative>
          The thirteen states and the four phases are the ones the orchestrator moves through. The
          run identifier is invented.
        </Illustrative>
      </PageSection>

      <PageSection tone="white">
        <PageHeading
          title="<strong>Isolation is a spec, not a hope.</strong> An unresolved secret fails closed and stops the run."
        />
        <p className="mt-6 max-w-[640px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
          These seven are in force today on the Docker runtime, which passes all thirty-two runtime
          conformance behaviours against a real daemon. The Kubernetes runtime is written and not yet
          proven to the same standard, and no cloud runtime exists. Convenience must not silently
          override containment.
        </p>
        <IsolationSpec />
      </PageSection>

      <PageSection tone="sage">
        <Split
          visual={
            <Panel className="rounded-[12px] bg-white p-6">
              <div className="flex items-center justify-between gap-3">
                <MonoLabel className="uppercase tracking-[0.14em]">where a run answers</MonoLabel>
                <StatusPill tone="PASS">local</StatusPill>
              </div>
              <div className="mt-4 flex items-center gap-2 border border-black/[0.08] bg-white px-3 py-2.5">
                <span className="size-2 rounded-full bg-[#33bf00]" aria-hidden />
                <span className="min-w-0 truncate font-mono text-[13px] tracking-extra-tight text-black tabular-nums">
                  http://127.0.0.1:46000
                </span>
              </div>
              <Hairline className="mt-5" />
              <p className="mt-4 font-mono text-[11px] leading-5 tracking-extra-tight text-black/50">
                A run serves on a loopback port on the machine that made it, which is what af up
                prints. There is no hosted preview hostname and no certificate for one; the
                per-environment certificate authority exists so the egress sidecar can terminate TLS,
                not to publish an address.
              </p>
            </Panel>
          }
        >
          <PageHeading title="<strong>A preview URL is not the product.</strong> The twin exists to answer whether the deployment is safe, then it is destroyed." />
          <p className="mt-6 max-w-[520px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            The output is a pass or a fail on the pull request, with the rows and the trace behind
            it. Not a dataset, and not an address somebody has to remember to shut down.
          </p>
          <div className="mt-8 flex flex-wrap items-center gap-x-5 gap-y-2">
            <Node label="af up" lit />
            <Node label="af ci" lit />
            <Node label="af down" lit />
          </div>
        </Split>
      </PageSection>

      <PageSection>
        <Split
          visual={
            <Panel className="rounded-[12px] bg-white">
              <div className="flex items-center justify-between gap-3 px-5 py-3">
                <MonoLabel className="uppercase tracking-[0.14em]">journal replay</MonoLabel>
                <StatusPill tone="PASS">counted</StatusPill>
              </div>
              <Hairline />
              <div className="flex flex-wrap items-center gap-x-4 gap-y-2 px-5 py-3">
                <Node label="af down" lit />
                <Node label="reverse order" lit />
                <MonoLabel className="ml-auto tabular-nums">14 removed</MonoLabel>
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
                <MonoLabel className="tabular-nums text-black/70">14 removed</MonoLabel>
                <MonoLabel className="tabular-nums">0 left behind</MonoLabel>
                <MonoLabel>production never in the path</MonoLabel>
              </div>
            </Panel>
          }
        >
          <PageHeading title="<strong>Cleanup is a first-class safety property.</strong> Resource deletion is not a background convenience." />
          <p className="mt-6 max-w-[520px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Every resource is written to the journal as it is created, so a run that dies halfway
            still has a list of what it made. Teardown replays that journal in reverse and counts
            what it removed. A continuous integration step counts the managed containers and networks
            afterwards and fails the build if any are left.
          </p>
          <Illustrative className="mt-8">
            A teardown of one run. The journal, the reverse replay and the count of what was removed
            are real; the resource names and the timestamps are written.
          </Illustrative>
          <div className="mt-8 grid grid-cols-2 gap-x-8 gap-y-5 max-xl:grid-cols-1">
            <div>
              <MonoLabel className="uppercase tracking-[0.14em]">sweep</MonoLabel>
              <p className="mt-1.5 text-[15px] leading-6 tracking-extra-tight text-black">
                af env prune removes environments older than a cutoff you pass.
              </p>
            </div>
            <div>
              <MonoLabel className="uppercase tracking-[0.14em]">limit</MonoLabel>
              <p className="mt-1.5 text-[15px] leading-6 tracking-extra-tight text-black">
                There is no automatic time-to-live and no independent reaper yet. The sweep is a
                command a person or a schedule runs.
              </p>
            </div>
          </div>
        </Split>
      </PageSection>

      <RelatedGrid
        items={[
          { href: "/product/safe-state", title: "Safe State", description: "What gets restored into the twin." },
          { href: "/product/architecture", title: "Architecture", description: "Customer-hosted data plane." },
          { href: "/product/report", title: "Safety Report", description: "What the run says it could not measure." },
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
        <div className="pointer-events-none absolute inset-y-0 right-0 z-10 w-8 bg-gradient-to-l from-white xl:hidden" />
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
      <div className="grid grid-cols-4 max-xl:grid-cols-2 max-md:grid-cols-1">
        {PHASES.map((phase, i) => (
          <div key={phase.name} className="relative px-5 py-5">
            {i < PHASES.length - 1 ? (
              <Hairline vertical className="absolute top-5 right-0 bottom-5 hidden h-auto xl:block" />
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
        <div className="flex min-w-0 items-center gap-4">
          <MonoLabel className="uppercase tracking-[0.14em]">isolation model</MonoLabel>
          <Node label="run_08f2" lit />
        </div>
        <StatusPill tone="FAIL">fail closed</StatusPill>
      </div>
      <Hairline />
      {/* Four across, not five. The list lost three claims that were not built
          and seven do not divide by five without leaving a row three cells
          short. */}
      <div className="grid grid-cols-4 max-xl:grid-cols-2 max-md:grid-cols-1">
        {ISOLATION.map((item, i) => {
          const lastInRowXl = (i + 1) % 4 === 0 || i === ISOLATION.length - 1;
          return (
            <div
              key={item.title}
              className="relative px-5 py-6 max-md:border-t max-md:border-black/10 max-md:first:border-t-0 md:max-xl:border-black/10 md:max-xl:[&:nth-child(n+3)]:border-t xl:border-black/10 xl:[&:nth-child(n+5)]:border-t"
            >
              {!lastInRowXl ? (
                <Hairline vertical className="absolute top-5 right-0 bottom-5 hidden h-auto xl:block" />
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
