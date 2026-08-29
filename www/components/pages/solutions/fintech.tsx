import { FirewallScene } from "@/components/home/visuals/FirewallScene";
import { PageHero, PageSection, PageShell, RelatedGrid } from "@/components/pages/kit";
import { AFTER_HEADING, FeatureList, Lead, Note, SectionHeading, SpecRows, Split } from "./visuals";

export function FintechPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Solutions · Fintech"
        title="Billing, ledgers, and side effects that must never hit live processors."
        lead="The firewall simulates Stripe. Safe State masks account identifiers. The oracle compares ledger writes. Duplicate events are incidents — they belong in a report, not in production."
        visual={<FirewallScene />}
      />
      <PageSection>
        <SectionHeading title="<strong>Simulators, not live processors.</strong> Charging a card from a twin is an existential failure." />
        <div className={AFTER_HEADING}>
          <FeatureList
            items={[
              { title: "Clone-local Stripe", body: "Payment creation is stored in a clone-local ledger. Nothing charges." },
              { title: "Email captured", body: "SendGrid and similar sinks render and store. Nothing is delivered." },
              { title: "Fail closed", body: "Unknown processors and production API hostnames are blocked and ledgered." },
              { title: "Ledger comparison", body: "The oracle compares writes, events, and third-party effects against baseline." },
              { title: "Irreversible writes", body: "Candidate billing events that old code cannot reconcile show up before ship." },
              { title: "Mid-market first", body: "Technically sophisticated billing teams. Not a regulated-enterprise procurement motion." },
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="white">
        <Split
          reverse
          visual={
            <SpecRows
              rows={[
                ["Stripe payment", "Simulate and store in a clone-local ledger"],
                ["SendGrid email", "Render and capture, never deliver"],
                ["Slack webhook", "Store a message preview"],
                ["Production hostname", "Block and flag as critical"],
                ["Unknown TCP", "Deny by default"],
                ["Attempted-effect ledger", "Every outbound attempt is recorded, including denies"],
              ]}
            />
          }
        >
          <SectionHeading title="<strong>Containment is the product surface.</strong>" />
          <Lead>
            We do not claim every workflow is automatically compliant. We claim evidence under
            production-shaped conditions, with production data remaining in the customer boundary, and with
            live processors unreachable from the twin.
          </Lead>
        </Split>
      </PageSection>
      <PageSection tone="sage">
        <Note label="Existential failure" tone="block">
          Charging a live processor, emailing a real customer, or invoking a production webhook from a twin
          is not a warning. It is a failed containment model.
        </Note>
      </PageSection>
      <RelatedGrid
        items={[
          { href: "/product/firewall", title: "Side-Effect Firewall", description: "How egress is denied and simulated." },
          { href: "/product/architecture", title: "Architecture", description: "Control plane and customer data plane." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}
