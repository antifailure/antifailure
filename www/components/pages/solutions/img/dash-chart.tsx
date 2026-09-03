import { cn } from "@/lib/cn";
import { FloatWindow, SageWell } from "../well";

const TICKS = [100, 75, 50, 25, 0] as const;

export function DashChart({
  title,
  popup,
  bars,
  popupSide = "right",
}: {
  title: string;
  popup: { title: string; rows: [string, string][] };
  bars: number[];
  popupSide?: "left" | "right";
}) {
  const n = Math.max(bars.length, 1);
  const peakAt = bars.reduce((best, h, i) => (h >= (bars[best] ?? -1) ? i : best), 0);
  const points = bars.map((h, i) => `${((i + 0.5) / n) * 100},${100 - clamp(h)}`).join(" ");
  const ridge =
    bars.length > 0
      ? `${((0.5) / n) * 100},100 ${points} ${((n - 0.5) / n) * 100},100`
      : undefined;

  return (
    <SageWell>
      <div
        className={cn(
          "relative grid w-full gap-3 max-md:grid-cols-1",
          popupSide === "left"
            ? "md:grid-cols-[220px_minmax(0,1fr)]"
            : "md:grid-cols-[minmax(0,1fr)_220px]",
        )}
      >
        <FloatWindow
          className={cn(
            "min-w-0 overflow-hidden",
            popupSide === "left" ? "max-md:order-1 md:order-2" : "order-1",
          )}
        >
          <div className="flex flex-col gap-2 border-b border-black/[0.06] bg-[#f7f7f5] px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:gap-3">
            <div className="min-w-0 text-[13px] font-medium tracking-tight text-black">{title}</div>
            <div className="flex shrink-0 items-center gap-3">
              <span className="inline-flex items-center gap-1.5">
                <span className="h-2.5 w-2 rounded-[2px] bg-[#CAE6D9]" aria-hidden />
                <span className="font-mono text-[10px] uppercase tracking-[0.12em] text-gray-new-40">
                  cadence
                </span>
              </span>
              <span className="inline-flex items-center gap-1.5">
                <span className="relative h-px w-3 bg-[#285D49]" aria-hidden>
                  <span className="absolute top-1/2 left-1/2 size-[5px] -translate-x-1/2 -translate-y-1/2 rotate-45 border border-[#285D49] bg-white" />
                </span>
                <span className="font-mono text-[10px] uppercase tracking-[0.12em] text-gray-new-40">
                  drift
                </span>
              </span>
            </div>
          </div>

          <div className="px-3 pt-3 pb-2 sm:px-4 sm:pt-4">
            <div className="flex h-[196px] md:h-[268px]">
              <div className="flex w-8 shrink-0 flex-col justify-between pt-3 pr-2 text-right font-mono text-[10px] leading-none text-gray-new-40">
                {TICKS.map((tick) => (
                  <span key={tick}>{tick}</span>
                ))}
              </div>

              <div className="relative min-w-0 flex-1 overflow-hidden rounded-[10px] bg-[#f7f7f5]">
                <div className="absolute inset-x-2 top-3 bottom-0">
                  {TICKS.map((tick) => (
                    <div
                      key={tick}
                      className={cn(
                        "absolute right-0 left-0",
                        tick === 0 ? "border-t border-[#285D49]/25" : "border-t border-black/[0.06]",
                      )}
                      style={{ top: `${100 - tick}%` }}
                    />
                  ))}

                  {bars.length > 0 ? (
                    <svg
                      className="pointer-events-none absolute inset-0 h-full w-full"
                      viewBox="0 0 100 100"
                      width="100"
                      height="100"
                      preserveAspectRatio="none"
                      aria-hidden
                    >
                      {ridge ? <polygon points={ridge} fill="#dceee6" /> : null}
                    </svg>
                  ) : null}

                  <div className="relative z-[1] flex h-full items-end gap-0.5 sm:gap-1">
                    {bars.map((h, i) => {
                      const drop = i > 0 && h < (bars[i - 1] ?? h) - 8;
                      return (
                        <div
                          key={i}
                          className={cn(
                            "relative flex h-full flex-1 items-end justify-center",
                            drop && "bg-[#f4edd6]/70",
                          )}
                        >
                          <div
                            className="relative w-[56%] max-w-[22px] rounded-t-[3px] bg-[#CAE6D9] sm:max-w-[26px]"
                            style={{ height: `${clamp(h)}%` }}
                          >
                            {i === peakAt ? (
                              <span
                                className="absolute inset-x-0 top-0 h-[3px] rounded-t-[3px] bg-[#33bf00]"
                                aria-hidden
                              />
                            ) : null}
                          </div>
                        </div>
                      );
                    })}
                  </div>

                  {bars.length > 0 ? (
                    <svg
                      className="pointer-events-none absolute inset-0 z-[2] h-full w-full"
                      viewBox="0 0 100 100"
                      width="100"
                      height="100"
                      preserveAspectRatio="none"
                      aria-hidden
                    >
                      <polyline
                        fill="none"
                        points={points}
                        stroke="#285D49"
                        strokeLinejoin="round"
                        strokeLinecap="round"
                        strokeWidth="1.75"
                        vectorEffect="non-scaling-stroke"
                      />
                    </svg>
                  ) : null}

                  {bars.map((h, i) => (
                    <span
                      key={`m-${i}`}
                      className={cn(
                        "absolute z-[3] size-[7px] -translate-x-1/2 translate-y-1/2 rotate-45 border bg-white",
                        i === peakAt ? "border-[#33bf00]" : "border-[#285D49]",
                      )}
                      style={{
                        left: `${((i + 0.5) / n) * 100}%`,
                        bottom: `${clamp(h)}%`,
                      }}
                      aria-hidden
                    />
                  ))}
                </div>
              </div>
            </div>

            <div className="mt-2 flex justify-between pl-8 font-mono text-[10px] uppercase tracking-[0.12em] text-gray-new-40">
              <span>t0</span>
              <span>peak</span>
              <span>now</span>
            </div>
          </div>
        </FloatWindow>

        <div
          className={cn(
            "z-10 h-fit self-center rounded-[12px] bg-white p-4 shadow-[0_16px_48px_rgba(0,0,0,0.14)] max-md:order-2 max-md:w-full",
            popupSide === "left" ? "md:order-1 md:translate-x-1" : "md:order-2 md:-translate-x-1",
          )}
        >
          <div className="text-[13px] font-semibold tracking-tight text-black">{popup.title}</div>
          <dl className="mt-3 space-y-2">
            {popup.rows.map(([k, v]) => (
              <div key={k} className="flex items-baseline justify-between gap-3 text-[12px] leading-4">
                <dt className="text-gray-new-40">{k}</dt>
                <dd className="font-medium text-black">{v}</dd>
              </div>
            ))}
          </dl>
        </div>
      </div>
    </SageWell>
  );
}

function clamp(n: number) {
  return Math.min(100, Math.max(0, n));
}
