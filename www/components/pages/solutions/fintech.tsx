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
        title="Rehearse billing changes before money moves."
        paragraphs={[
          "Test payment flows with masked account data and a local Stripe simulator.",
          "Check the resulting ledger entries against your expected outcomes.",
          "Find duplicate charges, missed events, and inconsistent balances in an isolated run.",
        ]}
        visual={<PacketPath />}
      />

      <FeatureRow
        stack
        kicker="Payment integrations"
        title="Run the payment flow inside your test environment."
        items={[
          { title: "The mode is set per host", body: "block, allow, capture, mock, emulate, sandbox or synth, written against the host in antifailure.yaml." },
          { title: "Nothing leaves without a rule", body: "Egress defaults to block, so a processor nobody configured is refused on its first run rather than passed through." },
          { title: "The ledger records the decision", body: "Each attempt is stored with the mode that decided it, so the reason a request never left is readable afterwards." },
        ]}
        visual={<HostModeMatrix />}
      />

      <FeatureRow
        reverse
        kicker="Containment"
        title="Check the result across your services."
        items={[
          { title: "Ledger comparison", body: "The oracle compares writes, events, and third-party effects against baseline." },
          { title: "Irreversible writes", body: "Test whether the previous release can still read the updated schema." },
          { title: "Your billing workflows", body: "Define the payment and subscription journeys that matter to your team." },
        ]}
        visual={<TwinLiveSplit />}
      />

      <FeatureRow
        kicker="Request evidence"
        title="See how every gateway request was handled."
        items={[
          { title: "Inspect the decision", body: "Review whether each request was blocked, captured, simulated, or allowed by policy." },
          { title: "Capture notifications", body: "Read captured messages and inspect webhook payloads during the test." },
          { title: "Block unknown hosts", body: "New destinations remain blocked until you configure how they should behave." },
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
