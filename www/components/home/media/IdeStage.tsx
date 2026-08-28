"use client";

import Image from "next/image";
import { cn } from "@/lib/cn";
import { Grain } from "./icons";

export function IdeOverlay({ className }: { className?: string }) {
  return (
    <div className={cn("pointer-events-none overflow-hidden", className)}>
      <div className="film-scanline opacity-40" />
      <div className="absolute top-[18%] right-[8%] left-[22%] border border-white/10 bg-black/55 p-4 font-mono text-[12px] leading-5 text-white/80 shadow-[0_30px_80px_rgba(0,0,0,0.55)] backdrop-blur-[2px] max-lg:left-[12%] max-lg:text-[11px]">
        <div className="mb-2 flex items-center gap-2 text-[10px] tracking-[0.12em] text-white/40">
          <span className="size-1.5 rounded-full bg-[#33bf00]" />
          WORKLOAD STUDIO
        </div>
        <div className="text-[#6a9955]"># observed · deterministic · exploratory</div>
        <div>
          <span className="text-[#c586c0]">contain</span>
          <span className="text-white/50">: [stripe, email]</span>
        </div>
        <div>
          <span className="text-[#c586c0]">compare</span>
          <span className="text-white/50">: baseline_vs_candidate</span>
        </div>
        <div className="text-[#33bf00]">
          on_pr: create_twin<span className="film-caret">▍</span>
        </div>
      </div>
      <div className="absolute right-6 bottom-8 left-6 flex items-center justify-between border border-white/10 bg-black/65 px-3 py-2 font-mono text-[11px] text-white/45">
        <span>observed 42% · deterministic 38% · exploratory 20%</span>
        <span className="text-[#33bf00]">fail closed</span>
      </div>
      <Grain />
    </div>
  );
}

export function IdeStage() {
  return (
    <div className="pointer-events-none relative w-full bg-[#111315] outline outline-1 outline-black/20 max-sm:hidden">
      <Image
        className="relative w-full"
        src="/home/ide-stage.png"
        alt=""
        width={1056}
        height={628}
        sizes="(min-width: 1024px) 1056px, 100vw"
        quality={90}
      />
      <IdeOverlay className="absolute inset-0" />
    </div>
  );
}
