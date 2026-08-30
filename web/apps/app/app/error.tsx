"use client";

// The last resort.
//
// A page that throws in a way nothing above caught still has to say something
// true, and "try again" is genuinely the right advice for most of what reaches
// here. The digest is shown because it is the one thing that connects what
// somebody saw to a line in the server's log, and it carries no data.

import { useEffect } from "react";

export default function Error({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  useEffect(() => {
    // The browser console is where somebody debugging this will look first,
    // and the server log has the rest.
    console.error(error);
  }, [error]);

  return (
    <div className="mesh-grid flex min-h-dvh flex-col items-center justify-center px-4 py-10">
      <div className="w-full max-w-[440px] rounded-xl border border-hair bg-surface p-5 sm:p-6">
        <h1 className="text-[20px] font-semibold leading-[1.15] tracking-tighter text-ink">
          This page did not finish
        </h1>
        <p className="mt-2 text-[13px] leading-[1.6] text-muted">
          Something went wrong rendering it. Your session is untouched and nothing you were looking
          at has changed.
        </p>
        {error.digest ? (
          <p className="mt-2 font-mono text-[12px] text-faint">{error.digest}</p>
        ) : null}
        <div className="mt-5 flex gap-2">
          <button
            type="button"
            onClick={reset}
            className="inline-flex h-9 items-center rounded-lg bg-ink px-3.5 text-[13px] font-medium tracking-snug text-white transition-colors hover:bg-[#1c1c1c] active:translate-y-px"
          >
            Try again
          </button>
          <a
            href="/"
            className="inline-flex h-9 items-center rounded-lg border border-edge bg-surface px-3 text-[13px] font-medium tracking-snug text-ink transition-colors hover:bg-sunken"
          >
            Every environment
          </a>
        </div>
      </div>
    </div>
  );
}
