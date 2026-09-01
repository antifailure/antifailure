import { PageShell, RelatedGrid } from "@/components/pages/kit";
import { FailClosedScene } from "@/components/home/visuals/FailClosedScene";
import { CircularMap, DashChart, FeatureRow, Notebook, SplitHero, TaskTable } from "./well";

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
        visual={
          <Notebook
            overlaySide="right"
            tab="Side-Effect Firewall"
            rail="NOTES"
            overlay={{
              title: "Fail closed",
              checks: [
                "Payment creation is stored in a clone-local ledger. Nothing charges.",
                "SendGrid and similar sinks render and store. Nothing is delivered.",
                "Unknown processors and production API hostnames are blocked and ledgered.",
                "Every outbound attempt is recorded, including denies.",
                "Charging a live processor is not a warning. It is a failed containment model.",
              ],
            }}
          >
            <div className="overflow-hidden">
              <FailClosedScene />
            </div>
          </Notebook>
        }
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
        visual={
          <DashChart
            popupSide="left"
            title="Attempted-effect ledger"
            bars={[18, 24, 30, 42, 55, 48, 70, 62, 80, 74]}
            popup={{
              title: "Containment",
              rows: [
                ["Stripe", "Simulated"],
                ["Email", "Captured"],
                ["Hostname", "Blocked"],
              ],
            }}
          />
        }
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
        visual={
          <CircularMap
            shift="left"
            tabs={["MOCK", "CAPTURE", "BLOCK"]}
            active="BLOCK"
            rings={[
              { label: "Stripe", r: 40 },
              { label: "Email", r: 32 },
              { label: "Slack", r: 36 },
              { label: "prod DNS", r: 28 },
            ]}
          />
        }
      />

      <FeatureRow
        kicker="Existential failure"
        title="Not a warning. A failed containment model."
        items={[
          { title: "There is no warning level for this", body: "A twin that reaches a live processor has not failed a check. Its containment did not hold." },
          { title: "The customer finds out", body: "A real card, a real inbox and a real partner endpoint are the three places a contained run becomes somebody else's incident." },
          { title: "So the default refuses", body: "A host with no rule against it is blocked, which is the only default that stays safe as the integration list grows." },
        ]}
        visual={
          <TaskTable
            shift="left"
            heading="Processors · must never"
            rows={[
              { task: "Stripe payment", status: "Simulated", tone: "PASS", who: "F", date: "ledger" },
              { task: "SendGrid email", status: "Captured", tone: "WARN", who: "E", date: "never" },
              { task: "Slack webhook", status: "Preview", tone: "WARN", who: "S", date: "store" },
              { task: "Production hostname", status: "BLOCK", tone: "BLOCK", who: "G", date: "deny" },
            ]}
          />
        }
      />

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
