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
 * These slots used to be `size-8 rounded-full bg-black`, a filled circle in
 * place of an icon, which reads as a decision nobody made. They now say what
 * the claim underneath them says: a packet stopped at a closed gate, and a
 * boundary with the data inside it.
 */
export function TrustIcon({
  name,
  className,
}: {
  name: "failclosed" | "hosted";
  className?: string;
}) {
  const cls = cn("pointer-events-none size-10 max-xl:size-9 max-lg:size-8", className);
  if (name === "failclosed") {
    // An attempt that stops short of the wall, and the production behind the
    // wall it never reached.
    return (
      <svg viewBox="0 0 36 36" className={cls} fill="none" aria-hidden>
        <path d="M2 18h11.4" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
        <path
          d="m9.8 14.4 3.6 3.6-3.6 3.6"
          stroke="currentColor"
          strokeWidth="1.6"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
        <path d="M18.4 3v30" stroke="#33bf00" strokeWidth="2.4" strokeLinecap="round" />
        <path
          d="M23.4 11h10.6M23.4 18h10.6M23.4 25h7.6"
          stroke="currentColor"
          strokeOpacity="0.3"
          strokeWidth="1.6"
          strokeLinecap="round"
        />
      </svg>
    );
  }
  // The customer boundary with the data inside it, and the control plane
  // outside, holding no copy.
  return (
    <svg viewBox="0 0 36 36" className={cls} fill="none" aria-hidden>
      <rect x="1.8" y="8.6" width="22" height="21.6" stroke="currentColor" strokeWidth="1.6" />
      <rect x="6.4" y="13.6" width="12.8" height="11.6" stroke="#33bf00" strokeWidth="1.6" />
      <path d="M24.6 19.4h3.4" stroke="currentColor" strokeWidth="1.6" strokeDasharray="2 2.2" />
      <rect
        x="28.6"
        y="15.4"
        width="5.6"
        height="8"
        stroke="currentColor"
        strokeOpacity="0.45"
        strokeWidth="1.6"
      />
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
