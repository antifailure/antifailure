"use client";

import { Suspense } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { CardSkeleton } from "@/components/ui";
import { AdminPage } from "@/components/admin/primitives";
import { WatchWall } from "@/components/watch/WatchWall";
import { replaySource, type LiveSource } from "@/lib/live-client";

/**
 * Watch a run happen: every agent in it, live, side by side.
 *
 * WHERE THE FRAMES COME FROM is the whole design. The events and the frames
 * stream from the runner edge through a signed relay, never from this portal's
 * store, which by design holds counts, verdicts and a reference and never a
 * body. So this page takes a `source` in its address: an https relay URL for a
 * live run, or the replay it defaults to, which plays a recorded cast that
 * ships with the console. Either way a frame never comes from a control plane
 * query.
 *
 * The signed relay that a running preview environment exposes is the next piece
 * of infrastructure; until it is wired, a live run reaches this page by handing
 * it that relay URL, and the recorded replay is what the page shows by default.
 */
const BOUNDARY_NOTE =
  "The video and the frames stream from the runner edge. This portal holds a reference and a hash, never the bytes: no body, no log, no screenshot is served from here.";

export default function WatchRunPage() {
  return (
    <Suspense
      fallback={
        <AdminPage title="Watch" lede="A run, live.">
          <CardSkeleton count={2} />
        </AdminPage>
      }
    >
      <WatchView />
    </Suspense>
  );
}

function sourceFrom(params: URLSearchParams): LiveSource {
  const source = params.get("source");
  // An https relay URL streams a live run. Anything else, including the demo
  // flag and the absent case, plays the bundled replay so the page is never
  // blank and the design can be seen without a live runner attached.
  if (source && /^https?:\/\//.test(source)) {
    return { kind: "sse", url: source };
  }
  return replaySource();
}

function WatchView() {
  const params = useSearchParams();
  const id = params.get("id");
  const source = sourceFrom(params);
  const live = source.kind === "sse";

  return (
    <AdminPage
      title="Watch"
      lede={
        live
          ? "A run, live, streaming from the runner edge."
          : "A recorded run, replayed. Hand this page a signed relay URL to watch a live one."
      }
      actions={
        id ? (
          <Link
            href={`/admin/product/runs/detail?kind=agent&id=${encodeURIComponent(id)}`}
            className="text-[13px] text-muted underline underline-offset-2 hover:text-ink"
          >
            Run detail
          </Link>
        ) : null
      }
    >
      <WatchWall source={source} boundaryNote={BOUNDARY_NOTE} />
    </AdminPage>
  );
}
