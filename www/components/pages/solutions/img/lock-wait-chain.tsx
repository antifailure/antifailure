import { cn } from "@/lib/cn";
import { SageWell, FloatWindow } from "../well";

const CHAIN = [
  {
    who: "pid 1842",
    title: "ALTER subscriptions",
    wait: "holds 27.4s",
    mode: "ACCESS EXCLUSIVE",
    tone: "block" as const,
  },
  {
    who: "pid 2210",
    title: "SELECT events",
    wait: "waiting · p99 6.9s",
    mode: "ACCESS SHARE",
    tone: "block" as const,
  },
  {
    who: "pool",
    title: "migrate-and-serve",
    wait: "connections queued",
    mode: "pool wait",
    tone: "warn" as const,
  },
];

const INK = { block: "#C43D3D", warn: "#8A6A12" } as const;

const JOINS = [
  { label: "blocks", tone: "block" as const },
  { label: "queued", tone: "warn" as const },
];

export function LockWaitChain() {
  return (
    <SageWell>
      <FloatWindow className="overflow-hidden md:mx-[8%]">
        <div className="bg-[#f7f7f5]">
          <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 px-4 py-2.5 sm:px-5">
            <div className="text-[13px] font-medium tracking-tight text-black">Lock wait graph</div>
            <span className="font-mono text-[10px] uppercase tracking-[0.12em] text-[#C43D3D]">
              ACCESS EXCLUSIVE
            </span>
          </div>

          <ol
            className="flex flex-col border-t border-black/[0.06] px-4 pt-4 pb-4 sm:px-5 md:grid md:grid-cols-[auto_minmax(64px,1fr)_auto_minmax(64px,1fr)_auto] md:items-stretch"
            aria-label="Lock wait graph: pid 1842 ACCESS EXCLUSIVE holds 27.4s, blocking pid 2210 ACCESS SHARE waiting p99 6.9s, pool migrate-and-serve connections queued"
          >
            {CHAIN.flatMap((node, i) => {
              const fitting = (
                <li key={node.who} className="min-w-0 md:max-w-[220px] md:shrink-0">
                  <Fitting
                    who={node.who}
                    title={node.title}
                    wait={node.wait}
                    mode={node.mode}
                    tone={node.tone}
                  />
                </li>
              );
              if (i >= JOINS.length) return [fitting];
              const join = JOINS[i];
              return [fitting, <Join key={join.label} label={join.label} tone={join.tone} />];
            })}
          </ol>

          <div className="border-t border-black/[0.06] px-4 py-2.5 sm:px-5">
            <p className="font-mono text-[11px] leading-5 tracking-extra-tight text-gray-new-40">
              27.4s hold · events p99 820ms → 6.9s
            </p>
          </div>
        </div>
      </FloatWindow>
    </SageWell>
  );
}

function Fitting({
  who,
  title,
  wait,
  mode,
  tone,
}: {
  who: string;
  title: string;
  wait: string;
  mode: string;
  tone: "block" | "warn";
}) {
  const ink = INK[tone];
  const queued = who === "pool";

  return (
    <div className="flex h-full flex-col">
      <div className="flex gap-3 pb-1 md:block">
        <span className="mt-1.5 size-2 shrink-0 rounded-full md:hidden" style={{ background: ink }} aria-hidden />
        <div className="min-w-0 flex-1 border-l-[3px] pl-3" style={{ borderColor: ink }}>
          <div className="font-mono text-[10px] tracking-extra-tight text-gray-new-40">{who}</div>
          <div className="mt-1 text-[14px] font-medium leading-snug tracking-tight text-black">{title}</div>
          <div className="mt-2 flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
            <span className="font-mono text-[10px] uppercase tracking-[0.12em]" style={{ color: ink }}>
              {mode}
            </span>
            <span className="font-mono text-[11px] tracking-extra-tight text-gray-new-40">{wait}</span>
          </div>
          {queued ? <QueueTicks className="mt-2 md:hidden" /> : null}
        </div>
      </div>
      <div className="mt-auto hidden pt-3 md:block" aria-hidden>
        <div className="flex h-3 items-center">
          <span
            className={cn(
              "size-2.5 shrink-0 rounded-full bg-white ring-2",
              tone === "block" ? "ring-[#C43D3D]" : "ring-[#8A6A12]",
            )}
          />
          {queued ? <QueueTicks className="ml-1.5" /> : null}
        </div>
      </div>
    </div>
  );
}

function Join({ label, tone }: { label: string; tone: "block" | "warn" }) {
  const ink = INK[tone];

  return (
    <li className="flex flex-col items-center py-2 md:h-full md:min-w-0 md:py-0" aria-hidden>
      <span className="font-mono text-[10px] uppercase tracking-[0.12em] md:mt-auto md:mb-1" style={{ color: ink }}>
        {label}
      </span>
      <svg viewBox="0 0 12 16" className="mt-0.5 h-4 w-3 md:hidden">
        <path
          d="M6 1 V11 M2.2 8.2 L6 13.2 L9.8 8.2"
          fill="none"
          stroke={ink}
          strokeWidth="1.4"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
      <div className="hidden h-3 w-full items-center md:flex" aria-hidden>
        <span className="relative mx-0.5 h-2 min-w-0 flex-1 bg-[#CAE6D9]">
          <span className="absolute inset-x-0 top-0 h-px bg-[#285D49]" />
          <span className="absolute inset-x-0 bottom-0 h-px bg-[#285D49]" />
        </span>
      </div>
    </li>
  );
}

function QueueTicks({ className }: { className?: string }) {
  return (
    <span className={cn("flex items-end gap-px", className)} aria-hidden>
      {[9, 13, 8, 16].map((h, i) => (
        <span key={i} className="w-1 rounded-[1px] bg-[#8A6A12]" style={{ height: h }} />
      ))}
    </span>
  );
}
