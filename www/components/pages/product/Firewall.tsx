import {
  Callout,
  PageHeading,
  PageHero,
  PageSection,
  PageShell,
  RelatedGrid,
  Split,
} from "@/components/pages/kit";
import { FailClosedScene } from "@/components/home/visuals/FailClosedScene";
import {
  Hairline,
  MonoLabel,
  Panel,
  QueueChip,
  Receipt,
  StatusPill,
} from "@/components/home/visuals/primitives";

type LedgerTone = "PASS" | "BLOCK";

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
    action: "simulate",
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
    dest: "hooks.slack.com",
    action: "store",
    receipt: "evt_sim_91c0",
    tone: "PASS",
  },
  {
    method: "PUT",
    dest: "s3.amazonaws.com/prod-bucket → twin-bucket",
    action: "clone-bucket",
    receipt: "obj_sim_44",
    tone: "PASS",
  },
  {
    method: "GET",
    dest: "api.prod.internal/v1/health",
    action: "production-host",
    receipt: "deny_01",
    tone: "BLOCK",
  },
  {
    method: "TCP",
    dest: "18.4.2.9:443",
    action: "DENY",
    receipt: "deny_02",
    tone: "BLOCK",
    bypass: true,
  },
];

const FEATURED = [
  {
    provider: "Stripe",
    op: "POST /v1/charges",
    body: "Simulate and store in a clone-local ledger. Not live.",
    tone: "PASS" as const,
    chip: "simulate",
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
    tone: "BLOCK" as const,
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
  { title: "Stateful simulators", body: "Stripe, email, and webhooks keep clone-local ledgers." },
] as const;

export function FirewallPage() {
  return (
    <PageShell>
      <PageHero
        eyebrow="Side-Effect Firewall"
        title="The twin cannot act on the real world."
        lead="No default public egress. Clone-local DNS. Stateful provider simulators. Unknown destinations are blocked and written to the attempted-effect ledger."
        framed={false}
        visual={<FailClosedScene />}
      />

      <PageSection>
        <PageHeading
          kicker="Attempted-effect ledger"
          title="<strong>Every outbound action is recorded.</strong> Simulate, capture, or deny — never a live processor."
        />
        <ul className="mt-14 grid grid-cols-3 gap-5 max-lg:grid-cols-1">
          {FEATURED.map((item) => (
            <li key={item.provider}>
              <Panel className="flex h-full flex-col rounded-[12px] bg-white p-5">
                <div className="flex items-center justify-between gap-3">
                  <MonoLabel>{item.provider}</MonoLabel>
                  <StatusPill tone={item.tone}>{item.tone}</StatusPill>
                </div>
                <div className="mt-4 font-mono text-[13px] tracking-extra-tight text-black">{item.op}</div>
                <p className="mt-2 text-[14px] leading-6 tracking-extra-tight text-gray-new-40">{item.body}</p>
                <div className="mt-5 flex flex-wrap items-center gap-2">
                  <QueueChip blocked={item.tone === "BLOCK"}>{item.chip}</QueueChip>
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
          <Hairline className="block" />
          <ul>
            {LEDGER.map((row) => (
              <li
                key={row.receipt}
                className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-black/[0.08] px-5 py-3 last:border-b-0"
              >
                <span className="w-10 shrink-0 font-mono text-[10px] tracking-extra-tight text-black/40">
                  {row.method}
                </span>
                <span className="min-w-0 flex-1 font-mono text-[12px] tracking-extra-tight text-black">
                  {row.dest}
                </span>
                <QueueChip blocked={row.bypass}>{row.action}</QueueChip>
                <StatusPill tone={row.tone}>{row.tone}</StatusPill>
                <span className="w-[88px] shrink-0 text-right font-mono text-[10px] tracking-extra-tight text-black/45">
                  {row.receipt}
                </span>
              </li>
            ))}
          </ul>
          <Hairline className="block" />
          <div className="flex flex-wrap items-center gap-2 px-5 py-3">
            <QueueChip>duplicate would have been live.</QueueChip>
            <QueueChip blocked>unknown dest · run blocked by policy</QueueChip>
          </div>
        </Panel>
      </PageSection>

      <PageSection tone="white">
        <Split
          visual={
            <Panel className="flex flex-col rounded-[12px] bg-white">
              <div className="flex items-center justify-between gap-3 px-5 py-3">
                <MonoLabel>BYPASS DETECTED</MonoLabel>
                <StatusPill tone="BLOCK">BLOCK</StatusPill>
              </div>
              <Hairline className="block" />
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
          <ul className="mt-10 grid grid-cols-2 gap-x-8 gap-y-8 max-sm:grid-cols-1">
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
          { href: "/security", title: "Security", description: "Fail closed is a product principle." },
          { href: "/product/oracle", title: "Differential Oracle", description: "Third-party effects are compared." },
          { href: "/docs/firewall", title: "Firewall docs", description: "Controls and example behavior." },
        ]}
      />
    </PageShell>
  );
}
