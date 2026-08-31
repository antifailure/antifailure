import type { CSSProperties, ReactNode } from "react";
import { cn } from "@/lib/cn";

const EASE = "cubic-bezier(0.16, 1, 0.3, 1)";
const GREEN = "#33BF00";
const CREAM = "#f4f7f5";
const INK = "#111111";

const MINI_CSS = `
.mini-frame { --hovered: 0; }
.group:hover .mini-frame { --hovered: 1; }

.mini-twin-ring { stroke-dasharray: 100.53; stroke-dashoffset: 0; }
.mini-twin-inset { fill: ${GREEN}; }
.mini-scramble {
  font-family: var(--font-geist-sans), var(--font-inter), ui-sans-serif, system-ui, sans-serif;
  font-size: 6.5px;
  letter-spacing: -0.02em;
  color: ${GREEN};
  white-space: nowrap;
  /* The masked address is longer than the cell. Dissolve it rather than
     letting it run into the frame edge. */
  -webkit-mask-image: linear-gradient(to right, #000 60%, transparent 100%);
  mask-image: linear-gradient(to right, #000 60%, transparent 100%);
}
.mini-scramble::after { content: "m***@twin"; }
.mini-packet { transform: translate(28px, 0); }
.mini-fw-block,
.mini-report-block,
.mini-high {
  transform-box: fill-box;
  transform-origin: 50% 50%;
  opacity: 1;
}
.mini-hit { opacity: 1; }
.mini-fader {
  transform-box: fill-box;
  transform-origin: 50% 100%;
  transform: scaleY(1);
}
.mini-lock { transform-origin: 0 50%; transform-box: fill-box; transform: scaleX(1); }
.mini-pip { fill: #dc2626; stroke: #dc2626; }
.mini-bar { transform-origin: 0 50%; transform-box: fill-box; transform: scaleX(1); }

@media (prefers-reduced-motion: no-preference) {
  .group:hover .mini-twin-ring { animation: mini-twin-pulse 900ms ${EASE} both; }
  .group:hover .mini-twin-inset { animation: mini-twin-glow 900ms ${EASE} both; }
  .group:hover .mini-scramble::after { animation: mini-scramble 720ms steps(1, end) both; }
  .group:hover .mini-packet { animation: mini-packet 880ms ${EASE} both; }
  .group:hover .mini-fw-block,
  .group:hover .mini-report-block,
  .group:hover .mini-high { animation: mini-stamp 720ms ${EASE} both; }
  .group:hover .mini-fader { animation: mini-fader 820ms ${EASE} both; }
  .group:hover .mini-lock { animation: mini-lock 900ms ${EASE} both; }
  .group:hover .mini-pip { animation: mini-pip 720ms ${EASE} both; }
  .group:hover .mini-hit { animation: mini-hit 820ms ${EASE} both; }
  .group:hover .mini-bar {
    animation: mini-bar 820ms ${EASE} both;
    animation-delay: calc(var(--stagger, 0) * 50ms);
  }
}

@keyframes mini-twin-pulse {
  0%, 100% { opacity: 1; }
  40% { opacity: 0.28; }
}
@keyframes mini-twin-glow {
  0%, 100% { fill: ${GREEN}; }
  40% { fill: ${CREAM}; }
}
@keyframes mini-scramble {
  0% { content: "ada@corp.io"; color: ${INK}; }
  22% { content: "a#a@c*rp.io"; color: ${INK}; }
  45% { content: "m**@twin.**"; color: ${INK}; }
  72% { content: "m***@twin"; color: ${GREEN}; }
  100% { content: "m***@twin"; color: ${GREEN}; }
}
@keyframes mini-packet {
  0%, 100% { transform: translate(28px, 0); }
  38% { transform: translate(0, 0); }
}
@keyframes mini-stamp {
  0%, 100% { transform: scale(1); opacity: 1; }
  40% { transform: scale(0.96); opacity: 0.55; }
}
@keyframes mini-fader {
  0%, 100% { transform: scaleY(1); }
  40% { transform: scaleY(0.7); }
}
@keyframes mini-lock {
  0%, 100% { transform: scaleX(1); }
  42% { transform: scaleX(0.76); }
}
@keyframes mini-pip {
  0%, 100% { fill: #dc2626; stroke: #dc2626; }
  40% { fill: ${CREAM}; stroke: #9a9a9a; }
}
@keyframes mini-hit {
  0%, 100% { opacity: 1; }
  40% { opacity: 0.2; }
}
@keyframes mini-bar {
  0%, 100% { transform: scaleX(1); }
  40% { transform: scaleX(0.8); }
}
`;

const SANS = "var(--font-geist-sans), var(--font-inter), ui-sans-serif, system-ui, sans-serif";

function Svg({ children }: { children: ReactNode }) {
  return (
    <svg viewBox="0 0 96 64" className="absolute inset-0 h-full w-full overflow-hidden" aria-hidden>
      {children}
    </svg>
  );
}

function OverviewMini() {
  const cells = [
    { x: 12, y: 10, label: "twin" },
    { x: 50, y: 10, label: "state" },
    { x: 12, y: 34, label: "fire" },
    { x: 50, y: 34, label: "gate" },
  ];
  return (
    <Svg>
      {cells.map((c) => (
        <g key={c.label}>
          <rect x={c.x} y={c.y} width="34" height="20" fill="none" stroke="rgba(0,0,0,0.16)" />
          <text x={c.x + 17} y={c.y + 13} textAnchor="middle" fill={INK} fontFamily={SANS} fontSize="7">
            {c.label}
          </text>
        </g>
      ))}
    </Svg>
  );
}

function IsolatedTwinMini() {
  return (
    <Svg>
      <circle cx="48" cy="27" r="16" fill="none" stroke="rgba(0,0,0,0.12)" strokeWidth="1" />
      <circle className="mini-twin-ring" cx="48" cy="27" r="16" fill="none" stroke={INK} strokeWidth="1.25" />
      <rect className="mini-twin-inset" x="40" y="19" width="16" height="16" rx="1" />
      <line x1="43" y1="24" x2="53" y2="24" stroke={INK} strokeOpacity="0.4" strokeWidth="1" />
      <line x1="43" y1="27" x2="51" y2="27" stroke={INK} strokeOpacity="0.3" strokeWidth="1" />
      <line x1="43" y1="30" x2="52" y2="30" stroke={INK} strokeOpacity="0.22" strokeWidth="1" />
      <text x="48" y="56" textAnchor="middle" fill="rgba(0,0,0,0.45)" fontFamily={SANS} fontSize="7">
        candidate
      </text>
    </Svg>
  );
}

function SafeStateMini() {
  return (
    <>
      <Svg>
        <rect x="10" y="10" width="76" height="44" fill="none" stroke="rgba(0,0,0,0.14)" />
        <line x1="10" y1="32" x2="86" y2="32" stroke="rgba(0,0,0,0.1)" />
        <line x1="28" y1="10" x2="28" y2="54" stroke="rgba(0,0,0,0.1)" />
        <text x="14" y="24" fill="rgba(0,0,0,0.4)" fontFamily={SANS} fontSize="7">
          em
        </text>
        <text x="14" y="46" fill="rgba(0,0,0,0.4)" fontFamily={SANS} fontSize="7">
          tok
        </text>
        <text x="34" y="46" fill={INK} fontFamily={SANS} fontSize="7">
          deleted
        </text>
      </Svg>
      <span className="mini-scramble pointer-events-none absolute top-[22%] left-[34%] right-[12%] overflow-hidden leading-none" />
    </>
  );
}

function FirewallMini() {
  return (
    <Svg>
      <line x1="12" y1="32" x2="50" y2="32" stroke="rgba(0,0,0,0.16)" strokeDasharray="3 3" />
      <g className="mini-packet">
        <rect x="12" y="27" width="9" height="10" fill={INK} />
      </g>
      <line x1="56" y1="16" x2="56" y2="48" stroke={INK} strokeWidth="1.5" />
      <g className="mini-fw-block">
        <rect x="62" y="25" width="22" height="14" fill={CREAM} stroke="#dc2626" strokeWidth="1" />
        <text x="73" y="35" textAnchor="middle" fill="#b91c1c" fontFamily={SANS} fontSize="6.5">
          DENY
        </text>
      </g>
    </Svg>
  );
}

function WorkloadMini() {
  const tracks = [
    { x: 18, h: 26, label: "obs" },
    { x: 42, h: 18, label: "det" },
    { x: 66, h: 12, label: "ai" },
  ];
  return (
    <Svg>
      {tracks.map((t) => (
        <g key={t.x}>
          <rect x={t.x} y="10" width="12" height="34" fill="none" stroke="rgba(0,0,0,0.16)" />
          <g className="mini-fader">
            <rect x={t.x + 2} y={42 - t.h} width="8" height={t.h} fill={t.x === 18 ? GREEN : "rgba(0,0,0,0.2)"} />
          </g>
          <text x={t.x + 6} y="56" textAnchor="middle" fill="rgba(0,0,0,0.45)" fontFamily={SANS} fontSize="6">
            {t.label}
          </text>
        </g>
      ))}
    </Svg>
  );
}

function MigrationMini() {
  return (
    <Svg>
      <text x="12" y="16" fill="#b91c1c" fontFamily={SANS} fontSize="7">
        ACCESS EXCL
      </text>
      <rect x="12" y="24" width="72" height="14" fill="rgba(0,0,0,0.06)" />
      <rect className="mini-lock" x="12" y="24" width="52" height="14" fill="#dc2626" />
      <text x="12" y="56" fill="rgba(0,0,0,0.5)" fontFamily={SANS} fontSize="7">
        subscriptions
      </text>
      <text x="84" y="56" textAnchor="end" fill="#b91c1c" fontFamily={SANS} fontSize="7">
        27.4s
      </text>
    </Svg>
  );
}

function SafetyReportMini() {
  return (
    <Svg>
      <circle className="mini-pip" cx="16" cy="32" r="4.5" fill="none" strokeWidth="1.5" />
      <text x="26" y="27" fill="rgba(0,0,0,0.4)" fontFamily={SANS} fontSize="6">
        check
      </text>
      <text x="26" y="41" fill={INK} fontFamily={SANS} fontSize="7">
        lock
      </text>
      <g className="mini-report-block">
        <rect x="60" y="25" width="24" height="14" fill={CREAM} stroke="#dc2626" strokeWidth="1" />
        <text x="72" y="35" textAnchor="middle" fill="#b91c1c" fontFamily={SANS} fontSize="6.5">
          BLOCK
        </text>
      </g>
    </Svg>
  );
}

function ArchitectureMini() {
  return (
    <Svg>
      <rect x="10" y="10" width="34" height="44" fill="none" stroke="rgba(0,0,0,0.16)" />
      <rect x="52" y="10" width="34" height="44" fill="none" stroke="rgba(0,0,0,0.16)" />
      <line x1="48" y1="12" x2="48" y2="52" stroke={GREEN} strokeWidth="1.5" />
      <text x="27" y="24" textAnchor="middle" fill="rgba(0,0,0,0.45)" fontFamily={SANS} fontSize="6">
        control
      </text>
      <text x="69" y="24" textAnchor="middle" fill="rgba(0,0,0,0.45)" fontFamily={SANS} fontSize="6">
        data
      </text>
      <line x1="16" y1="32" x2="38" y2="32" stroke="rgba(0,0,0,0.14)" />
      <line x1="16" y1="40" x2="34" y2="40" stroke="rgba(0,0,0,0.1)" />
      <line x1="58" y1="32" x2="80" y2="32" stroke="rgba(0,0,0,0.14)" />
      <line x1="58" y1="40" x2="76" y2="40" stroke="rgba(0,0,0,0.1)" />
    </Svg>
  );
}

const MINIS: Record<string, () => ReactNode> = {
  Overview: OverviewMini,
  "Isolated Twin": IsolatedTwinMini,
  "Safe State": SafeStateMini,
  "Side-Effect Firewall": FirewallMini,
  Load: WorkloadMini,
  "Migration Safety": MigrationMini,
  "Safety Report": SafetyReportMini,
  Architecture: ArchitectureMini,
};

export function ProductMiniStyles() {
  return <style dangerouslySetInnerHTML={{ __html: MINI_CSS }} />;
}

function Cylinder({
  x,
  y,
  on,
}: {
  x: number;
  y: number;
  on?: boolean;
}) {
  const fill = on ? GREEN : "white";
  const stroke = on ? GREEN : "rgba(0,0,0,0.22)";
  return (
    <g>
      <path d={`M${x} ${y + 5}v10c0 2.6 4 4.8 9 4.8s9-2.2 9-4.8V${y + 5}`} fill={fill} stroke={stroke} strokeWidth="1.2" />
      <ellipse cx={x + 9} cy={y + 5} rx="9" ry="4.8" fill={fill} stroke={stroke} strokeWidth="1.2" />
    </g>
  );
}

export function MenuCardArt({ kind }: { kind: "twin" | "fleet" }) {
  return (
    <svg viewBox="0 0 168 88" className="h-[88px] w-[168px] shrink-0" fill="none" aria-hidden>
      {kind === "twin" ? (
        <>
          <rect x="8" y="32" width="28" height="18" rx="3" fill="white" stroke="rgba(0,0,0,0.22)" strokeWidth="1.2" />
          <text x="22" y="44" textAnchor="middle" fill={INK} fontFamily={SANS} fontSize="8" fontWeight="600">
            twin
          </text>
          <path d="M36 41H58" stroke="rgba(0,0,0,0.2)" strokeWidth="1.2" />
          <path d="M58 41V18" stroke="rgba(0,0,0,0.2)" strokeWidth="1.2" />
          <path d="M58 41V64" stroke="rgba(0,0,0,0.2)" strokeWidth="1.2" />
          <path d="M58 18H78" stroke="rgba(0,0,0,0.2)" strokeWidth="1.2" />
          <path d="M58 41H78" stroke="rgba(0,0,0,0.2)" strokeWidth="1.2" />
          <path d="M58 64H78" stroke="rgba(0,0,0,0.2)" strokeWidth="1.2" />
          <Cylinder x={78} y={8} />
          <Cylinder x={78} y={32} on />
          <Cylinder x={78} y={56} />
        </>
      ) : (
        <>
          {[0, 1, 2].map((row) =>
            [0, 1, 2].map((col) => (
              <Cylinder
                key={`${row}-${col}`}
                x={18 + col * 48}
                y={8 + row * 26}
                on={(row === 1 && col === 1) || (row === 0 && col === 2) || (row === 2 && col === 0)}
              />
            )),
          )}
        </>
      )}
    </svg>
  );
}

export function HeaderMini({ title, size = "nav" }: { title: string; size?: "nav" | "card" }) {
  const Mini = MINIS[title];
  if (!Mini) return null;
  return (
    <span
      className={cn(
        "mini-frame pointer-events-none relative isolate block shrink-0 overflow-hidden",
        size === "card"
          ? "h-[92px] w-[148px] rounded-[8px] bg-white ring-1 ring-black/[0.08]"
          : "h-14 w-[84px] rounded-[3px] bg-[#f4f7f5] ring-1 ring-black/10",
      )}
      aria-hidden
    >
      <span className="absolute inset-[2px] overflow-hidden">
        <Mini />
      </span>
    </span>
  );
}
