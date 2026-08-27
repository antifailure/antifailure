"use client";

import { useEffect, useRef } from "react";

export function usePausedRaf(
  active: boolean,
  onFrame: (now: number, elapsed: number) => void,
) {
  const cb = useRef(onFrame);
  cb.current = onFrame;

  useEffect(() => {
    if (!active) return;
    let raf = 0;
    const t0 = performance.now();
    const loop = (now: number) => {
      cb.current(now, now - t0);
      raf = requestAnimationFrame(loop);
    };
    raf = requestAnimationFrame(loop);
    return () => cancelAnimationFrame(raf);
  }, [active]);
}
