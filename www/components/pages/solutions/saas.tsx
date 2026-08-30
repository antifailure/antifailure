import { PageHero, PageSection, PageShell, RelatedGrid } from "@/components/pages/kit";
import {
  AFTER_HEADING,
  FeatureList,
  Lead,
  Metrics,
  SectionHeading,
  SpecRows,
  Split,
  TenantSubsetScene,
} from "./visuals";

export function SaasPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Solutions · B2B SaaS"
        title="Daily deploys. Expanding schemas. Staging that drifted years ago."
        lead="The first twin should catch the migration that locks subscriptions during peak traffic — against sanitized tenant-shaped state, not a fixture dump."
        visual={<TenantSubsetScene />}
      />
      <PageSection tone="sage">
        <SectionHeading title="<strong>Tenant-shaped state.</strong> Checkout and seat changes against sanitized accounts." />
        <div className={AFTER_HEADING}>
          <Metrics
            items={[
              { value: "Daily", label: "or weekly production deploys. Staging cannot keep up with schema change." },
              { value: "N tenants", label: "Long-tail accounts and malformed historical seats that fixtures omit." },
              { value: "Rare rows", label: "The historical account whose shape no fixture has, and which the constraint fails on." },
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="white">
        <Split
          visual={
            <SpecRows
              rows={[
                ["Restore", "Referential subset of orgs, seats, subscriptions, invoices"],
                ["Mask", "Account identifiers replaced inside the customer boundary"],
                ["Exercise", "Checkout, upgrades, and seat changes at production-shaped concurrency"],
                ["Decide", "Pass or fail on the pull request, then destroy the twin"],
              ]}
            />
          }
        >
          <SectionHeading title="<strong>Staging differs in too many dimensions at once.</strong>" />
          <Lead>
            A change can pass unit, integration, end-to-end, and a manual staging check, then still
            fail in production. The twin reproduces tenant shape and production's route mix, then
            reports whether the deploy is safe.
          </Lead>
        </Split>
        <div className={AFTER_HEADING}>
          <FeatureList
            items={[
              { title: "Tenant-shaped state", body: "Referential subsets of accounts, seats, and billing without production identities." },
              { title: "Checkout and upgrades", body: "Critical workflows under production-shaped concurrency." },
              { title: "Rehearsed migrations", body: "The pending migrations applied to a branch with production's row counts, before they reach it." },
            ]}
          />
        </div>
      </PageSection>
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
