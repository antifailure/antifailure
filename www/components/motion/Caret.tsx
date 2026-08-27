"use client";

export function Caret({ className = "bg-white" }: { className?: string }) {
  return (
    <span
      className={`caret-live inline-block h-[1em] w-[7px] translate-y-[2px] align-middle ${className}`}
      style={{ animation: "wt-caret 1.05s steps(1) infinite" }}
      aria-hidden
    />
  );
}
