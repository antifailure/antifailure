import { CodePanel, PageHero, PageSection, PageShell, RelatedGrid } from "@/components/pages/kit";
import {
  AFTER_HEADING,
  FeatureList,
  Lead,
  Metrics,
  MigrationHero,
  Note,
  SectionHeading,
  Split,
} from "./visuals";

export function MigrationsPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Solutions · Schema migrations"
        title="The failure mode staging never catches."
        lead="Long exclusive locks, full table rewrites, pool exhaustion, plan regressions, rare constraint failures, and rollback that is no longer safe."
        framed={false}
        visual={<MigrationHero tab={0} />}
      />
      <PageSection tone="sage">
        <SectionHeading title="<strong>A lock you can measure.</strong> A p99 you can compare. A rollback you can call unsafe." />
        <div className={AFTER_HEADING}>
          <Metrics
            items={[
              { value: "27.4s", label: "ACCESS EXCLUSIVE on subscriptions. Checkout stalls for the duration of the lock." },
              { value: "6.9s", label: "Checkout p99 under equivalent traffic. Baseline was 820ms." },
              { value: "Unsafe", label: "Old application instances cannot deserialize candidate writes. Rolling rollback fails." },
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="white">
        <Split
          visual={
            <CodePanel label="BLOCKED: unsafe schema migration">{`Migration 20260824_add_billing_status
held ACCESS EXCLUSIVE on subscriptions
for 27.4 seconds.

checkout p99        820ms → 6.9s
upgrade timeouts    11.8%
old app             cannot read candidate rows
rolling rollback    unsafe

suggested: nullable column, batched backfill,
dual-read, constraint in a later migration.`}
            </CodePanel>
          }
        >
          <SectionHeading title="<strong>This is the wedge.</strong> Not universal multicloud cloning." />
          <Lead>
            Automated safety validation for risky Postgres-backed web deployments, especially schema
            migrations. Clear failure mode, technical buyer, measurable value. Land on one migration, expand
            to every risky pull request.
          </Lead>
        </Split>
        <div className={AFTER_HEADING}>
          <FeatureList
            items={[
              { title: "Locks", body: "Acquisition, duration, blocked statements, and cumulative blocked time." },
              { title: "Rewrites", body: "Full table rewrites, index builds, constraint failures on rare rows." },
              { title: "Rollback", body: "Whether candidate writes make rolling rollback unsafe." },
            ]}
          />
        </div>
        <div className={AFTER_HEADING}>
          <Note label="Safer pattern">
            Expand-and-contract. Dual-read compatibility. Constraint later. The engine should generate both
            evidence and a recommended safer migration pattern.
          </Note>
        </div>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/product/migrations", title: "Migration Safety Engine", description: "How locks become a GitHub check." },
          { href: "/product/report", title: "Safety Report", description: "The block, with a cause." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}
