import { ReportScene } from "@/components/home/visuals/ReportScene";
import { PageHero, PageSection, PageShell, RelatedGrid } from "@/components/pages/kit";
import { AFTER_HEADING, Lead, Note, PolicyList, SectionHeading, Split, Verdicts } from "./visuals";

export function ReleaseGatesPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Solutions · Release gates"
        title="Evidence-backed pass, warning, or block."
        lead="Attach a report to the pull request. Enforce organizational release policy. Do not ship on a green preview URL alone."
        framed={false}
        visual={
          <div className="[&>:first-child]:mt-0 [&>:first-child]:max-xl:mt-0 [&>:first-child]:max-lg:mt-0">
            <ReportScene />
          </div>
        }
      />
      <PageSection tone="sage">
        <SectionHeading title="<strong>The only output that matters</strong> is the deployment decision." />
        <div className={AFTER_HEADING}>
          <Verdicts
            items={[
              { tone: "PASS", title: "Ship", body: "Ship with evidence attached to the pull request." },
              { tone: "WARN", title: "Warning", body: "Human review. Fidelity, latency, or an expected difference." },
              { tone: "BLOCK", title: "Block", body: "Do not merge. The cause, the workflow, and the remediation are in the report." },
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="white">
        <Split
          reverse
          visual={
            <PolicyList
              items={[
                { tone: "BLOCK", text: "Block if an exclusive lock exceeds 2 seconds on a critical table." },
                { tone: "BLOCK", text: "Block if unknown external egress is attempted." },
                { tone: "WARN", text: "Warn if p95 latency increases by more than 15%." },
                { tone: "WARN", text: "Require approval if fidelity is below 80%." },
              ]}
            />
          }
        >
          <SectionHeading title="<strong>Policy is the platform-team surface.</strong> Enforce it organization-wide." />
          <Lead>
            Reduce the probability and blast radius of high-risk releases by validating them under
            production-shaped conditions. That is not a zero-rollback guarantee. Reports that are noisy get
            ignored — baseline comparison and severity policy keep the gate useful.
          </Lead>
        </Split>
      </PageSection>
      <PageSection tone="sage">
        <Note label="Center the decision">
          Every workflow and report centers on the deployment decision. Environment creation, data, agents,
          and load are supporting systems. A preview URL is not the product.
        </Note>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/product/report", title: "Safety Report", description: "How the gate is attached to the PR." },
          { href: "/product/oracle", title: "Differential Oracle", description: "Where comparisons are produced." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}
