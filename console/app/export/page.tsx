"use client";

import { Suspense, useEffect, useState } from "react";
import { useSearchParams } from "next/navigation";
import { ApiError, rest } from "@/lib/api";
import { LogoMark } from "@/components/icons";
import { Lede, Standalone } from "@/components/ui";

interface Held {
  organization: string;
  slug: string;
  generatedAt: string | null;
  expiresAt: string;
  sizeBytes: number;
}

function size(bytes: number): string {
  if (bytes < 1024) return `${bytes} bytes`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

/**
 * Downloading the export of an organization that has been deleted.
 *
 * There is no session here and there cannot be: the organization the session
 * belonged to no longer exists, so there is no membership left to authorise
 * anything. The token in the link is the whole authorisation, which is why the
 * link is handed over once, at the moment the deletion is requested, and why
 * this page never asks anybody to sign in.
 *
 * It asks the API to describe the export before offering it, and does not fetch
 * the document itself. Describing first is what lets a dead link say so instead
 * of showing a button that does nothing, which is indistinguishable from a
 * browser that failed. Not fetching the document is why the download is a plain
 * link: the API answers with a Content-Disposition header, so following it IS
 * the download, and pulling several megabytes of JSON into a tab to hand it
 * straight back to the same browser would only add a way to fail.
 */
function Download() {
  const params = useSearchParams();
  const token = params.get("token") ?? "";
  const [held, setHeld] = useState<Held | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const [looking, setLooking] = useState(true);

  useEffect(() => {
    let alive = true;
    if (!token) {
      setLooking(false);
      setError(
        "This link is missing its token. Use the link you were given when you asked for the deletion; it is the only copy.",
      );
      return;
    }
    rest<Held>(`/exports/deletion?describe=1&token=${encodeURIComponent(token)}`)
      .then((found) => {
        if (alive) setHeld(found);
      })
      .catch((err: unknown) => {
        if (!alive) return;
        // 409 is a real link for an export the deletion has not produced yet.
        // Calling that "not valid" would send somebody looking for another
        // link, and there is no other link.
        if (err instanceof ApiError && err.status === 409) setPending(true);
        setError(
          err instanceof ApiError
            ? err.message
            : "The control plane could not be reached. Try the link again in a moment.",
        );
      })
      .finally(() => {
        if (alive) setLooking(false);
      });
    return () => {
      alive = false;
    };
  }, [token]);

  if (looking) {
    return (
      <div className="grid min-h-dvh place-items-center" role="status">
        <LogoMark className="h-8 w-8 opacity-40" />
        <span className="sr-only">Loading</span>
      </div>
    );
  }

  if (pending) {
    return (
      <Standalone title="Your export is not ready yet">
        <Lede>{error}</Lede>
        <p className="mt-4 text-[12.5px] leading-5 text-dim">
          Keep this link. It is the one that works when the export is done, and there is no other.
        </p>
      </Standalone>
    );
  }

  if (error || !held) {
    return (
      <Standalone title="That download link is not valid" alert>
        <Lede>
          {error ??
            "This link is not valid any more. An export is kept for a limited time after a deletion, and there is no way to produce another copy once the organization is gone."}
        </Lede>
      </Standalone>
    );
  }

  return (
    <Standalone title={`Export of ${held.organization}`} width={460}>
      <Lede>
        A complete copy, taken before anything was deleted. One JSON file, {size(held.sizeBytes)}:
        people, repositories, masking rules, egress policy, environments, runs, verdicts, billing
        history and the audit log.
      </Lede>

      <div className="mt-7">
        <a
          href={`/exports/deletion?token=${encodeURIComponent(token)}`}
          download
          className="inline-flex h-11 w-full items-center justify-center gap-2.5 rounded-md bg-ink px-4 text-[14px] font-medium text-white transition-colors hover:bg-[#2b2b2b]"
        >
          Download the export
        </a>
      </div>

      <dl className="mt-6 grid grid-cols-2 gap-x-4 gap-y-4 rounded-lg border border-rule bg-card px-4 py-4">
        <div className="min-w-0">
          <dt className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">Taken</dt>
          <dd className="mt-1 text-[13px] text-ink">
            {held.generatedAt ? new Date(held.generatedAt).toLocaleDateString() : "--"}
          </dd>
        </div>
        <div className="min-w-0">
          <dt className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">
            Link works until
          </dt>
          <dd className="mt-1 text-[13px] text-ink">
            {new Date(held.expiresAt).toLocaleDateString()}
          </dd>
        </div>
      </dl>

      <p className="mt-3 text-[12.5px] leading-5 text-dim">
        Keep the file somewhere safe. There is no way to produce another copy once the organization
        is gone.
      </p>

      <div className="mt-8 border-t border-rule pt-5">
        <h2 className="text-[13px] font-semibold tracking-extra-tight text-ink">
          What you can put back
        </h2>
        <p className="mt-2 text-[12.5px] leading-5 text-muted">
          Inside the file, <span className="font-mono text-[11.5px]">files</span> holds text keyed
          by path. The masking file goes at the root of the repository it names and the engine reads
          it as it is; the egress block goes into{" "}
          <span className="font-mono text-[11.5px]">antifailure.yaml</span>.
        </p>
      </div>
    </Standalone>
  );
}

export default function ExportPage() {
  return (
    <Suspense
      fallback={
        <div className="grid min-h-dvh place-items-center" role="status">
          <LogoMark className="h-8 w-8 opacity-40" />
          <span className="sr-only">Loading</span>
        </div>
      }
    >
      <Download />
    </Suspense>
  );
}
