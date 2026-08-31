"use client";

import { Picture } from "@/components/Picture";
import SoftAurora from "@/components/SoftAurora";

/**
 * One `sizes` string, used by both copies of the image below, and they have to
 * stay identical.
 *
 * The same aurora is rendered twice: once in the wide desktop frame that the
 * SoftAurora canvas sits on top of, and once as a plain image for phones,
 * where the canvas is not worth the battery. Only one of the two is ever
 * visible, but `hidden` is a paint instruction and not a fetch one, so the
 * browser downloads whatever candidate each of them resolves to regardless.
 *
 * If the two disagree they resolve to different files and a phone downloads
 * both. Agreeing means the pair always resolves to the same candidate, which
 * is one request out of the cache either way: 768w below the `sm` breakpoint,
 * the full 1536w above it.
 */
const AURORA_SIZES = "(max-width: 639px) 768px, 1920px";

export function HeroFilm() {
  return (
    <>
      <div className="relative -top-16 left-1/2 h-[832px] w-480 -translate-x-1/2 overflow-hidden max-xl:-top-12.5 max-xl:h-[700px] max-xl:w-326 max-lg:-top-2 max-lg:h-[560px] max-lg:w-254 max-sm:hidden">
        <Picture
          src="/home/hero-aurora.png"
          alt=""
          fill
          priority
          sizes={AURORA_SIZES}
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
      </div>
      <Picture
        className="relative left-1/2 hidden w-[min(752px,180%)] max-w-none -translate-x-1/2 max-sm:block"
        src="/home/hero-aurora.png"
        width={752}
        height={326}
        alt=""
        priority
        sizes={AURORA_SIZES}
      />
    </>
  );
}
