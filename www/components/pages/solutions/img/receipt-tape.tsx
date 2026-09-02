import { cn } from "@/lib/cn";
import { SageWell, FloatWindow } from "../well";

const TAPE = [
  { sku: "STRIPE", detail: "POST /v1/charges  $49.00", mode: "MOCK", tone: "pass" as const },
  { sku: "SENDGRID", detail: "render invoice.paid.html", mode: "CAPTURE", tone: "warn" as const },
  { sku: "SLACK", detail: "webhook preview stored", mode: "CAPTURE", tone: "warn" as const },
  { sku: "HOSTNAME", detail: "api.prod.internal", mode: "DENY", tone: "block" as const },
];

const PINHOLES = 13;

function ledgerClip(teeth: number) {
  const top: string[] = [];
  const bottom: string[] = [];
  for (let i = 0; i <= teeth; i++) {
    const x = `${((i / teeth) * 100).toFixed(4)}%`;
    const even = i % 2 === 0;
    top.push(`${x} ${even ? "6px" : "0px"}`);
    bottom.push(`${x} ${even ? "calc(100% - 6px)" : "100%"}`);
  }
  return `polygon(${top.join(",")}, ${bottom.reverse().join(",")})`;
}

const LEDGER_CLIP = ledgerClip(26);

function toneInk(tone: "pass" | "warn" | "block") {
  return tone === "pass" ? "text-[#285D49]" : tone === "warn" ? "text-[#8A6A12]" : "text-[#C43D3D]";
}

function toneRail(tone: "pass" | "warn" | "block") {
  return tone === "pass" ? "bg-[#285D49]" : tone === "warn" ? "bg-[#8A6A12]" : "bg-[#C43D3D]";
}

export function ReceiptTape() {
  return (
    <SageWell className="!min-h-0 !py-6 max-md:!min-h-0 max-md:!py-5 md:!py-7">
      <div className="flex justify-center px-1">
        <div className="w-full max-w-[300px] drop-shadow-[0_10px_22px_rgba(0,0,0,0.12)] sm:max-w-[320px]">
          <div className="bg-[#f7f7f5]" style={{ clipPath: LEDGER_CLIP }}>
            <FloatWindow chrome={false}>
              <div className="flex">
                <div
                  className="flex w-[18px] shrink-0 flex-col items-center justify-between py-[22px]"
                  aria-hidden
                >
                  {Array.from({ length: PINHOLES }, (_, i) => (
                    <span
                      key={i}
                      className="size-[6px] rounded-full bg-[#E4F1EB] shadow-[inset_0_0.5px_0.5px_rgba(0,0,0,0.22)] ring-1 ring-[#CAE6D9]"
                    />
                  ))}
                </div>
                <div className="w-px shrink-0 self-stretch bg-[#CAE6D9]" aria-hidden />
                <div className="min-w-0 flex-1 px-3.5 pt-[18px] pb-[16px] sm:px-4">
                  <header>
                    <div className="flex items-baseline justify-between gap-3">
                      <p className="font-mono text-[10px] font-medium uppercase tracking-[0.16em] text-gray-new-40">
                        Attempted-effect ledger
                      </p>
                      <p className="shrink-0 font-mono text-[10px] tabular-nums tracking-extra-tight text-gray-new-40">
                        LNS 04
                      </p>
                    </div>
                    <h3 className="mt-1.5 text-[16px] font-medium tracking-tight text-black">
                      Twin run 08f2
                    </h3>
                    <p className="mt-0.5 font-mono text-[11px] tracking-extra-tight text-gray-new-40">
                      must never · fail closed
                    </p>
                    <div className="mt-3 h-px bg-black" aria-hidden />
                  </header>

                  <div className="mt-2 grid grid-cols-[22px_minmax(0,1fr)_auto] gap-x-2 px-0.5 font-mono text-[9px] font-medium uppercase tracking-[0.14em] text-gray-new-40">
                    <span>Ln</span>
                    <span>Effect</span>
                    <span>Mode</span>
                  </div>
                  <div className="mt-1.5 h-px bg-[#dceee6]" aria-hidden />

                  <ol>
                    {TAPE.map((row, i) => (
                      <li key={row.sku} className="relative">
                        <span
                          className={cn("absolute top-2 bottom-2 left-0 w-[2px]", toneRail(row.tone))}
                          aria-hidden
                        />
                        <div className="grid grid-cols-[22px_minmax(0,1fr)_auto] items-start gap-x-2 py-2 pl-2">
                          <span className="pt-px font-mono text-[10px] tabular-nums tracking-extra-tight text-gray-new-40">
                            {String(i + 1).padStart(2, "0")}
                          </span>
                          <div className="min-w-0">
                            <div className="truncate font-mono text-[11px] tracking-[0.06em] text-black">
                              {row.sku}
                            </div>
                            <div className="mt-0.5 truncate font-mono text-[11px] tracking-extra-tight text-gray-new-40">
                              {row.detail}
                            </div>
                          </div>
                          <span
                            className={cn(
                              "flex shrink-0 items-center gap-1.5 pt-px font-mono text-[10px] tracking-[0.12em]",
                              toneInk(row.tone),
                            )}
                          >
                            {row.tone === "pass" ? (
                              <span className="size-[5px] bg-[#33bf00]" aria-hidden />
                            ) : null}
                            {row.mode}
                          </span>
                        </div>
                        <div className="h-px bg-[#dceee6]" aria-hidden />
                      </li>
                    ))}
                  </ol>

                  <div className="pt-2.5">
                    <p className="font-mono text-[11px] tracking-extra-tight text-gray-new-40">
                      3 contained · 1 denied · 0 charged
                    </p>
                    <div className="relative mt-2.5 border-t-2 border-black pt-2.5">
                      <div className="absolute top-[3px] right-0 left-0 border-t border-black" aria-hidden />
                      <div className="flex items-end justify-between gap-3">
                        <span className="font-mono text-[11px] font-medium uppercase tracking-[0.12em] text-black">
                          Total live
                        </span>
                        <span className="font-mono text-[22px] leading-none font-medium tracking-tight text-black tabular-nums">
                          $0.00
                        </span>
                      </div>
                    </div>
                  </div>
                </div>
              </div>
            </FloatWindow>
          </div>
        </div>
      </div>
    </SageWell>
  );
}
