"use client";

import { Picture } from "@/components/Picture";
import { cn } from "@/lib/cn";
import { Grain } from "./icons";

function Frame({
  src,
  alt = "",
  children,
  active,
}: {
  src: string;
  alt?: string;
  children: React.ReactNode;
  active: boolean;
}) {
  return (
    <div className="absolute inset-0 bg-[#111315]">
      <Picture src={src} alt={alt} fill sizes="512px" className="object-cover opacity-90" />
      <div
        className={cn(
          "absolute inset-0 transition-opacity duration-200",
          active ? "opacity-100" : "opacity-40",
        )}
      >
        {children}
      </div>
      <Grain />
      <div className="pointer-events-none absolute inset-0 bg-gradient-to-t from-[#111315]/80 via-transparent to-[#111315]/20" />
    </div>
  );
}

function TwinDemo({ active }: { active: boolean }) {
  return (
    <Frame src="/home/twin-stack.png" active={active}>
      <div className="absolute inset-0 p-4">
        <div className={cn("film-clone absolute top-6 left-4 right-10 h-[38%] border border-white/15 bg-black/40", active && "film-play")}>
          <div className="flex items-center justify-between px-3 py-2 font-mono text-[9px] tracking-extra-tight text-white/50">
            <span>baseline</span>
            <span>prod-shaped</span>
          </div>
          <div className="mx-3 h-px bg-white/10" />
          <div className="space-y-1.5 p-3">
            <div className="h-1.5 w-3/4 bg-white/20" />
            <div className="h-1.5 w-1/2 bg-white/12" />
            <div className="h-1.5 w-2/3 bg-white/12" />
          </div>
        </div>
        <div className={cn("film-clone-late absolute bottom-6 left-8 right-4 h-[42%] border border-[#33bf00]/70 bg-black/50", active && "film-play")}>
          <div className="flex items-center justify-between px-3 py-2 font-mono text-[9px] tracking-extra-tight text-[#33bf00]">
            <span>candidate twin</span>
            <span className="film-blink">isolated</span>
          </div>
          <div className="mx-3 h-px bg-[#33bf00]/30" />
          <div className="space-y-1.5 p-3">
            <div className="h-1.5 w-3/4 bg-[#33bf00]/50" />
            <div className="h-1.5 w-2/3 bg-white/15" />
            <div className="h-1.5 w-1/2 bg-white/12" />
          </div>
          <div className="film-scanline" />
        </div>
      </div>
    </Frame>
  );
}

const STATE_ROWS = [
  ["email", "m***@twin.local"],
  ["card", "tok_sim_9f2"],
  ["user_id", "u_8f2a"],
  ["ssn", "masked"],
  ["phone", "+1 ••• ••19"],
];

function StateDemo({ active }: { active: boolean }) {
  return (
    <Frame src="/home/safe-state.png" active={active}>
      <div className="absolute inset-0 flex flex-col justify-end p-3">
        <div className="border border-white/10 bg-[#111315]/85 font-mono text-[10px] text-white/80">
          {STATE_ROWS.map(([col, val], i) => (
            <div
              key={col}
              className={cn(
                "flex items-center justify-between border-b border-white/8 px-3 py-2 last:border-0",
                active && "film-row",
              )}
              style={{ animationDelay: `${i * 180}ms` }}
            >
              <span className="text-white/35">{col}</span>
              <span className={col === "user_id" ? "text-[#33bf00]" : "text-white"}>{val}</span>
            </div>
          ))}
        </div>
      </div>
    </Frame>
  );
}

const FIRE_ROWS = [
  ["stripe.charges", "simulated", "ok"],
  ["sendgrid.send", "captured", "ok"],
  ["hooks.prod", "blocked", "bad"],
  ["unknown:443", "denied", "bad"],
  ["s3.clone", "allowed", "ok"],
];

function FirewallDemo({ active }: { active: boolean }) {
  return (
    <Frame src="/home/firewall-log.png" active={active}>
      <div className="absolute inset-0 overflow-hidden p-3">
        <div className="mb-2 font-mono text-[9px] tracking-[0.14em] text-white/35">EGRESS · FAIL CLOSED</div>
        <div className={cn("space-y-1.5", active && "film-log")}>
          {[...FIRE_ROWS, ...FIRE_ROWS].map(([host, status, tone], i) => (
            <div key={`${host}-${i}`} className="flex items-center justify-between bg-black/45 px-2 py-1.5 font-mono text-[10px]">
              <span className="text-white/55">{host}</span>
              <span className={tone === "bad" ? "text-[#ff5a5a]" : "text-[#33bf00]"}>{status}</span>
            </div>
          ))}
        </div>
      </div>
    </Frame>
  );
}

function WorkloadDemo({ active }: { active: boolean }) {
  return (
    <div className="absolute inset-0 bg-[#111315]">
      <Picture src="/home/ide-stage.png" alt="" fill sizes="512px" className="object-cover object-top opacity-80" />
      <div className={cn("absolute inset-0", active ? "opacity-100" : "opacity-50")}>
        <div className="absolute top-3 left-3 right-3 h-5 bg-black/50">
          <div className="flex h-full items-center gap-1 px-2">
            <span className="size-1.5 rounded-full bg-[#ff5f57]" />
            <span className="size-1.5 rounded-full bg-[#febc2e]" />
            <span className="size-1.5 rounded-full bg-[#28c840]" />
            <span className="ml-2 font-mono text-[8px] text-white/40">wind-tunnel.yml</span>
          </div>
        </div>
        <pre className="absolute top-10 left-3 right-3 font-mono text-[9px] leading-4 text-[#9cdcfe]">
          <span className="text-[#6a9955]"># observed · deterministic · exploratory</span>
          {"\n"}
          <span className="text-[#c586c0]">contain</span>: [stripe, email]
          {"\n"}
          <span className="text-[#c586c0]">compare</span>: baseline_vs_candidate
          {"\n"}
          <span className={cn("text-[#33bf00]", active && "film-type")}>on_pr: create_twin</span>
          {active ? <span className="film-caret">▍</span> : null}
        </pre>
        <div className="absolute right-3 bottom-16 left-3 space-y-2">
          {["observed 42%", "deterministic 38%", "exploratory 20%"].map((row, i) => (
            <div key={row}>
              <div className="mb-1 font-mono text-[8px] uppercase tracking-extra-tight text-white/40">{row}</div>
              <div className="h-1 overflow-hidden bg-white/10">
                <div
                  className={cn("h-full bg-[#33bf00]", active && "film-bar")}
                  style={{ width: `${42 - i * 11}%`, animationDelay: `${i * 200}ms` }}
                />
              </div>
            </div>
          ))}
        </div>
      </div>
      <Grain />
    </div>
  );
}

function MigrationDemo({ active }: { active: boolean }) {
  return (
    <Frame src="/home/lock-chart.png" active={active}>
      <div className="absolute inset-0 flex flex-col justify-end p-3">
        <div className="font-mono text-[9px] uppercase tracking-[0.12em] text-[#ff5a5a]">ACCESS EXCLUSIVE</div>
        <div className="mt-2 h-2 overflow-hidden bg-white/10">
          <div className={cn("h-full w-[78%] origin-left hatch-red", active && "film-bar")} />
        </div>
        <div className="mt-2 flex justify-between font-mono text-[9px] text-white/45">
          <span>subscriptions</span>
          <span className={cn(active && "film-blink")}>27.4s lock</span>
        </div>
      </div>
    </Frame>
  );
}

export function HeroDemo({
  kind,
  active,
}: {
  kind: "twin" | "state" | "firewall" | "workload" | "migration";
  active: boolean;
}) {
  if (kind === "twin") return <TwinDemo active={active} />;
  if (kind === "state") return <StateDemo active={active} />;
  if (kind === "firewall") return <FirewallDemo active={active} />;
  if (kind === "workload") return <WorkloadDemo active={active} />;
  return <MigrationDemo active={active} />;
}
