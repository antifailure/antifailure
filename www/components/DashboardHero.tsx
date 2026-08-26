"use client";

import { CopyCli, Pill } from "./Pills";

export function DashboardHero() {
  return (
    <section id="dashboard" className="relative min-h-screen overflow-hidden bg-black">
      <img
        src="/proving-ground.jpg"
        alt=""
        aria-hidden
        className="absolute inset-0 h-full w-full object-cover object-center"
      />
      <div className="absolute inset-0 bg-black/35" />
      <div className="relative z-10 flex min-h-screen flex-col px-10 py-16 lg:px-16">
        <h2 className="max-w-[720px] text-[48px] font-semibold leading-[1.05] tracking-[-0.04em] text-white md:text-[64px]">
          The proving ground for the conditions that actually matter.
        </h2>
        <div className="mt-auto flex flex-wrap items-end justify-between gap-6 pt-24">
          <p className="max-w-[420px] text-[15px] leading-6 text-white/90">
            Know what happens before you deploy.
            <br />
            Catch migration failures, performance regressions, and dangerous side effects.
          </p>
          <div className="flex flex-wrap gap-3">
            <Pill href="/signup">Sign up</Pill>
            <Pill variant="ghost" href="/docs">
              Read the docs
            </Pill>
            <CopyCli variant="green" />
          </div>
        </div>
      </div>
    </section>
  );
}
