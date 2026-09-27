"use client";

import { Suspense, useEffect } from "react";
import { usePathname } from "next/navigation";
import { startProductAnalytics, watchMeasurement } from "@/lib/posthog";

/**
 * Starts PostHog, once, for a reader who is being measured.
 *
 * Mounted by the root layout beside <PageViews />, and for the same reason it
 * is: a layout survives client side navigation and a page does not, so a
 * component mounted per page would start on the first route and then be torn
 * down and rebuilt on every subsequent one.
 *
 * A route effect resumes recording only after public content replaces the
 * preview DOM. The synchronous History API boundary stops it before entering
 * preview, and the library still counts public route changes on its own.
 *
 * IT RENDERS NOTHING AND IT LOADS NOTHING UNTIL THE GATE SAYS SO. The import of
 * posthog-js is dynamic and lives behind the check in lib/posthog.ts, so a
 * reader who has opted out, or whose browser sends Global Privacy Control, does
 * not fetch the library at all. There is no recorder to stop because there is
 * no recorder.
 */
function ProductAnalyticsBoundary(): null {
  const pathname = usePathname();
  useEffect(() => {
    // Subscribed BEFORE the start, so that the switch on the privacy page works
    // even in the case where the gate refused and nothing started. A reader who
    // arrives opted out, reads the page and then turns measurement on has
    // changed the answer, and nothing would be listening if this ran second.
    return watchMeasurement();
  }, []);
  useEffect(() => { void startProductAnalytics(); }, [pathname]);
  return null;
}

export function ProductAnalytics() {
  return <Suspense fallback={null}><ProductAnalyticsBoundary /></Suspense>;
}
