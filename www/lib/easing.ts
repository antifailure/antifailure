export const EASE = [0.16, 1, 0.3, 1] as const;
export const EASE_OUT_CUBIC = (t: number) => 1 - Math.pow(1 - t, 3);
export const EASE_OUT_QUART = (t: number) => 1 - Math.pow(1 - t, 4);
export const EASE_IN_OUT = [0.45, 0, 0.55, 1] as const;

export function prefersReducedMotion() {
  if (typeof window === "undefined") return false;
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

export function clamp(n: number, min = 0, max = 1) {
  return Math.min(max, Math.max(min, n));
}

export function lerp(a: number, b: number, t: number) {
  return a + (b - a) * t;
}
