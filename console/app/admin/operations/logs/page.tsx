"use client";

import { PlannedSection } from "@/components/admin/primitives";

/**
 * Logs & Error Explorer
 *
 * Not written yet. The route exists from the first commit because a navigation
 * entry that 404s teaches the reader to distrust the rail, and they cannot tell
 * a section that is coming from one that is broken.
 *
 * TO BUILD THIS SECTION: replace the body below with the real page, and add its
 * routes to web/apps/api/src/admin/operations.ts. Nothing else in the portal
 * has to change. See console/app/admin/README.md.
 */
export default function OperationsLogsPage() {
  return <PlannedSection href="/admin/operations/logs" />;
}
