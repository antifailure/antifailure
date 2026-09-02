import { cn } from "@/lib/cn";
import { FloatWindow, SageWell } from "../well";

type Mode = "block" | "allow" | "capture" | "mock";

const MODES: Mode[] = ["block", "allow", "capture", "mock"];

const MATRIX: { host: string; mode: Mode }[] = [
  { host: "api.stripe.com", mode: "mock" },
  { host: "api.sendgrid.com", mode: "capture" },
  { host: "hooks.slack.com", mode: "capture" },
  { host: "unknown TCP", mode: "block" },
  { host: "api.prod.internal", mode: "block" },
];

const MODE_INK: Record<Mode, string> = {
  block: "#C43D3D",
  allow: "#61646b",
  capture: "#8A6A12",
  mock: "#285D49",
};

const MODE_WELL: Record<Mode, string> = {
  block: "bg-[#f8e4e4]",
  allow: "bg-[#E4F1EB]",
  capture: "bg-[#f4edd6]",
  mock: "bg-[#dceee6]",
};

function ModeMark({ mode, on }: { mode: Mode; on: boolean }) {
  const ink = on ? MODE_INK[mode] : "#61646b";
  return (
    <svg viewBox="0 0 12 12" className="size-3" aria-hidden>
      {mode === "block" ? (
        on ? (
          <rect x="2" y="2" width="8" height="8" rx="1.5" fill={ink} />
        ) : (
          <rect x="2.5" y="2.5" width="7" height="7" rx="1.5" fill="none" stroke={ink} strokeWidth="1.25" />
        )
      ) : null}
      {mode === "allow" ? (
        <circle cx="6" cy="6" r="3.25" fill="none" stroke={ink} strokeWidth="1.25" />
      ) : null}
      {mode === "capture" ? (
        on ? (
          <path d="M6 1.8 L10.2 6 L6 10.2 L1.8 6 Z" fill={ink} />
        ) : (
          <path d="M6 2.1 L9.9 6 L6 9.9 L2.1 6 Z" fill="none" stroke={ink} strokeWidth="1.25" />
        )
      ) : null}
      {mode === "mock" ? (
        on ? (
          <circle cx="6" cy="6" r="3.4" fill={ink} />
        ) : (
          <circle cx="6" cy="6" r="3.15" fill="none" stroke={ink} strokeWidth="1.25" />
        )
      ) : null}
    </svg>
  );
}

function Gutter({ n }: { n: number }) {
  return (
    <span className="w-6 shrink-0 text-right font-mono text-[10px] tabular-nums text-gray-new-40">
      {n}
    </span>
  );
}

export function HostModeMatrix() {
  return (
    <SageWell className="!min-h-0 max-md:!min-h-0">
      <div className="relative flex min-w-0 flex-col gap-3 md:grid md:grid-cols-[minmax(0,1fr)_200px] md:items-center md:gap-4">
        <FloatWindow className="min-w-0 overflow-hidden">
          <div className="flex items-end gap-1 border-b border-black/[0.06] bg-[#f7f7f5] px-3 pt-2.5">
            <div className="flex items-center gap-2 rounded-t-[8px] bg-[#CAE6D9] px-3 py-2 font-mono text-[11px] tracking-extra-tight text-[#285D49]">
              antifailure.yaml
              <span className="text-[#285D49]/40" aria-hidden>
                ×
              </span>
            </div>
            <div className="mb-2 ml-auto flex items-center gap-2 pr-1">
              <span className="size-1.5 rounded-full bg-[#33bf00]" aria-hidden />
              <span className="border-b-2 border-[#33bf00] pb-0.5 font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-black">
                egress
              </span>
            </div>
          </div>

          <div className="bg-[#f7f7f5]">
            <div className="flex items-center gap-2.5 px-3 py-1.5">
              <Gutter n={1} />
              <span className="font-mono text-[12px] tracking-extra-tight text-black">egress:</span>
            </div>
            <div className="flex items-center gap-2.5 border-l-2 border-[#C43D3D] bg-white px-3 py-1.5">
              <Gutter n={2} />
              <span className="pl-3 font-mono text-[12px] tracking-extra-tight text-black">
                default: <span className="text-[#C43D3D]">block</span>
              </span>
            </div>
            <div className="flex items-center gap-2.5 px-3 py-1.5">
              <Gutter n={3} />
              <span className="pl-3 font-mono text-[12px] tracking-extra-tight text-black">hosts:</span>
            </div>
          </div>

          <div
            role="table"
            aria-label="Host egress mode matrix. Default block."
            className="border-t border-black/[0.06] bg-[#f7f7f5] pb-2.5"
          >
            <div
              role="row"
              className="hidden grid-cols-[28px_minmax(0,1fr)_repeat(4,minmax(44px,1fr))] items-center gap-1 px-3 py-2 sm:grid"
            >
              <span role="columnheader" className="sr-only">
                line
              </span>
              <span
                role="columnheader"
                className="text-[10px] font-medium uppercase tracking-[0.12em] text-gray-new-40"
              >
                Host
              </span>
              {MODES.map((mode) => (
                <span
                  key={mode}
                  role="columnheader"
                  className={cn(
                    "truncate text-center font-mono text-[9px] uppercase tracking-[0.08em]",
                    mode === "block"
                      ? "border-b-2 border-[#C43D3D] pb-0.5 text-[#C43D3D]"
                      : "text-gray-new-40",
                  )}
                >
                  {mode}
                </span>
              ))}
            </div>
            <div
              role="row"
              className="grid grid-cols-[28px_minmax(0,1fr)_auto] items-center gap-2 px-3 py-1.5 sm:hidden"
            >
              <span className="sr-only">Host</span>
              <span className="col-start-2 text-[10px] font-medium uppercase tracking-[0.12em] text-gray-new-40">
                Host
              </span>
              <span className="font-mono text-[10px] uppercase tracking-[0.12em] text-gray-new-40">
                mode
              </span>
            </div>

            {MATRIX.map((row, i) => {
              const implicit = row.mode === "block";
              return (
                <div
                  key={row.host}
                  role="row"
                  className={cn(
                    "grid items-center gap-1 border-t border-black/[0.05] px-3 py-1.5",
                    "grid-cols-[28px_minmax(0,1fr)_auto] sm:grid-cols-[28px_minmax(0,1fr)_repeat(4,minmax(44px,1fr))]",
                  )}
                >
                  <Gutter n={i + 4} />
                  <div
                    role="cell"
                    className={cn(
                      "flex min-w-0 items-center gap-2 border-l-2 pl-2",
                      implicit ? "border-[#C43D3D]" : "border-[#CAE6D9]",
                    )}
                  >
                    <span className="min-w-0 truncate font-mono text-[12px] tracking-extra-tight text-black">
                      {row.host}
                    </span>
                    <span className="sr-only">
                      {row.mode}
                      {implicit ? ", default" : ""}
                    </span>
                  </div>
                  {MODES.map((mode) => {
                    const on = row.mode === mode;
                    return (
                      <span
                        key={mode}
                        role="cell"
                        className="hidden min-w-0 sm:flex"
                        aria-hidden
                      >
                        <span
                          className={cn(
                            "flex h-8 w-full items-center justify-center rounded-[8px]",
                            on ? MODE_WELL[mode] : "bg-white",
                          )}
                        >
                          <ModeMark mode={mode} on={on} />
                        </span>
                      </span>
                    );
                  })}
                  <span
                    role="cell"
                    aria-hidden
                    className="flex items-center justify-end gap-1.5 sm:hidden"
                    style={{ color: MODE_INK[row.mode] }}
                  >
                    <ModeMark mode={row.mode} on />
                    <span className="font-mono text-[10px] uppercase tracking-[0.12em]">{row.mode}</span>
                  </span>
                </div>
              );
            })}
          </div>
        </FloatWindow>

        <div className="h-fit min-w-0 rounded-[12px] bg-white p-4 shadow-[0_16px_48px_rgba(0,0,0,0.14)] max-md:w-full md:self-center">
          <div className="text-[13px] font-semibold tracking-tight text-black">Default refuses</div>
          <p className="mt-2 text-[12px] leading-5 text-gray-new-40">
            A host with no rule is blocked on first contact.
          </p>
          <div className="mt-3 rounded-[8px] bg-[#f7f7f5] px-2.5 py-2 font-mono text-[10px] tracking-extra-tight text-[#285D49]">
            default: <span className="text-[#C43D3D]">block</span>
          </div>
        </div>
      </div>
    </SageWell>
  );
}
