import type { ReactNode } from "react";
import { cn } from "@/lib/cn";

function FigureFrame({
  id,
  tab,
  rail,
  caption,
  children,
  className,
}: {
  id: string;
  tab: string;
  rail: string;
  caption: string;
  children: ReactNode;
  className?: string;
}) {
  const titleId = `${id.toLowerCase()}-title`;

  return (
    <figure
      aria-labelledby={titleId}
      className="relative w-full overflow-hidden rounded-[20px] bg-[#dcece3] p-3 sm:p-5"
    >
      <div
        aria-hidden="true"
        className="pointer-events-none absolute inset-0 opacity-55"
        style={{
          backgroundImage: "radial-gradient(circle, rgba(40,93,73,0.24) 0.75px, transparent 0.9px)",
          backgroundSize: "15px 15px",
          maskImage: "linear-gradient(to bottom, black, rgba(0,0,0,.35) 72%, transparent)",
        }}
      />
      <div className="relative overflow-hidden rounded-[12px] border border-black/[0.09] bg-[#fcfcfa] shadow-[0_18px_42px_rgba(23,58,45,0.10),0_2px_8px_rgba(0,0,0,0.04)]">
        <header className="flex min-w-0 items-center justify-between gap-3 border-b border-black/[0.07] bg-[#f5f6f2] px-3 py-2.5">
          <div
            id={titleId}
            className="min-w-0 text-[13px] font-semibold leading-5 tracking-extra-tight text-[#285D49]"
          >
            <span className="block truncate">{tab}</span>
          </div>
          <div className="flex shrink-0 items-baseline gap-2">
            <span className="font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-black">
              {rail}
            </span>
            <span className="hidden font-mono text-[10px] uppercase tracking-[0.12em] text-black/35 sm:inline">
              FIG. {id}
            </span>
          </div>
        </header>
        <div className={cn("p-3 sm:p-4", className)}>{children}</div>
      </div>
      <figcaption className="sr-only">{caption}</figcaption>
    </figure>
  );
}

function Eyebrow({ children, tone = "muted" }: { children: ReactNode; tone?: "muted" | "green" | "red" }) {
  return (
    <span
      className={cn(
        "font-mono text-[10px] font-medium uppercase tracking-[0.12em]",
        tone === "muted" && "text-black/42",
        tone === "green" && "text-[#285D49]",
        tone === "red" && "text-[#B44848]",
      )}
    >
      {children}
    </span>
  );
}

function Status({ children, tone }: { children: ReactNode; tone: "pass" | "block" | "neutral" }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 font-mono text-[10px] font-medium uppercase tracking-[0.1em]",
        tone === "pass" && "text-[#285D49]",
        tone === "block" && "text-[#B44848]",
        tone === "neutral" && "text-black/55",
      )}
    >
      <span
        aria-hidden="true"
        className={cn(
          "size-1.5 rounded-full",
          tone === "pass" && "bg-[#33bf00]",
          tone === "block" && "bg-[#B44848]",
          tone === "neutral" && "bg-black/25",
        )}
      />
      {children}
    </span>
  );
}

function Arrow({ vertical = false, muted = false }: { vertical?: boolean; muted?: boolean }) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "relative block shrink-0",
        vertical ? "h-5 w-px" : "h-px min-w-3 flex-1",
        muted ? "bg-black/12" : "bg-[#285D49]/35",
      )}
    >
      <span
        className={cn(
          "absolute size-1.5 rotate-45 border-[#285D49]/45",
          vertical ? "-bottom-px -left-[3px] border-r border-b" : "-right-px -top-[3px] border-t border-r",
          muted && "border-black/20",
        )}
      />
    </span>
  );
}

function SystemNode({
  eyebrow,
  title,
  note,
  tone = "plain",
  className,
}: {
  eyebrow: string;
  title: string;
  note?: string;
  tone?: "plain" | "mint" | "blocked" | "dark";
  className?: string;
}) {
  return (
    <div
      className={cn(
        "min-w-0 border p-3",
        tone === "plain" && "border-black/[0.08] bg-white",
        tone === "mint" && "border-[#285D49]/20 bg-[#e8f2ed]",
        tone === "blocked" && "border-[#B44848]/20 bg-[#f9ecea]",
        tone === "dark" && "border-black bg-black text-white",
        className,
      )}
    >
      <Eyebrow tone={tone === "blocked" ? "red" : tone === "dark" ? "muted" : "green"}>{eyebrow}</Eyebrow>
      <div className={cn("mt-1 text-[13px] font-semibold leading-5 tracking-extra-tight", tone === "dark" ? "text-white" : "text-black")}>
        {title}
      </div>
      {note ? (
        <p className={cn("mt-1 text-[12px] leading-5 tracking-extra-tight", tone === "dark" ? "text-white/58" : "text-black/56")}>
          {note}
        </p>
      ) : null}
    </div>
  );
}

function Rule({ label, value, blocked = false }: { label: string; value: string; blocked?: boolean }) {
  return (
    <div className="flex min-w-0 items-center justify-between gap-3 border-t border-black/[0.07] px-3 py-2.5 first:border-t-0">
      <span className="min-w-0 text-[12px] leading-5 tracking-extra-tight text-black/60">{label}</span>
      <span
        className={cn(
          "max-w-[54%] text-right font-mono text-[10px] leading-4 [overflow-wrap:anywhere]",
          blocked ? "text-[#B44848]" : "text-[#285D49]",
        )}
      >
        {value}
      </span>
    </div>
  );
}

export function PTW01() {
  return (
    <FigureFrame
      id="P-TW-01"
      tab="one disposable twin"
      rail="TOPOLOGY"
      caption="Topology of one disposable twin: candidate code and sanitized state enter an isolated run boundary, live production routes are blocked, evidence leaves, and the environment is destroyed."
    >
      <div className="grid gap-3 sm:grid-cols-[0.88fr_auto_1.12fr] sm:items-center">
        <div className="grid gap-2">
          <SystemNode eyebrow="input" title="PR image" note="Built candidate code." />
          <SystemNode eyebrow="input" title="Sanitized state" note="Production-shaped, not production." />
        </div>
        <div className="hidden items-center sm:flex"><Arrow /></div>
        <section aria-label="Isolated twin boundary" className="border-2 border-[#285D49]/35 bg-[#f1f7f4] p-4">
          <div className="flex items-center justify-between gap-2">
            <Eyebrow tone="green">disposable twin</Eyebrow>
            <Status tone="pass">sealed</Status>
          </div>
          <div className="mt-3 grid gap-2 sm:grid-cols-2">
            <SystemNode eyebrow="runtime" title="Candidate app" tone="dark" note="Runs inside the twin." />
            <SystemNode eyebrow="database" title="Safe Postgres" tone="mint" note="Restored for this run only." />
            <SystemNode eyebrow="identity" title="Scoped secrets" tone="mint" note="Live credentials are replaced." />
            <SystemNode eyebrow="egress" title="Simulator route" tone="mint" note="External effects are local or simulated." />
          </div>
          <div className="mt-3 border-t border-[#285D49]/20 pt-3">
            <div className="flex items-center justify-between gap-3">
              <span className="text-[12px] font-medium text-black">Resource journal</span>
              <span className="font-mono text-[10px] text-[#285D49]">append on create</span>
            </div>
          </div>
        </section>
      </div>

      <div className="mt-3 grid gap-3 sm:grid-cols-[1fr_auto_1fr] sm:items-stretch">
        <div className="overflow-hidden border border-black/[0.07] bg-[#f6f6f3]">
          <Rule label="Production database" value="NO ROUTE" blocked />
          <Rule label="Production credentials" value="REPLACED" blocked />
          <Rule label="Default public egress" value="DENY" blocked />
        </div>
        <div className="hidden items-center sm:flex"><Arrow /></div>
        <div className="border border-[#285D49]/20 bg-[#e4f1eb] p-3">
          <div className="flex items-center justify-between gap-2">
            <div>
              <Eyebrow tone="green">only durable output</Eyebrow>
              <div className="mt-1 text-[13px] font-semibold text-black">Pull-request evidence</div>
            </div>
            <Status tone="pass">report</Status>
          </div>
          <p className="mt-2 text-[12px] leading-5 text-black/58">Rows, traces, invariant results, and cleanup proof leave the boundary. The twin does not.</p>
        </div>
      </div>

      {/* Two columns before sm. Four cells of "01 Validate" have a min-content
          of 243px and the track at 320px is 228px, so the fourth stage was cut
          off by this element's own overflow-hidden. The separators are the
          gap showing the container through, which is the one border rule that
          stays correct when the column count changes. */}
      <ol aria-label="Twin run sequence" className="mt-3 grid grid-cols-2 gap-px overflow-hidden border border-black/[0.07] bg-black/[0.07] sm:grid-cols-4">
        {[
          ["01", "Build"],
          ["02", "Restore"],
          ["03", "Validate"],
          ["04", "Destroy"],
        ].map(([number, label], index) => (
          <li key={label} className={cn("min-w-0 px-2 py-2", index === 3 ? "bg-[#e4f1eb]" : "bg-white")}>
            <span className="font-mono text-[10px] text-black/35">{number}</span>
            <span className="ml-1.5 text-[12px] font-medium text-black">{label}</span>
          </li>
        ))}
      </ol>
    </FigureFrame>
  );
}

type LifecycleState = {
  event: string;
  title: string;
  detail: string;
  tone?: "pass" | "neutral";
};

const SUCCESS_STATES: LifecycleState[] = [
  { event: "env.creating", title: "Plan + provision", detail: "Lock held; resources journaled." },
  { event: "env.ready", title: "Containment verified", detail: "Candidate can accept workload." },
  { event: "env.destroying", title: "Reverse replay", detail: "Journal drives teardown." },
  { event: "env.destroyed", title: "Terminal success", detail: "Cleanup count is complete.", tone: "pass" },
];

function StateCard({ state, index }: { state: LifecycleState; index: number }) {
  return (
    <div className={cn("min-w-0 border p-3", state.tone === "pass" ? "border-[#285D49]/20 bg-[#e4f1eb]" : "border-black/[0.08] bg-white")}>
      <div className="flex items-center justify-between gap-2">
        <span className="font-mono text-[10px] text-black/35">0{index + 1}</span>
        <span className={cn("size-1.5 rounded-full", state.tone === "pass" ? "bg-[#33bf00]" : "bg-black/24")} aria-hidden="true" />
      </div>
      <div className="mt-1.5 font-mono text-[11px] leading-4 text-black [overflow-wrap:anywhere]">{state.event}</div>
      <div className="mt-1 text-[13px] font-semibold leading-5 text-black">{state.title}</div>
      <p className="mt-1 text-[12px] leading-5 text-black/54">{state.detail}</p>
    </div>
  );
}

export function PTW02() {
  return (
    <FigureFrame
      id="P-TW-02"
      tab="environment event log"
      rail="STATE"
      caption="Environment state machine showing the successful lifecycle from creating to destroyed, plus env.failed as a terminal event reachable from any earlier state."
    >
      <div className="flex items-center justify-between gap-3">
        <div>
          <Eyebrow>observable success path</Eyebrow>
          <p className="mt-1 text-[12px] leading-5 text-black/56">Four normal events; failure can terminate the same journaled run.</p>
        </div>
        <Status tone="neutral">NDJSON</Status>
      </div>

      <ol className="mt-3 space-y-2 sm:hidden" aria-label="Successful environment lifecycle">
        {SUCCESS_STATES.map((state, index) => (
          <li key={state.event}>
            <StateCard state={state} index={index} />
            {index < SUCCESS_STATES.length - 1 ? <div className="flex justify-center py-1"><Arrow vertical /></div> : null}
          </li>
        ))}
      </ol>
      <ol className="mt-3 hidden grid-cols-[1fr_auto_1fr_auto_1fr_auto_1fr] items-stretch gap-1.5 sm:grid" aria-label="Successful environment lifecycle">
        {SUCCESS_STATES.map((state, index) => (
          <li key={state.event} className="contents">
            <StateCard state={state} index={index} />
            {index < SUCCESS_STATES.length - 1 ? <div className="flex items-center"><Arrow /></div> : null}
          </li>
        ))}
      </ol>

      <div className="relative mt-3 border border-[#B44848]/20 bg-[#f9ecea] p-3">
        <div className="absolute -top-3 left-6 h-3 border-l border-dashed border-[#B44848]/40" aria-hidden="true" />
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="min-w-0">
            <Eyebrow tone="red">terminal from any prior state</Eyebrow>
            <div className="mt-1 font-mono text-[12px] text-[#8f3535]">env.failed</div>
          </div>
          <Status tone="block">stop</Status>
        </div>
        <p className="mt-2 max-w-[520px] text-[12px] leading-5 text-black/56">Failure is emitted where the run stops. The existing journal remains the recovery source for teardown.</p>
      </div>

      <div className="mt-3 grid grid-cols-3 overflow-hidden border border-black/[0.07] bg-[#f6f6f3]">
        {[
          ["transition", "event emitted"],
          ["resource", "journal append"],
          ["retry", "same run lock"],
        ].map(([label, value], index) => (
          <div key={label} className={cn("min-w-0 p-2", index > 0 && "border-l border-black/[0.07]")}>
            <Eyebrow>{label}</Eyebrow>
            <div className="mt-1 text-[11px] leading-4 text-black/60">{value}</div>
          </div>
        ))}
      </div>
    </FigureFrame>
  );
}

type IsolationItem = {
  kicker: string;
  title: string;
  body?: string;
  node?: string;
};

export function PTW03({ items }: { items: readonly IsolationItem[] }) {
  return (
    <FigureFrame
      id="P-TW-03"
      tab="containment specification"
      rail="BOUNDARY"
      caption="Containment boundary showing clone-local DNS, twin-scoped secrets, safe Postgres and a simulator gateway, with production database, live credentials and default internet routes explicitly blocked."
      className="sm:p-3"
    >
      <div className="grid gap-3 sm:grid-cols-[1.08fr_.92fr]">
        <section aria-label="Twin network boundary" className="min-w-0 border-2 border-[#285D49]/30 bg-[#edf5f1] p-4">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <Eyebrow tone="green">twin boundary</Eyebrow>
            <Status tone="block">fail closed</Status>
          </div>

          <div className="mt-3 grid grid-cols-[1fr_auto_1fr] items-center gap-2">
            <SystemNode eyebrow="workload" title="Declared journeys" note="Synthetic but production-shaped." />
            <Arrow />
            <SystemNode eyebrow="runtime" title="Candidate app" tone="dark" note="Can only see twin-scoped dependencies." />
          </div>
          <div className="mt-3 grid grid-cols-2 gap-2">
            <SystemNode eyebrow="resolve" title="Clone-local DNS" tone="mint" />
            <SystemNode eyebrow="outbound" title="Simulator gateway" tone="mint" />
            <SystemNode eyebrow="state" title="Safe Postgres" tone="mint" />
            <SystemNode eyebrow="identity" title="Twin secrets" tone="mint" />
          </div>
          <div className="mt-3 border-t border-[#285D49]/20 pt-3">
            <div className="flex items-center justify-between gap-3">
              <span className="text-[12px] text-black/60">Every resource</span>
              <span className="font-mono text-[10px] text-[#285D49]">environment label required</span>
            </div>
          </div>

          <div className="mt-3 overflow-hidden border border-[#B44848]/20 bg-[#f9ecea]">
            <Rule label="Production database" value="ROUTE CUT" blocked />
            <Rule label="Live credentials" value="REPLACED" blocked />
            <Rule label="Unlisted destination" value="DEFAULT DENY" blocked />
          </div>
        </section>

        <section aria-label="Isolation controls" className="min-w-0 overflow-hidden border border-black/[0.08] bg-white">
          <div className="flex items-center justify-between gap-2 bg-[#f5f6f2] px-2.5 py-2">
            <Eyebrow>control register</Eyebrow>
            <span className="font-mono text-[10px] text-black/35">{items.length} enforced</span>
          </div>
          <ol>
            {items.map((item, index) => (
              <li key={item.kicker} className="grid grid-cols-[24px_1fr] gap-2 border-t border-black/[0.07] px-3 py-2.5 first:border-t-0">
                <span className="pt-px font-mono text-[10px] text-black/32">{String(index + 1).padStart(2, "0")}</span>
                <div className="min-w-0">
                  <div className="flex flex-wrap items-baseline justify-between gap-x-2 gap-y-0.5">
                    <span className="text-[12px] font-semibold leading-5 text-black">{item.kicker}</span>
                    {item.node ? <span className="max-w-full font-mono text-[10px] leading-4 text-[#285D49] [overflow-wrap:anywhere]">{item.node}</span> : null}
                  </div>
                  <p className="mt-0.5 text-[11px] leading-4 text-black/54">{item.title}</p>
                </div>
              </li>
            ))}
          </ol>
        </section>
      </div>
    </FigureFrame>
  );
}

function CommandStep({
  command,
  title,
  detail,
  last = false,
}: {
  command: string;
  title: string;
  detail: string;
  last?: boolean;
}) {
  return (
    <li className="relative grid grid-cols-[34px_1fr] gap-3 pb-4 last:pb-0">
      {!last ? <span aria-hidden="true" className="absolute top-8 bottom-0 left-[16px] w-px bg-black/12" /> : null}
      <span className="relative flex size-[34px] items-center justify-center border border-black/10 bg-white font-mono text-[12px] text-black">$</span>
      <div className="min-w-0 pt-0.5">
        <div className="flex flex-wrap items-baseline justify-between gap-x-2">
          <code className="font-mono text-[12px] font-medium text-black">{command}</code>
          <span className="font-mono text-[10px] uppercase tracking-[0.1em] text-[#285D49]">{title}</span>
        </div>
        <p className="mt-1 text-[12px] leading-5 text-black/54">{detail}</p>
      </div>
    </li>
  );
}

export function PTW04() {
  return (
    <FigureFrame
      id="P-TW-04"
      tab="one CI run"
      rail="DECIDE"
      caption="CI decision flow in which af up creates the twin, af ci collects rows and traces into a pull-request gate, and af down destroys the environment; the preview URL is explicitly temporary."
    >
      <div className="grid gap-3 sm:grid-cols-[.88fr_1.12fr]">
        <section aria-label="Run commands" className="border border-black/[0.08] bg-[#f6f6f3] p-4">
          <div className="flex items-center justify-between gap-2">
            <Eyebrow>orchestration</Eyebrow>
            <Status tone="neutral">local</Status>
          </div>
          <ol className="mt-3">
            <CommandStep command="af up" title="create" detail="Build, restore, isolate, verify." />
            <CommandStep command="af ci" title="judge" detail="Run workloads and invariants." />
            <CommandStep command="af down" title="destroy" detail="Replay the resource journal." last />
          </ol>
        </section>

        <section aria-label="CI outputs" className="overflow-hidden border border-black/[0.08] bg-white">
          <div className="border-b border-black/[0.07] bg-[#f5f6f2] px-3 py-2">
            <div className="flex min-w-0 items-center gap-2">
              <span aria-hidden="true" className="size-1.5 shrink-0 rounded-full bg-black/25" />
              <span className="min-w-0 truncate font-mono text-[11px] text-black/50">127.0.0.1 · loopback-only preview</span>
              <span className="ml-auto shrink-0 font-mono text-[10px] text-[#B44848]">TEMPORARY</span>
            </div>
          </div>
          <div className="p-3">
            <Eyebrow tone="green">evidence bundle</Eyebrow>
            {/* "invariants" is one word and cannot wrap, so three of these
                cells had a min-content of 210px against a 202px track and the
                third was clipped at 320px. The type and padding step up at sm
                rather than the columns changing, because three named artifacts
                read wrong in two columns. */}
            <div className="mt-2 grid grid-cols-3 gap-px overflow-hidden border border-black/[0.07] bg-black/[0.07]">
              {["rows", "traces", "invariants"].map((item) => (
                <div key={item} className="min-w-0 bg-[#f7f7f4] px-1 py-2.5 text-center font-mono text-[10px] text-black/60 sm:px-2 sm:text-[11px]">{item}</div>
              ))}
            </div>
            <div className="my-2.5 flex justify-center"><Arrow vertical /></div>
            <div className="border border-[#285D49]/20 bg-[#e4f1eb] p-3">
              <div className="flex items-center justify-between gap-2">
                <div>
                  <Eyebrow tone="green">pull request</Eyebrow>
                  <div className="mt-1 text-[13px] font-semibold text-black">Safety gate</div>
                </div>
                <div className="flex gap-1">
                  <Status tone="pass">pass</Status>
                  <Status tone="block">fail</Status>
                </div>
              </div>
            </div>
            <div className="mt-2 flex items-center justify-between gap-3 border border-black/[0.07] px-3 py-2.5">
              <span className="text-[12px] text-black/58">After the report</span>
              <span className="font-mono text-[10px] text-[#285D49]">environment destroyed</span>
            </div>
          </div>
        </section>
      </div>

      <div className="mt-3 flex flex-wrap items-center justify-between gap-2 bg-black px-3 py-2.5 text-white">
        <span className="text-[12px] leading-5 text-white/68">The local route disappears.</span>
        <span className="font-mono text-[10px] uppercase tracking-[0.1em] text-white">The gate remains.</span>
      </div>
    </FigureFrame>
  );
}

const JOURNAL_ROWS = [
  ["01", "network", "isolated network"],
  ["02", "volume", "database state"],
  ["03", "container", "candidate app"],
  ["04", "container", "side-effect simulator"],
] as const;

function JournalList({ reverse = false }: { reverse?: boolean }) {
  const rows = reverse ? [...JOURNAL_ROWS].reverse() : JOURNAL_ROWS;
  return (
    <ol className="mt-2 overflow-hidden border border-black/[0.07] bg-white">
      {rows.map(([number, kind, label], index) => (
        <li key={`${reverse ? "delete" : "create"}-${number}`} className={cn("grid grid-cols-[28px_1fr_auto] items-center gap-2 px-3 py-2.5", index > 0 && "border-t border-black/[0.07]", reverse && "bg-[#f3f8f5]")}>
          <span className="font-mono text-[10px] text-black/35">{number}</span>
          <span className="min-w-0 text-[12px] leading-5 text-black/68">{label}</span>
          <span className={cn("font-mono text-[10px] uppercase tracking-[0.08em]", reverse ? "text-[#285D49]" : "text-black/40")}>
            {reverse ? "delete" : kind}
          </span>
        </li>
      ))}
    </ol>
  );
}

export function PTW05() {
  return (
    <FigureFrame
      id="P-TW-05"
      tab="resource journal"
      rail="CLEANUP"
      caption="Cleanup proof showing resources appended to a journal in creation order, filtered by the environment label, deleted in reverse order, and verified by zero-resource assertions."
    >
      <div className="grid gap-3 sm:grid-cols-[1fr_auto_1fr] sm:items-stretch">
        <section aria-label="Journal creation order" className="border border-black/[0.08] bg-[#f6f6f3] p-3">
          <div className="flex items-center justify-between gap-2">
            <Eyebrow>append immediately</Eyebrow>
            <span className="font-mono text-[10px] text-black/40">create ↓</span>
          </div>
          <JournalList />
        </section>
        <div className="hidden items-center sm:flex"><Arrow /></div>
        <section aria-label="Journal deletion order" className="border border-[#285D49]/20 bg-[#e4f1eb] p-3">
          <div className="flex items-center justify-between gap-2">
            <Eyebrow tone="green">reverse replay</Eyebrow>
            <span className="font-mono text-[10px] text-[#285D49]">destroy ↑</span>
          </div>
          <JournalList reverse />
        </section>
      </div>

      <div className="mt-3 grid gap-2 sm:grid-cols-[1.1fr_.9fr]">
        <section aria-label="Ownership guard" className="border border-black/[0.08] bg-white p-3">
          <div className="flex items-center justify-between gap-2">
            <div>
              <Eyebrow>ownership guard</Eyebrow>
              <div className="mt-1 text-[13px] font-semibold text-black">Filter by this environment label</div>
            </div>
            <Status tone="pass">match</Status>
          </div>
          <p className="mt-2 text-[12px] leading-5 text-black/56">Teardown can enumerate only the containers, network, and volume created for this run.</p>
        </section>
        <section aria-label="Cleanup assertions" className="overflow-hidden border border-black/[0.08] bg-[#f6f6f3]">
          <div className="px-2.5 py-2"><Eyebrow>post-teardown assertions</Eyebrow></div>
          <Rule label="managed containers" value="COUNT == 0" />
          <Rule label="managed networks" value="COUNT == 0" />
          <Rule label="journal entries" value="ALL REPLAYED" />
        </section>
      </div>

      <div className="mt-2.5 flex flex-wrap items-center justify-between gap-2 border border-dashed border-black/15 bg-white px-3 py-2.5">
        <code className="font-mono text-[11px] text-black">af env prune --before &lt;cutoff&gt;</code>
        <span className="font-mono text-[10px] uppercase tracking-[0.1em] text-black/40">manual sweep · no automatic TTL</span>
      </div>
    </FigureFrame>
  );
}
