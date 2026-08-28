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
              "radial-gradient(ellipse 70% 90% at 0% 50%, rgba(20,200,184,0.55), transparent 62%), radial-gradient(ellipse 70% 90% at 100% 50%, rgba(232,137,48,0.58), transparent 62%)",
            opacity: story ? 1 : 0.78,
            transition: "opacity 0.8s ease",
          }}
        />
        <div
          className="absolute inset-0"
          style={{
            WebkitMaskImage:
              "linear-gradient(to bottom, transparent 2%, black 12%, black 90%, transparent 100%)",
            maskImage:
              "linear-gradient(to bottom, transparent 2%, black 12%, black 90%, transparent 100%)",
          }}
        >
          <div
            ref={glow}
            className="absolute inset-0"
            style={{
              backgroundImage: "radial-gradient(#0ea89c 1.15px, transparent 1.3px)",
              backgroundSize: "6.5px 6.5px",
              WebkitMaskImage: "linear-gradient(90deg, black 0%, black 28%, transparent 72%)",
              maskImage: "linear-gradient(90deg, black 0%, black 28%, transparent 72%)",
              opacity: story ? 0.85 : 0.55,
              transition: "opacity 0.8s ease",
            }}
          />
          <div
            className="absolute inset-0"
            style={{
              backgroundImage: "radial-gradient(#e07820 1.15px, transparent 1.3px)",
              backgroundSize: "6.5px 6.5px",
              WebkitMaskImage: "linear-gradient(90deg, transparent 28%, black 72%, black 100%)",
              maskImage: "linear-gradient(90deg, transparent 28%, black 72%, black 100%)",
              opacity: story ? 0.85 : 0.55,
              transition: "opacity 0.8s ease",
            }}
          />
        </div>
        <div
          className="auth-honeycomb absolute inset-0"
          style={{
            WebkitMaskImage:
              "linear-gradient(to top, rgba(0,0,0,0.55) 0%, transparent 58%), linear-gradient(90deg, rgba(0,0,0,0.5), transparent 38%, transparent 62%, rgba(0,0,0,0.5))",
            maskImage:
              "linear-gradient(to top, rgba(0,0,0,0.55) 0%, transparent 58%), linear-gradient(90deg, rgba(0,0,0,0.5), transparent 38%, transparent 62%, rgba(0,0,0,0.5))",
            opacity: 0.22,
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
            Crowdi explores. Deterministic runs at scale.
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
