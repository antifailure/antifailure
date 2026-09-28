const LOCKS = [
  { relation: "orders", time: "10.5s", width: "100%" },
  { relation: "orders_pkey", time: "5.0s", width: "48%" },
  { relation: "orders_customer_id_idx", time: "2.2s", width: "21%" },
] as const;

export function MigrationEvidence() {
  return (
    <figure aria-label="Example migration result: an orders table rewrite and a sampled lock lasting at least 10.5 seconds, with the measured evidence returned to the coding agent.">
      <div className="overflow-hidden border border-forest-line/25 bg-paper">
        <div className="flex flex-wrap items-center justify-between gap-x-8 gap-y-1 border-b border-forest-line/20 px-6 py-4 text-sm text-gray-new-40 md:px-8">
          <span className="font-medium text-forest">Migration rehearsal / orders</span>
          <span>Example result from the demo database</span>
        </div>

        <div className="grid lg:grid-cols-[minmax(0,0.88fr)_minmax(0,1.12fr)]">
          <div className="flex flex-col justify-between bg-forest px-6 py-8 text-sage md:px-8 md:py-10 lg:min-h-[385px]">
            <div>
              <p className="text-sm text-sage-2">What Antifailure found</p>
              <h3 className="mt-5 max-w-[14ch] text-[clamp(2rem,3.8vw,3.6rem)] leading-[1.05] tracking-[-0.045em] text-white">
                One column change rewrites the orders table.
              </h3>
            </div>
            <div className="mt-10 border-t border-sage/25 pt-5">
              <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1">
                <span className="text-[clamp(3.5rem,7vw,6.5rem)] leading-none tracking-[-0.075em] tabular-nums text-alert-on-dark">≥10.5s</span>
                <span className="max-w-[17ch] text-base leading-6 text-sage-2">AccessExclusiveLock on orders</span>
              </div>
              <p className="mt-4 max-w-[43ch] text-base leading-6 text-sage-2">Reads and writes can wait while the lock is held.</p>
            </div>
          </div>

          <div className="flex flex-col justify-between px-6 py-8 md:px-8 md:py-10">
            <div>
              <p className="text-sm text-gray-new-40">The change</p>
              <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-forest-line/20 pb-6 text-lg text-gray-new-10">
                <span className="font-medium">orders.total_cents</span>
                <span className="text-gray-new-40">integer</span>
                <span aria-hidden className="text-positive-ink">→</span>
                <span>bigint</span>
              </div>
              <div className="mt-7 flex items-baseline justify-between gap-4">
                <p className="text-lg font-medium text-gray-new-10">Lock evidence</p>
                <p className="text-sm text-gray-new-40">Sampled lower bounds</p>
              </div>
              <div className="mt-3 space-y-4">
                {LOCKS.map(({ relation, time, width }) => (
                  <div key={relation}>
                    <div className="mb-1.5 flex items-baseline justify-between gap-3 text-sm text-gray-new-20">
                      <span className="min-w-0 break-all">{relation}</span>
                      <span className="shrink-0 font-medium tabular-nums">≥{time}</span>
                    </div>
                    <div className="h-1.5 w-full bg-forest-line/15" aria-hidden>
                      <div className="h-full bg-ochre-ink" style={{ width }} />
                    </div>
                  </div>
                ))}
              </div>
            </div>
            <p className="mt-9 border-t border-forest-line/20 pt-5 text-base leading-6 text-gray-new-40">
              The result names the rewritten table and the lock timings, so the agent can change the migration before it ships.
            </p>
          </div>
        </div>

        <div className="grid gap-2 border-t border-forest-line/25 bg-sage px-6 py-5 text-base leading-6 md:grid-cols-[170px_1fr] md:gap-6 md:px-8">
          <p className="font-medium text-forest">Where it goes</p>
          <p className="text-gray-new-20">Your coding agent gets a verdict, ranked findings, measured metrics, and evidence through MCP. It starts the rehearsal, then reads the finished result with <code className="font-mono text-[0.9em]">get_rehearsal_run</code>.</p>
        </div>
      </div>
      <figcaption className="mt-3 text-sm leading-6 text-gray-new-40">Recorded demo rehearsal. Lock times are sampled lower bounds, not a prediction for every database.</figcaption>
    </figure>
  );
}
