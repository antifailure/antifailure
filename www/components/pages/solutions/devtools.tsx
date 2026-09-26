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
        title="Test schema changes at the scale your users depend on."
        paragraphs={[
          "See how a migration behaves on realistic Postgres data.",
          "Measure locks, blocked sessions, table rewrites, and query plan changes.",
          "Review the findings before merging your pull request.",
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
        ]}
        visual={<LockWaitChain />}
      />

      <FeatureRow
        reverse
        kicker="Postgres testing"
        title="Inspect the database behind your application."
        items={[
          { title: "Measure the migration", body: "Run each pending statement against a masked database branch and record its impact." },
          { title: "Postgres first", body: "Check query plans and lock duration at realistic row counts." },
          { title: "Review test coverage", body: "See which parts of your stack ran and which checks need more setup." },
        ]}
        visual={<ExpandContractColumns />}
      />

      <FeatureRow
        kicker="The wedge"
        title="Locks, plans, and release compatibility before deployment."
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
