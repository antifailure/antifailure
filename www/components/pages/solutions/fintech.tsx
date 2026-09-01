import { PageHero, PageSection, PageShell, RelatedGrid } from "@/components/pages/kit";
import { SFIN01, SFIN02 } from "@/components/pages/figures/solutions";
import { AFTER_HEADING, FeatureList, Lead, SectionHeading, Split } from "./visuals";

const FIN_SPEC: [string, string][] = [
  ["Stripe payment", "Answered from a stateful pack, in a clone-local ledger"],
  ["SendGrid email", "Render and capture, never deliver"],
  ["Slack webhook", "Captured, never posted"],
  ["Production hostname", "Block and flag as critical"],
  ["Unknown TCP", "Deny by default"],
  ["Attempted-effect ledger", "Every outbound attempt is recorded, including denies"],
];

export function FintechPage() {
  return (
    <PageShell>
      <PageHero
        path="/solutions/fintech"
        eyebrow="Solutions · Fintech"
        title="Billing, ledgers, and side effects that must never hit live processors."
        lead="The firewall answers Stripe from a stateful pack with the network unplugged. Safe State masks account identifiers inside your boundary. An invariant you write catches the duplicate ledger row, and the comment carries the rows."
        framed={false}
        visual={<SFIN01 />}
      />
      <PageSection>
        <SectionHeading title="<strong>Simulators, not live processors.</strong> Charging a card from a twin is an existential failure." />
        <div className={AFTER_HEADING}>
          <FeatureList
            items={[
              { title: "Clone-local Stripe", body: "Payment creation is stored in a clone-local ledger. Nothing charges." },
              { title: "Email captured", body: "SendGrid and similar sinks render and store. Nothing is delivered." },
              { title: "Fail closed", body: "Unknown processors and production API hostnames are blocked and ledgered." },
              { title: "Invariants on the ledger", body: "A statement you write, asked of the branch after the workflows run. If it returns rows, the check fails and the rows are in the comment." },
              { title: "A whole billing flow, offline", body: "Checkout, subscription and webhook against the built-in Stripe pack, with the network unplugged." },
              { title: "Mid-market first", body: "Technically sophisticated billing teams. Not a regulated-enterprise procurement motion." },
            ]}
          />
        </div>
      </PageSection>
      <PageSection tone="ruled">
        <Split reverse visual={<SFIN02 rows={FIN_SPEC} />}>
          <SectionHeading title="<strong>Containment is the product surface.</strong>" />
          <Lead>
            We do not claim every workflow is automatically compliant. We claim evidence under
            production-shaped conditions, with production data remaining in the customer boundary, and with
            live processors unreachable from the twin.
          </Lead>
        </Split>
      </PageSection>
      <PageSection tone="panel">
        <Split
          visual={
            <div className="border border-black/12 bg-[#f7f7f5] px-5 py-5">
              <p className="text-[16px] leading-7 tracking-extra-tight text-gray-new-40">
                Charging a live processor, emailing a real customer, or invoking a production webhook from a twin
                is not a warning. It is a failed containment model.
              </p>
            </div>
          }
        >
          <SectionHeading title="<strong>Existential failure.</strong>" />
        </Split>
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
