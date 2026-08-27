"use client";

import SoftAurora from "./SoftAurora";

export function HeroSection() {
  return (
    <section
      id="top"
      className="relative min-h-[calc(100vh-58px)] overflow-hidden bg-[#f7f7f5]"
    >
      <div className="pointer-events-none absolute inset-0">
        <SoftAurora
          speed={0.2}
          scale={3}
          brightness={0.55}
          color1="#33bf00"
          color2="#00921b"
          noiseFrequency={10}
          noiseAmplitude={9}
          bandHeight={0.5}
          bandSpread={3}
          octaveDecay={0.34}
          layerOffset={0}
          colorSpeed={0.9}
          enableMouseInteraction={false}
        />
        <div className="absolute inset-0 bg-gradient-to-r from-[#f7f7f5] via-[#f7f7f5]/80 to-transparent" />
        <div className="absolute inset-x-0 bottom-0 h-40 bg-gradient-to-t from-[#f7f7f5] to-transparent" />
      </div>
      <div className="relative z-10 flex min-h-[calc(100vh-58px)] flex-col justify-end px-8 pb-24 pt-28 lg:px-16 xl:px-24">
        <div className="mb-5 text-[11px] font-medium tracking-[0.14em] text-black/70">
          PRE-PRODUCTION DEPLOYMENT SAFETY
        </div>
        <h1 className="max-w-[980px] text-[40px] font-semibold leading-[1.06] tracking-[-0.038em] text-black md:text-[54px] lg:text-[60px]">
          A disposable production twin that proves whether a deployment is safe to ship.
        </h1>
        <div className="mt-8 flex flex-wrap gap-3">
          <a
            href="/signup"
            className="inline-flex h-[46px] items-center rounded-full bg-black px-7 text-[15px] font-medium text-white"
          >
            Sign up
          </a>
          <a
            href="/docs"
            className="inline-flex h-[46px] items-center rounded-full border border-black px-7 text-[15px] font-medium text-black"
          >
            Read the docs
          </a>
        </div>
      </div>
    </section>
  );
}
