import {
  Callout,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  RelatedGrid,
  Split,
} from "@/components/pages/kit";
import { Illustrative } from "@/components/layout/Illustrative";
import { FailClosedScene } from "@/components/home/visuals/FailClosedScene";
import {
  Hairline,
  MonoLabel,
  Panel,
  QueueChip,
  Receipt,
  StatusPill,
} from "@/components/home/visuals/primitives";

type LedgerTone = "PASS" | "FAIL";

type LedgerEntry = {
  method: string;
  dest: string;
  action: string;
  receipt: string;
  tone: LedgerTone;
  bypass?: boolean;
};

const LEDGER: LedgerEntry[] = [
  {
    method: "POST",
    dest: "api.stripe.com/v1/charges",
    action: "mock",
    receipt: "ch_sim_08f2",
    tone: "PASS",
  },
  {
    method: "POST",
    dest: "api.sendgrid.com/v3/mail/send",
    action: "capture",
    receipt: "msg_sim_2a91",
    tone: "PASS",
  },
  {
    method: "POST",
    dest: "hooks.slack.com/services/T0/B0",
    action: "capture",
    receipt: "req_sim_91c0",
    tone: "PASS",
  },
  {
    method: "POST",
    dest: "api.openai.com/v1/chat/completions",
    action: "mock",
    receipt: "mock_5b12",
    tone: "PASS",
  },
  {
    method: "GET",
    dest: "api.prod.internal/v1/health",
    action: "production-host",
    receipt: "deny_01",
    tone: "FAIL",
  },
  {
    method: "TCP",
    dest: "18.4.2.9:443",
    action: "DENY",
    receipt: "deny_02",
    tone: "FAIL",
    bypass: true,
  },
];

const FEATURED = [
  {
    provider: "Stripe",
    op: "POST /v1/charges",
    body: "Answered from the stateful pack that ships with the engine. Clone-local, not live.",
    tone: "PASS" as const,
    chip: "mock",
    receipt: (
      <>
        ch_sim_08f2
        <br />
        $49.00 · cus_sim_11
        <br />
        clone-local · not live
      </>
    ),
  },
  {
    provider: "SendGrid",
    op: "POST /v3/mail/send",
    body: "Render and capture. Never deliver.",
    tone: "PASS" as const,
    chip: "capture",
    receipt: (
      <>
        MIME · captured copy
        <br />
        Subject: Order #4182
        <br />
        NEVER DELIVERED
      </>
    ),
  },
  {
    provider: "Unknown TCP",
    op: "18.4.2.9:443",
    body: "Unknown destination. Deny by default.",
    tone: "FAIL" as const,
    chip: "DENY",
    receipt: (
      <>
        deny_02
        <br />
        direct-IP · ip-bypass
        <br />
        fail closed
      </>
    ),
  },
] as const;

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
        visual={<FailClosedScene />}
      />

      <PageSection>
        <PageHeading
          kicker="Attempted-effect ledger"
          title="<strong>Every outbound attempt is recorded, including the denials.</strong> Simulate, capture, mock, or deny. Never a live processor."
        />
        <ul className="mt-14 grid grid-cols-3 gap-5 max-xl:grid-cols-1">
          {FEATURED.map((item) => (
            <li key={item.provider}>
              <Panel className="flex h-full flex-col rounded-[12px] bg-white p-5">
                <div className="flex items-center justify-between gap-3">
                  <MonoLabel tone="reader">{item.provider}</MonoLabel>
                  <StatusPill tone={item.tone}>{item.tone}</StatusPill>
                </div>
                <div className="mt-4 font-mono text-[13px] tracking-extra-tight text-black">{item.op}</div>
                <p className="mt-2 text-[14px] leading-6 tracking-extra-tight text-gray-new-40">{item.body}</p>
                <div className="mt-5 flex flex-wrap items-center gap-2">
                  <QueueChip blocked={item.tone === "FAIL"}>{item.chip}</QueueChip>
                </div>
                <div className="mt-auto pt-5">
                  <Receipt>{item.receipt}</Receipt>
                </div>
              </Panel>
            </li>
          ))}
        </ul>

        <Panel className="mt-8 flex flex-col rounded-[12px] bg-white">
          <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-3 px-5 py-3">
            <MonoLabel>ATTEMPTED-EFFECT LEDGER</MonoLabel>
            <div className="flex flex-wrap items-center gap-5 font-mono text-[11px] tabular-nums tracking-extra-tight text-black/55">
              <span>
                rows <span className="text-black">{LEDGER.length}</span>
              </span>
              <span>
                escaped <span className="text-[#285D49]">0</span>
              </span>
              <span>
                critical <span className="text-red-700">2</span>
              </span>
              <StatusPill tone="PASS">FAIL CLOSED</StatusPill>
            </div>
          </div>
          <Hairline />
          <ul>
            {LEDGER.map((row) => (
              <li
                key={row.receipt}
                className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-black/[0.08] px-5 py-3 last:border-b-0"
              >
                <span className="w-10 shrink-0 font-mono text-[10px] tracking-extra-tight text-black/55">
                  {row.method}
                </span>
                <span className="min-w-0 flex-1 font-mono text-[12px] tracking-extra-tight text-black">
                  {row.dest}
                </span>
                <QueueChip blocked={row.bypass}>{row.action}</QueueChip>
                <StatusPill tone={row.tone}>{row.tone}</StatusPill>
                <span className="w-[88px] shrink-0 text-right font-mono text-[10px] tracking-extra-tight text-black/60">
                  {row.receipt}
                </span>
              </li>
            ))}
          </ul>
          <Hairline />
          <div className="flex flex-wrap items-center gap-2 px-5 py-3">
            <QueueChip>duplicate would have been live.</QueueChip>
            <QueueChip blocked>unknown destination · denied inside the twin</QueueChip>
          </div>
        </Panel>
        <Illustrative>
          Six rows chosen to show the five decisions. The hosts, the modes and the decision log are
          real: <code className="font-mono text-[12px] text-black/70">af net log</code> prints every
          attempt, and <code className="font-mono text-[12px] text-black/70">af ci</code> summarises
          them on the pull request. A denied destination is denied inside the twin; it does not on
          its own fail the check.
        </Illustrative>
      </PageSection>

      <PageSection tone="white">
        <Split
          visual={
            <Panel className="flex flex-col rounded-[12px] bg-white">
              <div className="flex items-center justify-between gap-3 px-5 py-3">
                <MonoLabel>BYPASS DETECTED</MonoLabel>
                <StatusPill tone="FAIL">DENY</StatusPill>
              </div>
              <Hairline />
              <div className="px-5 py-5">
                <div className="font-mono text-[13px] tracking-extra-tight text-black">TCP 18.4.2.9:443</div>
                <p className="mt-2 text-[14px] leading-6 tracking-extra-tight text-gray-new-40">
                  Direct-IP skip of clone-local DNS. Caught at the mandatory egress gateway. Denied. Logged.
                </p>
                <div className="mt-5 flex flex-wrap gap-2">
                  <QueueChip blocked>ip-bypass</QueueChip>
                  <QueueChip blocked>unknown TCP</QueueChip>
                  <QueueChip>no default public egress</QueueChip>
                </div>
                <Receipt className="mt-5 bg-white">
                  deny_02
                  <br />
                  source: twin egress slot
                  <br />
                  dest: 18.4.2.9:443
                  <br />
                  reason: unresolved · fail closed
                </Receipt>
              </div>
            </Panel>
          }
        >
          <PageHeading title="<strong>Bypass detection is not optional.</strong> Direct-IP attempts are caught and blocked." />
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

      <PageSection tone="sage">
        <PageHeading title="<strong>Charging a live processor is an existential failure.</strong> Not a warning. Not a retry." />
        <div className="mt-10 max-w-[720px]">
          <Callout label="Existential failure" tone="block">
            Charging cards, emailing users, or invoking production webhooks from a twin is a failed
            containment model. Read-only forwarding exists only for explicitly approved endpoints. Request
            and response redaction is mandatory. The ledger is the proof.
          </Callout>
        </div>
      </PageSection>

      <RelatedGrid
        items={[
          { href: "/product/architecture", title: "Architecture", description: "Control plane and customer data plane." },
          { href: "/product/report", title: "Safety Report", description: "Where the attempted effects are summarised." },
          { href: "/docs/concepts/egress", title: "Egress docs", description: "Controls and example behavior." },
        ]}
      />
    </PageShell>
  );
}
