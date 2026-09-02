import { cn } from "@/lib/cn";
import { FloatWindow, SageWell } from "../well";

const STAGES = [
  {
    title: "Expand",
    n: "01",
    tone: "plain" as const,
    rows: [
      ["col", "access_tier"],
      ["null", "yes"],
      ["default", "none"],
    ],
    fill: [0, 0, 0, 0, 0] as const,
  },
  {
    title: "Backfill",
    n: "02",
    tone: "sage" as const,
    rows: [
      ["batch", "12k / pass"],
      ["dual-read", "on"],
      ["pool", "live"],
    ],
    fill: [2, 2, 1, 0, 0] as const,
  },
  {
    title: "Contract",
    n: "03",
    tone: "deep" as const,
    rows: [
      ["constraint", "last"],
      ["old path", "kept"],
      ["safe", "not yet"],
    ],
    fill: [2, 2, 2, 2, 3] as const,
  },
];

function Shaft({ fill }: { fill: readonly number[] }) {
  return (
    <div
      className="mt-3 flex min-h-0 flex-1 gap-[3px] max-md:h-2.5 max-md:flex-none max-md:flex-row md:flex-col"
      aria-hidden
    >
      {fill.map((level, i) => (
        <div
          key={i}
          className={cn(
            "min-h-0 min-w-0 flex-1 overflow-hidden rounded-[3px]",
            level === 0 && "border border-dashed border-[#285D49]/40 bg-white",
            level === 1 && "flex bg-white",
            level === 2 && "bg-[#CAE6D9]",
            level === 3 && "bg-[#f4edd6]",
          )}
        >
          {level === 1 ? (
            <>
              <span className="h-full w-1/2 bg-[#CAE6D9]" />
              <span className="h-full w-1/2 bg-[#E4F1EB]" />
            </>
          ) : null}
        </div>
      ))}
    </div>
  );
}

export function ExpandContractColumns() {
  return (
    <SageWell>
      <FloatWindow className="flex min-h-[428px] w-full flex-col overflow-hidden max-md:min-h-0">
        <div className="flex items-baseline justify-between gap-x-3 gap-y-0.5 border-b border-black/[0.06] bg-[#f7f7f5] px-4 py-2.5 max-md:flex-col max-md:items-start">
          <div className="text-[13px] font-medium tracking-tight text-black">Schema coexistence</div>
          <div className="font-mono text-[10px] uppercase tracking-[0.12em] text-gray-new-40">
            expand · backfill · contract
          </div>
        </div>

        <ol className="grid min-h-0 flex-1 grid-cols-3 max-md:grid-cols-1">
          {STAGES.map((stage, i) => (
            <li
              key={stage.title}
              className={cn(
                "flex min-h-0 flex-col px-4 py-4",
                i < STAGES.length - 1 && "max-md:border-b md:border-r",
                "border-black/[0.06]",
                stage.tone === "plain" && "bg-white",
                stage.tone === "sage" && "bg-[#E4F1EB]",
                stage.tone === "deep" && "bg-[#dceee6]",
              )}
            >
              <div className="flex items-center gap-2">
                <span className="font-mono text-[10px] tabular-nums text-[#285D49]">{stage.n}</span>
                {i < STAGES.length - 1 ? (
                  <span className="h-px min-w-4 flex-1 bg-[#285D49]/30 max-md:hidden" aria-hidden />
                ) : (
                  <span className="h-px min-w-4 flex-1 bg-transparent max-md:hidden" aria-hidden />
                )}
                {stage.tone === "sage" ? (
                  <span className="size-1.5 rounded-full bg-[#33bf00]" aria-hidden />
                ) : null}
              </div>
              <div className="mt-1.5 text-[16px] font-medium tracking-tight text-black">{stage.title}</div>
              <Shaft fill={stage.fill} />
              <dl className="mt-3 space-y-0">
                {stage.rows.map(([k, v]) => {
                  const warn = v === "not yet";
                  const live = v === "on" || v === "live";
                  return (
                    <div
                      key={k}
                      className="flex items-baseline justify-between gap-2 border-b border-black/[0.06] py-2 last:border-0 last:pb-0"
                    >
                      <dt className="font-mono text-[10px] uppercase tracking-[0.1em] text-gray-new-40">{k}</dt>
                      <dd
                        className={cn(
                          "font-mono text-[12px] tracking-extra-tight text-black",
                          warn &&
                            "rounded-[4px] bg-[#f4edd6] px-1.5 py-px text-black shadow-[inset_2px_0_0_#8A6A12]",
                          live && "text-[#285D49]",
                        )}
                      >
                        {v}
                      </dd>
                    </div>
                  );
                })}
              </dl>
            </li>
          ))}
        </ol>

        <div className="border-t border-black/[0.06] bg-[#f7f7f5] px-4 py-3">
          <div className="text-[13px] font-medium tracking-tight text-black">Postgres first</div>
          <p className="mt-1 text-[12px] leading-5 text-[#285D49]">
            Publish what the twin reproduced. Do not pretend unsupported components are cloned.
          </p>
        </div>
      </FloatWindow>
    </SageWell>
  );
}
