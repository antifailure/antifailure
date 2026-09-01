"use client";

import { Suspense, useState } from "react";
import { useSearchParams } from "next/navigation";
import { LogoMark } from "@/components/icons";
import { Button, Lede, Standalone } from "@/components/ui";

/**
 * Downloading the export of an organization that has been deleted.
 *
 * There is no session here and there cannot be: the organization the session
 * belonged to no longer exists, so there is no membership left to authorise
 * anything. The token in the link is the whole authorisation, which is why the
 * link is handed over once, at the moment the deletion is requested, and why
 * this page never asks anybody to sign in.
 *
 * It does not fetch the document itself. The API answers with a
 * Content-Disposition header, so following the link IS the download, and
 * pulling several megabytes of JSON into a browser tab to hand it straight back
 * to the same browser would only add a way to fail.
 */
function Download() {
  const params = useSearchParams();
  const token = params.get("token") ?? "";
  const [tried, setTried] = useState(false);

  if (!token) {
    return (
      <Standalone title="That download link is not valid" alert>
        <Lede>
          This link is missing its token. Use the link you were given when you asked for the
          deletion; it is the only copy.
        </Lede>
      </Standalone>
    );
  }

  const href = `/exports/deletion?token=${encodeURIComponent(token)}`;

  return (
    <Standalone title="Your export" width={460}>
      <Lede>
        A complete copy of the organization, taken before anything was deleted. It is one JSON file:
        people, repositories, masking rules, egress policy, environments, runs, verdicts, billing
        history and the audit log.
      </Lede>

      <div className="mt-7">
        <a
          href={href}
          download
          onClick={() => setTried(true)}
          className="inline-flex h-11 w-full items-center justify-center gap-2.5 rounded-md bg-ink px-4 text-[14px] font-medium text-white transition-colors hover:bg-[#2b2b2b]"
        >
          Download the export
        </a>
      </div>

      {tried ? (
        <p role="status" className="mt-3 text-[12.5px] leading-5 text-dim">
          If nothing downloaded, the link has expired or the copy has been destroyed. Downloads are
          only kept for a limited time after a deletion.
        </p>
      ) : (
        <p className="mt-3 text-[12.5px] leading-5 text-dim">
          Keep the file somewhere safe. This link stops working when the retention window ends, and
          there is no way to produce another copy once the organization is gone.
        </p>
      )}

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
