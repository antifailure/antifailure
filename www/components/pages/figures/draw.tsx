/** Shared SVG drawings. Thin-line isometric and plot primitives. */

import type { ReactNode } from "react";

const MONO = "var(--font-geist-mono), ui-monospace, monospace";
const VB_W = 420;
const VB_H = 280;

function DrawWell({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-0 w-full flex-1 items-center justify-center px-2 py-3">{children}</div>
  );
}

function labelWidth(text: string) {
  return Math.max(56, text.length * 6.8 + 16);
}

function BoxedLabel({
  x,
  y,
  text,
  accent = false,
}: {
  x: number;
  y: number;
  text: string;
  accent?: boolean;
}) {
  const padX = 8;
  const w = labelWidth(text);
  const h = 16;
  const clampedX = Math.max(8, Math.min(VB_W - w - 8, x));
  const clampedY = Math.max(6, Math.min(VB_H - h - 6, y));
  return (
    <g>
      <rect
        x={clampedX}
        y={clampedY}
        width={w}
        height={h}
        fill={accent ? "rgba(51,191,0,0.14)" : "#f7f7f5"}
        stroke={accent ? "#33bf00" : "rgba(0,0,0,0.35)"}
      />
      <text
        x={clampedX + padX}
        y={clampedY + 11.5}
        fill="rgba(0,0,0,0.78)"
        fontSize="8"
        fontFamily={MONO}
        letterSpacing="0.12em"
      >
        {text}
      </text>
    </g>
  );
}

export function IsoStack({
  planes,
  compact,
}: {
  planes: { label: string; accent?: boolean }[];
  compact?: boolean;
}) {
  const ph = 28;
  const skew = 32;
  const planeW = 220;
  const gap = compact ? 34 : 50;
  const stackH = ph + (planes.length - 1) * gap;
  const vbH = compact ? stackH + 28 : VB_H;
  const top = compact ? 14 : Math.round((VB_H - stackH) / 2);
  const labelX = 16;
  const labelColW = 112;
  const originX = labelX + labelColW + 20;
  return (
    <DrawWell>
      <svg viewBox={`0 0 ${VB_W} ${vbH}`} className="h-auto w-full" preserveAspectRatio="xMidYMid meet" aria-hidden>
        {planes.map((plane, i) => {
          const y = top + i * gap;
          const x = originX + i * 8;
          const pw = planeW - i * 6;
          const fill = plane.accent ? "rgba(51,191,0,0.1)" : "rgba(255,255,255,0.7)";
          const stroke = plane.accent ? "#33bf00" : "rgba(0,0,0,0.45)";
          const midY = y + ph / 2;
          const leaderStart = labelX + labelWidth(plane.label) + 4;
          const dropX = x + pw + skew - 14;
          return (
            <g key={plane.label}>
              <BoxedLabel x={labelX} y={midY - 8} text={plane.label} accent={plane.accent} />
              <line
                x1={leaderStart}
                y1={midY}
                x2={x - 4}
                y2={midY}
                stroke="rgba(0,0,0,0.28)"
                strokeDasharray="2 3"
              />
              <path
                d={`M${x} ${y} L${x + pw} ${y} L${x + pw + skew} ${y + ph} L${x + skew} ${y + ph} Z`}
                fill={fill}
                stroke={stroke}
                strokeWidth="1.1"
              />
              {i < planes.length - 1 ? (
                <line
                  x1={dropX}
                  y1={y + ph}
                  x2={dropX + 8}
                  y2={y + gap}
                  stroke="rgba(0,0,0,0.28)"
                  strokeWidth="1"
                  strokeDasharray="2 3"
                />
              ) : null}
            </g>
          );
        })}
      </svg>
    </DrawWell>
  );
}

export function IsoRings() {
  return (
    <DrawWell>
      <svg viewBox={`0 0 ${VB_W} 176`} className="h-auto w-full" preserveAspectRatio="xMidYMid meet" aria-hidden>
        <ellipse cx="210" cy="88" rx="148" ry="42" fill="none" stroke="rgba(0,0,0,0.22)" strokeWidth="1" strokeDasharray="5 5" />
        <ellipse cx="210" cy="88" rx="96" ry="28" fill="none" stroke="rgba(0,0,0,0.45)" strokeWidth="1" />
        <ellipse cx="210" cy="88" rx="42" ry="13" fill="rgba(51,191,0,0.12)" stroke="#33bf00" strokeWidth="1.2" />
        <rect x="204" y="82" width="12" height="12" fill="#285D49" />
        <BoxedLabel x={210 - labelWidth("PROD") / 2} y={10} text="PROD" />
        <BoxedLabel x={210 - labelWidth("TWIN") / 2} y={150} text="TWIN" accent />
      </svg>
    </DrawWell>
  );
}

export function IsoTwoPlanes({
  top,
  bottom,
  callout,
}: {
  top: string;
  bottom: string;
  callout: string;
}) {
  const topW = labelWidth(top);
  const botW = labelWidth(bottom);
  const callW = labelWidth(callout);
  return (
    <DrawWell>
      <svg viewBox={`0 0 ${VB_W} ${VB_H}`} className="h-auto w-full" preserveAspectRatio="xMidYMid meet" aria-hidden>
        <BoxedLabel x={210 - topW / 2} y={28} text={top} />
        <path d="M88 56 L312 56 L352 90 L128 90 Z" fill="rgba(255,255,255,0.75)" stroke="rgba(0,0,0,0.4)" />
        <line x1="210" y1="90" x2="210" y2="154" stroke="rgba(0,0,0,0.3)" strokeDasharray="2 3" />
        <BoxedLabel x={Math.min(VB_W - callW - 16, 236)} y={112} text={callout} />
        <path d="M72 154 L328 154 L374 196 L118 196 Z" fill="rgba(51,191,0,0.08)" stroke="#33bf00" />
        <BoxedLabel x={210 - botW / 2} y={216} text={bottom} accent />
      </svg>
    </DrawWell>
  );
}

export function DiamondSchematic({
  nodes,
}: {
  nodes: { label: string; cmd?: string }[];
}) {
  const [n, e, s, w] = nodes;
  const placements = [
    { x: 210, y: 38, item: n, labelX: 210 - labelWidth(n.label) / 2, labelY: 8, cmdX: 210, cmdY: 56 },
    { x: 338, y: 140, item: e, labelX: 348, labelY: 108, cmdX: 368, cmdY: 164 },
    { x: 210, y: 232, item: s, labelX: 210 - labelWidth(s.label) / 2, labelY: 252, cmdX: 210, cmdY: 218 },
    { x: 82, y: 140, item: w, labelX: 10, labelY: 108, cmdX: 52, cmdY: 164 },
  ];
  return (
    <DrawWell>
      <svg viewBox={`0 0 ${VB_W} ${VB_H}`} className="h-auto w-full" preserveAspectRatio="xMidYMid meet" aria-hidden>
        <circle cx="210" cy="140" r="84" fill="none" stroke="rgba(0,0,0,0.12)" />
        <circle cx="210" cy="140" r="56" fill="none" stroke="rgba(0,0,0,0.12)" />
        <path d="M210 70 L270 140 L210 210 L150 140 Z" fill="rgba(255,255,255,0.5)" stroke="rgba(0,0,0,0.5)" />
        <path d="M204 134 L216 146 M216 134 L204 146" stroke="rgba(0,0,0,0.45)" strokeWidth="1.2" />
        {placements.map((p) => (
          <g key={p.item.label}>
            <circle cx={p.x} cy={p.y} r="4" fill="#285D49" />
            <BoxedLabel x={p.labelX} y={p.labelY} text={p.item.label} />
            {p.item.cmd ? (
              <text
                x={p.cmdX}
                y={p.cmdY}
                textAnchor="middle"
                fill="#33bf00"
                fontSize="9"
                fontFamily={MONO}
              >
                {p.item.cmd}
              </text>
            ) : null}
          </g>
        ))}
      </svg>
    </DrawWell>
  );
}

export function LockPlot({
  peak,
  peakLabel,
  tone = "fail",
}: {
  peak: number;
  peakLabel: string;
  tone?: "fail" | "pass";
}) {
  const color = tone === "fail" ? "#C43D3D" : "#33bf00";
  const points =
    tone === "fail"
      ? "24,148 48,146 76,142 108,132 140,72 176,36 212,32 248,36 284,76 320,140 356,146 392,148"
      : "24,146 76,142 140,136 176,128 212,124 248,128 284,136 320,142 392,146";
  return (
    <DrawWell>
      <svg viewBox="0 0 420 180" className="h-auto w-full" preserveAspectRatio="xMidYMid meet" aria-hidden>
        {[36, 68, 100, 132].map((y) => (
          <line key={y} x1="20" y1={y} x2="400" y2={y} stroke="rgba(0,0,0,0.08)" />
        ))}
        <polyline fill="none" stroke={color} strokeWidth="1.6" points={points} />
        <text x="24" y="22" fill={color} fontSize="12" fontFamily={MONO}>
          {peakLabel}
        </text>
        <text x="24" y="172" fill="rgba(0,0,0,0.4)" fontSize="9" fontFamily={MONO}>
          {peak}s hold
        </text>
      </svg>
    </DrawWell>
  );
}

export function KeepBar({ kept }: { kept: number }) {
  return (
    <svg viewBox="0 0 360 44" className="w-full" aria-hidden>
      <rect x="0" y="18" width="360" height="12" fill="rgba(0,0,0,0.08)" />
      <rect x="0" y="18" width={360 * kept} height="12" fill="#33bf00" />
      <text x="0" y="12" fill="rgba(0,0,0,0.5)" fontSize="9" fontFamily={MONO}>
        {Math.round(kept * 100)}% kept · joins valid
      </text>
    </svg>
  );
}

export function BypassSchematic() {
  return (
    <DrawWell>
      <svg viewBox="0 0 360 160" className="h-auto w-full" preserveAspectRatio="xMidYMid meet" aria-hidden>
        <rect x="36" y="48" width="116" height="64" fill="none" stroke="rgba(0,0,0,0.45)" />
        <BoxedLabel x={66} y={72} text="TWIN" />
        <line x1="152" y1="80" x2="228" y2="80" stroke="#C43D3D" strokeDasharray="3 3" />
        <path d="M220 72 L240 80 L220 88" fill="none" stroke="#C43D3D" />
        <circle cx="268" cy="80" r="16" fill="none" stroke="#C43D3D" />
        <path d="M258 70 L278 90 M278 70 L258 90" stroke="#C43D3D" />
      </svg>
    </DrawWell>
  );
}

export function CycleSchematic({ nodes }: { nodes: string[] }) {
  const steps = nodes.slice(0, 4);
  const positions = [
    { x: 210, y: 42 },
    { x: 332, y: 140 },
    { x: 210, y: 238 },
    { x: 88, y: 140 },
  ];

  return (
    <DrawWell>
      <svg viewBox={`0 0 ${VB_W} ${VB_H}`} className="h-auto w-full" preserveAspectRatio="xMidYMid meet" aria-hidden>
        <circle cx="210" cy="140" r="82" fill="rgba(51,191,0,0.035)" stroke="rgba(0,0,0,0.12)" />
        <path
          d="M210 58 C278 58 316 90 326 126 M326 154 C316 194 278 222 225 224 M195 224 C142 222 104 194 94 154 M94 126 C104 90 142 58 196 58"
          fill="none"
          stroke="rgba(0,0,0,0.34)"
          strokeDasharray="3 4"
        />
        {steps.map((node, index) => {
          const point = positions[index];
          return (
            <g key={node}>
              <circle cx={point.x} cy={point.y} r="18" fill="white" stroke={index === 3 ? "#33bf00" : "rgba(0,0,0,0.36)"} />
              <circle cx={point.x} cy={point.y} r="4" fill={index === 3 ? "#285D49" : "rgba(0,0,0,0.58)"} />
              <BoxedLabel x={point.x - labelWidth(node) / 2} y={point.y + (index === 0 ? -38 : index === 2 ? 24 : -8)} text={node} accent={index === 3} />
            </g>
          );
        })}
      </svg>
    </DrawWell>
  );
}

export function GatewaySchematic() {
  return (
    <DrawWell>
      <svg viewBox={`0 0 ${VB_W} ${VB_H}`} className="h-auto w-full" preserveAspectRatio="xMidYMid meet" aria-hidden>
        <BoxedLabel x={34} y={38} text="checkout twin" accent />
        <rect x="40" y="70" width="116" height="86" rx="2" fill="rgba(51,191,0,0.08)" stroke="#33bf00" />
        <path d="M156 112 H202" stroke="rgba(0,0,0,0.38)" strokeDasharray="3 4" />
        <rect x="202" y="76" width="72" height="72" rx="2" fill="#fff" stroke="rgba(0,0,0,0.42)" />
        <BoxedLabel x={202 + 36 - labelWidth("egress gateway") / 2} y={158} text="egress gateway" />
        <path d="M274 96 H348" stroke="#33bf00" />
        <path d="M274 128 H348" stroke="#C43D3D" strokeDasharray="4 4" />
        <circle cx="360" cy="96" r="14" fill="rgba(51,191,0,0.1)" stroke="#33bf00" />
        <circle cx="360" cy="128" r="14" fill="rgba(196,61,61,0.09)" stroke="#C43D3D" />
        <text x="291" y="90" fill="#285D49" fontSize="9" fontFamily={MONO}>
          ledger
        </text>
        <text x="291" y="122" fill="#C43D3D" fontSize="9" fontFamily={MONO}>
          live api
        </text>
        <path d="M354 122 L366 134 M366 122 L354 134" stroke="#C43D3D" strokeWidth="1.2" />
      </svg>
    </DrawWell>
  );
}

export function IsoWorkers() {
  const lanes = [
    { label: "orders", y: 70, x2: 294, tone: "#33bf00" },
    { label: "match", y: 116, x2: 258, tone: "#8A6A12" },
    { label: "notify", y: 162, x2: 314, tone: "#33bf00" },
  ];

  return (
    <DrawWell>
      <svg viewBox={`0 0 ${VB_W} ${VB_H}`} className="h-auto w-full" preserveAspectRatio="xMidYMid meet" aria-hidden>
        <BoxedLabel x={30} y={24} text="queue replay" accent />
        <BoxedLabel x={284} y={24} text="workers" />
        {lanes.map((lane, index) => (
          <g key={lane.label}>
            <rect x="34" y={lane.y - 16} width="92" height="32" fill="#fff" stroke="rgba(0,0,0,0.36)" />
            <text x="48" y={lane.y + 3} fill="rgba(0,0,0,0.72)" fontSize="10" fontFamily={MONO}>
              {lane.label}
            </text>
            <line x1="126" y1={lane.y} x2={lane.x2} y2={lane.y} stroke={lane.tone} strokeWidth="1.4" />
            <path d={`M${lane.x2 - 8} ${lane.y - 6} L${lane.x2} ${lane.y} L${lane.x2 - 8} ${lane.y + 6}`} fill="none" stroke={lane.tone} />
            <path
              d={`M${292 + index * 10} ${lane.y - 18} L350 ${lane.y - 18} L370 ${lane.y} L312 ${lane.y} Z`}
              fill={index === 1 ? "rgba(138,106,18,0.08)" : "rgba(51,191,0,0.08)"}
              stroke={index === 1 ? "#8A6A12" : "#33bf00"}
            />
          </g>
        ))}
      </svg>
    </DrawWell>
  );
}
