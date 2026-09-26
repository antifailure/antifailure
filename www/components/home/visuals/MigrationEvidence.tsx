// Recorded orders-app rehearsal, September 2026. The MCP migration tool and
// af insights both call Orchestrator.RunInsights. Sampled times are lower bounds.
const LOCKS = [
  { relation: "orders", seconds: 10.5, primary: true },
  { relation: "orders_pkey", seconds: 5.0, primary: false },
  { relation: "orders_customer_id_idx", seconds: 2.2, primary: false },
];

export function MigrationEvidence() {
  return (
    <figure className="border-y border-stroke">
      <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 border-b border-stroke py-5">
        <span className="font-mono text-[13px] text-gray-new-10 max-sm:text-xs">rehearse_migration_safety</span>
        <span className="text-sm text-gray-new-40">Available through MCP</span>
      </div>

      <div className="grid grid-cols-[minmax(0,0.85fr)_minmax(0,1.15fr)] gap-12 py-10 max-lg:grid-cols-1 max-lg:gap-10 max-md:py-7">
        <div className="min-w-0">
          <p className="font-mono text-xs text-gray-new-40">01 / The change</p>
          <pre className="mt-7 whitespace-pre-wrap break-words font-mono text-[19px] leading-8 tracking-extra-tight text-gray-new-10 max-md:text-base">
            <span className="text-gray-new-40">ALTER TABLE</span>{" orders\n"}
            <span className="text-gray-new-40">ALTER COLUMN</span>{"\n"}
            {"total_cents "}
            <span className="text-gray-new-40">TYPE</span>{" bigint;"}
          </pre>
          <p className="mt-7 max-w-[32ch] text-base leading-7 text-gray-new-40">A one-line type change. Tested on a disposable database branch.</p>
        </div>

        <div className="min-w-0">
          <p className="font-mono text-xs text-gray-new-40">02 / The rehearsal</p>
          <div className="mt-7 flex items-baseline justify-between gap-4">
            <h3 className="text-lg tracking-extra-tight text-gray-new-10">Exclusive locks</h3>
            <span className="text-xs text-gray-new-40">Time held, at least</span>
          </div>
          <ul className="mt-7 space-y-7">
            {LOCKS.map((lock) => (
              <li key={lock.relation}>
                <div className="mb-3 flex items-baseline justify-between gap-4 font-mono text-xs text-gray-new-40">
                  <span className="min-w-0 break-all">{lock.relation}</span>
                  <span className={lock.primary ? "shrink-0 text-danger-ink" : "shrink-0 text-gray-new-20"}>{lock.seconds.toFixed(1)}s</span>
                </div>
                <div className="h-3 border-l border-stroke bg-black/[0.035]" aria-hidden>
                  <div className={lock.primary ? "h-full bg-danger-ink" : "h-full bg-gray-new-80"} style={{ width: `${lock.seconds / 12 * 100}%` }} />
                </div>
              </li>
            ))}
          </ul>
          <div className="mt-4 flex justify-between font-mono text-[11px] text-gray-new-40" aria-hidden><span>0s</span><span>6s</span><span>12s</span></div>
        </div>
      </div>

      <div className="grid grid-cols-[minmax(0,0.85fr)_minmax(0,1.15fr)] gap-x-12 gap-y-5 border-t border-stroke py-8 max-lg:grid-cols-1">
        <div>
          <p className="font-mono text-xs text-gray-new-40">03 / Back to your agent</p>
          <h3 className="mt-4 text-[27px] leading-tight tracking-tighter text-gray-new-10">This change rewrites the table.</h3>
        </div>
        <div>
          <p className="text-base leading-7 text-gray-new-40">The result names the affected table, the locks, and the migration findings. Your agent has evidence to work from before you merge.</p>
          <a href="/docs/reference/mcp" className="mt-4 inline-flex min-h-11 items-center text-base font-medium text-gray-new-10 underline decoration-black/25 underline-offset-4 hover:decoration-black focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-black">Connect Antifailure to your agent →</a>
        </div>
      </div>
      <figcaption className="border-t border-stroke py-4 text-xs leading-6 text-gray-new-40">Recorded orders-app rehearsal. Lock times are sampled lower bounds.</figcaption>
    </figure>
  );
}
