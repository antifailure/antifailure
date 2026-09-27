export type FeatureKind = "twin" | "state" | "firewall" | "workload" | "migration";

/** Concept diagrams, not screenshots or simulated product interfaces. */
export function FeatureDiagram({ kind }: { kind: FeatureKind }) {
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
          <text x="13" y="37" className="fill-gray-new-40 font-mono text-[11px]">production</text>
          {[{ y: 44, label: "app" }, { y: 84, label: "worker" }, { y: 124, label: "Postgres" }].map((node) => (
            <g key={node.label}>
              <rect x="146" y={node.y - 13} width="80" height="26" rx="2" fill="var(--color-sage)" />
              <circle cx="153" cy={node.y} r="2" fill="var(--color-neon)" />
              <text x="163" y={node.y + 4} className="fill-gray-new-20 font-mono text-[11px]">{node.label}</text>
            </g>
          ))}
          <text x="153" y="18" className="fill-gray-new-40 font-mono text-[11px]">your twin</text>
        </>
      )}
      {kind === "state" && (
        <>
          <text x="17" y="27" className="fill-gray-new-40 font-mono text-[11px]">id</text>
          <text x="96" y="27" className="fill-gray-new-40 font-mono text-[11px]">email</text>
          <path d="M12 39H228M12 73H228M12 107H228" stroke="var(--color-stroke)" />
          {[54, 88, 122].map((y, index) => (
            <g key={y}>
              <text x="17" y={y + 7} className="fill-gray-new-20 font-mono text-[12px]">{["u_01", "u_02", "u_03"][index]}</text>
              <rect x="89" y={y - 9} width="130" height="25" rx="2" fill="var(--color-sage)" />
              <text x="98" y={y + 7} className="fill-gray-new-20 font-mono text-[12px]">••••@••••</text>
              <path d={`M73 ${y + 3}h9`} stroke="var(--color-neon)" strokeWidth="2" />
            </g>
          ))}
        </>
      )}
      {kind === "firewall" && (
        <>
          <text x="15" y="25" className="fill-gray-new-40 font-mono text-[11px]">outbound calls</text>
          <rect x="99" y="39" width="9" height="94" fill="var(--color-gray-new-20)" />
          {[{ y: 52, label: "mock" }, { y: 86, label: "capture" }, { y: 120, label: "block" }].map((row, index) => (
            <g key={row.label}>
              <path d={`M16 ${row.y}H97`} stroke="var(--color-gray-new-80)" strokeWidth="2" />
              <circle cx={32 + index * 18} cy={row.y} r="3" fill="var(--color-gray-new-40)" />
              <path d={`M110 ${row.y}H147`} stroke={index === 2 ? "var(--color-danger-ink)" : "var(--color-neon)"} strokeWidth="2" strokeDasharray={index === 2 ? "3 4" : undefined} />
              <text x="159" y={row.y + 4} className="fill-gray-new-20 font-mono text-[12px]">{row.label}</text>
            </g>
          ))}
        </>
      )}
      {kind === "workload" && (
        <>
          <text x="14" y="25" className="fill-gray-new-40 font-mono text-[11px]">production traffic mix</text>
          {[58, 91, 124].map((y) => <path key={y} d={`M12 ${y}H228`} stroke="var(--color-stroke)" strokeDasharray="2 5" />)}
          <path d="M12 124L30 118L44 121L57 93L68 102L81 52L92 66L104 107L117 98L132 104L146 76L157 90L171 43L185 59L201 93L215 81L228 86V138H12Z" fill="var(--color-sage)" />
          <path d="M12 124L30 118L44 121L57 93L68 102L81 52L92 66L104 107L117 98L132 104L146 76L157 90L171 43L185 59L201 93L215 81L228 86" stroke="var(--color-gray-new-20)" strokeWidth="1.5" strokeLinejoin="round" />
          <circle cx="171" cy="43" r="4" fill="var(--color-neon)" />
          <path d="M171 43V137" stroke="var(--color-neon)" strokeDasharray="2 4" />
        </>
      )}
      {kind === "migration" && (
        <>
          <text x="25" y="26" className="fill-gray-new-40 font-mono text-[11px]">integer</text>
          <text x="155" y="26" className="fill-gray-new-20 font-mono text-[11px]">bigint</text>
          {[47, 75, 103].map((y) => (
            <g key={y}>
              <rect x="17" y={y} width="29" height="18" fill="var(--color-gray-new-90)" />
              <rect x="49" y={y} width="39" height="18" fill="var(--color-gray-new-80)" />
              <rect x="151" y={y} width="29" height="18" fill="var(--color-gray-new-90)" />
              <rect x="183" y={y} width="42" height="18" fill="var(--color-sage-2)" />
            </g>
          ))}
          <path d="M103 83H136M130 77L136 83L130 89" stroke="var(--color-gray-new-40)" strokeWidth="1.5" />
          <path d="M183 42H228V126H183" stroke="var(--color-neon)" />
          <text x="69" y="144" className="fill-gray-new-40 font-mono text-[10px]">rehearse the change</text>
        </>
      )}
    </svg>
  );
}
