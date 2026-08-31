"use client";

export function Caret({ className = "bg-white" }: { className?: string }) {
  return (
    <span
      // Static. A block at the end of the line reads as a terminal cursor
      // without blinking at somebody who is trying to read the line it sits
      // on, and a thing that animates forever while the reader does nothing is
      // the one motion this project does not ship.
      className={`caret-live inline-block h-[1em] w-[7px] translate-y-[2px] align-middle ${className}`}
      aria-hidden
    />
  );
}
