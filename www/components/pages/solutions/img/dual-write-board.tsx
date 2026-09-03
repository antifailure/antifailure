import { cn } from "@/lib/cn";
import { SageWell, FloatWindow } from "../well";

const BASE_WRITES = [
  { ev: "listings.created", out: "queued" },
  { ev: "orders.paid", out: "match.ok" },
  { ev: "notify.worker", out: "sent" },
] as const;

const CAND_WRITES = [
  { ev: "listings.created", out: "queued" },
  { ev: "orders.paid", out: "match.miss" },
  { ev: "notify.worker", out: "skipped" },
] as const;

const PAIRS = BASE_WRITES.map((row, i) => {
  const cand = CAND_WRITES[i] ?? row;
  return {
    ev: row.ev,
    base: row.out,
    cand: cand.out,
    same: row.out === cand.out,
  };
});

export function DualWriteBoard() {
  return (
    <SageWell className="max-md:!min-h-0">
      <div className="flex flex-col gap-3 md:grid md:grid-cols-[200px_minmax(0,1fr)] md:items-start md:gap-3">
        <FloatWindow className="order-1 min-w-0 overflow-hidden md:order-2">
          <div className="flex flex-wrap items-end justify-between gap-x-2 gap-y-1 border-b border-black/[0.06] bg-[#f7f7f5] px-3 pt-2.5">
            <div className="flex min-w-0 items-center gap-1.5 rounded-t-[8px] bg-[#CAE6D9] px-2.5 py-1.5 text-[12px] font-medium text-[#285D49]">
              <span className="truncate">Oracle · dual-write</span>
              <span className="text-[#285D49]/40" aria-hidden>
                ×
              </span>
            </div>
            <div className="mb-1.5 font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-gray-new-40">
              old worker / new worker
            </div>
          </div>

          <div className="bg-[#f7f7f5] px-2.5 pt-2.5 pb-2 md:px-3 md:pt-3">
            <div className="grid grid-cols-[minmax(0,1fr)_28px_minmax(0,1fr)] md:grid-cols-[minmax(0,1fr)_36px_minmax(0,1fr)]">
              <section className="min-w-0 overflow-hidden rounded-t-[10px] bg-[#E4F1EB]">
                <div className="relative flex h-8 items-center bg-[#CAE6D9] px-2.5">
                  <span className="absolute inset-y-0 left-0 w-1 bg-[#285D49]" aria-hidden />
                  <h3 className="pl-1 font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-[#285D49]">
                    Baseline
                  </h3>
                </div>
              </section>

              <div className="flex flex-col items-center justify-center" aria-hidden>
                <span className="size-1 rounded-full bg-[#285D49]" />
                <span className="mt-1 h-2 w-px bg-[#285D49]/30" />
              </div>

              <section className="min-w-0 overflow-hidden rounded-t-[10px] bg-white">
                <div className="relative flex h-8 items-center px-2.5">
                  <span className="absolute inset-y-0 left-0 w-1 bg-[#f4edd6]" aria-hidden />
                  <h3 className="pl-1 font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-[#8A6A12]">
                    Candidate
                  </h3>
                </div>
              </section>

              {PAIRS.map((row, i) => (
                <PairRow key={row.ev} row={row} last={i === PAIRS.length - 1} />
              ))}
            </div>

            <ForkJoin />

            <div className="overflow-hidden rounded-[10px] bg-white">
              <div className="flex items-center justify-between gap-2 bg-[#E4F1EB] px-3 py-2">
                <div className="min-w-0">
                  <div className="text-[13px] font-medium tracking-tight text-black">
                    Branched database
                  </div>
                  <div className="mt-0.5 font-mono text-[10px] tracking-extra-tight text-gray-new-40">
                    one row the oracle returns
                  </div>
                </div>
                <span className="shrink-0 rounded-full border border-[#8A6A12]/35 bg-white px-2 py-0.5 font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-[#8A6A12]">
                  miss
                </span>
              </div>
              <div className="grid grid-cols-[minmax(0,1fr)_auto_auto] gap-x-3 px-3 py-1.5 font-mono text-[9px] font-medium uppercase tracking-[0.12em] text-gray-new-40">
                <span>row</span>
                <span>base</span>
                <span>cand</span>
              </div>
              <div className="relative grid grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-x-3 border-t border-black/[0.06] bg-[#f7f7f5] px-3 py-2">
                <span className="absolute inset-y-0 left-0 w-1 bg-[#f4edd6]" aria-hidden />
                <span className="truncate pl-1 font-mono text-[12px] tracking-extra-tight text-black">
                  order_992
                </span>
                <span className="font-mono text-[12px] tracking-extra-tight text-[#285D49]">ok</span>
                <span className="font-mono text-[12px] tracking-extra-tight text-[#8A6A12]">miss</span>
              </div>
            </div>
          </div>
        </FloatWindow>

        <aside className="order-2 h-fit w-full rounded-[12px] border-l-2 border-[#8A6A12] bg-white p-3.5 shadow-[0_16px_48px_rgba(0,0,0,0.14)] md:order-1">
          <div className="text-[13px] font-semibold tracking-tight text-black">
            Visible when workers run
          </div>
          <p className="mt-2 text-[12px] leading-5 text-gray-new-40">
            Duplicate events and missed matches do not appear if staging skips the queue.
          </p>
        </aside>
      </div>
    </SageWell>
  );
}

function PairRow({
  row,
  last,
}: {
  row: (typeof PAIRS)[number];
  last: boolean;
}) {
  return (
    <>
      <WriteCell ev={row.ev} out={row.base} tone={row.same ? "pass" : "base"} last={last} />
      <Hinge same={row.same} />
      <WriteCell ev={row.ev} out={row.cand} tone={row.same ? "pass" : "miss"} last={last} />
    </>
  );
}

function WriteCell({
  ev,
  out,
  tone,
  last,
}: {
  ev: string;
  out: string;
  tone: "pass" | "base" | "miss";
  last: boolean;
}) {
  return (
    <div
      className={cn(
        "relative flex min-h-[52px] items-center justify-between gap-2 border-t border-black/[0.06] px-2.5",
        tone === "pass" && "bg-[#dceee6]",
        tone === "base" && "bg-[#E4F1EB]",
        tone === "miss" && "bg-white",
        last && "rounded-b-[10px]",
      )}
    >
      <span
        className={cn(
          "absolute inset-y-0 left-0 w-1",
          tone === "miss" ? "bg-[#f4edd6]" : "bg-[#CAE6D9]",
        )}
        aria-hidden
      />
      <div className="min-w-0 pl-1">
        <div
          className={cn(
            "font-mono text-[12px] tracking-extra-tight",
            tone === "miss" ? "text-[#8A6A12]" : "text-black",
          )}
        >
          {out}
        </div>
        <div className="mt-0.5 truncate font-mono text-[10px] tracking-extra-tight text-gray-new-40">
          {ev}
        </div>
      </div>
      {tone === "pass" ? (
        <span className="size-1.5 shrink-0 rounded-full bg-[#33bf00]" aria-hidden />
      ) : tone === "miss" ? (
        <span className="size-1.5 shrink-0 rounded-[2px] bg-[#8A6A12]" aria-hidden />
      ) : (
        <span className="size-1.5 shrink-0 rounded-full bg-[#285D49]" aria-hidden />
      )}
    </div>
  );
}

function Hinge({ same }: { same: boolean }) {
  return (
    <div className="flex flex-col items-center justify-center border-t border-black/[0.04] bg-[#f7f7f5]" aria-hidden>
      <span className="mb-1 h-1.5 w-px bg-[#285D49]/25" />
      <span
        className={cn(
          "font-mono text-[11px] leading-none",
          same ? "text-[#285D49]" : "text-[#8A6A12]",
        )}
      >
        {same ? "=" : "≠"}
      </span>
      <span className="mt-1 h-1.5 w-px bg-[#285D49]/25" />
    </div>
  );
}

function ForkJoin() {
  return (
    <svg viewBox="0 0 320 30" className="block h-7 w-full" aria-hidden>
      <path
        d="M72 0 V7 C72 16 160 12 160 22"
        fill="none"
        stroke="#285D49"
        strokeWidth="1.25"
      />
      <path
        d="M248 0 V7 C248 16 160 12 160 22"
        fill="none"
        stroke="#8A6A12"
        strokeWidth="1.25"
      />
      <circle cx="160" cy="24" r="3.5" fill="#E4F1EB" stroke="#285D49" strokeWidth="1.25" />
    </svg>
  );
}
