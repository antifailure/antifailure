import { PageHeading, PageHero, PageSection, PageShell, RelatedGrid, Split } from "@/components/pages/kit";
import { Illustrative } from "@/components/layout/Illustrative";
import { PTW01, PTW02, PTW03, PTW04, PTW05 } from "@/components/pages/figures/product-twins";
import { MonoLabel, Node } from "@/components/home/visuals/primitives";

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

export function TwinsPage() {
  return (
    <PageShell>
      <PageHero
        path="/product/twins"
        eyebrow="Twin Orchestrator"
        title="An isolated environment for every change."
        lead="Run your new build with a masked database and its own network. Antifailure tracks the resources it creates and removes them after testing."
        framed={false}
        visual={<PTW01 />}
      />

      <PageSection>
        <Split visual={<PTW02 />}>
          <PageHeading
            kicker="Lifecycle"
            title="<strong>From repository to test result.</strong>"
          />
          <ul className="mt-10 grid grid-cols-2 gap-x-7 gap-y-7 max-md:grid-cols-1">
            {PHASES.map((phase) => (
              <li key={phase.name} className="min-w-0 border-t border-black/12 pt-3">
                <MonoLabel tone="reader" className="uppercase tracking-[0.14em]">{phase.name}</MonoLabel>
                <p className="mt-2 max-w-[280px] text-[13px] leading-5 tracking-extra-tight text-gray-new-40">{phase.note}</p>
              </li>
            ))}
          </ul>
        </Split>
        <Illustrative>
            Example lifecycle log.
          </Illustrative>
      </PageSection>

      <PageSection tone="ruled">
        <Split visual={<PTW03 items={ISOLATION} />}>
          <PageHeading
            title="<strong>Keep each run separate from production.</strong>"
          />
          <p className="mt-6 max-w-[640px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Each environment has its own credentials, network, and resource ownership. See the runtime guides for provider support and verification status.
          </p>
        </Split>
      </PageSection>

      <PageSection tone="panel">
        <Split visual={<PTW04 />}>
          <PageHeading title="<strong>A test result you can review on the pull request.</strong>" />
          <p className="mt-6 max-w-[520px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            The output is a pass or a fail on the pull request, with the rows and the trace behind
            it.
          </p>
          <div className="mt-8 flex flex-wrap items-center gap-x-5 gap-y-2">
            <Node label="af up" lit />
            <Node label="af ci" lit />
            <Node label="af down" lit />
          </div>
        </Split>
      </PageSection>

      <PageSection>
        <Split visual={<PTW05 />}>
          <PageHeading title="<strong>Remove the environment when the run ends.</strong>" />
          <p className="mt-6 max-w-[520px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Antifailure records resources as it creates them. Teardown uses that journal to remove them, even after an interrupted run, and reports what was deleted.
          </p>
          <Illustrative className="mt-8">
            Example teardown log with sample resource names and timestamps.
          </Illustrative>
          <div className="mt-8 grid grid-cols-2 gap-x-8 gap-y-5 max-xl:grid-cols-1">
            <div>
              <MonoLabel tone="reader" className="uppercase tracking-[0.14em]">sweep</MonoLabel>
              <p className="mt-1.5 text-[15px] leading-6 tracking-extra-tight text-black">
                af env prune removes environments older than a cutoff you pass.
              </p>
            </div>
            <div>
              <MonoLabel tone="reader" className="uppercase tracking-[0.14em]">limit</MonoLabel>
              <p className="mt-1.5 text-[15px] leading-6 tracking-extra-tight text-black">
                Environments expire after 24 hours by default. Schedule af env reap to remove expired environments, or run it manually.
              </p>
            </div>
          </div>
        </Split>
      </PageSection>


      <RelatedGrid
        items={[
          { href: "/product/safe-state", title: "Safe State", description: "What gets restored into the twin." },
          { href: "/product/firewall", title: "Side-Effect Firewall", description: "How side effects are contained." },
          { href: "/product/load", title: "Load", description: "Traffic shaped like production's access log." },
        ]}
      />
    </PageShell>
  );
}
