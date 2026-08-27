"use client";

import Image from "next/image";
import SoftAurora from "@/components/SoftAurora";

export function CtaFilm() {
  return (
    <div className="pointer-events-none relative overflow-hidden">
      <div className="relative aspect-[1920/944] max-h-[944px] w-full overflow-hidden max-lg:w-[1024px] max-md:hidden">
        <Image src="/home/cta-atmosphere.png" alt="" fill sizes="1920px" className="object-cover" quality={90} />
        <SoftAurora
          className="absolute inset-0"
          color1="#33bf00"
          color2="#00e599"
          brightness={0.55}
          speed={0.35}
          scale={1.6}
          bandHeight={0.55}
          enableMouseInteraction={false}
        />
        <div className="absolute inset-0 opacity-25 mix-blend-overlay noise" />
      </div>
      <div className="relative hidden h-[500px] w-full overflow-hidden max-md:block">
        <Image src="/home/cta-atmosphere.png" alt="" fill sizes="767px" className="object-cover" />
        <div className="absolute inset-0 opacity-30 mix-blend-overlay noise" />
      </div>
    </div>
  );
}
