"use client";

import { useEffect, useRef, useState } from "react";
import { clamp } from "@/lib/easing";
import { useInViewPlay } from "@/lib/useInViewPlay";
import { usePausedRaf } from "@/lib/usePausedRaf";
import { seededNoise } from "@/components/home/visuals/primitives";

export type FilmProps = { active: boolean };

/**
 * A hero film's clock. It runs once, from the first frame to the last, and
 * then holds the last frame.
 *
 * It used to advance on `sec % loop`, which is a film that replays for as long
 * as the page is open. In practice it almost never reached the wrap, because
 * HeroServices deactivated each card at 6.4s and the loop is 8 film-seconds at
 * 1.12x, so what a reader actually saw was every film cut off just short of its
 * ending and reset to a blank first frame, over and over, five cards deep,
 * forever. The loop was not even the mechanism; the rotation was.
 *
 * So `active` now only starts the film. Once started it plays through under its
 * own clock and rests, and coming back into view does not rewind it: a film
 * that replays every time it is scrolled past is the same defect wearing a
 * different mechanism.
 *
 * `reducedT` is the frame somebody who asked us not to move things sees, and it
 * is the last frame rather than the first for the same reason the film stops
 * there: the end of these films is the composed state, and the beginning is an
 * empty card.
 */
export function useHeroFilmClock({
  loop,
  active,
  stillT,
  reducedT,
}: {
  loop: number;
  active: boolean;
  stillT?: number;
  reducedT?: number;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const { inView, reduced } = useInViewPlay(ref, 0.15);
  // Just inside the end, not on it. `on(t, a, b)` is exclusive at b, so a film
  // held at exactly `loop` loses every element whose window closes with the
  // film: the first card's candidate branch went blank at the moment it was
  // supposed to be finished.
  const end = loop - 0.001;
  const poster = stillT ?? 0;
  const resting = reducedT ?? end;
  const [t, setT] = useState(poster);
  const [elapsedSec, setElapsedSec] = useState(0);
  const [started, setStarted] = useState(false);
  const [done, setDone] = useState(false);

  useEffect(() => {
    if (active) setStarted(true);
  }, [active]);

  const playing = started && inView && !reduced && !done;

  usePausedRaf(playing, (_now, elapsed) => {
    const sec = (elapsed / 1000) * 1.12;
    if (sec >= end) {
      setElapsedSec(loop);
      setT(end);
      setDone(true);
      return;
    }
    setElapsedSec(sec);
    setT(sec);
  });

  let clock = t;
  if (reduced) clock = resting;
  else if (done) clock = end;
  else if (!started) clock = poster;

  return { ref, t: clock, elapsed: reduced ? loop : elapsedSec, reduced, playing, inView };
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
