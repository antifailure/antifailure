import { WorkloadScene } from "@/components/home/visuals/WorkloadScene";
import { CodePanel, PageHero, PageSection, PageShell, RelatedGrid } from "@/components/pages/kit";
import { AFTER_HEADING, FeatureList, Lead, SectionHeading, Split } from "./visuals";

export function WorkflowPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Solutions · Workflow products"
        title="Workers, schedules, and long-tail state."
        lead="Timing among services, queues, and workers is a dimension staging drops. The twin includes background jobs and scheduled tasks against sanitized historical state."
        visual={<WorkloadScene />}
      />
      <PageSection tone="sage">
        <SectionHeading title="<strong>Jobs in the twin.</strong> Duplicate events and irreversible writes show up in the oracle." />
        <Lead>
          Workflow and collaboration products share Postgres, frequent deploys, and workers that staging does
          not run the same way. Malformed historical records that fixtures never include are the ones that
          fail the constraint.
        </Lead>
        <div className={AFTER_HEADING}>
          <FeatureList
            items={[
              { title: "Jobs in the twin", body: "Background workers and scheduled tasks against sanitized state." },
              { title: "Long-tail records", body: "Malformed historical state that fixtures never include." },
              { title: "Multi-tab behavior", body: "Exploratory personas that abandon, resume, and retry — compiled into deterministic scenarios." },
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="white">
        <Split
          reverse
          visual={
            <CodePanel label="scenario: retry_after_abandon">{`identity_fixture: returning_editor
schedule: billing.reconcile  */15

steps:
  - open: /docs/1841
  - edit: title
  - abandon_for_ms: 40000
  - resume_other_tab: /docs/1841
  - submit: save
assertions:
  - one_version_created
  - reconcile_job_idempotent
  - no_duplicate_share_email`}
            </CodePanel>
          }
        >
          <SectionHeading title="<strong>Schedules are production behavior.</strong> They belong in the proving ground." />
          <Lead>
            Exploratory users try abandon-and-resume and multi-tab edits. The deterministic runner proves them at
            scale, including the reconcile job that must stay idempotent. They live in Workload Studio beside
            observed and deterministic traffic.
          </Lead>
        </Split>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/product/workload", title: "Workload Studio", description: "Observed, deterministic, exploratory." },
          { href: "/product/exploratory-users", title: "Exploratory users", description: "Exploratory users inside Workload Studio." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}
