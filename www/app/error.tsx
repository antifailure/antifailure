"use client";

import { useEffect } from "react";
import Link from "next/link";

// The reason this file exists rather than being left to the framework: the
// homepage renders a WebGL scene, and a browser that cannot give it a context
// throws during render. Without a boundary here, the whole page becomes Next's
// stock "Application error: a client-side exception has occurred", which is a
// black screen with a sentence on it. That is what a visitor with hardware
// acceleration turned off used to get.
//
// The scene itself now degrades instead of throwing, so this should be
// unreachable. It is here because "should be unreachable" is exactly the
// claim an error boundary exists to stop us relying on.
export default function Error({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  useEffect(() => {
    // Server-side digests are the only handle on what actually failed in
    // production, so keep it where somebody debugging can find it.
    console.error("[antifailure] render failed", error.digest ?? "", error);
  }, [error]);

  return (
    <main className="flex min-h-screen flex-col justify-center bg-white px-8 py-24 max-sm:px-5">
      <div className="mx-auto w-full max-w-[46rem]">
        <p className="font-mono text-[13px] uppercase tracking-[0.14em] text-gray-new-40">
          Something broke
        </p>
        <h1 className="mt-4 max-w-[20ch] text-[40px] font-medium leading-[1.05] tracking-tighter text-black max-lg:text-[32px] max-sm:text-[26px]">
          This page failed to render.
        </h1>
        <p className="mt-5 max-w-[54ch] text-[15px] leading-[1.6] tracking-extra-tight text-gray-new-40">
          That is our fault, not yours, and it is worth telling us about. The
          rest of the site is unaffected, and the documentation is plain HTML
          that does not depend on any of this.
        </p>

        {error.digest ? (
          <p className="mt-6 font-mono text-[13px] tracking-extra-tight text-gray-new-40">
            Reference <span className="text-black">{error.digest}</span>
          </p>
        ) : null}

        <div className="mt-10 flex flex-wrap items-center gap-3 max-sm:flex-col max-sm:items-stretch">
          <button
            type="button"
            onClick={reset}
            className="inline-flex h-11 cursor-pointer items-center justify-center rounded-full bg-black px-7 text-center text-[15px] font-medium leading-none tracking-extra-tight text-white transition-colors duration-200 hover:bg-[#292929]"
          >
            Try again
          </button>
          <Link prefetch={false}
            href="/docs"
            className="inline-flex h-11 items-center justify-center rounded-full border border-black/40 bg-black/[0.02] px-7 text-center text-[15px] leading-none tracking-extra-tight text-black transition-colors duration-200 hover:border-black"
          >
            Read the documentation
          </Link>
          <a
            href="https://github.com/antifailure/antifailure/issues/new"
            className="text-[15px] tracking-extra-tight text-gray-new-40 underline decoration-gray-new-40/40 underline-offset-4 hover:text-black hover:decoration-black"
          >
            Report it
          </a>
        </div>
      </div>
    </main>
  );
}
