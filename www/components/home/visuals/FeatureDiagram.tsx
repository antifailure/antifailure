"use client";

import { useEffect } from "react";
import { resolveField, type FieldDefinition } from "@antifailure/website";
import { useCms, useCmsField } from "@/components/cms/CmsProvider";

export type FeatureKind = "twin" | "state" | "firewall" | "workload" | "migration";
const DIAGRAM_NAMES: Record<FeatureKind, string> = { twin: "Isolated Twin", state: "Safe State", firewall: "Side-Effect Firewall", workload: "Load", migration: "Migration Safety" };

function useDiagramLabel(kind: FeatureKind, part: string, defaultValue: string, active: boolean) {
  const cms = useCms();
  const key = `hero-services.items.${kind}.diagram.${part}`;
  useEffect(() => {
    if (!active) return;
    const definition: FieldDefinition = { key, defaultValue, kind: "text", label: `${DIAGRAM_NAMES[kind]} · ${part.replace(/([a-z])(\d)/g, "$1 $2")}`, sectionId: "hero-services" };
    cms.register([definition]);
  }, [active, cms.register, defaultValue, key, kind, part]);
  const value = resolveField(cms.document, key, defaultValue);
  return typeof value === "string" ? value : defaultValue;
}

function DiagramMark({ x, y, shape, cmsKey, size = 2, color = "var(--color-neon)" }: { x: number; y: number; shape: string; cmsKey: string; size?: number; color?: string }) {
  if (shape === "none") return null;
  const mark = shape === "square" ? <rect x={x - size} y={y - size} width={size * 2} height={size * 2} fill={color} /> :
    shape === "diamond" ? <path d={`M${x} ${y - size}l${size} ${size}L${x} ${y + size}l-${size} -${size}Z`} fill={color} /> :
      shape === "line" ? <path d={`M${x - 5} ${y}h10`} stroke={color} strokeWidth="2" /> :
        shape === "arrow" ? <path d={`M${x - 17} ${y}h33m-6 -6 6 6-6 6`} stroke={color} strokeWidth="1.5" /> :
          <circle cx={x} cy={y} r={size} fill={color} />;
  return <g data-cms-key={cmsKey}><circle cx={x} cy={y} r="7" fill="transparent" />{mark}</g>;
}

/** Concept diagrams, not screenshots or simulated product interfaces. */
export function FeatureDiagram({ kind }: { kind: FeatureKind }) {
  const key = `hero-services.items.${kind}.diagram`;
  const production = useDiagramLabel(kind, "production", "production", kind === "twin");
  const app = useDiagramLabel(kind, "app", "app", kind === "twin");
  const worker = useDiagramLabel(kind, "worker", "worker", kind === "twin");
  const postgres = useDiagramLabel(kind, "postgres", "Postgres", kind === "twin");
  const twin = useDiagramLabel(kind, "twin", "your twin", kind === "twin");
  const id = useDiagramLabel(kind, "id", "id", kind === "state");
  const email = useDiagramLabel(kind, "email", "email", kind === "state");
  const users = [useDiagramLabel(kind, "user1", "u_01", kind === "state"), useDiagramLabel(kind, "user2", "u_02", kind === "state"), useDiagramLabel(kind, "user3", "u_03", kind === "state")];
  const masked = useDiagramLabel(kind, "masked", "••••@••••", kind === "state");
  const outbound = useDiagramLabel(kind, "outbound", "outbound calls", kind === "firewall");
  const mock = useDiagramLabel(kind, "mock", "mock", kind === "firewall");
  const capture = useDiagramLabel(kind, "capture", "capture", kind === "firewall");
  const block = useDiagramLabel(kind, "block", "block", kind === "firewall");
  const traffic = useDiagramLabel(kind, "traffic", "production traffic mix", kind === "workload");
  const integer = useDiagramLabel(kind, "integer", "integer", kind === "migration");
  const bigint = useDiagramLabel(kind, "bigint", "bigint", kind === "migration");
  const rehearsal = useDiagramLabel(kind, "rehearsal", "rehearse the change", kind === "migration");
  const defaultMark = kind === "state" ? "line" : kind === "migration" ? "arrow" : "circle";
  const markValue = useCmsField({ key: `${key}.mark`, label: `${DIAGRAM_NAMES[kind]} · marker shape`, sectionId: "hero-services", kind: "select", defaultValue: defaultMark, options: [
    { label: "Circle", value: "circle" }, { label: "Square", value: "square" }, { label: "Diamond", value: "diamond" },
    { label: "Line", value: "line" }, { label: "Arrow", value: "arrow" }, { label: "None", value: "none" },
  ] });
  const mark = typeof markValue === "string" ? markValue : defaultMark;
  return (
    <svg viewBox="0 0 240 164" className="h-full w-full" fill="none" aria-hidden>
      <g stroke="var(--color-stroke)" strokeWidth="1">
        <path d="M12 148H228" />
      </g>
      {kind === "twin" && (
        <>
          <path d="M68 84H109M109 44V124M109 44H146M109 84H146M109 124H146" stroke="var(--color-gray-new-80)" strokeWidth="1.5" />
          <path d="M20 67V101C20 113 68 113 68 101V67" fill="var(--color-sage)" stroke="var(--color-gray-new-40)" />
          <ellipse cx="44" cy="67" rx="24" ry="8" fill="var(--color-sage)" stroke="var(--color-gray-new-40)" />
          <path d="M20 82C20 94 68 94 68 82" stroke="var(--color-gray-new-40)" />
          <text x="13" y="37" data-cms-key={`${key}.production`} className="fill-gray-new-40 font-mono text-[11px]">{production}</text>
          {[{ y: 44, label: app, part: "app" }, { y: 84, label: worker, part: "worker" }, { y: 124, label: postgres, part: "postgres" }].map((node) => (
            <g key={node.part}>
              <rect x="146" y={node.y - 13} width="80" height="26" rx="2" fill="var(--color-sage)" />
              <DiagramMark x={153} y={node.y} shape={mark} cmsKey={`${key}.mark`} />
              <text x="163" y={node.y + 4} data-cms-key={`${key}.${node.part}`} className="fill-gray-new-20 font-mono text-[11px]">{node.label}</text>
            </g>
          ))}
          <text x="153" y="18" data-cms-key={`${key}.twin`} className="fill-gray-new-40 font-mono text-[11px]">{twin}</text>
        </>
      )}
      {kind === "state" && (
        <>
          <text x="17" y="27" data-cms-key={`${key}.id`} className="fill-gray-new-40 font-mono text-[11px]">{id}</text>
          <text x="96" y="27" data-cms-key={`${key}.email`} className="fill-gray-new-40 font-mono text-[11px]">{email}</text>
          <path d="M12 39H228M12 73H228M12 107H228" stroke="var(--color-stroke)" />
          {[54, 88, 122].map((y, index) => (
            <g key={y}>
              <text x="17" y={y + 7} data-cms-key={`${key}.user${index + 1}`} className="fill-gray-new-20 font-mono text-[12px]">{users[index]}</text>
              <rect x="89" y={y - 9} width="130" height="25" rx="2" fill="var(--color-sage)" />
              <text x="98" y={y + 7} data-cms-key={`${key}.masked`} className="fill-gray-new-20 font-mono text-[12px]">{masked}</text>
              <DiagramMark x={78} y={y + 3} shape={mark} cmsKey={`${key}.mark`} />
            </g>
          ))}
        </>
      )}
      {kind === "firewall" && (
        <>
          <text x="15" y="25" data-cms-key={`${key}.outbound`} className="fill-gray-new-40 font-mono text-[11px]">{outbound}</text>
          <rect x="99" y="39" width="9" height="94" fill="var(--color-gray-new-20)" />
          {[{ y: 52, label: mock, part: "mock" }, { y: 86, label: capture, part: "capture" }, { y: 120, label: block, part: "block" }].map((row, index) => (
            <g key={row.part}>
              <path d={`M16 ${row.y}H97`} stroke="var(--color-gray-new-80)" strokeWidth="2" />
              <DiagramMark x={32 + index * 18} y={row.y} shape={mark} size={3} color="var(--color-gray-new-40)" cmsKey={`${key}.mark`} />
              <path d={`M110 ${row.y}H147`} stroke={index === 2 ? "var(--color-danger-ink)" : "var(--color-neon)"} strokeWidth="2" strokeDasharray={index === 2 ? "3 4" : undefined} />
              <text x="159" y={row.y + 4} data-cms-key={`${key}.${row.part}`} className="fill-gray-new-20 font-mono text-[12px]">{row.label}</text>
            </g>
          ))}
        </>
      )}
      {kind === "workload" && (
        <>
          <text x="14" y="25" data-cms-key={`${key}.traffic`} className="fill-gray-new-40 font-mono text-[11px]">{traffic}</text>
          {[58, 91, 124].map((y) => <path key={y} d={`M12 ${y}H228`} stroke="var(--color-stroke)" strokeDasharray="2 5" />)}
          <path d="M12 124L30 118L44 121L57 93L68 102L81 52L92 66L104 107L117 98L132 104L146 76L157 90L171 43L185 59L201 93L215 81L228 86V138H12Z" fill="var(--color-sage)" />
          <path d="M12 124L30 118L44 121L57 93L68 102L81 52L92 66L104 107L117 98L132 104L146 76L157 90L171 43L185 59L201 93L215 81L228 86" stroke="var(--color-gray-new-20)" strokeWidth="1.5" strokeLinejoin="round" />
          <DiagramMark x={171} y={43} shape={mark} size={4} cmsKey={`${key}.mark`} />
          <path d="M171 43V137" stroke="var(--color-neon)" strokeDasharray="2 4" />
        </>
      )}
      {kind === "migration" && (
        <>
          <text x="25" y="26" data-cms-key={`${key}.integer`} className="fill-gray-new-40 font-mono text-[11px]">{integer}</text>
          <text x="155" y="26" data-cms-key={`${key}.bigint`} className="fill-gray-new-20 font-mono text-[11px]">{bigint}</text>
          {[47, 75, 103].map((y) => (
            <g key={y}>
              <rect x="17" y={y} width="29" height="18" fill="var(--color-gray-new-90)" />
              <rect x="49" y={y} width="39" height="18" fill="var(--color-gray-new-80)" />
              <rect x="151" y={y} width="29" height="18" fill="var(--color-gray-new-90)" />
              <rect x="183" y={y} width="42" height="18" fill="var(--color-sage-2)" />
            </g>
          ))}
          <DiagramMark x={120} y={83} shape={mark} size={5} color="var(--color-gray-new-40)" cmsKey={`${key}.mark`} />
          <path d="M183 42H228V126H183" stroke="var(--color-neon)" />
          <text x="69" y="144" data-cms-key={`${key}.rehearsal`} className="fill-gray-new-40 font-mono text-[10px]">{rehearsal}</text>
        </>
      )}
    </svg>
  );
}
