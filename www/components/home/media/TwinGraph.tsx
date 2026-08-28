"use client";

import Image from "next/image";
import { Grain } from "./icons";

const NODES = [
  { x: 180, y: 250, label: "Detect" },
  { x: 400, y: 140, label: "Twin" },
  { x: 620, y: 250, label: "Exercise" },
  { x: 840, y: 140, label: "Judge" },
  { x: 1040, y: 250, label: "Destroy" },
];

export function TwinGraph() {
  return (
    <div className="relative mt-14 aspect-[1184/500] w-full overflow-hidden max-xl:mt-12">
      <Image src="/home/twin-graph.png" alt="" fill sizes="1184px" className="object-cover opacity-80" />
      <svg viewBox="0 0 1184 500" className="absolute inset-0 h-full w-full" aria-hidden>
        <path
          d="M180 250 C280 250, 320 140, 400 140 C500 140, 540 250, 620 250 C720 250, 760 140, 840 140 C940 140, 980 250, 1040 250"
          fill="none"
          stroke="rgba(0,0,0,0.35)"
          strokeWidth="1.6"
          className="film-stroke"
        />
        <path
          d="M180 250 C280 250, 320 140, 400 140 C500 140, 540 250, 620 250 C720 250, 760 140, 840 140 C940 140, 980 250, 1040 250"
          fill="none"
          stroke="#33bf00"
          strokeWidth="2"
          strokeDasharray="8 160"
          className="film-dash"
        />
        {NODES.map((node, i) => (
          <g key={node.label} className="film-node" style={{ animationDelay: `${i * 180}ms` }}>
            <circle cx={node.x} cy={node.y} r="18" fill="#f7f7f5" stroke="rgba(0,0,0,0.2)" />
            <circle cx={node.x} cy={node.y} r="5" fill={i === 4 ? "#000" : "#33bf00"} />
            <text
              x={node.x}
              y={node.y + 42}
              textAnchor="middle"
              fontSize="14"
              fill="#000"
              fontFamily="var(--font-inter), system-ui, sans-serif"
              letterSpacing="-0.02em"
            >
              {node.label}
            </text>
          </g>
        ))}
      </svg>
      <Grain className="opacity-10 mix-blend-multiply" />
    </div>
  );
}
