import { PageHero, PageSection, PageShell, RelatedGrid } from "@/components/pages/kit";
import { SMKT01 } from "@/components/pages/figures/solutions";
import { AFTER_HEADING, FeatureList, Lead, OpenSteps, SectionHeading } from "./visuals";

export function MarketplacesPage() {
  return (
    <PageShell>
      <PageHero
        path="/solutions/marketplaces"
        eyebrow="Solutions · Marketplaces"
        title="Queues, workers, dual-writes, matching logic staging never reproduces."
        lead="The twin runs your own workers and cron services beside the web tier, on the same branched database. Production webhooks are denied inside it, and every attempt is recorded."
        framed={false}
        visual={<SMKT01 />}
      />
      <PageSection tone="panel">
        <SectionHeading title="<strong>Timing is the bug.</strong> Services, queues, and workers are a dimension staging drops." />
        <Lead>
          Matching logic that depends on queue order will not show up in a shared staging environment
          that skips workers. The twin runs them, against a branch carrying production's shape, and
          then asks the data whether the ordering held.
        </Lead>
        <div className={AFTER_HEADING}>
          <FeatureList
            items={[
              { title: "Workers in the twin", body: "The web, worker and cron services the manifest declares, all against one branched database." },
              { title: "Webhook containment", body: "Production partner webhooks are blocked and written to the attempted-effect ledger." },
              { title: "Invariants on the orders", body: "A statement asked of the data after the run: no order matched twice, no settlement without a match." },
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="ruled">
        <OpenSteps
          items={[
            { title: "Restore both sides", body: "Buyers, sellers, listings, and in-flight orders as a referential subset." },
            { title: "Run the workers", body: "Matching, notify and settle, against the branched database." },
            { title: "Contain partners", body: "Outbound webhooks are captured. Production hostnames never resolve." },
            { title: "Ask the data", body: "Duplicate events and missed matches, as invariants that return the offending rows." },
          ]}
        />
      </PageSection>

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
