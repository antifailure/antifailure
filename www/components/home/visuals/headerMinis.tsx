import type { CSSProperties, ReactNode } from "react";
import { cn } from "@/lib/cn";

const EASE = "cubic-bezier(0.16, 1, 0.3, 1)";
const GREEN = "#33BF00";
const CREAM = "#f4f7f5";
const INK = "#111111";

const MINI_CSS = `
.mini-frame { --hovered: 0; }
.group:hover .mini-frame { --hovered: 1; }

.mini-twin-ring { stroke-dasharray: 113.1; stroke-dashoffset: 0; }
.mini-twin-inset { fill: ${GREEN}; }
.mini-scramble {
  font-family: var(--font-inter), ui-sans-serif, system-ui, sans-serif;
  font-size: 7px;
  letter-spacing: -0.02em;
  color: ${GREEN};
  white-space: nowrap;
}
.mini-scramble::after { content: "m***@twin.local"; }
.mini-packet { transform: translate(40px, 0); }
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
.mini-scan { transform: translateY(36px); }
.mini-bar { transform-origin: 0 50%; transform-box: fill-box; transform: scaleX(1); }

@media (prefers-reduced-motion: no-preference) {
  .group:hover .mini-twin-ring { animation: mini-twin-ring 400ms ${EASE} both; }
  .group:hover .mini-twin-inset { animation: mini-twin-inset 400ms ${EASE} both; }
  .group:hover .mini-scramble::after { animation: mini-scramble 500ms steps(1, end) both; }
  .group:hover .mini-packet { animation: mini-packet 450ms ${EASE} both; }
  .group:hover .mini-fw-block,
  .group:hover .mini-report-block,
  .group:hover .mini-high { animation: mini-stamp 450ms ${EASE} both; }
  .group:hover .mini-fader { animation: mini-fader 400ms ${EASE} both; }
  .group:hover .mini-lock { animation: mini-lock 500ms ${EASE} both; }
  .group:hover .mini-pip { animation: mini-pip 450ms ${EASE} both; }
  .group:hover .mini-scan { animation: mini-scan 600ms ${EASE} both; }
  .group:hover .mini-hit { animation: mini-hit 600ms ${EASE} both; }
  .group:hover .mini-bar {
    animation: mini-bar 600ms ${EASE} both;
    animation-delay: calc(var(--stagger, 0) * 40ms);
  }
}

@keyframes mini-twin-ring {
  from { stroke-dashoffset: 113.1; }
  to { stroke-dashoffset: 0; }
}
@keyframes mini-twin-inset {
  from { fill: ${CREAM}; }
  to { fill: ${GREEN}; }
}
@keyframes mini-scramble {
  0% { content: "ada@corp.io"; color: ${INK}; }
  20% { content: "a#a@c*rp.io"; color: ${INK}; }
  40% { content: "m**@twin.**"; color: ${INK}; }
  70% { content: "m***@twin.local"; color: ${GREEN}; }
  100% { content: "m***@twin.local"; color: ${GREEN}; }
}
@keyframes mini-packet {
  from { transform: translate(0, 0); }
  to { transform: translate(40px, 0); }
}
@keyframes mini-stamp {
  0% { transform: scale(1.18); opacity: 0; }
  100% { transform: scale(1); opacity: 1; }
}
@keyframes mini-fader {
  from { transform: scaleY(0.12); }
  to { transform: scaleY(1); }
}
@keyframes mini-lock {
  from { transform: scaleX(0); }
  to { transform: scaleX(1); }
}
@keyframes mini-pip {
  0%, 40% { fill: ${CREAM}; stroke: #9a9a9a; }
  100% { fill: #dc2626; stroke: #dc2626; }
}
@keyframes mini-scan {
  from { transform: translateY(0); }
  to { transform: translateY(36px); }
}
@keyframes mini-hit {
  0%, 55% { opacity: 0; }
  100% { opacity: 1; }
}
@keyframes mini-bar {
  from { transform: scaleX(0); }
  to { transform: scaleX(1); }
}
`;

const SANS = "var(--font-inter), ui-sans-serif, system-ui, sans-serif";

function Svg({ children }: { children: ReactNode }) {
  return (
    <svg viewBox="0 0 96 64" className="absolute inset-0 h-full w-full" aria-hidden>
      {children}
    </svg>
  );
}

function OverviewMini() {
  const cells = [
    { x: 10, y: 10, label: "twin" },
    { x: 50, y: 10, label: "state" },
    { x: 10, y: 34, label: "fire" },
    { x: 50, y: 34, label: "gate" },
  ];
  return (
    <Svg>
      {cells.map((c) => (
        <g key={c.label}>
          <rect x={c.x} y={c.y} width="36" height="20" fill="none" stroke="rgba(0,0,0,0.16)" />
          <text x={c.x + 18} y={c.y + 13} textAnchor="middle" fill={INK} fontFamily={SANS} fontSize="7">
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
      <circle cx="48" cy="28" r="18" fill="none" stroke="rgba(0,0,0,0.12)" strokeWidth="1" />
      <circle
        className="mini-twin-ring"
        cx="48"
        cy="28"
        r="18"
        fill="none"
        stroke={INK}
        strokeWidth="1.25"
      />
      <rect className="mini-twin-inset" x="38" y="18" width="20" height="20" rx="1" />
      <line x1="42" y1="24" x2="54" y2="24" stroke={INK} strokeOpacity="0.4" strokeWidth="1" />
      <line x1="42" y1="28" x2="51" y2="28" stroke={INK} strokeOpacity="0.3" strokeWidth="1" />
      <line x1="42" y1="32" x2="53" y2="32" stroke={INK} strokeOpacity="0.22" strokeWidth="1" />
      <text x="48" y="58" textAnchor="middle" fill="rgba(0,0,0,0.45)" fontFamily={SANS} fontSize="7">
        candidate
      </text>
    </Svg>
  );
}

function SafeStateMini() {
  return (
    <>
      <Svg>
        <rect x="6" y="10" width="84" height="44" fill="none" stroke="rgba(0,0,0,0.14)" />
        <line x1="6" y1="32" x2="90" y2="32" stroke="rgba(0,0,0,0.1)" />
        <line x1="30" y1="10" x2="30" y2="54" stroke="rgba(0,0,0,0.1)" />
        <text x="10" y="24" fill="rgba(0,0,0,0.4)" fontFamily={SANS} fontSize="7">
          em
        </text>
        <text x="10" y="46" fill="rgba(0,0,0,0.4)" fontFamily={SANS} fontSize="7">
          tok
        </text>
        <text x="36" y="46" fill={INK} fontFamily={SANS} fontSize="7">
          deleted
        </text>
      </Svg>
      <span className="mini-scramble pointer-events-none absolute top-[16px] left-[36px] w-[50px] overflow-hidden leading-none" />
    </>
  );
}

function FirewallMini() {
  return (
    <Svg>
      <line x1="8" y1="32" x2="50" y2="32" stroke="rgba(0,0,0,0.16)" strokeDasharray="3 3" />
      <g className="mini-packet">
        <rect x="8" y="27" width="10" height="10" fill={INK} />
      </g>
      <line x1="58" y1="12" x2="58" y2="52" stroke={INK} strokeWidth="1.5" />
      <g className="mini-fw-block">
        <rect x="64" y="24" width="26" height="16" fill={CREAM} stroke="#dc2626" strokeWidth="1" />
        <text x="77" y="35" textAnchor="middle" fill="#b91c1c" fontFamily={SANS} fontSize="7">
          DENY
        </text>
      </g>
    </Svg>
  );
}

function WorkloadMini() {
  const tracks = [
    { x: 16, h: 28, label: "obs" },
    { x: 42, h: 20, label: "det" },
    { x: 68, h: 14, label: "ai" },
  ];
  return (
    <Svg>
      {tracks.map((t) => (
        <g key={t.x}>
          <rect x={t.x} y="8" width="12" height="36" fill="none" stroke="rgba(0,0,0,0.16)" />
          <g className="mini-fader">
            <rect x={t.x + 2} y={44 - t.h} width="8" height={t.h} fill={t.x === 16 ? GREEN : "rgba(0,0,0,0.2)"} />
          </g>
          <text x={t.x + 6} y="58" textAnchor="middle" fill="rgba(0,0,0,0.45)" fontFamily={SANS} fontSize="6">
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
      <text x="8" y="16" fill="#b91c1c" fontFamily={SANS} fontSize="7">
        ACCESS EXCL
      </text>
      <rect x="8" y="24" width="80" height="16" fill="rgba(0,0,0,0.06)" />
      <rect className="mini-lock" x="8" y="24" width="58" height="16" fill="#dc2626" />
      <text x="8" y="56" fill="rgba(0,0,0,0.5)" fontFamily={SANS} fontSize="7">
        subscriptions
      </text>
      <text x="88" y="56" textAnchor="end" fill="#b91c1c" fontFamily={SANS} fontSize="7">
        27.4s
      </text>
    </Svg>
  );
}

function SafetyReportMini() {
  return (
    <Svg>
      <circle className="mini-pip" cx="16" cy="32" r="5" fill="none" strokeWidth="1.5" />
      <text x="28" y="29" fill="rgba(0,0,0,0.4)" fontFamily={SANS} fontSize="6">
        check
      </text>
      <text x="28" y="40" fill={INK} fontFamily={SANS} fontSize="7">
        lock
      </text>
      <g className="mini-report-block">
        <rect x="62" y="24" width="28" height="16" fill={CREAM} stroke="#dc2626" strokeWidth="1" />
        <text x="76" y="35" textAnchor="middle" fill="#b91c1c" fontFamily={SANS} fontSize="7">
          BLOCK
        </text>
      </g>
    </Svg>
  );
}

function ChangeIntelligenceMini() {
  return (
    <Svg>
      <text x="8" y="18" fill="rgba(0,0,0,0.4)" fontFamily={SANS} fontSize="7">
        app.ts
      </text>
      <text x="8" y="36" fill={INK} fontFamily={SANS} fontSize="7">
        migrate.sql
      </text>
      <g className="mini-high">
        <rect x="62" y="26" width="26" height="14" fill={CREAM} stroke="#dc2626" strokeWidth="1" />
        <text x="75" y="36" textAnchor="middle" fill="#b91c1c" fontFamily={SANS} fontSize="7">
          HIGH
        </text>
      </g>
      <line x1="8" y1="46" x2="88" y2="46" stroke="rgba(0,0,0,0.1)" />
      <text x="8" y="58" fill="rgba(0,0,0,0.45)" fontFamily={SANS} fontSize="6">
        migration detected
      </text>
    </Svg>
  );
}

function DifferentialOracleMini() {
  return (
    <Svg>
      <line x1="8" y1="32" x2="88" y2="32" stroke={GREEN} strokeWidth="1.4" />
      <circle cx="18" cy="32" r="2.2" fill={GREEN} />
      <path d="M34 32 C34 22, 34 16, 44 16 H78" fill="none" stroke={GREEN} strokeWidth="1.2" strokeDasharray="2 3" />
      <path d="M58 32 C58 42, 58 48, 68 48 H88" fill="none" stroke={GREEN} strokeWidth="1.2" strokeDasharray="2 3" />
      <rect className="mini-hit" x="46" y="11" width="28" height="10" rx="5" fill="#111" />
      <circle cx="78" cy="16" r="3" fill="#dc2626" />
      <circle cx="82" cy="48" r="3" fill={GREEN} />
    </Svg>
  );
}

function FidelityGraphMini() {
  const rows = [
    { label: "svc", w: 58 },
    { label: "pg", w: 10 },
    { label: "3p", w: 44 },
    { label: "cov", w: 50 },
  ];
  return (
    <Svg>
      {rows.map((row, i) => {
        const y = 10 + i * 13;
        return (
          <g key={row.label}>
            <text x="8" y={y + 7} fill="rgba(0,0,0,0.45)" fontFamily={SANS} fontSize="6">
              {row.label}
            </text>
            <rect x="28" y={y} width="60" height="8" fill="rgba(0,0,0,0.06)" />
            <rect
              className="mini-bar"
              x="28"
              y={y}
              width={row.w}
              height="8"
              fill={GREEN}
              style={{ ["--stagger" as `--stagger`]: i } as CSSProperties}
            />
          </g>
        );
      })}
    </Svg>
  );
}

function ArchitectureMini() {
  return (
    <Svg>
      <rect x="6" y="10" width="38" height="44" fill="none" stroke="rgba(0,0,0,0.16)" />
      <rect x="52" y="10" width="38" height="44" fill="none" stroke="rgba(0,0,0,0.16)" />
      <line x1="48" y1="10" x2="48" y2="54" stroke={GREEN} strokeWidth="1.5" />
      <text x="25" y="24" textAnchor="middle" fill="rgba(0,0,0,0.45)" fontFamily={SANS} fontSize="6">
        control
      </text>
      <text x="71" y="24" textAnchor="middle" fill="rgba(0,0,0,0.45)" fontFamily={SANS} fontSize="6">
        data
      </text>
      <line x1="12" y1="32" x2="38" y2="32" stroke="rgba(0,0,0,0.14)" />
      <line x1="12" y1="40" x2="34" y2="40" stroke="rgba(0,0,0,0.1)" />
      <line x1="58" y1="32" x2="84" y2="32" stroke="rgba(0,0,0,0.14)" />
      <line x1="58" y1="40" x2="80" y2="40" stroke="rgba(0,0,0,0.1)" />
    </Svg>
  );
}

const MINIS: Record<string, () => ReactNode> = {
  Overview: OverviewMini,
  "Isolated Twin": IsolatedTwinMini,
  "Safe State": SafeStateMini,
  "Side-Effect Firewall": FirewallMini,
  "Workload Studio": WorkloadMini,
  "Migration Safety": MigrationMini,
  "Safety Report": SafetyReportMini,
  "Change Intelligence": ChangeIntelligenceMini,
  "Differential Oracle": DifferentialOracleMini,
  "Fidelity Graph": FidelityGraphMini,
  Architecture: ArchitectureMini,
};

export function ProductMiniStyles() {
  return <style dangerouslySetInnerHTML={{ __html: MINI_CSS }} />;
}

export function HeaderMini({ title }: { title: string }) {
  const Mini = MINIS[title];
  if (!Mini) return null;
  return (
    <span
      className={cn(
        "mini-frame pointer-events-none relative isolate block h-12 w-[72px] shrink-0 overflow-hidden",
        "bg-[#f4f7f5] ring-1 ring-black/10",
      )}
      aria-hidden
    >
      <Mini />
    </span>
  );
}
