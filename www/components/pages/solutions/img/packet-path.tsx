import { cn } from "@/lib/cn";
import { FloatWindow, SageWell } from "../well";

const HOPS: {
  host: string;
  verb: string;
  ms: string;
  mode: string;
  tone: "plain" | "sage" | "block";
  hold?: boolean;
}[] = [
  { host: "twin.app.internal", verb: "POST /checkout", ms: "0.4ms", mode: "origin", tone: "plain" },
  { host: "af-proxy:8443", verb: "egress rule", ms: "1.1ms", mode: "inspect", tone: "sage" },
  { host: "stripe.pack.local", verb: "POST /v1/charges", ms: "ledger", mode: "mock", tone: "sage", hold: true },
  { host: "api.stripe.com", verb: "live processor", ms: "refused", mode: "deny", tone: "block" },
];

function Station({ tone }: { tone: "plain" | "sage" | "block" }) {
  if (tone === "block") {
    return (
      <svg viewBox="0 0 12 12" className="size-3 shrink-0" aria-hidden>
        <rect x="1" y="1" width="10" height="10" rx="2" fill="#C43D3D" />
        <path d="M3.25 6h5.5" stroke="white" strokeWidth="1.5" strokeLinecap="round" />
      </svg>
    );
  }
  if (tone === "sage") {
    return (
      <svg viewBox="0 0 12 12" className="size-3 shrink-0" aria-hidden>
        <rect x="1" y="1" width="10" height="10" rx="2" fill="#285D49" />
      </svg>
    );
  }
  return (
    <svg viewBox="0 0 12 12" className="size-3 shrink-0" aria-hidden>
      <rect x="1.25" y="1.25" width="9.5" height="9.5" rx="2" fill="white" stroke="#285D49" strokeWidth="1.5" />
    </svg>
  );
}

function PacketMark() {
  return (
    <span className="inline-flex shrink-0 items-center gap-1 rounded-[5px] bg-white px-1.5 py-0.5">
      <svg viewBox="0 0 14 10" className="h-2.5 w-3.5" aria-hidden>
        <path d="M1.2 1.4h8.1L12.8 5 9.3 8.6H1.2V1.4z" fill="#E4F1EB" stroke="#285D49" strokeWidth="1.1" />
      </svg>
      <span className="font-mono text-[10px] leading-none text-[#285D49]">412b</span>
      <span className="size-1.5 rounded-full bg-[#33bf00]" aria-hidden />
    </span>
  );
}

export function PacketPath() {
  return (
    <SageWell compact>
      <div className="flex min-h-0 flex-col gap-2 md:flex-row md:items-end md:justify-center md:gap-3">
        <FloatWindow className="min-w-0 overflow-hidden md:w-[min(100%,352px)] md:flex-none">
          <ol>
            {HOPS.map((hop, i) => {
              const last = i === HOPS.length - 1;
              const toDeny = i === HOPS.length - 2;
              return (
                <li
                  key={hop.host}
                  className={cn(
                    "grid grid-cols-[26px_14px_minmax(0,1fr)_auto] gap-x-2 px-2.5 py-1.5 md:px-3 md:py-2",
                    hop.tone === "plain" && "bg-white",
                    hop.tone === "sage" && (hop.hold ? "bg-[#dceee6]" : "bg-[#E4F1EB]"),
                    hop.tone === "block" && "bg-[#f8e4e4]",
                  )}
                >
                  <span
                    className={cn(
                      "self-start pt-0.5 text-right font-mono text-[10px] leading-3 tracking-extra-tight",
                      hop.tone === "block" ? "text-black" : "text-[#285D49]",
                    )}
                  >
                    0{i + 1}
                  </span>
                  <span className="flex flex-col items-center" aria-hidden>
                    <Station tone={hop.tone} />
                    {last ? (
                      <svg viewBox="0 0 14 10" className="mt-1 h-2.5 w-3.5" aria-hidden>
                        <path d="M7 0v4M1 4h12M2.5 4v5M11.5 4v5" stroke="#C43D3D" strokeWidth="1.4" strokeLinecap="round" />
                      </svg>
                    ) : (
                      <span
                        className={cn(
                          "mt-1 w-0 flex-1 min-h-[10px] border-l-2",
                          toDeny ? "border-dashed border-[#C43D3D]" : "border-[#285D49]",
                        )}
                      />
                    )}
                  </span>
                  <div className="min-w-0 self-center">
                    <div className="flex min-w-0 items-center gap-1.5">
                      <span className="min-w-0 truncate font-mono text-[12px] tracking-extra-tight text-black">
                        {hop.host}
                      </span>
                      {hop.hold ? <PacketMark /> : null}
                    </div>
                    <div className="mt-0.5 truncate font-mono text-[11px] tracking-extra-tight text-gray-new-40">
                      {hop.verb}
                    </div>
                  </div>
                  <div className="self-center text-right">
                    {hop.tone === "block" ? (
                      <span className="inline-flex rounded-full bg-white px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-[0.12em] text-[#C43D3D]">
                        {hop.mode}
                      </span>
                    ) : (
                      <div
                        className={cn(
                          "font-mono text-[10px] uppercase tracking-[0.12em]",
                          hop.tone === "sage" ? "text-[#285D49]" : "text-gray-new-40",
                        )}
                      >
                        {hop.mode}
                      </div>
                    )}
                    <div className="mt-0.5 font-mono text-[10px] text-gray-new-40">{hop.ms}</div>
                  </div>
                </li>
              );
            })}
          </ol>
        </FloatWindow>

        <div className="w-full shrink-0 rounded-[12px] bg-white p-3 shadow-[0_16px_48px_rgba(0,0,0,0.14)] md:w-[176px] md:p-4">
          <div className="font-mono text-[10px] uppercase tracking-[0.14em] text-[#C43D3D]">fail closed</div>
          <div className="mt-1.5 text-[13px] font-semibold tracking-tight text-black">Live hop refused</div>
          <p className="mt-1.5 text-[12px] leading-4 text-gray-new-40 max-md:hidden">
            The charge is written to a clone-local ledger. api.stripe.com never resolves.
          </p>
          <div className="mt-2 inline-flex items-center gap-1.5 rounded-full bg-[#f8e4e4] px-2.5 py-0.5 font-mono text-[10px] uppercase tracking-[0.12em] text-black">
            <span className="size-1.5 rounded-full bg-[#C43D3D]" aria-hidden />
            deny · 0 charged
          </div>
        </div>
      </div>
    </SageWell>
  );
}
