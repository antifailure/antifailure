import { cn } from "@/lib/cn";
import { FloatWindow, SageWell } from "../well";

const LANES = ["web", "queue", "worker", "partner"] as const;

const MESSAGES = [
  { from: 0, to: 1, label: "listings.created", tone: "pass" as const },
  { from: 1, to: 2, label: "matching.worker", tone: "pass" as const },
  { from: 2, to: 3, label: "api.partners.test", tone: "block" as const },
];

const INK = "#285D49";
const BLOCK = "#C43D3D";
const CREAM = "#f7f7f5";
const SAGE_A = "#E4F1EB";
const SAGE_B = "#dceee6";
const SAGE_C = "#CAE6D9";
const BLUSH = "#f8e4e4";
const PASS = "#33bf00";
const MUTED = "#61646b";

const XS = [70, 180, 290, 400] as const;
const HOP_Y = [48, 80, 112] as const;
const LANE_WASH = [SAGE_A, SAGE_B, SAGE_C, BLUSH] as const;

const MONO = "ui-monospace, SFMono-Regular, Menlo, monospace";

function participantBox(i: number) {
  const cx = XS[i];
  const blocked = i === 3;
  return (
    <g key={`p-${LANES[i]}`}>
      <rect
        x={cx - 35}
        y={5}
        width="70"
        height="17"
        rx="4"
        fill="white"
        stroke={blocked ? BLOCK : INK}
        strokeOpacity={blocked ? 1 : 0.28}
      />
      <text
        x={cx}
        y={14}
        textAnchor="middle"
        dominantBaseline="middle"
        fill={blocked ? BLOCK : INK}
        fontSize="8.5"
        fontFamily={MONO}
        letterSpacing="0.7"
      >
        {LANES[i]}
      </text>
    </g>
  );
}

function messageLabel(label: string, mid: number, y: number, blocked: boolean) {
  const width = label.length * 5 + 10;
  return (
    <g>
      <rect
        x={mid - width / 2}
        y={y - 7}
        width={width}
        height="14"
        rx="7"
        fill="white"
        stroke={blocked ? BLOCK : INK}
        strokeOpacity={blocked ? 0.35 : 0.18}
      />
      <text
        x={mid}
        y={y}
        textAnchor="middle"
        dominantBaseline="middle"
        fill={blocked ? BLOCK : INK}
        fontSize="8.5"
        fontFamily={MONO}
      >
        {label}
      </text>
    </g>
  );
}

export function SequenceDiagram() {
  return (
    <SageWell compact>
      <div className="flex min-h-0 w-full flex-col gap-1.5 md:grid md:grid-cols-[minmax(156px,176px)_minmax(0,1fr)] md:items-center md:gap-3">
        <FloatWindow className="order-1 min-w-0 overflow-hidden md:order-2">
          <div className="flex items-center justify-between gap-3 border-b border-black/[0.06] bg-[#f7f7f5] px-3 py-2 md:py-2.5">
            <div className="min-w-0 truncate text-[13px] font-medium text-black">
              Trace · matching.worker
            </div>
            <span className="shrink-0 font-mono text-[10px] uppercase tracking-[0.12em] text-gray-new-40">
              3 hops · <span className="text-[#C43D3D]">1 denied</span>
            </span>
          </div>

          <div className="hidden bg-white px-2 pt-1.5 pb-2 md:block">
            <svg
              viewBox="0 0 460 128"
              className="mx-auto h-[164px] w-full max-h-[164px]"
              aria-hidden
            >
              <rect x="0" y="3" width="20" height="122" rx="4" fill={CREAM} />
              {XS.map((cx, i) => (
                <rect
                  key={`wash-${LANES[i]}`}
                  x={cx - 48}
                  y="3"
                  width="96"
                  height="122"
                  rx="8"
                  fill={LANE_WASH[i]}
                />
              ))}
              <line
                x1={XS[0] + 35}
                y1="13.5"
                x2={XS[3] - 35}
                y2="13.5"
                stroke={INK}
                strokeOpacity="0.2"
              />
              {LANES.map((_, i) => participantBox(i))}
              {XS.map((cx, i) => (
                <line
                  key={`life-${LANES[i]}`}
                  x1={cx}
                  y1="24"
                  x2={cx}
                  y2="124"
                  stroke={i === 3 ? BLOCK : INK}
                  strokeOpacity={i === 3 ? 0.32 : 0.22}
                  strokeDasharray="3.2 3.4"
                />
              ))}
              <rect x="65.5" y="40" width="9" height="20" rx="2.5" fill={INK} />
              <rect x="175.5" y="40" width="9" height="48" rx="2.5" fill={INK} />
              <rect x="285.5" y="72" width="9" height="48" rx="2.5" fill={INK} />
              {HOP_Y.map((y, i) => (
                <g key={`tick-${i}`}>
                  <circle cx="10" cy={y} r="2.2" fill={i === 2 ? BLOCK : INK} />
                  <text
                    x="10"
                    y={y - 8}
                    textAnchor="middle"
                    fill={MUTED}
                    fontSize="8"
                    fontFamily={MONO}
                  >
                    {String(i + 1).padStart(2, "0")}
                  </text>
                </g>
              ))}
              {MESSAGES.map((msg, i) => {
                const x1 = XS[msg.from];
                const x2 = XS[msg.to];
                const y = HOP_Y[i];
                const blocked = msg.tone === "block";
                const color = blocked ? BLOCK : INK;
                const start = x1 + 7;
                const end = blocked ? x2 - 16 : x2 - 7;
                const mid = (start + end) / 2 - (blocked ? 8 : 0);
                return (
                  <g key={msg.label}>
                    <line
                      x1={start}
                      y1={y}
                      x2={end - 7}
                      y2={y}
                      stroke={color}
                      strokeWidth="1.5"
                      strokeLinecap="round"
                      strokeDasharray={blocked ? "4.5 3.2" : undefined}
                    />
                    <path d={`M${end} ${y} l-7.2 -3.6 v7.2 z`} fill={color} />
                    {blocked ? (
                      <g transform={`translate(${x2}, ${y})`}>
                        <circle r="7.5" fill="white" stroke={BLOCK} strokeWidth="1.4" />
                        <path
                          d="M-3.1 -3.1 L3.1 3.1 M3.1 -3.1 L-3.1 3.1"
                          stroke={BLOCK}
                          strokeWidth="1.4"
                          strokeLinecap="round"
                        />
                      </g>
                    ) : (
                      <circle cx={x1} cy={y} r={i === 0 ? 2.8 : 2.3} fill={i === 0 ? PASS : INK} />
                    )}
                    {messageLabel(msg.label, mid, y, blocked)}
                  </g>
                );
              })}
            </svg>
          </div>

          <ol className="md:sr-only">
            {MESSAGES.map((msg, i) => (
              <li
                key={msg.label}
                className={cn(
                  "flex items-start gap-2.5 border-b border-l-2 border-black/[0.05] px-3 py-1.5 last:border-b-0",
                  msg.tone === "block" ? "border-l-[#C43D3D] bg-white" : "border-l-transparent",
                )}
              >
                <span
                  className={cn(
                    "mt-0.5 w-5 shrink-0 font-mono text-[10px] tabular-nums",
                    msg.tone === "block" ? "text-[#C43D3D]" : "text-[#285D49]",
                  )}
                >
                  {String(i + 1).padStart(2, "0")}
                </span>
                <div className="min-w-0">
                  <div
                    className={cn(
                      "font-mono text-[12px] tracking-extra-tight",
                      msg.tone === "block" ? "text-[#C43D3D]" : "text-black",
                    )}
                  >
                    {msg.label}
                  </div>
                  <div className="mt-0.5 font-mono text-[11px] text-gray-new-40">
                    {LANES[msg.from]} → {LANES[msg.to]}
                    {msg.tone === "block" ? " · denied" : ""}
                  </div>
                </div>
              </li>
            ))}
          </ol>
        </FloatWindow>

        <aside className="order-2 h-fit rounded-[12px] bg-white p-2.5 shadow-[0_16px_48px_rgba(0,0,0,0.14)] max-md:w-full md:order-1 md:p-4">
          <div className="text-[13px] font-semibold tracking-tight text-black">Webhook deny</div>
          <p className="mt-1.5 text-[12px] leading-4 text-gray-new-40 md:mt-2 md:leading-5">
            Production partner hostnames never resolve. The attempt is ledgered.
          </p>
          <div className="mt-2.5 hidden rounded-full border border-[#C43D3D]/30 bg-white px-2.5 py-0.5 font-mono text-[10px] uppercase tracking-[0.12em] text-[#C43D3D] md:inline-flex">
            denied
          </div>
        </aside>
      </div>
    </SageWell>
  );
}
