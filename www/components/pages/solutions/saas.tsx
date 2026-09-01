import { PageHero, PageSection, PageShell } from "@/components/pages/kit";
import { SSAAS01, SSAAS02 } from "@/components/pages/figures/solutions";
import {
  AFTER_HEADING,
  FeatureList,
  Lead,
  Metrics,
  SectionHeading,
  Split,
} from "./visuals";

export function SaasPage() {
  return (
    <PageShell>
      <PageHero
        path="/solutions/saas"
        eyebrow="Solutions · B2B SaaS"
        title="Daily deploys. Expanding schemas. Staging that drifted years ago."
        lead="The first twin should catch the migration that locks subscriptions during peak traffic, against sanitized tenant-shaped state rather than a fixture dump."
        framed={false}
        visual={<SSAAS01 />}
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
        <Split visual={<SSAAS02 />}>
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
    </PageShell>
  );
}
