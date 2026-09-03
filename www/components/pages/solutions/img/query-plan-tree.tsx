import { cn } from "@/lib/cn";
import { FloatWindow, SageWell } from "../well";

export function QueryPlanTree() {
  return (
    <SageWell compact>
      <div className="flex h-[332px] min-h-0 w-full flex-col gap-2 max-md:h-[268px] md:flex-row md:items-stretch">
        <FloatWindow className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
          <header className="flex items-center justify-between gap-3 bg-[#f7f7f5] px-3 py-2 sm:px-3.5">
            <div className="flex min-w-0 items-center gap-2">
              <span className="size-1.5 shrink-0 rounded-full bg-[#33bf00]" aria-hidden />
              <div className="min-w-0 truncate font-mono text-[11px] tracking-extra-tight text-black">
                EXPLAIN ANALYZE · events
              </div>
            </div>
            <span className="shrink-0 font-mono text-[10px] tabular-nums text-gray-new-40">
              12,403,881 rows
            </span>
          </header>
          <div className="h-0.5 bg-[#CAE6D9]" aria-hidden />

          <div className="grid min-h-0 flex-1 grid-cols-[minmax(0,1fr)_12px_minmax(0,1fr)] bg-[#f7f7f5]">
            <div className="flex min-h-0 min-w-0 flex-col bg-[#dceee6] px-2 pb-2 sm:px-2.5">
              <ColHead ink="sage">Baseline</ColHead>
              <LimitNode
                time="12.00ms"
                pct={3}
                cost="0.43..8.43"
                rows="1"
                tone="pass"
              />
              <ScanNode
                title="Index Scan"
                rel="events_created_at_idx"
                cond="Index Cond: (created_at > $1)"
                time="12ms"
                pct={3}
                cost="0.43..8.42"
                rows="1"
                tone="pass"
              />
            </div>

            <Spine />

            <div className="flex min-h-0 min-w-0 flex-col bg-[#f8e4e4]/80 px-2 pb-2 sm:px-2.5">
              <ColHead ink="block">Candidate</ColHead>
              <LimitNode
                time="410.12ms"
                pct={100}
                cost="0.00..184102"
                rows="12.4M"
                tone="block"
              />
              <ScanNode
                title="Seq Scan"
                rel="events"
                cond="Filter: (created_at > $1)"
                time="410ms"
                pct={100}
                cost="0.00..184102.00"
                rows="12.4M"
                tone="block"
              />
            </div>
          </div>

          <footer className="flex items-center justify-between gap-2 border-t border-black/[0.06] bg-[#f4edd6] px-3 py-1.5 sm:px-3.5">
            <p className="min-w-0 truncate font-mono text-[10px] leading-4 tracking-extra-tight text-black sm:text-[11px] sm:leading-5">
              lock ACCESS EXCLUSIVE · 4.2s · another session waiting
            </p>
            <span className="hidden shrink-0 rounded-full bg-white px-2 py-0.5 font-mono text-[9px] uppercase tracking-[0.12em] text-[#8A6A12] sm:inline">
              ACCESS EXCLUSIVE
            </span>
          </footer>
        </FloatWindow>

        <aside className="flex shrink-0 justify-center max-md:w-full md:w-[188px] md:items-center">
          <div className="relative w-full rounded-[12px] bg-white px-3 py-2 shadow-[0_16px_48px_rgba(0,0,0,0.14)] max-md:flex max-md:items-center max-md:gap-3 md:p-4">
            <span className="absolute inset-x-0 top-0 h-0.5 rounded-t-[12px] bg-[#C43D3D]" aria-hidden />
            <div className="min-w-0 flex-1">
              <div className="text-[13px] font-semibold tracking-tight text-black">Plan regression</div>
              <p className="mt-0.5 text-[12px] leading-4 text-[#285D49] max-md:line-clamp-2 md:mt-2 md:leading-5">
                Index Scan 12ms becomes Seq Scan 410ms on production-shaped volume.
              </p>
            </div>
            <div className="inline-flex shrink-0 items-center gap-1.5 rounded-full bg-[#f8e4e4] px-2.5 py-0.5 font-mono text-[10px] uppercase tracking-[0.12em] text-black md:mt-3">
              <span className="size-1.5 rounded-full bg-[#C43D3D]" aria-hidden />
              block
            </div>
          </div>
        </aside>
      </div>
    </SageWell>
  );
}

function ColHead({ children, ink }: { children: string; ink: "sage" | "block" }) {
  return (
    <div className="flex items-center gap-1.5 px-0.5 pt-2 pb-1.5 font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-black">
      <span
        className={cn("size-1.5 shrink-0 rounded-full", ink === "sage" ? "bg-[#33bf00]" : "bg-[#C43D3D]")}
        aria-hidden
      />
      {children}
    </div>
  );
}

function Spine() {
  return (
    <div className="relative" aria-hidden>
      <span className="absolute top-2 bottom-3 left-1/2 w-px -translate-x-1/2 bg-[#285D49]/30" />
      <span className="absolute top-[28%] left-1/2 size-1.5 -translate-x-1/2 -translate-y-1/2 rounded-full bg-[#285D49]" />
      <span className="absolute top-[28%] right-0 left-0 h-px bg-[#285D49]/25" />
      <span className="absolute top-[64%] left-1/2 size-1.5 -translate-x-1/2 -translate-y-1/2 rounded-full bg-[#C43D3D]" />
      <span className="absolute top-[64%] right-0 left-0 h-px bg-[#C43D3D]/30" />
    </div>
  );
}

function TimeBar({
  ms,
  pct,
  tone,
}: {
  ms: string;
  pct: number;
  tone: "pass" | "block";
}) {
  return (
    <div className="flex items-center gap-1.5">
      <span
        className={cn(
          "shrink-0 font-mono text-[11px] tabular-nums tracking-extra-tight",
          tone === "pass" ? "text-[#285D49]" : "text-[#C43D3D]",
        )}
      >
        {ms}
      </span>
      <span
        className={cn(
          "relative h-1.5 min-w-0 flex-1 overflow-hidden rounded-full",
          tone === "pass" ? "bg-[#dceee6]" : "bg-[#f8e4e4]",
        )}
        aria-hidden
      >
        <span
          className={cn(
            "block h-full rounded-full",
            tone === "pass" ? "bg-[#285D49]" : "bg-[#C43D3D]",
          )}
          style={{ width: `${Math.max(pct, 5)}%` }}
        />
      </span>
    </div>
  );
}

function LimitNode({
  time,
  pct,
  cost,
  rows,
  tone,
}: {
  time: string;
  pct: number;
  cost: string;
  rows: string;
  tone: "pass" | "block";
}) {
  return (
    <div
      className={cn(
        "shrink-0 rounded-[8px] border px-2 py-1.5 sm:px-2.5",
        tone === "pass" && "border-[#285D49]/20 bg-white",
        tone === "block" && "border-[#C43D3D]/25 bg-white",
      )}
    >
      <div className="flex items-baseline justify-between gap-2">
        <span className="text-[12px] font-medium tracking-tight text-black">Limit</span>
        <span className="hidden font-mono text-[9px] text-gray-new-40 sm:inline">rows={rows}</span>
      </div>
      <div className="mt-1">
        <TimeBar ms={time} pct={pct} tone={tone} />
      </div>
      <div className="mt-1 truncate font-mono text-[9px] text-gray-new-40">cost={cost}</div>
    </div>
  );
}

function ScanNode({
  title,
  rel,
  cond,
  time,
  pct,
  cost,
  rows,
  tone,
}: {
  title: string;
  rel: string;
  cond: string;
  time: string;
  pct: number;
  cost: string;
  rows: string;
  tone: "pass" | "block";
}) {
  return (
    <div
      className={cn(
        "mt-2 flex min-h-0 flex-1 flex-col rounded-[10px] border px-2 py-2 sm:mt-2.5 sm:px-2.5 sm:py-2.5",
        tone === "pass" && "border-[#285D49]/20 bg-[#E4F1EB]",
        tone === "block" && "border-[#C43D3D]/25 bg-white",
      )}
    >
      <div>
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0 text-[13px] leading-4 font-medium tracking-tight text-black sm:text-[14px] sm:leading-5">
            {title}
          </div>
          {tone === "pass" ? (
            <span className="inline-flex shrink-0 items-center gap-1 rounded-full bg-white px-1.5 py-0.5 font-mono text-[9px] uppercase tracking-[0.12em] text-[#285D49]">
              <span className="size-1.5 rounded-full bg-[#33bf00]" aria-hidden />
              pass
            </span>
          ) : (
            <span className="inline-flex shrink-0 items-center gap-1 rounded-full bg-white px-1.5 py-0.5 font-mono text-[9px] uppercase tracking-[0.12em] text-[#C43D3D]">
              <span className="size-1.5 rounded-full bg-[#C43D3D]" aria-hidden />
              block
            </span>
          )}
        </div>
        <div className="mt-0.5 truncate font-mono text-[10px] tracking-extra-tight text-gray-new-40">
          {rel}
        </div>
      </div>

      <div className="mt-2 min-w-0">
        <TimeBar ms={time} pct={pct} tone={tone} />
      </div>
      <div className="mt-auto min-w-0 pt-1.5">
        <div className="hidden font-mono text-[10px] leading-4 tracking-extra-tight text-gray-new-40 sm:block">
          {cond}
        </div>
        <div className="mt-1 flex flex-wrap gap-x-2 font-mono text-[9px] text-gray-new-40">
          <span className="truncate">cost={cost}</span>
          <span className="tabular-nums">rows={rows} width=128</span>
        </div>
      </div>
    </div>
  );
}
