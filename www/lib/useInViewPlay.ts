"use client";

import { useEffect, useState, type RefObject } from "react";
import { useInView } from "motion/react";
import { prefersReducedMotion } from "./easing";

export function useInViewPlay(ref: RefObject<Element | null>, amount = 0.15) {
  const inView = useInView(ref, { amount, once: false, margin: "0px 0px -8% 0px" });
  const [reduced, setReduced] = useState(false);
  const [story, setStory] = useState(false);

  useEffect(() => {
    const reducedNow = prefersReducedMotion();
    setReduced(reducedNow);
    if (reducedNow) setStory(true);
  }, []);

  useEffect(() => {
    if (reduced) return;
    if (inView) setStory(true);
  }, [inView, reduced]);

  return {
    inView,
    reduced,
    story: reduced || story,
    idle: inView && !reduced && story,
  };
}

export function useDelayedFlag(active: boolean, delayMs = 0) {
  const [ready, setReady] = useState(delayMs === 0 && active);

  useEffect(() => {
    if (!active) {
      setReady(false);
      return;
    }
    if (delayMs <= 0) {
      setReady(true);
      return;
    }
    const t = window.setTimeout(() => setReady(true), delayMs);
    return () => window.clearTimeout(t);
  }, [active, delayMs]);

  return ready;
}
