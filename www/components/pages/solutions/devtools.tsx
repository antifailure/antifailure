import { PageShell, RelatedGrid } from "@/components/pages/kit";
import { MigrationScene } from "@/components/home/visuals/MigrationScene";
import { CircularMap, DashChart, FeatureRow, Notebook, SplitHero, TaskTable } from "./well";

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
        visual={
          <DashChart
            title="plan regression · events"
            bars={[12, 18, 22, 40, 55, 70, 88, 82, 95, 90]}
            popup={{
              title: "events 12,403,881",
              rows: [
                ["baseline", "Index Scan 12ms"],
                ["candidate", "Seq Scan 410ms"],
                ["lock", "ACCESS SHARE 1.1s"],
              ],
            }}
          />
        }
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
        visual={
          <Notebook
            overlaySide="right"
            tab="ALTER subscriptions"
            rail="NOTES"
            rows={[
              { id: "LCK", label: "ACCESS EXCLUSIVE on subscriptions", kind: "lock", status: "BLOCK", tone: "BLOCK", bar: 18 },
              { id: "P99", label: "Checkout p99 820ms → 6.9s", kind: "p99", status: "REGRESS", tone: "BLOCK", bar: 22 },
              { id: "POOL", label: "84 connections waited", kind: "pool", status: "PRESSURE", tone: "WARN", bar: 46 },
              { id: "PLAN", label: "Seq Scan events · 410ms", kind: "plan", status: "REGRESS", tone: "BLOCK", bar: 30 },
              { id: "RB", label: "Old app cannot decode events.v2", kind: "rollback", status: "UNSAFE", tone: "BLOCK", bar: 14 },
            ]}
            overlay={{
              title: "The finding",
              checks: [
                "Exclusive locks and rewrites that never show up on a laptop database.",
                "Plan regressions under production-shaped volume.",
                "Connection-pool exhaustion during migrate-and-serve.",
                "Old instances cannot still read the new schema.",
                "Publish what the twin reproduced. Do not pretend unsupported components are cloned.",
              ],
            }}
          >
            <div className="overflow-hidden">
              <MigrationScene tab={0} playId={0} />
            </div>
          </Notebook>
        }
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
        visual={
          <CircularMap
            shift="left"
            tabs={["EXPAND", "BACKFILL", "CONTRACT"]}
            active="EXPAND"
            rings={[
              { label: "nullable", r: 40 },
              { label: "batches", r: 32 },
              { label: "dual-read", r: 36 },
              { label: "constraint", r: 28 },
            ]}
          />
        }
      />

      <FeatureRow
        kicker="The wedge"
        title="Locks, plans, and rollback feasibility before it ships."
        items={[
          { title: "Lock duration", body: "Measured together with the statements it blocks." },
          { title: "Schema coexistence", body: "Whether old instances can still read the new schema shows up here first." },
          { title: "Users notice p99 immediately", body: "Large tables plus frequent schema change." },
        ]}
        visual={
          <TaskTable
            heading="Tasks · schema change"
            rows={[
              { task: "Apply migration", status: "BLOCK", tone: "BLOCK", who: "M", date: "27.4s" },
              { task: "Checkout p99", status: "Regressed", tone: "BLOCK", who: "C", date: "6.9s" },
              { task: "Pool wait", status: "84 conns", tone: "WARN", who: "P", date: "wait" },
              { task: "Rollback", status: "Unsafe", tone: "BLOCK", who: "R", date: "no" },
            ]}
          />
        }
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
