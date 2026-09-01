import {
  Callout,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  Split,
} from "@/components/pages/kit";
import { Illustrative } from "@/components/layout/Illustrative";
import { PFW01, PFW02, PFW03, PFW04, PFW05, PFW06 } from "@/components/pages/figures/product";

const CONTROLS = [
  { title: "No default egress", body: "There is no default public internet route from the twin." },
  { title: "Clone-local DNS", body: "Production hostnames do not resolve to production." },
  { title: "Mandatory gateway", body: "Domain, IP, protocol, method, and operation policies at the edge." },
  { title: "A stateful Stripe", body: "One built-in pack answers the Stripe API from a clone-local ledger. Other mocked hosts answer without keeping state." },
] as const;

export function FirewallPage() {
  return (
    <PageShell>
      <PageHero
        path="/product/firewall"
        eyebrow="Side-Effect Firewall"
        title="The twin cannot act on the real world."
        lead="No default public egress. Clone-local DNS. A stateful Stripe that answers offline, mail rendered and captured rather than sent. Unknown destinations are denied and written to the attempted-effect ledger."
        framed={false}
        visual={<PFW01 />}
      />

      <PageSection>
        <Split visual={<PFW05 />}>
          <PageHeading
            kicker="Attempted-effect ledger"
            title="<strong>Every outbound attempt is recorded, including the denials.</strong> Six per-host modes, from refusing outright to answering from an offline pack. Never a live processor."
          />
        </Split>
        <ul className="mt-14 grid grid-cols-3 gap-x-16 gap-y-12 max-xl:grid-cols-1">
          <li>
            <PFW02 />
          </li>
          <li>
            <PFW03 />
          </li>
          <li>
            <PFW04 />
          </li>
        </ul>
        <Illustrative>
          Six rows chosen to show mocked calls, captured messages and denials. The hosts, the modes
          and the decision log are real:{" "}
          <code className="font-mono text-[12px] text-black/70">af net log</code> prints every
          request the gateway decided, allowed as well as refused, and{" "}
          <code className="font-mono text-[12px] text-black/70">af ci</code> summarises them on the
          pull request. A packet that never reaches the gateway, such as a connection straight to a
          public address, leaves no row: it fails at the network instead, which is stronger and is
          the section below. A denied destination is denied inside the twin; it does not on its own
          fail the check.
        </Illustrative>
      </PageSection>

      <PageSection tone="ruled">
        <Split visual={<PFW06 />}>
          <PageHeading title="<strong>Containment is not a rule you can edit.</strong> A direct-IP attempt does not get out." />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Clone-local DNS is not enough if the twin dials an address. The gateway matches domain, IP,
            protocol, method, and operation. Unknown destinations, unresolved secrets, or missing isolation
            block the run. Convenience does not silently override containment.
          </p>
          <ul className="mt-10 grid grid-cols-2 gap-x-8 gap-y-8 max-xl:grid-cols-1">
            {CONTROLS.map((item) => (
              <li key={item.title} className="min-w-0">
                <div className="mb-3 size-2 rounded-full bg-black" />
                <h3 className="text-[16px] leading-snug tracking-extra-tight text-black">{item.title}</h3>
                <p className="mt-1.5 max-w-[280px] text-[14px] leading-6 tracking-extra-tight text-gray-new-40">
                  {item.body}
                </p>
              </li>
            ))}
          </ul>
        </Split>
      </PageSection>

      <PageSection tone="panel">
        <Split
          visual={
            <Callout label="Existential failure" tone="block">
              Charging cards, emailing users, or invoking production webhooks from a twin is a failed
              containment model. Read-only forwarding exists only for explicitly approved endpoints. Request
              and response redaction is mandatory. The ledger is the proof.
            </Callout>
          }
        >
          <PageHeading title="<strong>Charging a live processor is an existential failure.</strong> Not a warning. Not a retry." />
        </Split>
      </PageSection>

    </PageShell>
  );
}
