"use client";

import { useRef } from "react";
import { CopyCli } from "@/components/Pills";
import { IdePlay } from "@/components/IdeSection";
import { useInViewPlay } from "@/lib/useInViewPlay";

export function WorkloadIdeStage() {
  const glow = useRef<HTMLDivElement>(null);
  const { story } = useInViewPlay(glow, 0.15);

  return (
    <div className="relative min-w-0 overflow-hidden border border-black/12 bg-[#f7f7f5]">
      <div className="pointer-events-none absolute inset-0" aria-hidden>
        <div
          className="absolute inset-0"
          style={{
            background:
              "radial-gradient(ellipse 52% 48% at 0% 0%, rgba(51,191,0,0.48), transparent 72%), radial-gradient(ellipse 52% 48% at 100% 100%, rgba(0,229,153,0.44), transparent 72%)",
            opacity: story ? 1 : 0.78,
            transition: "opacity 0.8s ease",
          }}
        />
        <div
          ref={glow}
          className="absolute inset-0"
          style={{
            backgroundImage: "radial-gradient(#33bf00 1.15px, transparent 1.3px)",
            backgroundSize: "6.5px 6.5px",
            WebkitMaskImage: "radial-gradient(ellipse 52% 48% at 0% 0%, black 0%, transparent 70%)",
            maskImage: "radial-gradient(ellipse 52% 48% at 0% 0%, black 0%, transparent 70%)",
            opacity: story ? 0.85 : 0.55,
            transition: "opacity 0.8s ease",
          }}
        />
        <div
          className="absolute inset-0"
          style={{
            backgroundImage: "radial-gradient(#00e599 1.15px, transparent 1.3px)",
            backgroundSize: "6.5px 6.5px",
            WebkitMaskImage: "radial-gradient(ellipse 52% 48% at 100% 100%, black 0%, transparent 70%)",
            maskImage: "radial-gradient(ellipse 52% 48% at 100% 100%, black 0%, transparent 70%)",
            opacity: story ? 0.85 : 0.55,
            transition: "opacity 0.8s ease",
          }}
        />
        <div
          className="auth-honeycomb absolute inset-0"
          style={{
            WebkitMaskImage:
              "radial-gradient(ellipse 50% 46% at 0% 0%, black 0%, transparent 68%), radial-gradient(ellipse 50% 46% at 100% 100%, black 0%, transparent 68%)",
            maskImage:
              "radial-gradient(ellipse 50% 46% at 0% 0%, black 0%, transparent 68%), radial-gradient(ellipse 50% 46% at 100% 100%, black 0%, transparent 68%)",
            opacity: 0.18,
          }}
        />
      </div>

      <div className="relative px-5 pb-6 pt-6 sm:px-8 sm:pb-8 sm:pt-8 lg:px-12 lg:pb-10">
        <IdePlay />
      </div>

      <div className="relative flex flex-col items-stretch justify-between gap-4 bg-[#18191b] px-5 py-5 sm:flex-row sm:flex-wrap sm:items-center sm:px-10 lg:px-12">
        <p className="min-w-0 text-[18px] tracking-[-0.015em] text-pretty text-white max-sm:text-[15px]">
          Try for yourself, start proving a change before it ships.
        </p>
        <div className="min-w-0 max-w-full">
          <CopyCli command="$ curl -fsSL https://antifailure.dev/install.sh | sh" variant="mint" />
        </div>
      </div>
    </div>
  );
}
