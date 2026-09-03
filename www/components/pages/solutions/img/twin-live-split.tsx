import { SageWell, FloatWindow } from "../well";

const LEDGER = [
  ["chg_184", "POST /v1/charges", "$49.00"],
  ["chg_185", "retry checkout", "$49.00"],
  ["inv_044", "refund path", "$0.00"],
] as const;

const PROCESSORS = ["api.stripe.com", "api.sendgrid.com", "hooks.slack.com", "api.prod.internal"] as const;

const SPINE = "BOUNDARY".split("");

export function TwinLiveSplit() {
  return (
    <SageWell className="!min-h-0 max-md:!min-h-0">
      <div className="flex flex-col gap-3 md:grid md:grid-cols-[minmax(0,1fr)_188px] md:items-start md:gap-3">
        <FloatWindow className="min-w-0 overflow-hidden">
          <div className="flex items-end gap-1 border-b border-black/[0.06] bg-[#f7f7f5] px-3 pt-2.5">
            <div className="flex items-center gap-1.5 rounded-t-[8px] bg-[#CAE6D9] px-2.5 py-1.5 text-[11px] font-medium text-[#285D49] sm:px-3 sm:py-2 sm:text-[12px]">
              <span className="truncate">twin ledger</span>
              <span className="text-[#285D49]/40" aria-hidden>
                ×
              </span>
            </div>
            <div className="mb-1.5 ml-auto border-b-2 border-[#33bf00] pb-0.5 font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-black">
              0 packets
            </div>
          </div>

          <div className="grid md:grid-cols-[minmax(0,1.2fr)_32px_minmax(0,0.92fr)]">
            <section className="relative flex min-w-0 flex-col bg-[#E4F1EB]">
              <div className="absolute inset-y-0 left-0 w-1 bg-[#CAE6D9]" aria-hidden />
              <header className="flex h-10 items-center justify-between gap-2 py-0 pr-3 pl-4">
                <h3 className="font-mono text-[10px] font-medium uppercase tracking-[0.14em] text-[#285D49]">
                  Twin ledger
                </h3>
                <span className="shrink-0 rounded-full bg-white px-2 py-0.5 font-mono text-[9px] uppercase tracking-[0.12em] text-[#285D49]">
                  in-boundary
                </span>
              </header>
              <div className="flex items-baseline justify-between gap-2 border-y border-[#285D49]/15 py-1 pr-3 pl-4">
                <span className="font-mono text-[9px] uppercase tracking-[0.12em] text-gray-new-40">id · effect</span>
                <span className="font-mono text-[9px] uppercase tracking-[0.12em] text-gray-new-40">amt</span>
              </div>
              <ul>
                {LEDGER.map(([id, verb, amt]) => (
                  <li
                    key={id}
                    className="grid grid-cols-[minmax(0,1fr)_auto] items-baseline gap-x-3 gap-y-0.5 border-b border-[#285D49]/12 py-2 pr-3 pl-4"
                  >
                    <span className="min-w-0 truncate font-mono text-[11px] leading-4 tracking-extra-tight text-gray-new-40">
                      {id}
                    </span>
                    <span className="flex items-center justify-end gap-1.5">
                      <span className="font-mono text-[12px] leading-4 tabular-nums tracking-extra-tight text-black">
                        {amt}
                      </span>
                      <span className="size-1.5 shrink-0 rounded-full bg-[#33bf00]" aria-hidden />
                    </span>
                    <span className="col-span-2 min-w-0 truncate text-[13px] leading-4 text-black">{verb}</span>
                  </li>
                ))}
              </ul>
              <div
                className="min-h-0 flex-1 bg-[repeating-linear-gradient(to_bottom,transparent_0,transparent_41px,rgba(40,93,73,0.12)_41px,rgba(40,93,73,0.12)_42px)]"
                aria-hidden
              />
            </section>

            <div
              className="flex items-center justify-between bg-[#f7f7f5] px-3 py-2 md:hidden"
              aria-hidden
            >
              <span className="size-1.5 rounded-full bg-[#33bf00]" />
              <span className="font-mono text-[9px] uppercase tracking-[0.16em] text-gray-new-40">Boundary</span>
              <span className="size-1.5 rounded-full bg-[#C43D3D]" />
            </div>

            <div
              className="hidden flex-col items-center justify-between border-x-2 border-l-[#285D49] border-r-[#C43D3D] bg-[#f7f7f5] py-3 md:flex"
              aria-hidden
            >
              <span className="size-1.5 rounded-full bg-[#33bf00]" />
              <div className="flex flex-col items-center gap-[3px]">
                {SPINE.map((ch, i) => (
                  <span key={`${ch}${i}`} className="font-mono text-[8px] leading-none text-gray-new-40">
                    {ch}
                  </span>
                ))}
              </div>
              <span className="size-1.5 rounded-full bg-[#C43D3D]" />
            </div>

            <section className="flex min-w-0 flex-col bg-[#f7f7f5]">
              <header className="flex h-10 items-center py-0 pr-3 pl-3.5">
                <h3 className="font-mono text-[10px] font-medium uppercase tracking-[0.14em] text-[#C43D3D]">
                  Live processors
                </h3>
              </header>
              <div className="border-y border-black/[0.06] py-1 pr-3 pl-3.5">
                <span className="font-mono text-[9px] uppercase tracking-[0.12em] text-gray-new-40">
                  dest · refused
                </span>
              </div>
              <ul className="flex-1">
                {PROCESSORS.map((host) => (
                  <li
                    key={host}
                    className="flex items-center justify-between gap-2 border-b border-black/[0.05] py-2 pr-3 pl-3.5 last:border-b-0"
                  >
                    <span className="min-w-0 truncate font-mono text-[12px] tracking-extra-tight text-gray-new-40 line-through">
                      {host}
                    </span>
                    <svg viewBox="0 0 12 12" className="size-3.5 shrink-0 text-[#C43D3D]" aria-hidden>
                      <path d="M2 2 L10 10 M10 2 L2 10" stroke="currentColor" strokeWidth="1.5" />
                    </svg>
                  </li>
                ))}
              </ul>
              <div className="mt-auto border-t-2 border-[#C43D3D] bg-white px-3.5 py-2.5">
                <div className="font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-[#C43D3D]">
                  0 packets out
                </div>
                <p className="mt-1 text-[12px] leading-4 text-gray-new-40">
                  TTL contained. Processors never resolved.
                </p>
              </div>
            </section>
          </div>
        </FloatWindow>

        <aside className="h-fit w-full rounded-[12px] border-l-2 border-[#C43D3D] bg-white p-3.5 shadow-[0_16px_48px_rgba(0,0,0,0.14)]">
          <div className="text-[13px] font-semibold tracking-tight text-black">Crossing</div>
          <p className="mt-2 text-[12px] leading-5 text-gray-new-40">
            Blocked. Nothing leaves the customer boundary.
          </p>
        </aside>
      </div>
    </SageWell>
  );
}
