import { PageShell, RelatedGrid } from "@/components/pages/kit";
import { FeatureRow, SplitHero } from "./well";
import { HostModeMatrix, PacketPath, ReceiptTape, TwinLiveSplit } from "./fintech-plates";

export function FintechPage() {
  return (
    <PageShell>
      <SplitHero
        flip
        path="/solutions/fintech"
        eyebrow="Solutions · Fintech"
        title="Billing, ledgers, and side effects that must never hit live processors."
        paragraphs={[
          "The firewall simulates Stripe. Safe State masks account identifiers.",
          "The oracle compares ledger writes.",
          "Duplicate events are incidents. They belong in a report, not in production.",
        ]}
        visual={<PacketPath />}
      />

      <FeatureRow
        stack
        kicker="Simulators, not live processors"
        title="Charging a card from a twin is an existential failure."
        items={[
          { title: "The mode is set per host", body: "block, allow, capture, mock, sandbox or synth, written against the host in antifailure.yaml." },
          { title: "Nothing leaves without a rule", body: "Egress defaults to block, so a processor nobody configured is refused on its first run rather than passed through." },
          { title: "The ledger records the decision", body: "Each attempt is stored with the mode that decided it, so the reason a request never left is readable afterwards." },
        ]}
        visual={<HostModeMatrix />}
      />

      <FeatureRow
        reverse
        kicker="Containment"
        title="Containment is the product surface."
        items={[
          { title: "Ledger comparison", body: "The oracle compares writes, events, and third-party effects against baseline." },
          { title: "Irreversible writes", body: "Candidate billing events that old code cannot reconcile show up before ship." },
          { title: "Mid-market first", body: "Technically sophisticated billing teams. Not a regulated-enterprise procurement motion." },
        ]}
        visual={<TwinLiveSplit />}
      />

      <FeatureRow
        kicker="Existential failure"
        title="Not a warning. A failed containment model."
        items={[
          { title: "There is no warning level for this", body: "A twin that reaches a live processor has not failed a check. Its containment did not hold." },
          { title: "The customer finds out", body: "A real card, a real inbox and a real partner endpoint are the three places a contained run becomes somebody else's incident." },
          { title: "So the default refuses", body: "A host with no rule against it is blocked, which is the only default that stays safe as the integration list grows." },
        ]}
        visual={<ReceiptTape />}
      />

      <RelatedGrid
        items={[
          { href: "/product/firewall", title: "Side-Effect Firewall", description: "How egress is denied and simulated." },
          { href: "/product/twins", title: "Isolated Twin", description: "Where the contained run lives." },
          { href: "/solutions", title: "All solutions", description: "Teams and jobs." },
        ]}
      />
    </PageShell>
  );
}
