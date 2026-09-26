import { LogoMark } from "@/components/icons";

// Excerpt from the recorded orders-app demo, af insights -q, September 2026.
// The sampled lock time is a lower bound for this demo branch, not a forecast.
const OUTPUT = `Migrations rehearsed: 1 pending, 10.7s in total.
     10.6s  ALTER TABLE orders ALTER COLUMN total_cents TYPE bigint
            rewrote orders, which copies every row under a lock nothing can read through

Locks held while the migrations ran:
  orders                       AccessExclusiveLock for at least 10.5s
  orders_pkey                  AccessExclusiveLock for at least 5.0s
  orders_customer_id_idx       AccessExclusiveLock for at least 2.2s`;

export function MigrationEvidence() {
  return (
    <figure className="overflow-hidden border border-stroke bg-white">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-stroke px-6 py-4 max-md:px-5">
        <div className="flex items-center gap-3">
          <LogoMark className="size-5" />
          <span className="text-sm font-medium text-gray-new-10">Antifailure CLI</span>
        </div>
        <span className="font-mono text-xs text-gray-new-40">Recorded demo · orders-app</span>
      </div>
      <div className="grid grid-cols-[minmax(0,1fr)_280px] max-lg:grid-cols-1">
        <div className="min-w-0 bg-gray-new-10 p-7 text-gray-new-90 max-md:p-5">
          <p className="mb-6 font-mono text-sm text-white"><span className="text-green-52">$</span> af insights -q</p>
          <pre className="whitespace-pre-wrap break-words font-mono text-[13px] leading-7 max-md:text-xs">{OUTPUT}</pre>
        </div>
        <div className="flex flex-col justify-between gap-8 p-7 max-md:p-5">
          <div>
            <p className="text-sm text-gray-new-40">What the rehearsal found</p>
            <p className="mt-3 text-[30px] leading-tight tracking-tighter text-gray-new-10">One type change.<br />A full table rewrite.</p>
            <p className="mt-5 text-base leading-7 text-gray-new-40">The migration held an exclusive lock for at least 10.5 seconds on the demo database.</p>
          </div>
          <a href="/docs/concepts/insights" className="inline-flex min-h-11 items-center text-sm font-medium text-gray-new-10 underline decoration-black/25 underline-offset-4 hover:decoration-black focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-black">Read the migration guide →</a>
        </div>
      </div>
      <figcaption className="border-t border-stroke px-6 py-4 text-sm leading-6 text-gray-new-40 max-md:px-5">Actual CLI output from a demo run. Lock durations are sampled lower bounds; timing depends on the database and environment.</figcaption>
    </figure>
  );
}
