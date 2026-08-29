"use client";

import { Picture } from "@/components/Picture";
import { Grain } from "./icons";

export function FirewallStage() {
  return (
    <div className="relative mt-16 aspect-[1184/580] w-full overflow-hidden bg-[#111315] max-xl:mt-12 max-lg:mt-10">
      <Picture src="/home/firewall-log.png" alt="" fill sizes="1184px" className="object-cover" />
      <div className="absolute inset-0 bg-gradient-to-t from-[#111315]/50 via-transparent to-[#111315]/20" />
      <div className="film-scanline opacity-50" />
      <div className="absolute top-0 right-0 left-0 z-10 flex items-center justify-between border-b border-white/10 bg-[#111315]/70 px-6 py-3 font-mono text-[11px] tracking-[0.14em] text-white/55">
        <span>SIDE-EFFECT FIREWALL</span>
        <span className="film-blink text-[#33bf00]">FAIL CLOSED</span>
      </div>
      <Grain />
    </div>
  );
}
