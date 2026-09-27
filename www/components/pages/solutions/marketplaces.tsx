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
        title="Test the whole order flow, including the workers."
        paragraphs={[
          "Run services, workers, and queues together in an isolated environment.",
          "Exercise retries and repeat submissions with workflows your team defines.",
          "Check matching, notifications, and settlement before releasing a change.",
        ]}
        visual={<SequenceDiagram />}
      />

      <FeatureRow
        reverse
        kicker="Timing is the bug"
        title="Give background jobs a place in your pre-deploy checks."
        items={[
          { title: "Queues in the twin", body: "The broker runs in the twin, with its topics and consumer groups created in its own image and no production messages copied." },
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
        title="Catch missed matches and inconsistent order state."
        items={[
          { title: "Rolling deploys", body: "Test the previous release against the updated schema." },
          { title: "Duplicate events", body: "Check outcomes after background jobs finish." },
          { title: "Compare", body: "The oracle diffs the twin's writes against the baseline run." },
        ]}
        visual={<DualWriteBoard />}
      />

      <RelatedGrid
        items={[
          { href: "/docs/concepts/load", title: "Load testing", description: "Traffic shaped like production's access log." },
          { href: "/docs/concepts/egress", title: "Egress", description: "How partner webhooks stay contained." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}
