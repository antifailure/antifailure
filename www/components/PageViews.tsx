"use client";

import { Suspense } from "react";
import { usePageViews } from "@/lib/analytics";

function Views(): null {
  usePageViews();
  return null;
}

/**
 * Counts one page view per route the reader lands on.
 *
 * Rendered by the root layout rather than by each page, so it survives
 * client-side navigation: a layout persists across route changes and a page
 * does not, so mounting this per page would count the first view of each route
 * and then stop counting when the reader moved on. That is the same mistake as
 * mounting the session provider per page, and the console layout carries a
 * comment saying so for the same reason.
 *
 * Wrapped in Suspense because usePathname opts the tree into client-side
 * rendering, and a static export refuses to build a page whose whole body has
 * been opted in without a boundary. This renders nothing either way, so the
 * fallback is nothing.
 *
 * Deliberately not a script tag and not a third party. There is no vendor here,
 * no cookie, and nothing loaded from another origin: see www/lib/analytics.ts
 * for exactly what is sent.
 */
export function PageViews(): React.ReactElement {
  return (
    <Suspense fallback={null}>
      <Views />
    </Suspense>
  );
}
