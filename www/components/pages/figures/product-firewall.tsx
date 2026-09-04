import type { ReactNode } from "react";
import { FloatWindow, SageWell } from "@/components/pages/solutions/well";
import { cn } from "@/lib/cn";

const MODES = ["MOCK", "CAPTURE", "DENY"] as const;

type Tone = "plain" | "sage" | "success" | "danger" | "muted";

const toneClasses: Record<Tone, string> = {
  plain: "border-black/[0.08] bg-white text-black",
  sage: "border-[#83B39F]/35 bg-[#E4F1EB] text-[#285D49]",
  success: "border-[#33bf00]/35 bg-[#eff8f3] text-[#285D49]",
  danger: "border-[#C43D3D]/25 bg-[#fbefef] text-[#9E3030]",
  muted: "border-black/[0.06] bg-[#f7f7f5] text-black/55",
};

function FigureChrome({ id, tab, rail }: { id: string; tab: string; rail: string }) {
  return (
    <div className="flex items-end justify-between gap-3 border-b border-black/[0.07] bg-[#f7f7f5] px-3 pt-2.5 sm:px-4">
      <div className="min-w-0 rounded-t-[9px] bg-[#CAE6D9] px-3 py-2 text-[11px] font-medium tracking-tight text-[#285D49] sm:text-[12px]">
        <span className="block truncate">{tab}</span>
      </div>
      <div className="mb-1.5 flex shrink-0 items-baseline gap-2.5">
        <span className="border-b-2 border-[#33bf00] pb-0.5 font-mono text-[9px] font-medium uppercase tracking-[0.14em] text-black sm:text-[10px]">
          {rail}
        </span>
        <span className="hidden font-mono text-[9px] uppercase tracking-[0.12em] text-black/35 sm:inline">
          FIG. {id}
        </span>
      </div>
    </div>
  );
}

function FirewallFigure({
  id,
  tab,
  rail,
  label,
  compact = false,
  children,
}: {
  id: string;
  tab: string;
  rail: string;
  label: string;
  compact?: boolean;
  children: ReactNode;
}) {
  const captionId = `${id.toLowerCase()}-caption`;
  return (
    <figure aria-labelledby={captionId} className="w-full min-w-0">
      <SageWell
        className={cn(
          "w-full !min-h-0 !px-3 !py-4 sm:!px-4 sm:!py-5",
          compact && "!rounded-[24px] !px-2.5 !py-3 sm:!px-3 sm:!py-4",
        )}
      >
        <FloatWindow className="w-full min-w-0 overflow-hidden !rounded-[13px]">
          <FigureChrome id={id} tab={tab} rail={rail} />
          <div className={cn("min-w-0 p-3 sm:p-4", compact && "p-2.5 sm:p-3")}>{children}</div>
        </FloatWindow>
      </SageWell>
      <figcaption id={captionId} className="sr-only">
        {label}
      </figcaption>
    </figure>
  );
}

function MicroLabel({ children, tone = "muted" }: { children: ReactNode; tone?: "muted" | "sage" | "danger" }) {
  return (
    <span
      className={cn(
        "font-mono text-[9px] font-medium uppercase tracking-[0.12em]",
        tone === "muted" && "text-black/45",
        tone === "sage" && "text-[#285D49]",
        tone === "danger" && "text-[#A73737]",
      )}
    >
      {children}
    </span>
  );
}

function StateChip({ children, tone = "plain" }: { children: ReactNode; tone?: Tone }) {
  return (
    <span
      className={cn(
        "inline-flex items-center whitespace-nowrap rounded-full border px-2.5 py-1 font-mono text-[9px] font-medium uppercase tracking-[0.1em]",
        toneClasses[tone],
      )}
    >
      {children}
    </span>
  );
}

function SectionTitle({
  eyebrow,
  title,
  tone = "muted",
}: {
  eyebrow: string;
  title: string;
  tone?: "muted" | "sage" | "danger";
}) {
  return (
    <div className="min-w-0">
      <MicroLabel tone={tone}>{eyebrow}</MicroLabel>
      <div className="mt-1 text-[13px] font-medium leading-5 tracking-tight text-black sm:text-[14px]">{title}</div>
    </div>
  );
}

function Direction({ danger = false }: { danger?: boolean }) {
  return (
    <div className="flex h-7 items-center justify-center sm:h-auto sm:min-h-[72px]" aria-hidden>
      <div
        className={cn(
          "relative h-full w-px sm:h-px sm:w-full",
          danger ? "border-l border-dashed border-[#C43D3D]/55 sm:border-t sm:border-l-0" : "bg-[#285D49]/35",
        )}
      >
        <span
          className={cn(
            "absolute -bottom-0.5 -left-[3px] size-2 rotate-45 border-r border-b sm:-right-0.5 sm:bottom-auto sm:left-auto sm:-top-[3px]",
            danger ? "border-[#C43D3D]" : "border-[#285D49]",
          )}
        />
      </div>
    </div>
  );
}

function Seal({ label, detail, tone = "sage" }: { label: string; detail: string; tone?: "sage" | "danger" }) {
  return (
    <div
      className={cn(
        "flex min-w-0 items-center gap-3 rounded-[9px] border bg-white px-3 py-2.5",
        tone === "sage" ? "border-[#83B39F]/30" : "border-[#C43D3D]/24 bg-[#fbefef]",
      )}
    >
      <span
        className={cn(
          "grid size-7 shrink-0 place-items-center rounded-full border",
          tone === "sage" ? "border-[#285D49]/30 bg-[#E4F1EB]" : "border-[#C43D3D]/25 bg-white",
        )}
        aria-hidden
      >
        <span className={cn("size-2 rounded-full", tone === "sage" ? "bg-[#285D49]" : "bg-[#C43D3D]")} />
      </span>
      <span className="min-w-0">
        <span className="block text-[12px] font-medium leading-4 text-black">{label}</span>
        <span className={cn("block truncate font-mono text-[10px] leading-4", tone === "sage" ? "text-[#285D49]" : "text-[#A73737]")}>
          {detail}
        </span>
      </span>
    </div>
  );
}

function SummaryMetric({ label, value, tone = "plain" }: { label: string; value: string; tone?: Tone }) {
  return (
    <div className={cn("min-w-0 rounded-[10px] border px-3 py-2.5 text-center", toneClasses[tone])}>
      <div className="font-mono text-[18px] leading-none text-black">{value}</div>
      <div className="mt-1 font-mono text-[9px] uppercase tracking-[0.1em] text-black/45">{label}</div>
    </div>
  );
}

export function PFW01() {
  return (
    <FirewallFigure
      id="P-FW-01"
      tab="containment map"
      rail="EGRESS"
      label="Side-effect containment architecture: application requests pass through clone-local resolution and a mandatory gateway before being simulated, captured, or blocked, with each gateway decision written to the attempted-effect ledger."
    >
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-black/[0.06] pb-3">
        <div className="flex min-w-0 items-center gap-3">
          <span className="grid size-8 shrink-0 place-items-center rounded-[8px] bg-black" aria-hidden>
            <svg viewBox="0 0 16 16" className="size-4" fill="none">
              <path d="M4 5.5h8M4 8h8M4 10.5h5" stroke="white" strokeWidth="1.3" strokeLinecap="round" />
            </svg>
          </span>
          <div className="min-w-0">
            <div className="truncate text-[14px] font-medium tracking-tight text-black">Fail-closed egress boundary</div>
            <div className="font-mono text-[10px] text-black/45">network namespace sealed; run 08f2</div>
          </div>
        </div>
        <StateChip tone="success">0 escaped</StateChip>
      </div>

      <div className="mt-4 grid min-w-0 grid-cols-1 items-stretch sm:grid-cols-[minmax(0,0.95fr)_28px_minmax(0,1.08fr)_28px_minmax(0,1fr)]">
        <section className="min-w-0 rounded-[12px] bg-[#f7f7f5] p-4" aria-label="Candidate application attempts">
          <SectionTitle eyebrow="candidate" title="Application attempts an external effect" />
          <div className="mt-4 space-y-2 font-mono text-[10px] leading-4 text-black/70">
            <div>POST api.stripe.com/v1/charges</div>
            <div>POST api.sendgrid.com/v3/mail/send</div>
            <div className="text-[#A73737]">TCP 18.4.2.9:443</div>
          </div>
        </section>

        <Direction />

        <section className="min-w-0 rounded-[12px] border border-[#83B39F]/35 bg-[#E4F1EB] p-4" aria-label="Mandatory egress gateway policy checks">
          <SectionTitle eyebrow="mandatory gateway" title="Resolve, inspect, then apply the first explicit rule" tone="sage" />
          <div className="mt-4 rounded-[9px] bg-white/75 px-3 py-2.5 font-mono text-[10px] leading-5 text-[#285D49]">
            Default posture: BLOCK. Unknown or ambiguous traffic fails closed.
          </div>
        </section>

        <Direction />

        <section className="min-w-0 rounded-[12px] border border-black/[0.07] bg-white p-4" aria-label="Contained outcomes">
          <SectionTitle eyebrow="contained outcomes" title="Mock, capture, or deny without public egress" />
          <div className="mt-4 space-y-2">
            <Seal label="Stripe pack" detail="MOCK; stateful response" />
            <Seal label="Mail inbox" detail="CAPTURE; rendered artifact" />
            <Seal label="Unknown host" detail="DENY; no route out" tone="danger" />
          </div>
        </section>
      </div>

      <div className="mt-3 grid min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-2 rounded-[10px] bg-black px-3 py-2.5 text-white">
        <MicroLabel tone="sage">attempted-effect ledger</MicroLabel>
        <div className="h-px min-w-0 bg-white/15" aria-hidden />
        <span className="font-mono text-[10px] text-white/70">every gateway decision is append-only</span>
      </div>
    </FirewallFigure>
  );
}

export function PFW02() {
  return (
    <FirewallFigure
      id="P-FW-02"
      tab="stripe.pack.local"
      rail="MOCK"
      compact
      label="A Stripe charge request is answered by the stateful clone-local pack, which returns a simulated charge and records the state transition without contacting Stripe."
    >
      <div className="flex items-center justify-between gap-3">
        <SectionTitle eyebrow="stateful offline pack" title="Stripe charge lifecycle" tone="sage" />
        <StateChip tone="success">contained</StateChip>
      </div>

      <div className="mt-3 grid min-w-0 grid-cols-1 items-stretch gap-2.5 sm:grid-cols-[minmax(0,1fr)_22px_minmax(0,1fr)]">
        <section className="min-w-0 rounded-[10px] bg-[#f7f7f5] p-3" aria-label="Charge request">
          <MicroLabel>request</MicroLabel>
          <div className="mt-2 font-mono text-[11px] leading-5 text-black">POST /v1/charges</div>
          <div className="mt-2 font-mono text-[10px] leading-5 text-black/55">amount 4900; usd; cus_sim_11</div>
        </section>

        <div className="flex items-center justify-center" aria-hidden>
          <span className="font-mono text-[14px] text-[#285D49] max-sm:rotate-90">-&gt;</span>
        </div>

        <section className="min-w-0 rounded-[10px] border border-[#83B39F]/35 bg-[#E4F1EB] p-3" aria-label="Simulated Stripe response">
          <MicroLabel tone="sage">mock response</MicroLabel>
          <div className="mt-2 font-mono text-[11px] leading-5 text-[#285D49]">200 OK; ch_sim_08f2</div>
          <div className="mt-2 font-mono text-[10px] leading-5 text-black/60">status succeeded; livemode false</div>
        </section>
      </div>

      <div className="mt-3 grid grid-cols-[auto_minmax(0,1fr)] items-center gap-3 rounded-[10px] border border-[#83B39F]/30 bg-[#eff8f3] px-3 py-2.5">
        <span className="grid size-7 place-items-center rounded-full bg-[#285D49] font-mono text-[12px] text-white" aria-hidden>
          +
        </span>
        <div className="min-w-0">
          <div className="truncate text-[12px] font-medium text-black">Clone-local state transition persisted</div>
          <div className="truncate font-mono text-[10px] text-black/45">cus_sim_11; charge.created; api.stripe.com never resolved</div>
        </div>
      </div>
    </FirewallFigure>
  );
}

export function PFW03() {
  return (
    <FirewallFigure
      id="P-FW-03"
      tab="capture inbox"
      rail="CAPTURE"
      compact
      label="A SendGrid API call is terminated inside the twin and rendered as a captured MIME message that can be inspected, but is never delivered."
    >
      <div className="flex items-center justify-between gap-3">
        <SectionTitle eyebrow="searchable artifact" title="Rendered, not delivered" tone="sage" />
        <StateChip tone="success">202 shape</StateChip>
      </div>

      <article className="mt-2.5 overflow-hidden rounded-[10px] border border-black/[0.08] bg-white" aria-label="Captured email Order 4182">
        <header className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-b border-black/[0.06] bg-[#f7f7f5] px-3 py-2.5">
          <div className="flex min-w-0 items-center gap-2">
            <span className="grid size-6 shrink-0 place-items-center rounded-[7px] bg-[#E4F1EB]" aria-hidden>
              <svg viewBox="0 0 18 18" className="size-3.5" fill="none">
                <rect x="2.5" y="4" width="13" height="10" rx="2" stroke="#285D49" strokeWidth="1.3" />
                <path d="m3.5 5 5.5 4.5L14.5 5" stroke="#285D49" strokeWidth="1.3" strokeLinejoin="round" />
              </svg>
            </span>
            <div className="min-w-0">
              <div className="truncate text-[12px] font-medium text-black">Order #4182</div>
              <div className="truncate font-mono text-[10px] text-black/45">msg_sim_2a91; multipart/alternative</div>
            </div>
          </div>
          <StateChip tone="danger">not sent</StateChip>
        </header>

        <dl className="grid grid-cols-[3.25rem_minmax(0,1fr)] gap-x-3 gap-y-1.5 border-b border-black/[0.06] px-3 py-2.5 font-mono text-[10px] leading-4">
          <dt className="text-black/35">from</dt>
          <dd className="min-w-0 truncate text-black/70">checkout@twin.local</dd>
          <dt className="text-black/35">to</dt>
          <dd className="min-w-0 truncate text-black/70">customer@example.test</dd>
          <dt className="text-black/35">subject</dt>
          <dd className="min-w-0 truncate text-black/70">Your order #4182</dd>
        </dl>

        <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-3 px-3 py-3">
          <div className="min-w-0">
            <div className="h-2 w-[72%] rounded-full bg-black/10" />
            <div className="mt-2 h-2 w-[92%] rounded-full bg-black/[0.07]" />
            <div className="mt-2 h-2 w-[56%] rounded-full bg-black/[0.07]" />
          </div>
          <div className="grid content-start gap-1" aria-label="Captured MIME parts">
            <span className="rounded-[6px] bg-[#f7f7f5] px-2 py-1 font-mono text-[9px] text-black/45">plain</span>
            <span className="rounded-[6px] bg-[#E4F1EB] px-2 py-1 font-mono text-[9px] text-[#285D49]">html</span>
          </div>
        </div>
      </article>

      <div className="mt-2.5 grid grid-cols-[auto_minmax(0,1fr)] items-center gap-2 rounded-[9px] bg-black px-3 py-2.5 text-white">
        <span className="size-1.5 rounded-full bg-[#33bf00]" aria-hidden />
        <span className="truncate font-mono text-[10px] text-white/70">POST /v3/mail/send returns provider-shaped 202; no delivery attempted.</span>
      </div>
    </FirewallFigure>
  );
}

export function PFW04() {
  const checks = [
    { label: "Resolve host", value: "example.com", tone: "plain" as const },
    { label: "Match explicit rule", value: "none", tone: "danger" as const },
    { label: "Apply default", value: "BLOCK", tone: "danger" as const },
    { label: "Write decision", value: "deny_02", tone: "sage" as const },
  ];
  return (
    <FirewallFigure
      id="P-FW-04"
      tab="policy trace"
      rail="BLOCK"
      compact
      label="An unlisted connection to example.com is evaluated by the gateway, matches no explicit rule, inherits the BLOCK default, and receives a denial receipt in the decision log."
    >
      <div className="flex items-start justify-between gap-3">
        <SectionTitle eyebrow="unknown destination" title="CONNECT example.com:443" tone="danger" />
        <StateChip tone="danger">refused</StateChip>
      </div>

      <ol className="mt-3 overflow-hidden rounded-[10px] border border-black/[0.08]" aria-label="Gateway policy evaluation trace">
        {checks.map((check, index) => (
          <li
            key={check.label}
            className={cn(
              "grid grid-cols-[28px_minmax(0,1fr)_auto] items-center gap-3 border-t border-black/[0.06] px-3 py-2.5 first:border-t-0",
              index === 2 ? "bg-[#fbefef]" : index === 3 ? "bg-[#eff8f3]" : "bg-white",
            )}
          >
            <span
              className={cn(
                "grid size-6 place-items-center rounded-full font-mono text-[9px]",
                check.tone === "danger" ? "bg-[#f4d9d9] text-[#A73737]" : check.tone === "sage" ? "bg-[#CAE6D9] text-[#285D49]" : "bg-[#f0f0ee] text-black/45",
              )}
            >
              {String(index + 1).padStart(2, "0")}
            </span>
            <span className="min-w-0 truncate text-[11px] font-medium text-black/75">{check.label}</span>
            <span
              className={cn(
                "shrink-0 font-mono text-[10px] font-medium",
                check.tone === "danger" ? "text-[#A73737]" : check.tone === "sage" ? "text-[#285D49]" : "text-black/45",
              )}
            >
              {check.value}
            </span>
          </li>
        ))}
      </ol>

      <div className="mt-3 rounded-[10px] bg-black px-3 py-2.5 font-mono text-[10px] leading-4 text-white/70">
        Socket not opened; 0 bytes out; denial receipt deny_02 recorded.
      </div>
    </FirewallFigure>
  );
}

const LEDGER_ROWS = [
  { method: "POST", target: "api.stripe.com/v1/charges", mode: "MOCK", receipt: "ch_sim_08f2", result: "handled" },
  { method: "POST", target: "api.sendgrid.com/v3/mail/send", mode: "CAPTURE", receipt: "msg_sim_2a91", result: "handled" },
  { method: "POST", target: "hooks.slack.com/services/T0/B0", mode: "CAPTURE", receipt: "req_sim_91c0", result: "handled" },
  { method: "POST", target: "api.openai.com/v1/chat/completions", mode: "MOCK", receipt: "mock_5b12", result: "handled" },
  { method: "GET", target: "api.prod.internal/v1/health", mode: "DENY", receipt: "deny_01", result: "blocked" },
  { method: "CONNECT", target: "example.com:443", mode: "DENY", receipt: "deny_02", result: "blocked" },
] as const;

function OutcomeDot({ blocked }: { blocked: boolean }) {
  return <span className={cn("inline-block size-1.5 shrink-0 rounded-full", blocked ? "bg-[#C43D3D]" : "bg-[#33bf00]")} aria-hidden />;
}

export function PFW05() {
  return (
    <FirewallFigure
      id="P-FW-05"
      tab="af net log; run 08f2"
      rail="LEDGER"
      label="Attempted-effect ledger containing six gateway decisions: four safely handled by mock or capture modes, two blocked, and zero requests escaping the twin."
    >
      <div className="grid grid-cols-3 gap-2">
        <SummaryMetric label="decisions" value="6" />
        <SummaryMetric label="blocked" value="2" tone="danger" />
        <SummaryMetric label="escaped" value="0" tone="success" />
      </div>

      <div className="mt-3 overflow-hidden rounded-[10px] border border-black/[0.08]">
        <div className="grid grid-cols-[minmax(0,1fr)_5.5rem_5.5rem] gap-3 bg-black px-3 py-2 font-mono text-[9px] font-medium uppercase tracking-[0.1em] text-white/60">
          <span>attempted effect</span>
          <span>mode</span>
          <span>receipt</span>
        </div>
        {LEDGER_ROWS.map((row, index) => {
          const blocked = row.result === "blocked";
          return (
            <div
              key={row.receipt}
              className={cn(
                "grid grid-cols-[minmax(0,1fr)_5.5rem_5.5rem] items-center gap-3 border-t border-black/[0.06] px-3 py-2.5 font-mono text-[10px]",
                blocked ? "bg-[#fbefef]" : index % 2 === 0 ? "bg-white" : "bg-[#f7f7f5]",
              )}
            >
              <span className="flex min-w-0 items-center gap-2 text-black/75" title={row.target}>
                <OutcomeDot blocked={blocked} />
                <span className="shrink-0 text-black/40">{row.method}</span>
                <span className="min-w-0 truncate">{row.target}</span>
              </span>
              <span className={blocked ? "text-[#A73737]" : "text-[#285D49]"}>{row.mode}</span>
              <span className="text-black/45">{row.receipt}</span>
            </div>
          );
        })}
      </div>

      <div className="mt-3 rounded-[10px] bg-[#eff8f3] px-3 py-2.5 font-mono text-[10px] leading-4 text-[#285D49]">
        {MODES.join(" / ")} are the only visible outcomes here. Every row represents an attempted effect; none escaped.
      </div>
    </FirewallFigure>
  );
}

function RouteNode({
  number,
  title,
  detail,
  tone = "plain",
}: {
  number: string;
  title: string;
  detail: string;
  tone?: Tone;
}) {
  return (
    <div className={cn("grid min-w-0 grid-cols-[28px_minmax(0,1fr)] items-center gap-3 rounded-[9px] border px-3 py-2.5", toneClasses[tone])}>
      <span className="grid size-7 place-items-center rounded-full border border-current/15 bg-white font-mono text-[9px]">{number}</span>
      <span className="min-w-0">
        <span className="block truncate text-[12px] font-medium text-black">{title}</span>
        <span className="block truncate font-mono text-[10px] text-black/45">{detail}</span>
      </span>
    </div>
  );
}

export function PFW06() {
  return (
    <FirewallFigure
      id="P-FW-06"
      tab="network namespace; route trace"
      rail="NO ROUTE"
      label="Comparison of a hostname request routed through clone-local DNS and the mandatory gateway with a direct-IP request stopped by the network namespace because no public default route exists. The direct-IP failure returns ENETUNREACH and creates no gateway ledger row."
    >
      <div className="flex flex-wrap items-start justify-between gap-3 border-b border-black/[0.06] pb-3">
        <SectionTitle eyebrow="isolation boundary" title="Two paths. Neither bypasses containment." />
        <StateChip tone="danger">default route none</StateChip>
      </div>

      <div className="mt-3 grid gap-3 lg:grid-cols-2">
        <section className="min-w-0 rounded-[12px] border border-[#83B39F]/35 bg-[#eff8f3] p-4" aria-label="Named host path through the gateway">
          <SectionTitle eyebrow="named host" title="Gateway path" tone="sage" />
          <div className="mt-3 space-y-2">
            <RouteNode number="01" title="Candidate application" detail="api.stripe.com:443" />
            <RouteNode number="02" title="Clone-local DNS" detail="stripe.pack.local" tone="sage" />
            <RouteNode number="03" title="Mandatory gateway" detail="MOCK; decision logged" tone="success" />
          </div>
          <div className="mt-3 flex items-center justify-between gap-2 rounded-[9px] bg-white px-3 py-2.5 font-mono text-[10px]">
            <span className="text-black/45">network result</span>
            <span className="text-[#285D49]">contained response</span>
          </div>
        </section>

        <section className="min-w-0 rounded-[12px] border border-[#C43D3D]/22 bg-[#fbefef] p-4" aria-label="Direct IP path stopped before the gateway">
          <SectionTitle eyebrow="raw address" title="Direct IP has no route" tone="danger" />
          <div className="mt-3 space-y-2">
            <RouteNode number="01" title="Candidate application" detail="TCP 18.4.2.9:443" />
            <RouteNode number="02" title="Route lookup" detail="0.0.0.0/0 -> none" tone="danger" />
            <div className="grid min-w-0 grid-cols-[28px_minmax(0,1fr)] items-center gap-3 rounded-[9px] border border-[#C43D3D]/25 bg-white px-3 py-2.5">
              <span className="grid size-7 place-items-center rounded-full bg-[#C43D3D] text-white" aria-hidden>
                <svg viewBox="0 0 16 16" className="size-3" fill="none">
                  <path d="m4 4 8 8m0-8-8 8" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
                </svg>
              </span>
              <span className="min-w-0">
                <span className="block truncate text-[12px] font-medium text-black">Public network unreachable</span>
                <span className="block truncate font-mono text-[10px] text-[#A73737]">ENETUNREACH; 0 bytes out</span>
              </span>
            </div>
          </div>
          <div className="mt-3 grid grid-cols-2 divide-x divide-black/[0.07] rounded-[9px] bg-white py-2.5 text-center">
            <div>
              <MicroLabel>gateway</MicroLabel>
              <div className="mt-1 font-mono text-[10px] text-black/55">not reached</div>
            </div>
            <div>
              <MicroLabel>ledger row</MicroLabel>
              <div className="mt-1 font-mono text-[10px] text-black/55">none</div>
            </div>
          </div>
        </section>
      </div>

      <div className="mt-3 flex items-center gap-3 rounded-[10px] bg-black px-3 py-2.5 text-white">
        <span className="grid size-6 shrink-0 place-items-center rounded-full border border-white/20" aria-hidden>
          <span className="size-2 rounded-full bg-[#33bf00]" />
        </span>
        <div className="min-w-0">
          <div className="text-[12px] font-medium">Containment is structural</div>
          <div className="truncate font-mono text-[10px] text-white/55">no DNS dependency; no editable bypass; no public route</div>
        </div>
      </div>
    </FirewallFigure>
  );
}
