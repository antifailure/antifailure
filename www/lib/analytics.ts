/**
 * The site beacon, as a React hook.
 *
 * Everything this file used to hold now lives in lib/beacon.ts, which imports no
 * framework and can therefore be loaded by a plain test runner. The move was not
 * tidying: next/navigation does not resolve outside the bundler, so while the
 * queue, the session rules and the opt out lived beside this hook, not one of
 * them could be tested at all.
 *
 * Everything is re-exported, so nothing that imported from here had to change.
 */

import { usePathname } from "next/navigation";
import { useEffect, useRef } from "react";
import { pageViewed, routeIdFor } from "./beacon";

export {
  SESSION_IDLE_TIMEOUT_MS,
  SESSION_MAX_LENGTH_MS,
  campaignFor,
  ctaEngaged,
  measurementStatus,
  pageViewed,
  retryDelay,
  routeIdFor,
  sessionEnded,
  setMeasurement,
  sourceFor,
  waitlistSubmitted,
} from "./beacon";
export type {
  Cta,
  MeasurementOff,
  MeasurementStatus,
  SessionEnd,
  SiteRoute,
  VisitSource,
} from "./beacon";

/**
 * Fires one page view per route the reader lands on.
 *
 * Mounted once, in the layout, so it survives client-side navigation between
 * pages. The ref is what stops React's development double-render, and a route
 * the reader has come back to, from being counted twice: usePathname changes on
 * navigation and the effect runs again, which is exactly what is wanted, and it
 * also runs again on a re-render that changed nothing, which is not.
 */
export function usePageViews(): void {
  const pathname = usePathname();
  const last = useRef<string | null>(null);

  useEffect(() => {
    if (last.current === pathname) return;
    last.current = pathname;
    pageViewed(routeIdFor(pathname));
  }, [pathname]);
}
