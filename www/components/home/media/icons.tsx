import { cn } from "@/lib/cn";

export type HeadingIconName = "workload" | "migrations" | "twins" | "firewall" | "features";

export function HeadingIcon({
  name,
  className,
}: {
  name: HeadingIconName;
  className?: string;
}) {
  const cls = cn("pointer-events-none size-14 max-xl:size-12 max-lg:size-10 max-md:size-9", className);
  if (name === "migrations") {
    return (
      <svg viewBox="0 0 56 56" className={cls} fill="none" aria-hidden>
        <rect x="8" y="10" width="40" height="36" stroke="currentColor" strokeWidth="1.6" />
        <path d="M8 38h40" stroke="currentColor" strokeWidth="1.6" />
        <path d="M14 34l8-10 7 6 9-14 4 6" stroke="#33bf00" strokeWidth="1.8" />
        <circle cx="14" cy="34" r="1.6" fill="#33bf00" />
        <circle cx="38" cy="16" r="1.6" fill="#33bf00" />
      </svg>
    );
  }
  if (name === "workload") {
    return (
      <svg viewBox="0 0 56 56" className={cls} fill="none" aria-hidden>
        <path d="M10 18h36" stroke="currentColor" strokeWidth="1.6" />
        <path d="M10 28h36" stroke="#33bf00" strokeWidth="1.6" />
        <path d="M10 38h36" stroke="currentColor" strokeWidth="1.6" />
        <circle cx="18" cy="18" r="2.2" fill="currentColor" />
        <circle cx="30" cy="28" r="2.2" fill="#33bf00" />
        <circle cx="42" cy="38" r="2.2" fill="currentColor" />
      </svg>
    );
  }
  if (name === "twins") {
    return (
      <svg viewBox="0 0 56 56" className={cls} fill="none" aria-hidden>
        <path d="M12 44V20h14v24H12Z" stroke="currentColor" strokeWidth="1.6" />
        <path d="M30 44V12h14v32H30Z" stroke="#33bf00" strokeWidth="1.6" />
        <path d="M19 20V12h11" stroke="currentColor" strokeWidth="1.6" />
      </svg>
    );
  }
  if (name === "firewall") {
    return (
      <svg viewBox="0 0 56 56" className={cls} fill="none" aria-hidden>
        <path
          d="M28 8l16 6v12c0 10.5-7.2 18.4-16 22-8.8-3.6-16-11.5-16-22V14l16-6Z"
          stroke="currentColor"
          strokeWidth="1.6"
        />
        <path d="M20 28h16M28 20v16" stroke="#33bf00" strokeWidth="1.6" />
      </svg>
    );
  }
  return (
    <svg viewBox="0 0 56 56" className={cls} fill="none" aria-hidden>
      <rect x="10" y="10" width="14" height="14" stroke="currentColor" strokeWidth="1.6" />
      <rect x="32" y="10" width="14" height="14" stroke="#33bf00" strokeWidth="1.6" />
      <rect x="10" y="32" width="14" height="14" stroke="currentColor" strokeWidth="1.6" />
      <rect x="32" y="32" width="14" height="14" stroke="currentColor" strokeWidth="1.6" />
    </svg>
  );
}

export function FeatureIcon({
  name,
  className,
}: {
  name: "closed" | "boundary" | "report" | "cleanup" | "oracle" | "postgres";
  className?: string;
}) {
  const cls = cn("pointer-events-none size-6 max-lg:size-5 max-md:size-4", className);
  if (name === "closed") {
    return (
      <svg viewBox="0 0 24 24" className={cls} fill="none" aria-hidden>
        <circle cx="12" cy="12" r="8.2" stroke="currentColor" strokeWidth="1.4" />
        <path d="M8 12h8" stroke="currentColor" strokeWidth="1.4" />
      </svg>
    );
  }
  if (name === "boundary") {
    return (
      <svg viewBox="0 0 24 24" className={cls} fill="none" aria-hidden>
        <rect x="4" y="6" width="16" height="12" stroke="currentColor" strokeWidth="1.4" />
        <path d="M4 10h16" stroke="currentColor" strokeWidth="1.4" />
      </svg>
    );
  }
  if (name === "report") {
    return (
      <svg viewBox="0 0 24 24" className={cls} fill="none" aria-hidden>
        <path d="M5 19V8l4 3 3-6 3 8 4-4v10H5Z" stroke="currentColor" strokeWidth="1.4" />
      </svg>
    );
  }
  if (name === "cleanup") {
    return (
      <svg viewBox="0 0 24 24" className={cls} fill="none" aria-hidden>
        <circle cx="12" cy="12" r="8.2" stroke="currentColor" strokeWidth="1.4" />
        <path d="M8 12l2.6 2.6L16 9.2" stroke="currentColor" strokeWidth="1.4" />
      </svg>
    );
  }
  if (name === "oracle") {
    return (
      <svg viewBox="0 0 24 24" className={cls} fill="none" aria-hidden>
        <path d="M12 4v4M12 16v4M4 12h4M16 12h4" stroke="currentColor" strokeWidth="1.4" />
        <circle cx="12" cy="12" r="3.2" stroke="currentColor" strokeWidth="1.4" />
      </svg>
    );
  }
  return (
    <svg viewBox="0 0 24 24" className={cls} fill="none" aria-hidden>
      <ellipse cx="12" cy="8" rx="7" ry="3" stroke="currentColor" strokeWidth="1.4" />
      <path d="M5 8v8c0 1.7 3.1 3 7 3s7-1.3 7-3V8" stroke="currentColor" strokeWidth="1.4" />
    </svg>
  );
}

/**
 * The two trust-model marks.
 *
 * These slots have now been wrong twice. They were `size-8 rounded-full
 * bg-black`, a filled circle standing in for an icon, and then they were two
 * 36px diagrams that tried to narrate the whole claim: an arrow, a green bar
 * and three ghosted lines for one, a square inside a square with a dotted stub
 * for the other. At the size they actually render, neither one resolved into a
 * thing. A reader saw a smudge and moved on, and the two marks did not even
 * carry the same visual weight.
 *
 * So they are drawn as pictograms rather than as diagrams: one subject each,
 * one stroke weight, one colour, sized so the subject survives. A shut padlock
 * is "fail closed" without reading the label under it, and a store of data
 * standing inside a wall is "customer-hosted". Nothing here is coloured,
 * because a highlight stroke inside a 48px mark is a detail nobody can see and
 * it fought the mark's own outline for attention.
 */
export function TrustIcon({
  name,
  className,
}: {
  name: "failclosed" | "hosted";
  className?: string;
}) {
  const cls = cn("pointer-events-none size-12 max-lg:size-11 max-md:size-10", className);
  if (name === "failclosed") {
    // A padlock, shut. The shackle is closed on purpose: the resting state of
    // the gate is the locked one, which is the whole claim.
    return (
      <svg viewBox="0 0 48 48" className={cls} fill="none" aria-hidden>
        <path
          d="M11.5 21v-6.5a8.5 8.5 0 0 1 17 0V21"
          stroke="currentColor"
          strokeWidth="1.6"
          strokeLinecap="round"
        />
        <rect x="3" y="21" width="34" height="21" rx="2" stroke="currentColor" strokeWidth="1.6" />
        <circle cx="20" cy="29" r="2.4" stroke="currentColor" strokeWidth="1.6" />
        <path d="M20 31.4v4.2" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
      </svg>
    );
  }
  // A store of data standing inside a wall that has no opening. The control
  // plane is not drawn at all, because the claim is about what never leaves.
  return (
    <svg viewBox="0 0 48 48" className={cls} fill="none" aria-hidden>
      <rect x="3" y="7" width="40" height="34" stroke="currentColor" strokeWidth="1.6" />
      <ellipse cx="23" cy="17" rx="11.5" ry="4" stroke="currentColor" strokeWidth="1.6" />
      <path
        d="M11.5 17v13c0 2.21 5.15 4 11.5 4s11.5-1.79 11.5-4V17"
        stroke="currentColor"
        strokeWidth="1.6"
      />
      <path d="M11.5 23.5c0 2.21 5.15 4 11.5 4s11.5-1.79 11.5-4" stroke="currentColor" strokeWidth="1.6" />
    </svg>
  );
}

export function LegendDot({ className }: { className?: string }) {
  return <span className={cn("size-2 shrink-0 rounded-full", className)} />;
}

export function Grain({ className }: { className?: string }) {
  return (
    <div
      className={cn("pointer-events-none absolute inset-0 opacity-[0.22] mix-blend-overlay noise", className)}
      aria-hidden
    />
  );
}
