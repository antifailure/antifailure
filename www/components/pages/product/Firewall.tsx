import { Callout, PageHeading, PageHero, PageSection, PageShell, RelatedGrid, Split } from "@/components/pages/kit";
import { Illustrative } from "@/components/layout/Illustrative";
import { PFW01, PFW02, PFW03, PFW04, PFW05, PFW06 } from "@/components/pages/figures/product-firewall";

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
        title="Test integrations without reaching real customers."
        lead="Use a local Stripe simulator, capture outgoing email, and choose how each external service responds. Antifailure blocks destinations you have not configured."
        framed={false}
        visual={<PFW01 />}
      />

      <PageSection>
        <Split visual={<PFW05 />}>
          <PageHeading
            kicker="Attempted-effect ledger"
            title="<strong>Choose how each integration behaves.</strong> Inspect the requests your gateway allowed, simulated, captured, or blocked."
          />
        </Split>
        <ul className="mt-10 grid grid-cols-3 items-start gap-x-16 gap-y-10 max-xl:grid-cols-1">
          <li className="min-w-0 [&_.rounded-\[32px\]]:!rounded-[18px]">
            <PFW02 />
          </li>
          <li className="min-w-0 [&_.rounded-\[32px\]]:!rounded-[18px]">
            <PFW03 />
          </li>
          <li className="min-w-0 [&_.rounded-\[32px\]]:!rounded-[18px]">
            <PFW04 />
          </li>
        </ul>
        <Illustrative>
            Example gateway log. Direct connections blocked by network isolation do not appear in this log.
          </Illustrative>
      </PageSection>

      <PageSection tone="ruled">
        <Split visual={<PFW06 />}>
          <PageHeading title="<strong>Isolation reaches beyond proxy settings.</strong>" />
          <p className="mt-6 max-w-[480px] text-[17px] leading-7 tracking-extra-tight text-gray-new-40">
            Services run on an isolated network, with outbound connections routed through the gateway. Policy controls destinations and operations, including requests from clients that ignore proxy settings.
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
            <Callout label="Payment testing" tone="block">
              Configure payment hosts to use a simulator or sandbox. The application uses placeholder credentials for intercepted calls, and the gateway records its decisions.
            </Callout>
          }
        >
          <PageHeading title="<strong>Keep payment tests inside the test environment.</strong>" />
        </Split>
      </PageSection>


      <RelatedGrid
        items={[
          { href: "/product/twins", title: "Isolated Twin", description: "Where containment is applied." },
          { href: "/product/load", title: "Load", description: "Traffic sent at the contained twin." },
          { href: "/docs/concepts/egress", title: "Egress docs", description: "Controls and example behavior." },
        ]}
      />
    </PageShell>
  );
}
