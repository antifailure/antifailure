const DECISIONS = [
  { call: "Payment request", route: "Mocked", outcome: "The app gets a test response. No real charge.", count: "01" },
  { call: "Email request", route: "Captured", outcome: "The message goes to the test inbox, not a user.", count: "01" },
  { call: "Unknown host", route: "Refused", outcome: "The outbound request stops at the firewall.", count: "01" },
] as const;

export function FailClosedScene() {
  return (
    <figure aria-label="Example firewall result: one payment mocked, one email captured, and one unknown destination refused, with no real charges or user emails in this scenario.">
      <div className="overflow-hidden border border-forest-line/25 bg-paper">
        <div className="flex flex-wrap items-center justify-between gap-x-8 gap-y-1 border-b border-forest-line/20 px-6 py-4 text-sm text-gray-new-40 md:px-8">
          <span className="font-medium text-forest">Side-effect firewall / example policy</span>
          <span>Three attempted calls inside a production twin</span>
        </div>

        <div className="grid lg:grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)]">
          <div className="flex flex-col justify-between px-6 py-8 md:px-8 md:py-10 lg:min-h-[385px]">
            <div>
              <p className="text-sm text-gray-new-40">What stayed outside production</p>
              <div className="mt-5 flex items-end gap-5">
                <span className="text-[clamp(6rem,12vw,10rem)] leading-[0.82] tracking-[-0.09em] tabular-nums text-forest">0</span>
                <span className="max-w-[14ch] pb-2 text-[clamp(1.3rem,2vw,1.8rem)] leading-tight tracking-[-0.03em] text-gray-new-10">real charges or user emails</span>
              </div>
            </div>
            <p className="mt-10 max-w-[37ch] border-t border-forest-line/20 pt-5 text-base leading-6 text-gray-new-40">Your app exercises its payment and email paths. The configured policy contains their effects.</p>
          </div>

          <div className="bg-forest px-6 py-8 text-sage md:px-8 md:py-10">
            <div className="flex flex-col items-start gap-1 border-b border-sage/25 pb-4 sm:flex-row sm:items-baseline sm:justify-between sm:gap-4">
              <p className="text-lg font-medium text-white">Call decisions</p>
              <p className="text-sm text-sage-2">3 attempted / 3 handled</p>
            </div>
            <ol>
              {DECISIONS.map(({ call, route, outcome, count }) => (
                <li key={call} className="grid grid-cols-[2rem_minmax(0,1fr)] gap-3 border-b border-sage/20 py-5 last:border-b-0">
                  <span className="pt-1 text-sm tabular-nums text-green-52" aria-hidden>{count}</span>
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-baseline justify-between gap-x-5 gap-y-1">
                      <span className="text-lg font-medium text-white">{call}</span>
                      <span className="text-sm font-medium text-green-52">{route}</span>
                    </div>
                    <p className="mt-1 text-base leading-6 text-sage-2">{outcome}</p>
                  </div>
                </li>
              ))}
            </ol>
          </div>
        </div>

        <div className="grid gap-2 border-t border-forest-line/25 bg-sage px-6 py-5 text-base leading-6 md:grid-cols-[170px_1fr] md:gap-6 md:px-8">
          <p className="font-medium text-forest">Where it goes</p>
          <p className="text-gray-new-20">Your agent calls <code className="font-mono text-[0.9em]">inspect_egress_firewall</code> and gets the containment verdict, counts by decision, and any findings. The full decision log stays with the environment.</p>
        </div>
      </div>
      <figcaption className="mt-3 text-sm leading-6 text-gray-new-40">Illustrative three-call scenario, not live customer telemetry. If the decision log is unavailable, Antifailure reports an inconclusive result instead of a zero.</figcaption>
    </figure>
  );
}
