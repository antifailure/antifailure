"use client";

/**
 * The Administration lane's client.
 *
 * A third file rather than more of admin.ts, for the reason admin.ts gives for
 * not being more of api.ts: the transport is shared and nothing else is. What
 * is here is one lane's route shapes and its hooks, so that six lanes editing
 * six files cannot collide in one, and so that deleting a section is deleting a
 * file rather than unpicking a module everybody imports.
 *
 * The transport is REUSED and not re-implemented. `query` and `adminMutate`
 * come from api.ts and admin.ts unchanged, because a second fetch wrapper is a
 * second place for the error shape, the credentials mode and the CSRF header to
 * drift, and the header is exactly the thing that was already missing once.
 *
 * EVERY TYPE HERE MIRRORS A REAL RESPONSE. Nothing is optimistic: a field is in
 * one of these interfaces because the route in
 * web/apps/api/src/admin/administration.ts returns it. Where the server can
 * genuinely answer "there is no measurement", the field is nullable here too,
 * and the page renders that as a sentence rather than as a zero.
 */

import { query, useApi } from "@/lib/api";
import { adminMutate, type Operator } from "@/lib/admin";

/* -------------------------------------------------------------------------
 * The overview
 * ---------------------------------------------------------------------- */

export interface SuspendedOrganization {
  slug: string;
  name: string;
  reason: string | null;
  since: string;
}

export interface StuckDeletion {
  id: string;
  slug: string;
  name: string;
  /** Which of the six steps raised. Null when the record predates the column
   *  being written, which is a real row and not a bug. */
  step: string | null;
  failedAt: string;
  requestedAt: string;
  attempts: number;
}

export interface AdminStanding {
  at: string;
  organizations: { total: number; suspended: number };
  suspended: SuspendedOrganization[];
  environments: { live: number };
  stuckDeletions: StuckDeletion[];
}

export interface AdminActivityEntry {
  seq: number;
  actor: string;
  action: string;
  targetType: string;
  targetId: string | null;
  organization: string | null;
  severity: string;
  occurredAt: string;
}

export interface AdminActivity {
  /** How many entries the summary below was actually computed over, and how
   *  many it asked for. The page prints both, because a count of refusals
   *  means nothing without the population it came from. */
  readOver: number;
  requested: number;
  oldest: string | null;
  newest: string | null;
  writes: number;
  refusals: number;
  critical: number;
  high: number;
  recent: AdminActivityEntry[];
}

/*
 * WHY EVERY HOOK HERE TAKES `enabled`.
 *
 * The overview composes four routes behind four different permissions, and a
 * hook cannot be called conditionally. Fetching all four regardless would mean
 * an operator whose role holds three of them reads an error panel for the
 * fourth every time they open the landing page, and a screen that always shows
 * one refusal teaches its reader to ignore refusals.
 *
 * So the flag is passed down to the fetch rather than around it: `false`
 * resolves to null without a request, and the page renders the section only for
 * data it actually asked for. The gate is still the server's; this only decides
 * what to ask.
 */
function useMaybe<T>(enabled: boolean, fetch: () => Promise<T>, deps: unknown[]) {
  return useApi<T | null>(() => (enabled ? fetch() : Promise.resolve(null)), [enabled, ...deps]);
}

/** The installation's standing. Guarded by admin.tenants.read, which every
 *  built in role holds, so this is the call the overview all but always makes. */
export function useAdminStanding(enabled = true) {
  return useMaybe<AdminStanding>(enabled, () => query("admin.administration.standing"), []);
}

export function useAdminActivity(enabled = true) {
  return useMaybe<AdminActivity>(enabled, () => query("admin.administration.activity"), []);
}

/* -------------------------------------------------------------------------
 * Analytics and usage
 * ---------------------------------------------------------------------- */

export type UsageWindow = "24h" | "7d" | "30d";

export interface UsageRow {
  id: string;
  slug: string;
  name: string;
  plan: string;
  suspended: boolean;
  /** Environment-hours inside the selected window. */
  hours: number;
  /** Environment-hours inside the rolling twenty four hours, which is the
   *  window the per-day cap is actually enforced over. Always present, at
   *  every window, so nobody reads a thirty day total against a daily cap. */
  dayHours: number;
  dayCapHours: number;
  overDayCap: boolean;
  environments: number;
  live: number;
}

export interface AdminUsage {
  at: string;
  window: UsageWindow;
  windowHours: number;
  since: string;
  rows: UsageRow[];
}

export interface SpendRow {
  slug: string;
  name: string;
  provider: string;
  /** The budget period, as a plain day. */
  period: string;
  capUsd: number;
  spentUsd: number;
  /** Null when the cap is zero, which the table's own constraint permits. A
   *  percentage of nothing is not a number and rendering it as zero reads as
   *  plenty of room. */
  usedPercent: number | null;
  updatedAt: string;
}

export interface AdminSpend {
  rows: SpendRow[];
}

export function useAdminUsage(window: UsageWindow, enabled = true) {
  return useMaybe<AdminUsage>(
    enabled,
    () => query("admin.administration.usage", { window, limit: 25 }),
    [window],
  );
}

export function useAdminSpend(enabled = true) {
  return useMaybe<AdminSpend>(enabled, () => query("admin.administration.spend", { limit: 50 }), []);
}

/* -------------------------------------------------------------------------
 * System configuration
 * ---------------------------------------------------------------------- */

export interface InstallationCapability {
  name: string;
  ready: boolean;
  /** The environment variable that turns it on. The NAME, never the value:
   *  a name is what somebody needs to fix it and the value is what they need
   *  to leak it. */
  enabledBy: string;
  whenReady: string;
  whenNot: string;
}

export interface InstallationControl {
  name: string;
  title: string;
  effect: string;
  enforcedBy: string;
  engaged: boolean;
  engagedAt: string | null;
  engagedBy: string | null;
  reason: string | null;
}

export interface AdminInstallation {
  at: string;
  productName: string;
  appBaseUrl: string;
  hostedRequiredPlan: string | null;
  capabilities: InstallationCapability[];
  /** Null on a database migrate.ts has never run against, which is reported as
   *  absent rather than as version zero. */
  schema: { version: string; appliedAt: string; applied: number } | null;
  controls: InstallationControl[];
  controlCount: number;
  runtimes: { registered: number; providers: number };
}

export function useAdminInstallation(enabled = true) {
  return useMaybe<AdminInstallation>(enabled, () => query("admin.administration.installation"), []);
}

/* -------------------------------------------------------------------------
 * Admins and permissions
 * ---------------------------------------------------------------------- */

export interface AdminCatalog {
  permissions: { name: string; description: string; roles: string[] }[];
  roles: { name: string; permissions: string[] }[];
}

/** The permission catalog, which is source rather than data: there is no
 *  custom role table, so the matrix a page draws is the one the server
 *  compiles with. Guarded by admin.operators.read. */
/** The operator directory, asked for only when the role holds
 *  admin.operators.read. lib/admin.ts exports an unconditional useOperators for
 *  the page whose whole subject is this list; the overview needs the version
 *  that does not ask, because a landing page that always renders one refusal
 *  teaches its reader to ignore refusals. */
export function useAdminOperators(enabled = true) {
  return useMaybe<Operator[]>(enabled, () => query("admin.operators.list"), []);
}

export function useAdminCatalog(enabled = true) {
  return useMaybe<AdminCatalog>(enabled, () => query("admin.catalog"), []);
}

/*
 * The four operator writes.
 *
 * They have existed in web/apps/api/src/admin/router.ts since 0029 and until
 * now had ZERO CALL SITES anywhere in the console: the operators page was read
 * only and said so. A guarded, audited, database-enforced write path that no
 * screen reaches is a feature that exists and does nothing, so these are the
 * calls that make it real, and the page that uses them is the proof.
 *
 * Every one of them goes through adminMutate rather than its own fetch, so the
 * CSRF header is sent from one place. `setRole` and `suspend` are both refused
 * by the server when the target is the caller, and the root operator is
 * protected by a database trigger rather than by anything here, so the page
 * shows those refusals rather than trying to predict them.
 */

export async function createOperator(input: { email: string; name: string; role: string }) {
  return adminMutate<{ id: string; provisioned: boolean; effect: string }>(
    "admin.operators.create",
    input,
  );
}

export async function setOperatorRole(adminUserId: string, role: string) {
  return adminMutate<{ role: string; changed: boolean }>("admin.operators.setRole", {
    adminUserId,
    role,
  });
}

export async function suspendOperator(adminUserId: string, reason: string) {
  return adminMutate<{ suspended: boolean; effect: string }>("admin.operators.suspend", {
    adminUserId,
    reason,
  });
}

export async function restoreOperator(adminUserId: string) {
  return adminMutate<{ suspended: boolean }>("admin.operators.restore", { adminUserId });
}
