import { PageShell, RelatedGrid } from "@/components/pages/kit";
import { FeatureRow, SplitHero } from "./well";
import { CircularMap } from "./img/circular-map";
import { DashChart } from "./img/dash-chart";
import { Notebook } from "./img/notebook";
import { TaskTable } from "./img/task-table";

export function SaasPage() {
  return (
    <PageShell>
      <SplitHero
        path="/solutions/saas"
        eyebrow="Solutions · B2B SaaS"
        title="Keep shipping as your database grows."
        paragraphs={[
          "Rehearse schema changes and account workflows on a masked copy of your data.",
          "Keep tenant relationships and unusual billing states in your tests.",
          "Checkout and seat changes run against sanitized accounts.",
        ]}
        visual={
          <Notebook
            tab="subscriptions · peak"
            rail="NOTES"
            rows={[
              { id: "org_a8c1", label: "acme-prod · 12.4k seats", kind: "org", status: "MASK", tone: "WARN", bar: 72 },
              { id: "org_n3w2", label: "northwind · 3.1k seats", kind: "org", status: "MASK", tone: "WARN", bar: 58 },
              { id: "org_h91e", label: "helix · children follow", kind: "org", status: "DROP", tone: "BLOCK", bar: 12 },
              { id: "sub_51Hq", label: "past_due · referential keep", kind: "sub", status: "KEEP", tone: "PASS", bar: 90 },
              { id: "inv_9f2a", label: "open invoice · join valid", kind: "inv", status: "KEEP", tone: "PASS", bar: 84 },
            ]}
            overlay={{
              title: "Sanitization evidence",
              checks: [
                "Account identifiers replaced inside the customer boundary.",
                "Referential subset of orgs, seats, subscriptions, invoices.",
                "Long-tail and malformed historical seats kept when the parent is kept.",
                "helix dropped. Children follow parent.",
                "Tokens hashed. Live sessions dropped.",
              ],
            }}
          />
        }
      />

      <FeatureRow
        kicker="Tenant-shaped state"
        title="Checkout and seat changes against sanitized accounts."
        items={[
          { title: "Tenant-shaped state", body: "Referential subsets of accounts, seats, and billing without production identities." },
          { title: "Checkout and upgrades", body: "Critical workflows under production-shaped concurrency." },
          { title: "Schema coexistence", body: "Old application instances still running while the new column lands." },
        ]}
        visual={
          <DashChart
            title="Deploy cadence vs staging drift"
            bars={[28, 36, 44, 40, 62, 70, 88, 76, 92, 84]}
            popup={{
              title: "Daily / weekly",
              rows: [
                ["Deploys", "Daily"],
                ["Tenants", "Rare states"],
                ["Schema", "Old + new"],
              ],
            }}
          />
        }
      />

      <FeatureRow
        reverse
        kicker="Staging"
        title="Test the conditions your next release will meet."
        items={[
          { title: "Unit, integration, and a manual staging check", body: "A change can pass all three and still fail in production." },
          { title: "Data, traffic, and release compatibility", body: "Bring those checks into one environment and review the result on your pull request." },
          { title: "Old + new", body: "Run the previous release against the new schema to catch incompatible reads before deployment." },
        ]}
        visual={
          <CircularMap
            tabs={["BASELINE", "CANDIDATE", "TWIN"]}
            active="TWIN"
            rings={[
              { label: "orgs", r: 42 },
              { label: "seats", r: 34 },
              { label: "subs", r: 28 },
              { label: "invoices", r: 38 },
            ]}
          />
        }
      />

      <FeatureRow
        kicker="The run"
        title="Pass, warning, or block on the pull request, then destroy the twin."
        items={[
          { title: "Restore", body: "The subset comes back first, so every step after it runs against rows that still join." },
          { title: "Mask", body: "Identifiers are replaced before anything reads them, and the raw snapshot never leaves the customer's cloud." },
          { title: "Exercise", body: "Checkout, upgrades, and seat changes at production-shaped concurrency." },
        ]}
        visual={
          <TaskTable
            heading="Twin run · subscriptions"
            rows={[
              { task: "Restore subset", status: "Completed", tone: "PASS", who: "T", date: "00:04" },
              { task: "Mask identifiers", status: "Completed", tone: "PASS", who: "S", date: "00:07" },
              { task: "Exercise checkout", status: "In progress", tone: "WARN", who: "W", date: "00:11" },
              { task: "Decide on the PR", status: "BLOCK", tone: "BLOCK", who: "R", date: "00:18" },
            ]}
          />
        }
      />

      <RelatedGrid
        items={[
          { href: "/docs/concepts/sql-workloads", title: "SQL workloads", description: "How database changes are exercised before deployment." },
          { href: "/request-demo", title: "Request a demo", description: "See how Antifailure would test it in a demo with the founder." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}
