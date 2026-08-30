"use client";

import Image from "next/image";
import SoftAurora from "@/components/SoftAurora";

/**
 * The same green northern-lights wash as the hero, sized to fill a section.
 */
export function GreenAurora({ className = "" }: { className?: string }) {
  return (
    <div className={`absolute inset-0 overflow-hidden ${className}`.trim()} aria-hidden>
      <Image
        src="/home/hero-aurora.png"
        alt=""
        fill
        sizes="100vw"
        quality={90}
        className="object-cover object-center"
      />
      <SoftAurora
        className="absolute inset-0"
        color1="#33bf00"
        color2="#00e599"
        brightness={0.9}
        speed={0.45}
        scale={1.35}
        bandHeight={0.38}
        bandSpread={1.15}
      />
      <div className="pointer-events-none absolute inset-0 opacity-30 mix-blend-overlay noise" />
      <div className="hero-scan pointer-events-none absolute inset-0" />
    </div>
  );
}
