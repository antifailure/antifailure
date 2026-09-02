import { PageShell, RelatedGrid } from "@/components/pages/kit";
import { FeatureRow, SplitHero } from "./well";
import { DualWriteBoard, QueueWaterfall, SequenceDiagram, TwoSidedMarket } from "./marketplaces-plates";

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
        visual={<SequenceDiagram />}
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
        visual={<QueueWaterfall />}
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
        visual={<TwoSidedMarket />}
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
        visual={<DualWriteBoard />}
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
