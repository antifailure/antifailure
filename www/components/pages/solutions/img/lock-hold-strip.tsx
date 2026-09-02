import { FloatWindow, SageWell } from "../well";

const T_MAX = 32;
const T_ACQUIRE = 4.2;
const T_PEAK = 27.4;
const PLOT_W = 480;
const PLOT_H = 176;
const COLS = 8;
const COL_W = PLOT_W / COLS;

const xAt = (t: number) => (t / T_MAX) * PLOT_W;
const X_ACQUIRE = xAt(T_ACQUIRE);
const X_PEAK = xAt(T_PEAK);
const X_RELEASE = xAt(30.2);

const HOLD_TOP = 16;
const HOLD_FLOOR = 96;
const P99_FLOOR = 168;
const P99_PEAK_Y = 122;

const HOLD_AREA = [
  `M 0,${HOLD_FLOOR}`,
  `C ${X_ACQUIRE * 0.42},${HOLD_FLOOR} ${X_ACQUIRE * 0.72},22 ${X_ACQUIRE},${HOLD_TOP}`,
  `L ${X_PEAK},${HOLD_TOP}`,
  `C ${X_PEAK + 10},${HOLD_TOP} ${X_RELEASE - 8},48 ${X_RELEASE},${HOLD_FLOOR}`,
  `L 0,${HOLD_FLOOR}`,
  "Z",
].join(" ");

const HOLD_EDGE = [
  `M 0,${HOLD_FLOOR}`,
  `C ${X_ACQUIRE * 0.42},${HOLD_FLOOR} ${X_ACQUIRE * 0.72},22 ${X_ACQUIRE},${HOLD_TOP}`,
  `L ${X_PEAK},${HOLD_TOP}`,
  `C ${X_PEAK + 10},${HOLD_TOP} ${X_RELEASE - 8},48 ${X_RELEASE},${HOLD_FLOOR}`,
].join(" ");

const P99_AREA = [
  `M 0,${P99_FLOOR}`,
  `C ${xAt(6)},166 ${xAt(12)},158 ${xAt(16)},148`,
  `C ${xAt(20)},138 ${xAt(24)},130 ${X_PEAK},${P99_PEAK_Y}`,
  `L ${X_RELEASE},${P99_PEAK_Y + 2}`,
  `L ${X_RELEASE},${P99_FLOOR}`,
  "Z",
].join(" ");

const P99_EDGE = [
  `M 0,${P99_FLOOR}`,
  `C ${xAt(6)},166 ${xAt(12)},158 ${xAt(16)},148`,
  `C ${xAt(20)},138 ${xAt(24)},130 ${X_PEAK},${P99_PEAK_Y}`,
  `L ${X_RELEASE},${P99_PEAK_Y + 2}`,
].join(" ");

const TICKS = [
  { t: 0, label: "0s", show: "always" as const },
  { t: T_ACQUIRE, label: "4.2s", show: "md" as const },
  { t: 16, label: "16s", show: "always" as const },
  { t: T_PEAK, label: "27.4s", show: "md" as const },
  { t: T_MAX, label: "32s", show: "always" as const },
];

export function LockHoldStrip() {
  return (
    <SageWell className="!min-h-0 max-md:!min-h-0">
      <FloatWindow className="overflow-hidden md:ml-[5%]">
        <header className="flex items-start justify-between gap-3 border-b border-black/[0.06] bg-[#f7f7f5] px-3.5 py-3 md:px-5 md:py-3.5">
          <div className="min-w-0">
            <div className="text-[13px] font-medium tracking-tight text-black">
              subscriptions · lock hold
            </div>
            <div className="mt-1.5 inline-flex items-center gap-1.5 rounded-full bg-[#f8e4e4] px-2 py-0.5">
              <span className="size-1.5 rounded-full bg-[#C43D3D]" aria-hidden />
              <span className="font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-black">
                ACCESS EXCLUSIVE
              </span>
            </div>
          </div>
          <div className="shrink-0 text-right">
            <div className="border-b-2 border-[#33bf00] pb-0.5 font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-black">
              peak hold
            </div>
            <div className="mt-1 font-mono text-[18px] leading-none font-medium tracking-tight text-black tabular-nums">
              27.4s
            </div>
          </div>
        </header>

        <div className="bg-[#E4F1EB] px-3 pt-3 pb-2 md:px-4 md:pt-3.5">
          <div className="grid grid-cols-[2.5rem_minmax(0,1fr)] items-stretch gap-2 md:grid-cols-[3rem_minmax(0,1fr)]">
            <div className="relative self-stretch font-mono text-[10px] leading-3 font-medium tracking-[0.08em] text-[#285D49] uppercase">
              <span className="absolute top-[8%]">Excl</span>
              <span className="absolute top-[38%]">Share</span>
              <span className="absolute top-[78%]">p99</span>
            </div>
            <div className="relative min-w-0">
              <svg viewBox={`0 0 ${PLOT_W} ${PLOT_H}`} className="h-auto w-full" aria-hidden>
                {Array.from({ length: COLS }, (_, i) => (
                  <rect
                    key={`u-${i}`}
                    x={i * COL_W}
                    y="0"
                    width={COL_W}
                    height="108"
                    fill={i % 2 === 0 ? "#E4F1EB" : "#CAE6D9"}
                  />
                ))}
                {Array.from({ length: COLS }, (_, i) => (
                  <rect
                    key={`l-${i}`}
                    x={i * COL_W}
                    y="116"
                    width={COL_W}
                    height="60"
                    fill={i % 2 === 0 ? "#dceee6" : "#E4F1EB"}
                  />
                ))}
                {[36, 72].map((y) => (
                  <line
                    key={y}
                    x1="0"
                    y1={y}
                    x2={PLOT_W}
                    y2={y}
                    stroke="#285D49"
                    strokeOpacity="0.14"
                  />
                ))}
                <line x1="0" y1="108" x2={PLOT_W} y2="108" stroke="#CAE6D9" strokeWidth="2" />
                <path d={HOLD_AREA} fill="#f8e4e4" />
                <path d={HOLD_EDGE} fill="none" stroke="#C43D3D" strokeWidth="2.2" strokeLinejoin="round" />
                <path d={P99_AREA} fill="#f4edd6" />
                <path d={P99_EDGE} fill="none" stroke="#8A6A12" strokeWidth="1.6" strokeLinejoin="round" />
                <line
                  x1={X_ACQUIRE}
                  y1="8"
                  x2={X_ACQUIRE}
                  y2={PLOT_H}
                  stroke="#285D49"
                  strokeOpacity="0.28"
                  strokeDasharray="3 3"
                />
                <line x1={X_PEAK} y1="4" x2={X_PEAK} y2={PLOT_H} stroke="#C43D3D" strokeWidth="1.4" />
                <circle cx={X_PEAK} cy={HOLD_TOP} r="4.5" fill="#C43D3D" stroke="white" strokeWidth="1.6" />
                <circle cx={X_PEAK} cy={P99_PEAK_Y} r="3.5" fill="#8A6A12" stroke="white" strokeWidth="1.4" />
              </svg>
              <div
                className="pointer-events-none absolute top-0 -translate-x-[70%] rounded-[7px] bg-white px-1.5 py-0.5 shadow-[0_4px_14px_rgba(0,0,0,0.08)]"
                style={{ left: `${(T_PEAK / T_MAX) * 100}%` }}
              >
                <span className="font-mono text-[10px] font-medium text-black tabular-nums md:text-[11px]">
                  27.4s
                </span>
              </div>
            </div>
          </div>
          <div className="mt-1.5 grid grid-cols-[2.5rem_minmax(0,1fr)] gap-2 md:grid-cols-[3rem_minmax(0,1fr)]">
            <span />
            <div className="relative h-4">
              {TICKS.map((tick) => (
                <span
                  key={tick.t}
                  className={
                    tick.show === "md"
                      ? "absolute hidden font-mono text-[10px] text-gray-new-40 tabular-nums md:block"
                      : "absolute font-mono text-[9px] text-gray-new-40 tabular-nums md:text-[10px]"
                  }
                  style={{
                    left: `${(tick.t / T_MAX) * 100}%`,
                    transform: tick.t === 0 ? "none" : tick.t === T_MAX ? "translateX(-100%)" : "translateX(-50%)",
                  }}
                >
                  {tick.label}
                </span>
              ))}
            </div>
          </div>
        </div>

        <dl className="grid grid-cols-3 divide-x divide-black/[0.06] border-t border-black/[0.06] bg-white">
          <div className="px-2.5 py-2.5 md:px-4 md:py-3">
            <dt className="font-mono text-[9px] font-medium tracking-[0.1em] text-gray-new-40 uppercase">
              peak hold
            </dt>
            <dd className="mt-1 font-mono text-[13px] font-medium text-[#C43D3D] tabular-nums md:text-[14px]">
              27.4s
            </dd>
          </div>
          <div className="px-2.5 py-2.5 md:px-4 md:py-3">
            <dt className="font-mono text-[9px] font-medium tracking-[0.1em] text-gray-new-40 uppercase">
              events p99
            </dt>
            <dd className="mt-1 font-mono text-[11px] font-medium whitespace-nowrap text-[#8A6A12] tabular-nums sm:text-[12px] md:text-[14px]">
              820ms → 6.9s
            </dd>
          </div>
          <div className="px-2.5 py-2.5 md:px-4 md:py-3">
            <dt className="font-mono text-[9px] font-medium tracking-[0.1em] text-gray-new-40 uppercase">
              waiter
            </dt>
            <dd className="mt-1 text-[13px] font-medium text-black md:text-[14px]">waiting</dd>
          </div>
        </dl>

        <p className="border-t border-black/[0.06] bg-[#f7f7f5] px-3.5 py-2.5 text-[12px] leading-5 text-gray-new-40 md:px-5 md:py-3">
          Another session was left waiting. events p99 moved 820ms → 6.9s.
        </p>
      </FloatWindow>
    </SageWell>
  );
}
