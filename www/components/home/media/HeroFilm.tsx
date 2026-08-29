"use client";

import Image from "next/image";
import SoftAurora from "@/components/SoftAurora";

export function HeroFilm() {
  return (
    <>
      <div className="relative -top-16 left-1/2 h-[832px] w-480 -translate-x-1/2 overflow-hidden max-xl:-top-12.5 max-xl:h-[700px] max-xl:w-326 max-lg:-top-2 max-lg:h-[560px] max-lg:w-254 max-sm:hidden">
        <Image
          src="/home/hero-aurora.png"
          alt=""
          fill
          priority
          sizes="1920px"
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
          enableMouseInteraction
        />
        <div className="pointer-events-none absolute inset-0 opacity-30 mix-blend-overlay noise" />
        <div className="hero-scan pointer-events-none absolute inset-0" />
      </div>
      <Image
        className="relative left-1/2 hidden w-[min(752px,180%)] max-w-none -translate-x-1/2 max-sm:block"
        src="/home/hero-aurora.png"
        width={752}
        height={326}
        alt=""
        priority
      />
    </>
  );
}
