import type { ReactNode } from "react";
import { cn } from "@/lib/cn";
import { FloatWindow, SageWell } from "../well";

type Tone = "PASS" | "WARN" | "BLOCK";

type RunRow = {
  task: string;
  status: string;
  tone: Tone;
  who: string;
  date: string;
};

const CHIP = ["#E4F1EB", "#CAE6D9", "#dceee6"] as const;

function pad(n: number) {
  return String(n).padStart(2, "0");
}

function TonePill({
  tone,
  children,
  className,
}: {
  tone: Tone;
  children: ReactNode;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex min-w-0 max-w-full items-center gap-1.5 rounded-full px-2 py-0.5 text-[12px] font-semibold leading-none whitespace-nowrap",
        tone === "PASS" && "bg-[#E4F1EB] text-[#285D49]",
        tone === "WARN" && "bg-[#f4edd6] text-[#8A6A12]",
        tone === "BLOCK" && "bg-[#f8e4e4] text-[#C43D3D]",
        className,
      )}
    >
      <span
        className={cn(
          "size-1.5 shrink-0",
          // A ternary rather than an override, because both set border-radius
          // and writing them side by side emits both: rounded-full won and the
          // blocked marker was a circle like the other two. The square is the
          // point, it is what tells BLOCK apart from PASS without relying on
          // the red.
          tone === "BLOCK" ? "rounded-[1px]" : "rounded-full",
          tone === "PASS" && "bg-[#33bf00]",
          tone === "WARN" && "bg-[#8A6A12]",
          tone === "BLOCK" && "bg-[#C43D3D]",
        )}
        aria-hidden
      />
      <span className="min-w-0 truncate">{children}</span>
    </span>
  );
}

function SpineNode({ tone }: { tone: Tone }) {
  return (
    <span
      className={cn(
        "relative z-[1] flex size-2.5 shrink-0 items-center justify-center",
        tone === "BLOCK" ? "rounded-[2px] bg-[#C43D3D]" : "rounded-full",
        tone === "PASS" && "bg-[#285D49]",
        tone === "WARN" && "bg-[#8A6A12]",
      )}
      aria-hidden
    >
      {tone === "PASS" ? <span className="size-1 rounded-full bg-[#33bf00]" /> : null}
    </span>
  );
}

function WhoChip({ who, i }: { who: string; i: number }) {
  return (
    <span
      className="inline-flex h-[22px] min-w-[22px] shrink-0 items-center justify-center rounded-[5px] px-1 font-mono text-[10px] font-medium text-[#285D49]"
      style={{ background: CHIP[i % CHIP.length] }}
    >
      {who}
    </span>
  );
}

export function TaskTable({
  heading,
  rows,
  shift = "right",
}: {
  heading: string;
  rows: RunRow[];
  shift?: "left" | "right";
}) {
  const last = rows[rows.length - 1];
  const steps = rows.length;

  return (
    <SageWell>
      <FloatWindow
        className={cn(
          "overflow-hidden max-md:ml-0 max-md:mr-0",
          shift === "right" ? "ml-[4%] mr-[-10px]" : "mr-[4%] ml-[-10px]",
        )}
      >
        <div className="flex items-baseline justify-between gap-3 border-b border-black/[0.06] bg-[#f7f7f5] px-4 py-3">
          <div className="min-w-0 truncate text-[13px] font-medium tracking-extra-tight text-black">
            {heading}
          </div>
          <div className="shrink-0 font-mono text-[10px] tracking-extra-tight text-gray-new-40">
            {last ? last.date : "no runs"}
            {steps ? ` · ${pad(steps)}` : null}
          </div>
        </div>

        {steps ? (
          <ol
            className="grid gap-px bg-black/[0.06]"
            style={{ gridTemplateColumns: `repeat(${steps}, minmax(0, 1fr))` }}
            aria-hidden
          >
            {rows.map((row, i) => (
              <li
                key={`station-${row.task}`}
                className={cn(
                  "min-w-0 px-2.5 py-2.5",
                  row.tone === "PASS" && "bg-[#E4F1EB]",
                  row.tone === "WARN" && "bg-[#f4edd6]",
                  row.tone === "BLOCK" && "bg-[#f8e4e4]",
                )}
              >
                <div className="flex items-center gap-1.5">
                  <span className="font-mono text-[12px] font-semibold text-black">
                    {pad(i + 1)}
                  </span>
                  {i < steps - 1 ? (
                    <span className="h-px min-w-2 flex-1 bg-black/15" />
                  ) : null}
                </div>
                <div
                  className="mt-1 hidden truncate font-mono text-[10px] tracking-extra-tight text-black sm:block"
                  title={row.task}
                >
                  {row.task}
                </div>
              </li>
            ))}
          </ol>
        ) : null}

        <ol>
          {rows.map((row, i) => {
            const next = rows[i + 1];
            return (
              <li
                key={row.task}
                className="grid grid-cols-[48px_16px_minmax(0,1fr)] items-stretch border-b border-black/[0.05] last:border-0 sm:grid-cols-[56px_16px_minmax(0,1fr)]"
              >
                <div className="flex items-center justify-end gap-1 bg-[#f7f7f5] py-2.5 pr-0 pl-3 sm:pl-4">
                  <span className="hidden h-px w-2 bg-black/20 sm:block" aria-hidden />
                  <span className="font-mono text-[11px] leading-none tracking-extra-tight text-gray-new-40">
                    {row.date}
                  </span>
                </div>

                <div className="relative flex flex-col items-center bg-[#f7f7f5]" aria-hidden>
                  {i > 0 ? (
                    <span
                      className={cn(
                        "w-px flex-1",
                        row.tone === "PASS" && "bg-[#CAE6D9]",
                        row.tone === "WARN" && "bg-[#8A6A12]/45",
                        row.tone === "BLOCK" && "bg-[#C43D3D]/45",
                      )}
                    />
                  ) : (
                    <span className="flex-1" />
                  )}
                  <SpineNode tone={row.tone} />
                  {next ? (
                    <span
                      className={cn(
                        "w-px flex-1",
                        next.tone === "PASS" && "bg-[#CAE6D9]",
                        next.tone === "WARN" && "bg-[#8A6A12]/45",
                        next.tone === "BLOCK" && "bg-[#C43D3D]/45",
                      )}
                    />
                  ) : (
                    <span className="flex-1" />
                  )}
                </div>

                <div
                  className={cn(
                    "flex min-w-0 flex-col gap-1.5 py-2.5 pr-3 pl-2 sm:flex-row sm:items-center sm:gap-2 sm:pr-4",
                    row.tone === "PASS" && "bg-white",
                    row.tone === "WARN" && "bg-[#f4edd6]",
                    row.tone === "BLOCK" && "bg-[#f8e4e4]",
                  )}
                >
                  <span
                    className="min-w-0 truncate text-[13px] font-medium tracking-extra-tight text-black sm:flex-1"
                    title={row.task}
                  >
                    {row.task}
                  </span>
                  <div className="flex min-w-0 items-center gap-2">
                    <TonePill tone={row.tone} className="min-w-0">
                      {row.status}
                    </TonePill>
                    <WhoChip who={row.who} i={i} />
                  </div>
                </div>
              </li>
            );
          })}
        </ol>

        {last ? (
          <div
            className={cn(
              "flex items-center justify-between gap-3 px-4 py-2.5",
              last.tone === "PASS" && "bg-[#dceee6]",
              last.tone === "WARN" && "bg-[#f4edd6]",
              last.tone === "BLOCK" && "bg-[#f8e4e4]",
            )}
            aria-hidden
          >
            <span className="min-w-0 truncate font-mono text-[11px] tracking-extra-tight text-black">
              {last.status}
              <span className="text-gray-new-40"> · </span>
              {last.task}
            </span>
            <WhoChip who={last.who} i={Math.max(steps - 1, 0)} />
          </div>
        ) : null}
      </FloatWindow>
    </SageWell>
  );
}
