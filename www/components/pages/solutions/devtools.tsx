import { PageShell, RelatedGrid } from "@/components/pages/kit";
import { FeatureRow, SplitHero } from "./well";
import {
  ExpandContractColumns,
  LockHoldStrip,
  LockWaitChain,
  QueryPlanTree,
} from "./devtools-plates";

export function DevtoolsPage() {
  return (
    <PageShell>
      <SplitHero
        path="/solutions/devtools"
        eyebrow="Solutions · Developer tools"
        title="Schema changes on large tables."
        paragraphs={[
          "The flagship wedge, felt first by teams whose users notice p99 immediately.",
          "Measure the strongest lock held per table, how long it was held, whether another session was left waiting on it, and how the query plans moved.",
          "Start with Postgres volume, plans, and pools, then expand.",
        ]}
        visual={<QueryPlanTree />}
      />

      <FeatureRow
        stack
        kicker="Users notice p99 immediately"
        title="Large tables plus frequent schema change."
        items={[
          { title: "Large tables", body: "Exclusive locks and rewrites that never show up on a laptop database." },
          { title: "Query plans", body: "Plan regressions under production-shaped volume." },
          { title: "Pools", body: "Connection-pool exhaustion during migrate-and-serve." },
        ]}
        visual={<LockWaitChain />}
      />

      <FeatureRow
        reverse
        kicker="Narrow adapters, complete stack"
        title="Exceptional Postgres instrumentation first."
        items={[
          { title: "The first supported stack should be exceptional", body: "A broad compatibility list with unreliable connectors would destroy trust." },
          { title: "Postgres first", body: "Volume, plans, and pools, then expand." },
          { title: "Publish what the twin reproduced", body: "Do not pretend unsupported components are cloned." },
        ]}
        visual={<ExpandContractColumns />}
      />

      <FeatureRow
        kicker="The wedge"
        title="Locks, plans, and rollback feasibility before it ships."
        items={[
          { title: "Lock duration", body: "The strongest mode held per table, how long it was held, and whether another session waited on it." },
          { title: "Schema coexistence", body: "Whether old instances can still read the new schema shows up here first." },
          { title: "Users notice p99 immediately", body: "Large tables plus frequent schema change." },
        ]}
        visual={<LockHoldStrip />}
      />

      <RelatedGrid
        items={[
          { href: "/product/migrations", title: "Migration Safety", description: "Locks, rewrites, plans, lint." },
          { href: "/product/load", title: "Load", description: "Production's own route mix against the branch." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}
