import { FigLabel, FigureFrame } from "./frame";
import { CycleSchematic, GatewaySchematic, IsoWorkers, LockPlot } from "./draw";

export function SSAAS01() {
  const rows = [
    ["acme-prod", "12.4k seats", true],
    ["northwind", "3.1k seats", true],
    ["helix", "890 seats · children follow", false],
  ] as const;
  return (
    <FigureFrame id="S-SAAS-01">
      <FigLabel>Tenant subset</FigLabel>
      <ul className="mt-6">
        {rows.map(([name, seats, keep]) => (
          <li
            key={name}
            className="flex items-center justify-between gap-4 border-b border-black/10 py-3"
          >
            <span className="font-mono text-[13px] tracking-extra-tight">{name}</span>
            <span className="font-mono text-[11px] text-black/45">{seats}</span>
            <span className={keep ? "text-[#285D49]" : "text-black/35"}>
              {keep ? "KEEP" : "DROP"}
            </span>
          </li>
        ))}
      </ul>
    </FigureFrame>
  );
}

export function SSAAS02() {
  return (
    <FigureFrame id="S-SAAS-02">
      <CycleSchematic nodes={["Restore", "Mask", "Exercise", "Decide"]} />
    </FigureFrame>
  );
}

export function SFIN01() {
  return (
    <FigureFrame id="S-FIN-01">
      <GatewaySchematic />
    </FigureFrame>
  );
}

export function SFIN02({ rows }: { rows: [string, string][] }) {
  return (
    <FigureFrame id="S-FIN-02">
      <FigLabel>containment spec</FigLabel>
      <ul className="mt-4">
        {rows.map(([k, v]) => (
          <li key={k} className="border-b border-black/10 py-2">
            <div className="font-mono text-[12px] tracking-extra-tight text-black">{k}</div>
            <div className="mt-0.5 text-[12px] leading-5 tracking-extra-tight text-black/45">{v}</div>
          </li>
        ))}
      </ul>
    </FigureFrame>
  );
}

export function SMKT01() {
  return (
    <FigureFrame id="S-MKT-01">
      <IsoWorkers />
    </FigureFrame>
  );
}

export function SDEV01() {
  return (
    <FigureFrame id="S-DEV-01">
      <LockPlot peak={4.2} peakLabel="Seq Scan 410ms · lock 4.2s" tone="fail" />
      <p className="mt-auto pt-2 font-mono text-[11px] text-black/45">
        events · Index Scan 12ms → Seq Scan 410ms
      </p>
    </FigureFrame>
  );
}

export function SDEV02({ source }: { source: string }) {
  return (
    <FigureFrame id="S-DEV-02" dark>
      <span className="font-mono text-[11px] uppercase tracking-[0.14em] text-white/40">
        af insights · events
      </span>
      <pre className="mt-3 flex-1 overflow-x-auto font-mono text-[12px] leading-5 text-white/70">{source}</pre>
    </FigureFrame>
  );
}
