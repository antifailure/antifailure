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

/**
 * The lifecycle events one run emits, which is the only sequence a reader can
 * check.
 *
 * This panel listed thirteen state names, REQUESTED through DESTROYED. They
 * existed in this file and in no other file in the repository: no state
 * machine in the engine, no column in the control plane, no string in any Go
 * or SQL source. A rail of invented names is worse than no rail, because it is
 * precise enough to be trusted and there is nothing behind it.
 *
 * The first correction here was the six values of the `environment_state` enum
 * in `web/packages/db/migrations/0001_init.sql`, and that was still one level
 * of the same mistake. Two of those six cannot be reached: `queued` has no
 * mapping in the engine's own `controlplane.typeMap` at all, and `sleeping`
 * has one for `events.EnvSleeping`, which is declared, described, and emitted
 * by nothing. So the enum has six values and an environment can be observed in
 * four of them.
 *
 * These five are the lifecycle events `internal/env/env.go` actually emits,
 * named by the type strings that appear in the NDJSON log. Somebody can run
 * `af up` and `af down` and watch each one arrive, which is what the thirteen
 * names never offered.
 */
const LIFECYCLE_STATES = [
  "env.creating",
  "env.ready",
  "env.destroying",
  "env.destroyed",
  "env.failed",
] as const;

type LifecycleState = (typeof LIFECYCLE_STATES)[number];

/**
 * The two events a run stops on, drawn at the end rather than mid-rail.
 *
 * The rail is a progression, so anything sitting in the middle of it reads as
 * a step every run takes. `env.failed` is not: it is emitted from any point
 * before it, by the deferred handler that covers every return in `Up`.
 */
const TERMINAL_STATES = new Set<LifecycleState>(["env.destroyed", "env.failed"]);

/**
 * The four phases of one run, and deliberately no identifier under each.
 *
 * The phases used to carry a from-state and a to-state drawn from the thirteen
 * invented names above. Replacing those with an event type per phase was the
 * obvious fix and it was wrong: three of the four had an emitter and the Run
 * phase's did not. `events.AgentStarted`, `AgentStep`, `AgentFinished` and
 * `AgentVerdict` are declared in the catalog, described there, mapped into the
 * control plane's vocabulary, and emitted by nothing, so labelling this phase
 * `agent.verdict` would have put a fourth invented identifier on the page
 * while removing thirteen.
 *
 * The phases are a true description of the work one run does, in order. That
 * is what they say now, with nothing under them pretending to be a key
 * somebody can filter on.
 */
const PHASES = [
  { name: "Plan", note: "Read the manifest, take the environment lock, write the plan." },
  {
    name: "Provision",
    note: "Build candidate, restore safe state, replace credentials, verify containment.",
  },
  { name: "Run", note: "Agents drive the declared workflows. Invariants are asked of the data." },
  {
    name: "Close",
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
    // AF-RUN-045 was the node here, and that code is emitted only by the
    // Kubernetes runtime, in internal/runtime/k8s/lifecycle.go. Docker has no
    // namespaces and never raises it. The property does hold on Docker, by a
    // different mechanism: every container, network and volume is labelled
    // with its environment before it is created, and Down lists by that label
    // filter, so a teardown cannot reach anything another run made. That is
    // the behaviour the conformance suite proves, so it is the one named here.
    kicker: "teardown",
    title: "Touches only what it made",
    body: "Teardown lists by this run's own label, so tearing one environment down leaves every other one running.",
    node: "Down_TouchesOnlyItsOwnEnvironment",
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
  const [namespace, event] = name.split(".");
  return [namespace + ".", event];
}

export function TwinsPage() {
  return (
    <PageShell>
      <PageHero
        path="/product/twins"
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
          These are the lifecycle events one run emits, so a reader can run{" "}
          <code className="font-mono text-[12px] text-black/70">af up</code> and{" "}
          <code className="font-mono text-[12px] text-black/70">af down</code> and watch each of
          them arrive in the log. The last two are where a run stops: it was torn down, or it
          failed, which is emitted from any point before it. The run identifier is invented.
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
          <Node label="internal/env" lit />
          <Node label="idempotent" lit />
          <StatusPill tone="PASS">env.destroyed</StatusPill>
        </div>
      </div>
      <Hairline />
      <div className="relative overflow-x-auto">
        <div className="pointer-events-none absolute inset-y-0 right-0 z-10 w-8 bg-gradient-to-l from-white xl:hidden" />
        <ol className="flex min-w-[540px] px-3 pt-6 pb-4">
          {LIFECYCLE_STATES.map((name, i) => {
            const terminal = TERMINAL_STATES.has(name);
            const bar =
              name === "env.destroyed" ? "#33bf00" : name === "env.failed" ? "#D94841" : "#CAE6D9";
            return (
              <li key={name} className="flex min-w-0 flex-1 flex-col px-0.5">
                <div className="relative h-1 w-full bg-black/[0.08]">
                  <div className="absolute inset-y-0 left-0 w-full" style={{ background: bar }} />
                </div>
                <div
                  className={`mt-2 text-center font-mono text-[11px] leading-[14px] tracking-extra-tight uppercase ${
                    terminal ? "text-black" : "text-black/55"
                  }`}
                >
                  {railLabel(name).map((line) => (
                    <div key={line}>{line}</div>
                  ))}
                </div>
                <div
                  className={`mx-auto mt-1.5 rounded-full ${terminal ? "size-1.5" : "size-1 bg-black/20"}`}
                  style={terminal ? { background: bar } : undefined}
                />
                <span className="sr-only">
                  {terminal ? "a run stops here. " : ""}
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
            <p className="mt-3 max-w-[280px] text-[13px] leading-5 tracking-extra-tight text-gray-new-40">
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
