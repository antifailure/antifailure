import { PageShell, RelatedGrid } from "@/components/pages/kit";
import { CircularMap, DashChart, FeatureRow, Notebook, SplitHero, TaskTable } from "./well";

export function MarketplacesPage() {
  return (
    <PageShell>
      <SplitHero
        stack
        path="/solutions/marketplaces"
        eyebrow="Solutions · Marketplaces"
        title="Queues, workers, dual-writes, matching logic staging never reproduces."
        paragraphs={[
          "The twin includes workers and queues. Production webhooks are blocked.",
          "Impatient retries and multi-tab checkout become deterministic scenarios.",
          "Matching logic that depends on queue order will not show up if staging skips workers.",
        ]}
        visual={
          <Notebook
            tab="matching.worker"
            rail="NOTES"
            rows={[
              { id: "W-01", label: "matching.worker", kind: "worker", status: "RUNNING", tone: "PASS", bar: 92 },
              { id: "W-02", label: "notify.worker", kind: "worker", status: "RUNNING", tone: "PASS", bar: 86 },
              { id: "W-03", label: "settle.worker", kind: "worker", status: "RUNNING", tone: "PASS", bar: 80 },
              { id: "WH-09", label: "api.partners.test", kind: "hook", status: "BLOCKED", tone: "BLOCK", bar: 10 },
              { id: "Q-14", label: "listings.created → match.attempt", kind: "queue", status: "QUEUED", tone: "WARN", bar: 54 },
            ]}
            overlay={{
              title: "Webhook deny",
              checks: [
                "Outbound webhooks store a preview. Production hostnames never resolve.",
                "Partner webhooks are written to the attempted-effect ledger.",
                "Impatient retries compile into deterministic scenarios.",
                "Multi-tab checkout is a scenario, not a surprise.",
                "api.partners.test stays BLOCKED.",
              ],
            }}
          />
        }
      />

      <FeatureRow
        reverse
        kicker="Timing is the bug"
        title="Services, queues, and workers are a dimension staging drops."
        items={[
          { title: "Queues in the twin", body: "Simulated streams so dual-writes and retries are visible." },
          { title: "Webhook containment", body: "Production partner webhooks are blocked and written to the attempted-effect ledger." },
          { title: "Retry personas", body: "Impatient users and API clients become deterministic scenarios." },
        ]}
        visual={
          <DashChart
            popupSide="left"
            title="Queue order"
            bars={[22, 30, 48, 36, 58, 64, 71, 60, 84, 78]}
            popup={{
              title: "clone-local queue",
              rows: [
                ["01", "listings.created"],
                ["02", "orders.paid"],
                ["03", "match.attempt"],
              ],
            }}
          />
        }
      />

      <FeatureRow
        stack
        kicker="Restore both sides"
        title="Buyers, sellers, listings, and in-flight orders as a referential subset."
        items={[
          { title: "Both sides of the market", body: "Buyers and sellers restored together, so a match has something to match against." },
          { title: "Run the workers", body: "Matching, notify, and settle against clone-local queues." },
          { title: "Contain partners", body: "Each partner host carries its own mode in the manifest, so the ones the twin simulates and the ones it refuses are written down rather than assumed." },
        ]}
        visual={
          <CircularMap
            shift="center"
            tabs={["MATCH", "NOTIFY", "SETTLE"]}
            active="MATCH"
            rings={[
              { label: "buyers", r: 40 },
              { label: "sellers", r: 34 },
              { label: "listings", r: 30 },
              { label: "orders", r: 38 },
            ]}
          />
        }
      />

      <FeatureRow
        reverse
        kicker="Compare"
        title="Duplicate events, missed matches, and irreversible writes in the oracle."
        items={[
          { title: "Rolling deploys", body: "Old and new schema coexistence is exactly what a disposable twin is for." },
          { title: "Duplicate events", body: "Visible when workers actually run." },
          { title: "Compare", body: "The oracle diffs the twin's writes against the baseline run." },
        ]}
        visual={
          <TaskTable
            shift="left"
            heading="Tasks · marketplace twin"
            rows={[
              { task: "Restore both sides", status: "Completed", tone: "PASS", who: "R", date: "00:03" },
              { task: "Run matching.worker", status: "Running", tone: "PASS", who: "M", date: "00:09" },
              { task: "Contain partners", status: "BLOCKED", tone: "BLOCK", who: "P", date: "n/a" },
              { task: "Compare in oracle", status: "In review", tone: "WARN", who: "O", date: "00:14" },
            ]}
          />
        }
      />

      <RelatedGrid
        items={[
          { href: "/product/load", title: "Load", description: "Traffic shaped like production's access log." },
          { href: "/product/firewall", title: "Side-Effect Firewall", description: "Partner webhooks stay captured." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}
