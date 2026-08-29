import { WorkloadScene } from "@/components/home/visuals/WorkloadScene";
import { PageHero, PageSection, PageShell, RelatedGrid } from "@/components/pages/kit";
import { AFTER_HEADING, FeatureList, Lead, Metrics, Note, SectionHeading } from "./visuals";

export function EcommercePage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Solutions · E-commerce"
        title="Checkout under production-shaped load."
        lead="A twin with production-shaped carts and long-tail SKUs. Payments and email stay captured. Locks on orders are measured while equivalent traffic hits baseline and candidate."
        visual={<WorkloadScene />}
      />
      <PageSection tone="sage">
        <SectionHeading title="<strong>The SKU that breaks the constraint</strong> is never in the fixture dump." />
        <Lead>
          A defaulted column on orders, a full table rewrite during a sale, pool exhaustion, and plan
          regressions on the hottest checkout paths. Promotions render in the twin. They are never emailed.
        </Lead>
        <div className={AFTER_HEADING}>
          <Metrics
            items={[
              { value: "6.9s", label: "Checkout p99 after an exclusive lock on orders. The sale still looks green in staging." },
              { value: "820ms", label: "Baseline p99 under equivalent traffic, before the candidate schema change." },
              { value: "Lock", label: "Measured on orders while checkout, inventory, and promotions run in the twin." },
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="white">
        <SectionHeading title="<strong>Load is not a separate product.</strong> It is how checkout actually behaves." />
        <Lead>
          Observed patterns, deterministic journeys, and a few exploratory users on the candidate. Production
          requests are never synchronously diverted. The report is a gate, not a flame graph.
        </Lead>
        <div className={AFTER_HEADING}>
          <FeatureList
            items={[
              { title: "Long-tail catalogs", body: "Toy product fixtures miss the SKU, cart, and promo combination that breaks a constraint." },
              { title: "Checkout p99", body: "Equivalent traffic against baseline and candidate, with lock timing on orders." },
              { title: "Promotions and email", body: "Rendered and captured. Never delivered to customers." },
            ]}
          />
        </div>
        <div className={AFTER_HEADING}>
          <Note label="During a sale" tone="warn">
            An exclusive lock that is invisible on a laptop database becomes a checkout outage when the
            catalog is production-shaped.
          </Note>
        </div>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/product/workload", title: "Workload Studio", description: "How production-shaped traffic is replayed." },
          { href: "/product/migrations", title: "Migration Safety", description: "Locks on orders, measured." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}
