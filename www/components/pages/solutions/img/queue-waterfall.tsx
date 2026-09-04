import { cn } from "@/lib/cn";
import { SageWell, FloatWindow } from "../well";

const GANTT = [
  { name: "listings.created", start: 6, width: 28, label: "t+00:02", tone: "pass" as const },
  { name: "orders.paid", start: 24, width: 26, label: "t+00:07", tone: "pass" as const },
  { name: "match.attempt", start: 42, width: 30, label: "t+00:11", tone: "warn" as const },
  { name: "notify.worker", start: 58, width: 20, label: "t+00:14", tone: "pass" as const },
  { name: "partner.webhook", start: 76, width: 16, label: "blocked", tone: "block" as const },
];

const GRID = [0, 100 / 3, 200 / 3, 100] as const;
type Mark = {
  at: number;
  label: string;
  align: "start" | "center" | "end";
  now?: boolean;
};

const MARKS: readonly Mark[] = [
  { at: 0, label: "00:00", align: "start" },
  { at: 100 / 3, label: "00:06", align: "center" },
  { at: 70, label: "now", align: "start", now: true },
  { at: 100, label: "00:18", align: "end" },
];
const NOW_AT = 70;
const ROW_H = 36;
const BAR_H = 20;

const TONE_INK = {
  pass: "text-[#285D49]",
  warn: "text-[#8A6A12]",
  block: "text-[#C43D3D]",
} as const;

const TONE_PIP = {
  pass: "bg-[#33bf00]",
  warn: "bg-[#8A6A12]",
  block: "bg-[#C43D3D]",
} as const;

export function QueueWaterfall() {
  const trackH = ROW_H * GANTT.length;
  const padY = (ROW_H - BAR_H) / 2;

  return (
    <SageWell>
      <div className="flex flex-col gap-3 md:grid md:grid-cols-[minmax(0,1fr)_216px] md:items-center md:gap-4">
        <FloatWindow className="min-w-0 overflow-hidden">
          <div className="flex items-center justify-between gap-3 px-3 py-2.5 sm:px-4">
            <div className="min-w-0 text-[13px] font-medium text-black">Clone-local queue</div>
            <span className="shrink-0 font-mono text-[10px] uppercase tracking-[0.12em] text-gray-new-40">
              t0 → now
            </span>
          </div>

          <div className="flex border-t border-black/[0.06]">
            <div className="w-[8.5rem] shrink-0">
              <div className="h-7 border-b border-black/[0.06] bg-[#f7f7f5]" />
              {GANTT.map((row) => (
                <div key={row.name} className="flex h-9 flex-col justify-center px-3">
                  <div className="font-mono text-[10px] leading-none tracking-extra-tight text-black sm:text-[11px]">
                    {row.name}
                  </div>
                  <div className={cn("mt-1 font-mono text-[10px] leading-none", TONE_INK[row.tone])}>
                    {row.label}
                  </div>
                </div>
              ))}
            </div>

            <div className="relative min-w-0 flex-1 bg-[#f7f7f5]">
              <div className="relative h-7 border-b border-black/[0.06]">
                {MARKS.map((mark) => (
                  <span
                    key={mark.label}
                    className={cn(
                      "absolute top-1/2 font-mono text-[10px]",
                      mark.now
                        ? "ml-1.5 font-medium uppercase tracking-[0.12em] text-[#285D49]"
                        : "text-gray-new-40",
                    )}
                    style={{
                      left: `${mark.at}%`,
                      transform:
                        mark.align === "end"
                          ? "translate(-100%, -50%)"
                          : mark.align === "center"
                            ? "translate(-50%, -50%)"
                            : "translateY(-50%)",
                    }}
                  >
                    {mark.label}
                  </span>
                ))}
              </div>

              <div className="relative" style={{ height: trackH }}>
                {GRID.map((x) => (
                  <span
                    key={`grid-${x}`}
                    aria-hidden
                    className="absolute top-0 bottom-0 w-px bg-[#285D49]/15"
                    style={{ left: `${x}%` }}
                  />
                ))}

                {GANTT.slice(1).map((row, i) => (
                  <span
                    key={`stem-${row.name}`}
                    aria-hidden
                    className="absolute w-0.5 -translate-x-1/2 bg-[#285D49]"
                    style={{
                      left: `${row.start}%`,
                      top: i * ROW_H + padY + BAR_H,
                      height: ROW_H - BAR_H,
                    }}
                  />
                ))}

                {GANTT.map((row, i) => (
                  <div
                    key={row.name}
                    className={cn(
                      "absolute rounded-[5px]",
                      row.tone === "pass" && "bg-[#CAE6D9]",
                      row.tone === "warn" && "border border-dashed border-[#8A6A12] bg-[#f4edd6]",
                      row.tone === "block" &&
                        "border border-[#C43D3D] bg-[repeating-linear-gradient(-45deg,#f8e4e4_0_3px,#C43D3D_3px_4px)]",
                    )}
                    style={{
                      left: `${row.start}%`,
                      width: `${row.width}%`,
                      top: i * ROW_H + padY,
                      height: BAR_H,
                    }}
                  >
                    {row.tone === "block" ? (
                      <span
                        className="absolute top-1/2 right-1 flex size-3 -translate-y-1/2 items-center justify-center"
                        aria-hidden
                      >
                        <svg viewBox="0 0 10 10" className="size-2.5" fill="none">
                          <path d="M2 2 L8 8 M8 2 L2 8" stroke="#C43D3D" strokeWidth="1.4" />
                        </svg>
                      </span>
                    ) : null}
                  </div>
                ))}

                {GANTT.map((row, i) => (
                  <span
                    key={`node-${row.name}`}
                    aria-hidden
                    className={cn(
                      "absolute size-2 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-white",
                      TONE_PIP[row.tone],
                    )}
                    style={{
                      left: `${row.start}%`,
                      top: i * ROW_H + ROW_H / 2,
                    }}
                  />
                ))}
              </div>

              <span
                className="pointer-events-none absolute inset-y-0 z-[1] w-0"
                style={{ left: `${NOW_AT}%` }}
                aria-hidden
              >
                <span className="absolute inset-y-0 left-0 w-px bg-[#285D49]" />
                <span className="absolute top-1.5 left-1/2 size-1.5 -translate-x-1/2 rotate-45 bg-[#285D49]" />
              </span>
            </div>
          </div>

          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-black/[0.06] bg-[#E4F1EB] px-3 py-2 sm:px-4">
            <Tally pip="bg-[#33bf00]" n={3} word="pass" />
            <Tally pip="bg-[#8A6A12]" n={1} word="warn" />
            <Tally pip="bg-[#C43D3D]" n={1} word="block" />
          </div>
        </FloatWindow>

        <aside className="rounded-[12px] bg-white p-4 shadow-[0_16px_48px_rgba(0,0,0,0.14)]">
          <div className="text-[13px] font-semibold tracking-tight text-black">Order is the bug</div>
          <p className="mt-2 text-[12px] leading-5 text-gray-new-40">
            Matching that depends on queue order only shows up when workers run.
          </p>
        </aside>
      </div>
    </SageWell>
  );
}

function Tally({ pip, n, word }: { pip: string; n: number; word: string }) {
  return (
    <span className="inline-flex items-center gap-1.5 font-mono text-[10px] uppercase tracking-[0.12em] text-[#285D49]">
      <span className={cn("size-1.5 rounded-full", pip)} aria-hidden />
      {n} {word}
    </span>
  );
}
