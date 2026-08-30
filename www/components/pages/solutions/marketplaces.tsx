import { PageHero, PageSection, PageShell, RelatedGrid } from "@/components/pages/kit";
import { AFTER_HEADING, FeatureList, Lead, OpenSteps, SectionHeading, WorkersScene } from "./visuals";

export function MarketplacesPage() {
  return (
    <PageShell>
      <PageHero
        path="/solutions/marketplaces"
        eyebrow="Solutions · Marketplaces"
        title="Queues, workers, dual-writes, matching logic staging never reproduces."
        lead="The twin includes workers and queues. Production webhooks are blocked. Impatient retries and multi-tab checkout become deterministic scenarios."
        visual={<WorkersScene />}
      />
      <PageSection tone="sage">
        <SectionHeading title="<strong>Timing is the bug.</strong> Services, queues, and workers are a dimension staging drops." />
        <Lead>
          Rolling deploys, old and new schema coexistence, and duplicate events are exactly what a disposable
          twin is for. Matching logic that depends on queue order will not show up in a shared staging
          environment that skips workers.
        </Lead>
        <div className={AFTER_HEADING}>
          <FeatureList
            items={[
              { title: "Queues in the twin", body: "Simulated streams so dual-writes and retries are visible." },
              { title: "Webhook containment", body: "Production partner webhooks are blocked and written to the attempted-effect ledger." },
              { title: "Retry personas", body: "Impatient users and API clients become deterministic scenarios." },
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="white">
        <OpenSteps
          items={[
            { title: "Restore both sides", body: "Buyers, sellers, listings, and in-flight orders as a referential subset." },
            { title: "Run the workers", body: "Matching, notify, and settle against clone-local queues." },
            { title: "Contain partners", body: "Outbound webhooks store a preview. Production hostnames never resolve." },
            { title: "Compare", body: "Duplicate events, missed matches, and irreversible writes in the oracle." },
          ]}
        />
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/product/workload", title: "Workload Studio", description: "Retries compiled into deterministic journeys." },
          { href: "/product/firewall", title: "Side-Effect Firewall", description: "Partner webhooks stay captured." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}
