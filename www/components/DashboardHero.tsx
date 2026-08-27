"use client";

import { CopyCli, Pill } from "./Pills";

export function DashboardHero() {
  return (
    <section id="dashboard" className="relative min-h-screen overflow-hidden bg-[#f7f7f5]">
      <img
        src="/proving-ground.jpg"
        alt=""
        aria-hidden
        className="absolute inset-0 h-full w-full object-cover object-center"
      />
      <div className="relative z-10 flex min-h-screen flex-col justify-end p-6 lg:p-10">
        <div className="rounded-2xl bg-[#f7f7f5] p-8 shadow-[0_20px_60px_rgba(0,0,0,0.18)] lg:max-w-[720px] lg:p-10">
          <h2 className="text-[40px] font-semibold leading-[1.08] tracking-[-0.04em] text-black md:text-[56px]">
            The proving ground for the conditions that actually matter.
          </h2>
          <div className="mt-8 flex flex-wrap items-end justify-between gap-6">
            <p className="max-w-[420px] text-[15px] leading-6 text-black/70">
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
      </div>
    </section>
  );
}
