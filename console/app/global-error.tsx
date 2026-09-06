"use client";

/**
 * The boundary of last resort, for a throw in the root layout itself.
 *
 * It renders its own html and body because the layout that would normally
 * provide them is the thing that failed, so nothing here can import the shell
 * or the design system's components; the classes are the same tokens by name.
 */
export default function GlobalError({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
  return (
    <html lang="en">
      <body className="bg-paper text-ink">
        <div className="px-6 py-16 text-center" role="alert">
          <p className="text-[14px] font-medium">The console stopped rendering</p>
          <p className="mx-auto mt-2 max-w-[52ch] text-[13px] leading-6 text-muted">
            {error.digest
              ? "Something threw before the page could draw. Trying again reloads it; if it stops again, the reference below is what to send us."
              : "Something threw before the page could draw. Trying again reloads it."}
          </p>
          {error.digest ? <p className="mt-2 font-mono text-[12px] text-muted">Reference: {error.digest}</p> : null}
          <div className="mt-5 flex justify-center">
            <button
              type="button"
              onClick={reset}
              className="rounded-md bg-ink px-4 py-2 text-[13px] font-medium text-paper"
            >
              Try again
            </button>
          </div>
        </div>
      </body>
    </html>
  );
}
