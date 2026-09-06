"use client";

import { Button } from "@/components/ui";

/**
 * The page under this boundary threw while rendering.
 *
 * Until this file existed the console had no boundary at all, so one uncaught
 * exception on any of its pages blanked the whole window with nothing to read
 * and nothing to press. The sentence names what happened, the reference is the
 * request id when the thrown error carried one, and Try again calls Next's
 * reset, which re-renders the segment rather than reloading the page.
 */
export default function PageError({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
  return (
    <div className="px-6 py-12 text-center" role="alert">
      <p className="text-[14px] font-medium text-ink">That page stopped rendering</p>
      <p className="mx-auto mt-2 max-w-[52ch] text-[13px] leading-6 text-muted">
        {error.digest
          ? "Something on this page threw before it could draw. Trying again re-renders it; if it stops again, the reference below is what to send us."
          : "Something on this page threw before it could draw. Trying again re-renders it."}
      </p>
      {error.digest ? (
        <p className="mt-2 font-mono text-[12px] text-muted">Reference: {error.digest}</p>
      ) : null}
      <div className="mt-5 flex justify-center">
        <Button onClick={reset}>Try again</Button>
      </div>
    </div>
  );
}
