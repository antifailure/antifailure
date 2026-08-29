import { TwinLifecycleScene } from "@/components/home/visuals/TwinLifecycleScene";
import { PageHero, PageSection, PageShell, RelatedGrid } from "@/components/pages/kit";
import { AFTER_HEADING, FeatureList, Lead, OpenSteps, SectionHeading, SpecRows, Split } from "./visuals";

export function PlatformPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Solutions · Platform engineering"
        title="Replace fragile shared staging with policy-controlled ephemeral validation."
        lead="Give every developer an isolated environment without a platform-team ticket. Destroy temporary infrastructure automatically and cap its cost."
        visual={<TwinLifecycleScene />}
      />
      <PageSection tone="sage">
        <SectionHeading kicker="No ticket" title="<strong>Create Wind Tunnel from the pull request.</strong>" />
        <div className={AFTER_HEADING}>
          <OpenSteps
            items={[
              { title: "Connect", body: "GitHub app, customer-hosted runner, Postgres source." },
              { title: "Review", body: "Sensitive fields and discovered outbound services." },
              { title: "Policy", body: "Isolation, cost, retention, and release gates — organization-wide." },
              { title: "Baseline", body: "Validate the current production version first. Then every risky PR." },
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="white">
        <Split
          visual={
            <SpecRows
              rows={[
                ["Adapter", "Detected: compose + postgres"],
                ["Isolation", "Customer VPC · no default egress"],
                ["TTL", "4h"],
                ["Budget", "$40"],
                ["Ticket", "None"],
                ["Lifecycle", "REQUESTED → READY → DESTROYED, cleanup attested"],
              ]}
            />
          }
        >
          <SectionHeading title="<strong>Shared staging is a queue.</strong> Ephemeral twins are a policy." />
          <Lead>
            The customer currently chooses between assembling several tools, maintaining expensive staging,
            testing in production, or accepting the risk. Platform teams set isolation, cost, and retention.
            Developers press one button.
          </Lead>
        </Split>
        <div className={AFTER_HEADING}>
          <FeatureList
            items={[
              { title: "No ticket", body: "Create Wind Tunnel from the PR. The adapter is chosen for you." },
              { title: "Policy", body: "Isolation, cost, retention, and release gates are organization-wide." },
              { title: "Cleanup", body: "TTL, cost ceiling, independent reaper, verifiable destruction record." },
            ]}
          />
        </div>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/product/twins", title: "Isolated Twin", description: "Provision, isolate, destroy, attest." },
          { href: "/product/architecture", title: "Architecture", description: "Customer-hosted data plane." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}
