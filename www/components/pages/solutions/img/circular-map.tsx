import { cn } from "@/lib/cn";
import { SageWell, FloatWindow } from "../well";

type Ring = { label: string; r: number };

const VB_W = 320;
const VB_H = 228;
const CX = 108;
const CY = 114;
const BEZEL = 90;
const HUB = 17;
const R_LO = 36;
const R_HI = 82;
const LABEL_X = 214;

function plotR(r: number, rings: Ring[], index: number) {
  if (rings.length === 0) return (R_LO + R_HI) / 2;
  const lo = Math.min(...rings.map((x) => x.r));
  const hi = Math.max(...rings.map((x) => x.r));
  const span = Math.max(rings.length - 1, 1);
  const t = hi === lo ? index / span : (r - lo) / (hi - lo);
  return R_LO + t * (R_HI - R_LO);
}

function fiducial() {
  const tip = pt(BEZEL + 7, 0);
  const l = pt(BEZEL + 1, -5.5);
  const r = pt(BEZEL + 1, 5.5);
  return `${tip.x},${tip.y} ${l.x},${l.y} ${r.x},${r.y}`;
}

function pt(radius: number, deg: number) {
  const a = ((deg - 90) * Math.PI) / 180;
  return { x: CX + Math.cos(a) * radius, y: CY + Math.sin(a) * radius };
}

function arc(radius: number, start: number, end: number) {
  const s = pt(radius, start);
  const e = pt(radius, end);
  const sweep = ((end - start) % 360 + 360) % 360;
  const large = sweep > 180 ? 1 : 0;
  return `M ${s.x.toFixed(2)} ${s.y.toFixed(2)} A ${radius} ${radius} 0 ${large} 1 ${e.x.toFixed(2)} ${e.y.toFixed(2)}`;
}

function clipLabel(label: string) {
  return label.length > 16 ? `${label.slice(0, 15)}…` : label;
}

export function CircularMap({
  tabs,
  active,
  rings,
  shift = "right",
}: {
  tabs: string[];
  active: string;
  rings: Ring[];
  shift?: "left" | "right" | "center";
}) {
  const uid = `cm-${[active, ...rings.map((r) => r.label)].join("-").replace(/[^a-zA-Z0-9-]/g, "") || "map"}`;
  const plotted = rings.map((ring, i) => ({
    ...ring,
    plotR: plotR(ring.r, rings, i),
  }));
  const ordered = [...plotted].sort((a, b) => b.plotR - a.plotR);
  const n = ordered.length;
  const labelTop = 50;
  const labelBot = 178;

  return (
    <SageWell>
      <FloatWindow
        className={cn(
          "overflow-hidden max-md:ml-0 max-md:mr-0",
          shift === "right" && "ml-[8%] mr-[-8px]",
          shift === "left" && "mr-[8%] ml-[-8px]",
          shift === "center" && "mx-auto",
        )}
      >
        <div className="flex flex-wrap items-end gap-x-5 gap-y-1 border-b border-black/[0.06] bg-[#f7f7f5] px-4 pt-3.5 md:px-5">
          {tabs.map((tab) => (
            <span
              key={tab}
              className={cn(
                "pb-2.5 text-[10px] font-medium uppercase tracking-[0.12em] md:text-[11px]",
                tab === active
                  ? "border-b-2 border-[#33bf00] text-black"
                  : "border-b-2 border-transparent text-gray-new-40",
              )}
            >
              {tab}
            </span>
          ))}
        </div>

        <div className="px-3 pt-2 pb-3 md:px-4 md:pb-4">
          <svg
            viewBox={`0 0 ${VB_W} ${VB_H}`}
            className="mx-auto h-auto w-full max-w-[480px] font-sans"
            preserveAspectRatio="xMidYMid meet"
            aria-hidden
          >
            <defs>
              <clipPath id={`${uid}-plot`}>
                <circle cx={CX} cy={CY} r={BEZEL - 0.5} />
              </clipPath>
              <clipPath id={`${uid}-stg`}>
                <rect x={CX - BEZEL} y={CY - BEZEL} width={BEZEL} height={BEZEL * 2} />
              </clipPath>
              <clipPath id={`${uid}-twn`}>
                <rect x={CX} y={CY - BEZEL} width={BEZEL} height={BEZEL * 2} />
              </clipPath>
            </defs>

            <g clipPath={`url(#${uid}-plot)`}>
              <g clipPath={`url(#${uid}-stg)`}>
                <circle cx={CX} cy={CY} r={BEZEL} fill="#f4edd6" />
              </g>
              <g clipPath={`url(#${uid}-twn)`}>
                <circle cx={CX} cy={CY} r={BEZEL} fill="#E4F1EB" />
              </g>
            </g>

            <circle
              cx={CX}
              cy={CY}
              r={BEZEL}
              fill="none"
              stroke="#285D49"
              strokeOpacity="0.22"
              strokeWidth="1"
            />

            {ordered.map((ring, i) => (
              <circle
                key={`bed-${ring.label}`}
                cx={CX}
                cy={CY}
                r={ring.plotR}
                fill="none"
                stroke={i % 2 === 0 ? "#CAE6D9" : "#dceee6"}
                strokeWidth="7"
              />
            ))}

            {ordered.map((ring) => (
              <path
                key={`stg-${ring.label}`}
                d={arc(ring.plotR, 198, 342)}
                fill="none"
                stroke="#8A6A12"
                strokeWidth="1.75"
                strokeDasharray="4 3"
                strokeLinecap="butt"
              />
            ))}

            {ordered.map((ring) => (
              <path
                key={`twn-${ring.label}`}
                d={arc(ring.plotR, 0, 180)}
                fill="none"
                stroke="#285D49"
                strokeWidth="2.25"
                strokeLinecap="butt"
              />
            ))}

            <line
              x1={CX}
              y1={CY - BEZEL}
              x2={CX}
              y2={CY + BEZEL}
              stroke="#285D49"
              strokeOpacity="0.28"
              strokeWidth="1"
            />

            {[0, 45, 90, 135, 180, 225, 270, 315].map((deg) => {
              const major = deg % 90 === 0;
              const a = pt(BEZEL - (major ? 1 : 0), deg);
              const b = pt(BEZEL + (major ? 6 : 3.5), deg);
              return (
                <line
                  key={deg}
                  x1={a.x}
                  y1={a.y}
                  x2={b.x}
                  y2={b.y}
                  stroke="#285D49"
                  strokeOpacity={major ? 0.55 : 0.28}
                  strokeWidth={major ? 1.2 : 1}
                />
              );
            })}

            <polygon points={fiducial()} fill="#33bf00" />

            <circle cx={CX} cy={CY} r={HUB} fill="#f7f7f5" stroke="#285D49" strokeWidth="1.25" />
            <line x1={CX - 7} y1={CY} x2={CX + 7} y2={CY} stroke="#285D49" strokeWidth="1" />
            <line x1={CX} y1={CY - 7} x2={CX} y2={CY + 7} stroke="#285D49" strokeWidth="1" />
            <circle cx={CX} cy={CY} r="2.75" fill="#285D49" />

            <text
              x={CX - 38}
              y={CY - BEZEL - 8}
              textAnchor="middle"
              fill="#8A6A12"
              fontSize="9"
              fontWeight="600"
              letterSpacing="0.14em"
            >
              STG
            </text>
            <text
              x={CX + 38}
              y={CY - BEZEL - 8}
              textAnchor="middle"
              fill="#285D49"
              fontSize="9"
              fontWeight="600"
              letterSpacing="0.14em"
            >
              TWN
            </text>

            {ordered.map((ring, i) => {
              const attachDeg = n <= 1 ? 90 : 70 + (i / Math.max(n - 1, 1)) * 40;
              const a = pt(ring.plotR, attachDeg);
              const y = n <= 1 ? (labelTop + labelBot) / 2 : labelTop + (i / Math.max(n - 1, 1)) * (labelBot - labelTop);
              return (
                <g key={`lab-${ring.label}`}>
                  <line
                    x1={a.x}
                    y1={a.y}
                    x2={LABEL_X - 8}
                    y2={y}
                    stroke="#285D49"
                    strokeOpacity="0.28"
                    strokeWidth="1"
                  />
                  <circle cx={a.x} cy={a.y} r="2" fill="#285D49" />
                  <text
                    x={LABEL_X}
                    y={y + 3.5}
                    fill="#61646b"
                    fontFamily="ui-monospace, SFMono-Regular, Menlo, monospace"
                    fontSize="9"
                    letterSpacing="0.08em"
                  >
                    {String(i + 1).padStart(2, "0")}
                  </text>
                  <text x={LABEL_X + 18} y={y + 3.5} fill="#000000" fontSize="11" fontWeight="500">
                    {clipLabel(ring.label)}
                  </text>
                </g>
              );
            })}
          </svg>

          <div className="mx-auto mt-2 flex max-w-[480px] flex-wrap items-center justify-center gap-x-5 gap-y-1.5 px-1 font-mono text-[10px] font-medium uppercase tracking-[0.12em]">
            <span className="inline-flex items-center gap-2 text-[#8A6A12]">
              <span className="w-5 border-t border-dashed border-[#8A6A12]" aria-hidden />
              staging
            </span>
            <span className="inline-flex items-center gap-2 text-[#285D49]">
              <span className="h-px w-5 bg-[#285D49]" aria-hidden />
              twin
            </span>
          </div>
        </div>
      </FloatWindow>
    </SageWell>
  );
}
