import { cn } from "@/lib/cn";
import { FloatWindow, SageWell } from "../well";

const BUYERS = [
  { id: "buyer_18c", state: "in-flight", join: "pass" as const },
  { id: "buyer_44a", state: "active", join: "pass" as const },
  { id: "buyer_09f", state: "long-tail", join: "miss" as const },
] as const;

const SELLERS = [
  { id: "seller_north", state: "listing open", join: "pass" as const },
  { id: "seller_helix", state: "listing open", join: "pass" as const },
  { id: "order_992", state: "join valid", join: "miss" as const },
] as const;

function Port({
  join,
  side,
}: {
  join: "pass" | "miss";
  side: "left" | "right";
}) {
  return (
    <span
      className={cn(
        "absolute top-1/2 z-10 flex size-2.5 -translate-y-1/2 items-center justify-center rounded-full ring-2 ring-white",
        side === "right" ? "right-0 translate-x-1/2" : "left-0 -translate-x-1/2",
        join === "pass" ? "bg-[#285D49]" : "border-[1.5px] border-[#8A6A12] bg-[#f4edd6]",
      )}
      aria-hidden
    >
      {join === "pass" ? <span className="size-1 rounded-full bg-[#33bf00]" /> : null}
    </span>
  );
}

function SideRow({
  id,
  state,
  join,
  index,
  via,
  side,
}: {
  id: string;
  state: string;
  join: "pass" | "miss";
  index: string;
  via: string;
  side: "buyers" | "sellers";
}) {
  const miss = join === "miss";
  const focus = id === "buyer_44a" || id === "seller_helix";
  return (
    <li
      className={cn(
        "relative flex h-full min-h-0 items-center gap-2 px-3 md:gap-2.5 md:px-4",
        miss ? "bg-[#f4edd6]" : focus ? "bg-[#E4F1EB]" : "bg-white",
      )}
    >
      {side === "buyers" ? (
        <span className="w-4 shrink-0 font-mono text-[9px] tabular-nums text-gray-new-40">{index}</span>
      ) : null}
      <div className="min-w-0 flex-1">
        <div className="truncate font-mono text-[12px] tracking-extra-tight text-black">{id}</div>
        <div className="mt-0.5 flex min-w-0 items-center gap-1.5">
          <span className="truncate text-[11px] leading-4 text-gray-new-40">{state}</span>
          <span
            className={cn(
              "shrink-0 font-mono text-[9px] uppercase tracking-[0.1em]",
              miss ? "text-[#8A6A12]" : "text-[#285D49]",
            )}
          >
            {miss ? "miss" : "joined"}
          </span>
        </div>
        <div
          className={cn(
            "mt-0.5 hidden truncate font-mono text-[10px] tracking-extra-tight md:block",
            miss ? "text-[#8A6A12]" : "text-[#285D49]",
          )}
        >
          {miss ? "—" : `↔ ${via}`}
        </div>
      </div>
      {side === "sellers" ? (
        <span className="w-4 shrink-0 text-right font-mono text-[9px] tabular-nums text-gray-new-40">
          {index}
        </span>
      ) : null}
      <Port join={join} side={side === "buyers" ? "right" : "left"} />
    </li>
  );
}

export function TwoSidedMarket() {
  return (
    <SageWell>
      <FloatWindow className="overflow-hidden">
        <div className="flex items-center justify-between gap-3 border-b border-black/[0.06] bg-[#f7f7f5] px-3 py-2.5 md:px-5">
          <div className="min-w-0 text-[13px] font-medium text-black">Referential subset</div>
          <span className="shrink-0 font-mono text-[10px] uppercase tracking-[0.12em] text-[#285D49]">
            2 joined
            <span className="text-[#8A6A12]"> · 1 miss</span>
          </span>
        </div>

        <div className="grid grid-cols-[minmax(0,1fr)_52px_minmax(0,1fr)] md:grid-cols-[minmax(168px,240px)_minmax(88px,1fr)_minmax(168px,240px)]">
          <div className="border-b border-black/[0.06] bg-[#f7f7f5] px-3 py-2 text-[10px] font-medium uppercase tracking-[0.12em] text-gray-new-40 md:px-4">
            Buyers
          </div>
          <div className="border-b border-black/[0.06] bg-[#E4F1EB] text-center font-mono text-[9px] uppercase tracking-[0.12em] text-[#285D49] max-md:leading-8 md:py-2">
            <span className="max-md:sr-only">match</span>
          </div>
          <div className="border-b border-black/[0.06] bg-[#dceee6] px-3 py-2 text-right text-[10px] font-medium uppercase tracking-[0.12em] text-[#285D49] md:px-4">
            Sellers
          </div>

          <ul className="grid h-full min-h-[200px] grid-rows-3 divide-y divide-black/[0.06] bg-[#f7f7f5] md:min-h-[300px]">
            {BUYERS.map((row, i) => (
              <SideRow
                key={row.id}
                id={row.id}
                state={row.state}
                join={row.join}
                index={`0${i + 1}`}
                via={SELLERS[i].id}
                side="buyers"
              />
            ))}
          </ul>

          <div className="relative min-h-[200px] bg-[#E4F1EB] md:min-h-[300px]">
            <svg
              viewBox="0 0 200 300"
              className="absolute inset-0 size-full"
              preserveAspectRatio="none"
              aria-hidden
            >
              <rect x="78" y="10" width="44" height="280" rx="10" fill="#CAE6D9" />
              <line
                x1="100"
                y1="18"
                x2="100"
                y2="282"
                stroke="#285D49"
                strokeOpacity="0.18"
                vectorEffect="non-scaling-stroke"
              />
              {[50, 150, 250].map((y) => (
                <g key={y}>
                  <line
                    x1="0"
                    y1={y}
                    x2="18"
                    y2={y}
                    stroke={y === 250 ? "#8A6A12" : "#285D49"}
                    strokeWidth="1.5"
                    vectorEffect="non-scaling-stroke"
                  />
                  {y === 250 ? null : (
                    <line
                      x1="182"
                      y1={y}
                      x2="200"
                      y2={y}
                      stroke="#285D49"
                      strokeWidth="1.5"
                      vectorEffect="non-scaling-stroke"
                    />
                  )}
                </g>
              ))}
              <path
                d="M18 50 C 48 50, 72 150, 100 150"
                fill="none"
                stroke="#285D49"
                strokeWidth="1.5"
                vectorEffect="non-scaling-stroke"
              />
              <path
                d="M100 150 C 128 150, 152 50, 182 50"
                fill="none"
                stroke="#285D49"
                strokeWidth="1.5"
                vectorEffect="non-scaling-stroke"
              />
              <path
                d="M18 150 H182"
                fill="none"
                stroke="#285D49"
                strokeWidth="1.5"
                vectorEffect="non-scaling-stroke"
              />
              <path
                d="M18 250 C 58 270, 102 270, 128 248"
                fill="none"
                stroke="#8A6A12"
                strokeWidth="1.5"
                vectorEffect="non-scaling-stroke"
              />
            </svg>
            <span
              className="absolute top-1/2 left-1/2 z-10 flex size-6 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-full bg-white ring-1 ring-[#285D49] md:size-7"
              aria-hidden
            >
              <span className="size-1.5 rounded-full bg-[#33bf00] md:size-2" />
            </span>
            <span className="absolute top-[83%] left-[64%] z-10 -translate-x-1/2 -translate-y-1/2 rounded-full bg-[#f4edd6] px-1.5 py-0.5 font-mono text-[8px] uppercase tracking-[0.1em] whitespace-nowrap text-[#8A6A12]">
              miss
            </span>
          </div>

          <ul className="grid h-full min-h-[200px] grid-rows-3 divide-y divide-black/[0.06] bg-[#dceee6] md:min-h-[300px]">
            {SELLERS.map((row, i) => (
              <SideRow
                key={row.id}
                id={row.id}
                state={row.state}
                join={row.join}
                index={`0${i + 1}`}
                via={BUYERS[i].id}
                side="sellers"
              />
            ))}
          </ul>
        </div>

        <div className="flex items-center justify-between gap-3 border-t border-black/[0.06] bg-[#f7f7f5] px-3 py-2.5 md:px-5">
          <span className="min-w-0 truncate font-mono text-[11px] tracking-extra-tight text-[#285D49]">
            buyer_44a ↔ seller_helix
          </span>
          <span className="shrink-0 font-mono text-[11px] tracking-extra-tight text-[#8A6A12]">1 miss</span>
        </div>
      </FloatWindow>
    </SageWell>
  );
}
