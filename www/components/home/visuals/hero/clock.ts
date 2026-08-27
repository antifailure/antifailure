"use client";

import { useRef, useState } from "react";
import { clamp } from "@/lib/easing";
import { useInViewPlay } from "@/lib/useInViewPlay";
import { usePausedRaf } from "@/lib/usePausedRaf";
import { seededNoise } from "@/components/home/visuals/primitives";

export type FilmProps = { active: boolean; hovered: boolean };

export function useHeroFilmClock({
  loop,
  active,
  hovered,
  hoverRange,
  stillT,
  reducedT,
}: {
  loop: number;
  active: boolean;
  hovered: boolean;
  hoverRange?: readonly [number, number];
  stillT?: number;
  reducedT?: number;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const { inView, reduced } = useInViewPlay(ref, 0.15);
  const freeze = stillT ?? loop * 0.4;
  const info = reducedT ?? freeze;
  const [t, setT] = useState(freeze);
  const [elapsedSec, setElapsedSec] = useState(0);
  const playing = active && inView && !reduced;

  usePausedRaf(playing, (_now, elapsed) => {
    const sec = elapsed / 1000;
    setElapsedSec(sec);
    if (hovered && hoverRange) {
      const [a, b] = hoverRange;
      setT(a + (sec % Math.max(0.001, b - a)));
      return;
    }
    setT(sec % loop);
  });

  let clock = freeze;
  if (reduced) clock = info;
  else if (active) clock = t;

  return { ref, t: clock, elapsed: playing ? elapsedSec : clock, reduced, playing, inView };
}

export function span(t: number, a: number, b: number) {
  if (b <= a) return t >= a ? 1 : 0;
  return clamp((t - a) / (b - a));
}

export function on(t: number, a: number, b?: number) {
  if (t < a) return false;
  if (b != null && t >= b) return false;
  return true;
}

export function typed(text: string, t: number, start: number, msPerChar: number) {
  if (t < start) return "";
  const n = Math.floor(((t - start) * 1000) / msPerChar);
  return text.slice(0, Math.min(text.length, n));
}

export function fmtHMS(sec: number) {
  const s = Math.max(0, Math.floor(sec));
  const mm = String(Math.floor(s / 60)).padStart(2, "0");
  const ss = String(s % 60).padStart(2, "0");
  return `${mm}:${ss}`;
}

export function sparkPath(seed: number, n = 18, w = 220, h = 56, amp = 0.55) {
  return Array.from({ length: n }, (_, i) => {
    const x = (i / (n - 1)) * w;
    const y = h * (0.55 - (seededNoise(i, seed) - 0.5) * amp);
    return `${i ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`;
  }).join("");
}
