import { useId, type ReactNode } from "react";
import { cn } from "@/lib/cn";
import { FloatWindow, SageWell } from "../well";

function Check({ on }: { on?: boolean }) {
  return (
    <span
      className={cn(
        "mt-px inline-flex size-4 shrink-0 items-center justify-center rounded-[4px] border",
        on ? "border-[#33bf00] bg-[#E4F1EB]" : "border-black/20 bg-white",
      )}
    >
      {on ? (
        <svg viewBox="0 0 12 12" className="size-2.5" fill="none" aria-hidden>
          <path
            d="M2.5 6.2 5 8.7 9.5 3.5"
            stroke="#285D49"
            strokeWidth="1.6"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      ) : null}
    </span>
  );
}

function TonePill({
  tone,
  children,
  className,
}: {
  tone: "PASS" | "WARN" | "BLOCK";
  children: ReactNode;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[10px] font-medium whitespace-nowrap",
        tone === "PASS" && "bg-[#E4F1EB] text-[#285D49]",
        tone === "WARN" && "bg-[#f4edd6] text-[#8A6A12]",
        tone === "BLOCK" && "bg-[#f8e4e4] text-[#C43D3D]",
        className,
      )}
    >
      <span
        className={cn(
          "size-1.5 shrink-0 rounded-[2px]",
          tone === "PASS" && "bg-[#33bf00]",
          tone === "WARN" && "bg-[#8A6A12]",
          tone === "BLOCK" && "bg-[#C43D3D]",
        )}
        aria-hidden
      />
      {children}
    </span>
  );
}

type NotebookRow = {
  id: string;
  label: string;
  status: string;
  tone: "PASS" | "WARN" | "BLOCK";
  bar: number;
  kind?: string;
};

type NotebookBody =
  | { rows: NotebookRow[]; children?: never }
  | { children: ReactNode; rows?: never };

function Spine({ rail }: { rail: string }) {
  return (
    <div className="relative flex w-5 shrink-0 flex-col items-center justify-between bg-[#CAE6D9] py-3">
      <svg viewBox="0 0 8 240" className="pointer-events-none absolute inset-0 h-full w-full" aria-hidden>
        {Array.from({ length: 14 }, (_, i) => (
          <line
            key={i}
            x1="2.2"
            x2="5.8"
            y1={10 + i * 16}
            y2={10 + i * 16}
            stroke="#285D49"
            strokeOpacity="0.28"
            strokeWidth="1"
          />
        ))}
      </svg>
      <span className="relative text-[9px] font-medium uppercase tracking-[0.16em] text-[#285D49] [writing-mode:vertical-rl]">
        {rail}
      </span>
      <span className="relative h-3 w-px bg-[#33bf00]" aria-hidden />
    </div>
  );
}

function RuledField({ id }: { id: string }) {
  return (
    <svg className="pointer-events-none absolute inset-0 h-full w-full" aria-hidden>
      <defs>
        <pattern id={id} width="8" height="8" patternUnits="userSpaceOnUse">
          <path d="M0 8 H8" stroke="#285D49" strokeOpacity="0.1" />
        </pattern>
      </defs>
      <rect width="100%" height="100%" fill={`url(#${id})`} />
    </svg>
  );
}

function Evidence({
  title,
  checks,
}: {
  title: string;
  checks: string[];
}) {
  const ruleId = `nb-rules-${useId().replace(/:/g, "")}`;
  const closed = Math.max(checks.length - 1, 0);

  return (
    <div className="relative flex h-full w-[148px] shrink-0 flex-col rounded-[12px] bg-white p-3 shadow-[0_16px_48px_rgba(0,0,0,0.14)] sm:w-[176px] md:w-[200px] md:p-3.5">
      <span className="absolute inset-x-0 top-0 h-0.5 rounded-t-[12px] bg-[#285D49]" aria-hidden />
      <div className="text-[12px] font-semibold tracking-tight text-black md:text-[13px]">{title}</div>
      <ul className="mt-2 flex flex-col gap-2">
        {checks.map((check, i) => (
          <li key={check} className="flex gap-2 text-[11px] leading-[14px] text-gray-new-40">
            <Check on={i < checks.length - 1} />
            <span className="min-w-0 max-md:line-clamp-2">{check}</span>
          </li>
        ))}
      </ul>
      <div className="relative mt-2 min-h-0 flex-1">
        <RuledField id={ruleId} />
      </div>
      <div className="relative mt-2 flex items-center gap-1" aria-hidden>
        {checks.map((check, i) => (
          <span
            key={check}
            className={cn(
              "h-1.5 flex-1 rounded-[2px]",
              i < closed ? "bg-[#CAE6D9]" : "bg-[#f7f7f5]",
            )}
          />
        ))}
        <span className="ml-1 h-1.5 w-1.5 rounded-[2px] bg-[#33bf00]" />
      </div>
    </div>
  );
}

function Register({ rows }: { rows: NotebookRow[] }) {
  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="grid grid-cols-[24px_minmax(0,1fr)_auto] items-center gap-2 bg-[#f7f7f5] px-2 py-1.5 text-[9px] font-medium uppercase tracking-[0.12em] text-gray-new-40 sm:grid-cols-[24px_64px_minmax(0,1fr)_auto_48px] sm:px-3">
        <span className="tabular-nums">#</span>
        <span className="max-sm:hidden">Id</span>
        <span>Entity</span>
        <span>Policy</span>
        <span className="max-sm:hidden">Cover</span>
      </div>
      <div className="flex min-h-0 flex-1 flex-col">
        {rows.map((row, i) => (
          <div
            key={row.id}
            className={cn(
              "grid min-h-0 flex-1 grid-cols-[24px_minmax(0,1fr)_auto] items-center gap-2 border-t border-black/[0.06] px-2 sm:grid-cols-[24px_64px_minmax(0,1fr)_auto_48px] sm:px-3",
              row.tone === "PASS" && "shadow-[inset_2px_0_0_#33bf00]",
              row.tone === "WARN" && "shadow-[inset_2px_0_0_#8A6A12]",
              row.tone === "BLOCK" && "bg-[#f8e4e4] shadow-[inset_2px_0_0_#C43D3D]",
              row.tone !== "BLOCK" && (i % 2 === 0 ? "bg-[#f7f7f5]" : "bg-white"),
            )}
          >
            <span className="font-mono text-[10px] tabular-nums text-gray-new-40">
              {String(i + 1).padStart(2, "0")}
            </span>
            <span className="hidden truncate font-mono text-[10px] text-gray-new-40 sm:block">{row.id}</span>
            <span className="min-w-0 truncate text-[12px] text-black md:text-[13px]">
              {row.label}
              {row.kind ? (
                <span className="ml-2 hidden font-mono text-[10px] text-gray-new-40 sm:inline">{row.kind}</span>
              ) : null}
            </span>
            <TonePill tone={row.tone}>{row.status}</TonePill>
            <span className="relative hidden h-1.5 overflow-hidden rounded-full bg-[#E4F1EB] sm:block" aria-hidden>
              <span
                className={cn(
                  "block h-full rounded-full",
                  row.tone === "BLOCK" ? "bg-[#C43D3D]" : "bg-[#CAE6D9]",
                )}
                style={{ width: `${row.bar}%` }}
              />
            </span>
          </div>
        ))}
      </div>
      <div className="border-t border-black/[0.06] bg-[#f7f7f5] px-2 py-1.5 font-mono text-[10px] text-gray-new-40 sm:px-3">
        {rows.length} rows · referential subset
      </div>
    </div>
  );
}

export function Notebook({
  tab,
  rail,
  rows,
  overlay,
  overlaySide = "left",
  children,
}: {
  tab: string;
  rail: string;
  overlay: { title: string; checks: string[] };
  overlaySide?: "left" | "right";
} & NotebookBody) {
  const overlayCard = <Evidence title={overlay.title} checks={overlay.checks} />;

  return (
    <SageWell compact>
      <div
        className={cn(
          "flex h-[332px] min-h-0 w-full items-stretch gap-2 max-md:h-[268px]",
          overlaySide === "right" && "flex-row-reverse",
        )}
      >
        {overlayCard}
        <FloatWindow className="flex h-full min-h-0 min-w-0 flex-1 overflow-hidden">
          <div className={cn("flex h-full min-h-0 min-w-0 w-full", overlaySide === "right" && "flex-row-reverse")}>
            <Spine rail={rail} />
            <div className="flex min-h-0 min-w-0 flex-1 flex-col">
              <div className="flex items-end justify-between gap-2 bg-[#f7f7f5] px-2 pt-2.5 pb-2 sm:px-3">
                <div className="min-w-0 truncate text-[12px] font-medium tracking-tight text-black md:text-[13px]">
                  {tab}
                </div>
                <span className="shrink-0 rounded-full bg-[#E4F1EB] px-2 py-0.5 font-mono text-[9px] font-medium uppercase tracking-[0.12em] text-[#285D49]">
                  {rail}
                </span>
              </div>
              <div className="h-0.5 bg-[#CAE6D9]" aria-hidden />
              {children ? (
                <div className="min-h-0 flex-1 bg-[#f7f7f5]">{children}</div>
              ) : rows ? (
                <Register rows={rows} />
              ) : null}
            </div>
          </div>
        </FloatWindow>
      </div>
    </SageWell>
  );
}
