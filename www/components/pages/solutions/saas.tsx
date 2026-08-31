import { PageShell, RelatedGrid } from "@/components/pages/kit";
import { CircularMap, DashChart, FeatureRow, Notebook, SplitHero, TaskTable } from "./well";

export function SaasPage() {
  return (
    <PageShell>
      <SplitHero
        path="/solutions/saas"
        eyebrow="Solutions · B2B SaaS"
        title="Daily deploys. Expanding schemas. Staging that drifted years ago."
        paragraphs={[
          "The first twin should catch the migration that locks subscriptions during peak traffic.",
          "Against sanitized tenant-shaped state, not a fixture dump.",
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
                "helix dropped — children follow parent.",
                "Tokens and sessions deleted, not masked.",
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
                ["Tenants", "N long-tail"],
                ["Schema", "Old + new"],
              ],
            }}
          />
        }
      />

      <FeatureRow
        reverse
        kicker="Staging"
        title="Staging differs in too many dimensions at once."
        items={[
          { title: "A change can pass unit, integration, and a manual staging check", body: "then still fail in production." },
          { title: "The twin reproduces tenant shape, concurrency, and schema coexistence", body: "then reports whether the deploy is safe." },
          { title: "Old + new", body: "Application instances still running while the new column lands." },
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
        title="Pass, warning, or block on the pull request — then destroy the twin."
        items={[
          { title: "Restore", body: "Referential subset of orgs, seats, subscriptions, invoices." },
          { title: "Mask", body: "Account identifiers replaced inside the customer boundary." },
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
          { href: "/product/migrations", title: "Migration Safety", description: "The lock on subscriptions is the first finding." },
          { href: "/signup", title: "Sign up", description: "Join the waitlist." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}
