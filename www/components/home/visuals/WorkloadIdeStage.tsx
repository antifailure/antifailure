"use client";

import { useRef } from "react";
import { CopyCli } from "@/components/Pills";
import { IdePlay } from "@/components/IdeSection";
import { useInViewPlay } from "@/lib/useInViewPlay";

export function WorkloadIdeStage() {
  const glow = useRef<HTMLDivElement>(null);
  const { story } = useInViewPlay(glow, 0.15);

  return (
    <div className="relative overflow-hidden border border-black/12 bg-[#f7f7f5]">
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

      <div className="relative px-6 pb-8 pt-[72px] sm:px-10 lg:px-12 lg:pb-10 lg:pt-[88px]">
        <div className="pointer-events-none absolute inset-x-6 top-[88px] hidden h-px bg-black/15 lg:block lg:inset-x-12" />
        <div
          className="pointer-events-none absolute left-[16%] top-5 hidden items-start gap-2 lg:flex"
          aria-hidden
        >
          <span className="h-[56px] w-px bg-black/25" />
          <span className="pt-0.5 text-[12px] leading-4 text-black/45">
            Observed patterns compiled into journeys
          </span>
        </div>
        <div
          className="pointer-events-none absolute right-[14%] top-5 hidden items-start gap-2 lg:flex"
          aria-hidden
        >
          <span className="h-[56px] w-px bg-black/25" />
          <span className="pt-0.5 text-[12px] leading-4 text-black/45">
            Exploratory users discover. Deterministic runs at scale.
          </span>
        </div>
        <div className="pointer-events-none absolute bottom-8 left-12 hidden h-px w-16 bg-black/15 lg:block" />
        <div className="pointer-events-none absolute right-12 bottom-8 hidden h-px w-16 bg-black/15 lg:block" />
        <IdePlay />
      </div>

      <div className="relative flex flex-wrap items-center justify-between gap-4 bg-[#18191b] px-6 py-5 sm:px-10 lg:px-12">
        <p className="text-[18px] tracking-[-0.015em] text-white max-sm:text-[15px]">
          Try for yourself, start proving a change before it ships.
        </p>
        <CopyCli command="$ curl -fsSL https://antifailure.dev/install.sh | sh" variant="mint" />
      </div>
    </div>
  );
}
