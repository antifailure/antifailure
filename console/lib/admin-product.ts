"use client";

/**
 * The Product lane's client: twins, runs, branches, safe state and flags.
 *
 * WHY THIS IS A THIRD FILE AND NOT ADDITIONS TO admin.ts. That module is the
 * portal's own client: who the operator is, what they may do, and the three
 * screens the foundation built. Six lanes appending their hooks to it would put
 * six writers in one file for no gain, since none of them shares a type with
 * another. What is REUSED is the transport, deliberately and entirely: query,
 * adminMutate, useApi and usePages all come from admin.ts and api.ts unchanged,
 * because a second fetch wrapper is a second place for the error shape, the
 * credentials mode and the CSRF header to drift.
 *
 * EVERY LIST HERE IS PAGED WITH usePages, without exception. Three screens in
 * this console already read one page of a cursored route and told an operator
 * the list was complete when it was showing a third of it. Every route below
 * returns a nextCursor, so every hook below returns pages and every screen over
 * it renders `More` in both states.
 */

import { query, usePages, useApi } from "@/lib/api";
import type { RunKind, RunStanding } from "@/lib/productshapes";
import { adminMutate, type AdminPage } from "@/lib/admin";

/* -------------------------------------------------------------------------
 * Twins
 * ---------------------------------------------------------------------- */

/** Re-exported so a page reads one module for the row and its meaning. The
 *  declarations live in productshapes.ts, which holds no import that reaches
 *  React and is therefore the half of this lane the unit tests can run. */
export type { RunKind, RunStanding };

export interface Twin {
  id: string;
  orgId: string;
  orgSlug: string;
  envId: string;
  repository: string;
  branch: string;
  pullRequest: number | null;
  state: string;
  previewUrl: string | null;
  runtime: string | null;
  goldenVersion: string | null;
  /** Null when the twin names no golden version, or names one this
   *  installation has no row for. Both differ from `false`, which means a
   *  version exists and was never verified. */
  goldenVerified: boolean | null;
  createdAt: string;
  updatedAt: string;
  expiresAt: string | null;
  tornDownAt: string | null;
  /** Past the lifetime it was created with and still running. This is the row
   *  that is costing somebody money right now. */
  overdue: boolean;
  teardownPending: boolean;
  runs: number;
}

export interface TwinRunSummary {
  id: string;
  kind: string;
  state: string;
  standing: RunStanding;
  verdicts: number;
  failing: number;
  startedAt: string | null;
  finishedAt: string | null;
  createdAt: string;
}

export interface TwinWorkloadSummary {
  id: string;
  workload: string;
  state: string;
  standing: RunStanding;
  verdict: string | null;
  failureCode: string | null;
  requestedAt: string;
  finishedAt: string | null;
}

export interface TwinTeardown {
  id: string;
  state: string;
  reason: string;
  attempts: number;
  lastError: string | null;
  requestedAt: string;
  acknowledgedAt: string | null;
}

export interface TwinDetail extends Omit<Twin, "overdue" | "teardownPending" | "runs"> {
  orgName: string;
  defaultBranch: string | null;
  createdBy: string | null;
  lastSequence: number;
  golden: {
    version: string;
    verified: boolean;
    sourceDigest: string | null;
    rulesDigest: string | null;
    sizeBytes: number | null;
    createdAt: string;
  } | null;
  runs: TwinRunSummary[];
  workloadRuns: TwinWorkloadSummary[];
  teardowns: TwinTeardown[];
}

export type TwinScope = "live" | "overdue" | "all";

export function useTwins(options: { scope: TwinScope; search: string; orgId?: string | null }) {
  const { scope, search, orgId } = options;
  return usePages<Twin>(
    async (cursor) => {
      const p = await query<AdminPage<Twin>>("admin.product.twins.list", {
        limit: 50,
        scope,
        ...(search ? { query: search } : {}),
        ...(orgId ? { orgId } : {}),
        ...(cursor === null ? {} : { cursor }),
      });
      return { rows: p.rows, next: p.nextCursor };
    },
    [scope, search, orgId ?? ""],
  );
}

export function useTwin(id: string) {
  return useApi<TwinDetail>(
    () => query("admin.product.twins.get", { id }),
    [id],
  );
}

/* -------------------------------------------------------------------------
 * Branches
 * ---------------------------------------------------------------------- */

export interface BranchRow {
  orgId: string;
  orgSlug: string;
  repositoryId: string;
  repository: string;
  branch: string;
  twins: number;
  live: number;
  overdue: number;
  pullRequest: number | null;
  pullRequestState: string | null;
  pullRequestTitle: string | null;
  pullRequestDraft: boolean | null;
  pullRequestFromFork: boolean | null;
  pullRequestClosedAt: string | null;
  /** Live twins on a branch whose pull request is closed or merged. Nothing has
   *  failed; the environment is simply still running for a change that landed. */
  orphaned: boolean;
  lastActivity: string;
  latestState: string;
}

export type BranchScope = "all" | "live" | "orphaned";

export function useBranches(options: { scope: BranchScope; search: string }) {
  const { scope, search } = options;
  return usePages<BranchRow>(
    async (cursor) => {
      const p = await query<AdminPage<BranchRow>>("admin.product.twins.branches", {
        limit: 50,
        scope,
        ...(search ? { query: search } : {}),
        ...(cursor === null ? {} : { cursor }),
      });
      return { rows: p.rows, next: p.nextCursor };
    },
    [scope, search],
  );
}

/* -------------------------------------------------------------------------
 * Runs
 * ---------------------------------------------------------------------- */

export interface RunRow {
  kind: RunKind;
  id: string;
  orgId: string;
  orgSlug: string;
  repository: string | null;
  ref: string | null;
  pullRequest: number | null;
  envId: string | null;
  state: string;
  standing: RunStanding;
  verdict: string | null;
  failure: string | null;
  at: string;
  startedAt: string | null;
  finishedAt: string | null;
  durationMs: number | null;
}

export interface AgentVerdict {
  id: string;
  workflow: string;
  persona: string | null;
  value: string;
  summary: string | null;
  steps: number;
  durationMs: number | null;
  reproduction: unknown;
  createdAt: string;
}

export interface RunArtifact {
  id: string;
  kind: string;
  step: number | null;
  contentType: string | null;
  sizeBytes: number | null;
  sha256: string | null;
  /** False once retention removed the bytes. The row stays so the timeline can
   *  say "not retained" rather than showing a gap that reads as a bug. */
  retained: boolean;
  createdAt: string;
}

export interface AgentRunDetail {
  kind: "agent";
  id: string;
  orgId: string;
  orgSlug: string;
  environmentId: string;
  envId: string;
  repository: string;
  branch: string;
  pullRequest: number | null;
  previewUrl: string | null;
  runKind: string;
  state: string;
  standing: RunStanding;
  startedAt: string | null;
  finishedAt: string | null;
  createdAt: string;
  durationMs: number | null;
  lastSequence: number;
  verdicts: AgentVerdict[];
  artifacts: RunArtifact[];
}

interface LoadRunResult extends Record<string, unknown> {
  kind: string;
  recordedAt: string;
}

export interface LoadRunDetail {
  kind: "load";
  id: string;
  orgId: string;
  orgSlug: string;
  environmentId: string | null;
  envId: string | null;
  repository: string;
  gitRef: string;
  workflowFile: string | null;
  workload: string;
  workloadSlug: string;
  workloadKind: string;
  workloadVersion: number | null;
  state: string;
  standing: RunStanding;
  verdict: string | null;
  failureCode: string | null;
  detail: string | null;
  /** The command that reproduces this run, as the engine reported it. Null
   *  until one reports, and the page says so rather than assembling a command
   *  that drifts from what actually ran. */
  reproduceCommand: string | null;
  manifestDigest: string | null;
  attempt: number;
  requestedAt: string;
  dispatchedAt: string | null;
  acceptedAt: string | null;
  startedAt: string | null;
  finishedAt: string | null;
  deadlineAt: string;
  durationMs: number | null;
  leaseHolder: string | null;
  leaseExpiresAt: string | null;
  leaseLostAt: string | null;
  leaseTakeovers: number;
  cancelRequestedAt: string | null;
  cancelReason: string | null;
  cancelledAt: string | null;
  retryOf: string | null;
  supersededBy: string | null;
  result: LoadRunResult | null;
}

export interface CheckRunDetail {
  kind: "check";
  id: string;
  orgId: string;
  orgSlug: string;
  repository: string;
  pullRequest: number;
  title: string | null;
  headRef: string;
  baseRef: string;
  pullRequestState: string;
  draft: boolean;
  /** A head branch living in another repository, which is the whole of what
   *  makes a pull request untrusted here. */
  fromFork: boolean;
  headRepository: string;
  approvedSha: string | null;
  approvedBy: string | null;
  approvedAt: string | null;
  headSha: string;
  attempt: number;
  state: string;
  standing: RunStanding;
  detail: string | null;
  /** Null while the installation does not hold `checks: write`, which is a
   *  state to serve rather than to crash in. */
  checkRunId: string | null;
  workflowRunId: string | null;
  reportedBy: string | null;
  envId: string | null;
  verdict: unknown;
  supersededBy: string | null;
  queuedAt: string;
  startedAt: string | null;
  finishedAt: string | null;
  deadlineAt: string;
  durationMs: number | null;
}

export type RunDetail = AgentRunDetail | LoadRunDetail | CheckRunDetail;

export function useRuns(options: {
  kind: RunKind;
  search: string;
  failedOnly: boolean;
  /** One twin, reached from the twin page. Stricter than orgId and applied
   *  alongside it, because an environment belongs to exactly one tenant. */
  environmentId?: string | null;
  /** One organization, reached from a twin when the question widens from
   *  "why did this one fail" to "is the whole tenant failing". */
  orgId?: string | null;
}) {
  const { kind, search, failedOnly, environmentId, orgId } = options;
  return usePages<RunRow>(
    async (cursor) => {
      const p = await query<AdminPage<RunRow>>("admin.product.runs.list", {
        limit: 50,
        kind,
        failedOnly,
        ...(search ? { query: search } : {}),
        ...(environmentId ? { environmentId } : {}),
        ...(orgId ? { orgId } : {}),
        ...(cursor === null ? {} : { cursor }),
      });
      return { rows: p.rows, next: p.nextCursor };
    },
    [kind, search, String(failedOnly), environmentId ?? "", orgId ?? ""],
  );
}

export function useRun(kind: RunKind, id: string) {
  return useApi<RunDetail>(() => query("admin.product.runs.get", { kind, id }), [kind, id]);
}

/* -------------------------------------------------------------------------
 * Safe state
 * ---------------------------------------------------------------------- */

export interface GoldenVersion {
  id: string;
  orgId: string;
  orgSlug: string;
  repository: string;
  version: string;
  sourceDigest: string | null;
  rulesDigest: string | null;
  verified: boolean;
  sizeBytes: number | null;
  createdAt: string;
  /** Live twins built from this version. An unverified version with twins on
   *  it is the row that matters; one with none is history. */
  twins: number;
}

export interface MaskingRule {
  id: string;
  orgId: string;
  orgSlug: string;
  repository: string;
  table: string;
  column: string;
  transform: string;
  link: string | null;
  reason: string | null;
  /** False means a scan suggested it and no human has agreed, which means the
   *  column is not being transformed. */
  confirmed: boolean;
  createdAt: string;
  updatedAt: string;
}

export function useGoldenVersions(options: {
  scope: "all" | "verified" | "unverified";
  search: string;
}) {
  const { scope, search } = options;
  return usePages<GoldenVersion>(
    async (cursor) => {
      const p = await query<AdminPage<GoldenVersion>>("admin.product.data.goldens", {
        limit: 50,
        scope,
        ...(search ? { query: search } : {}),
        ...(cursor === null ? {} : { cursor }),
      });
      return { rows: p.rows, next: p.nextCursor };
    },
    [scope, search],
  );
}

export function useMaskingRules(options: {
  scope: "all" | "confirmed" | "unconfirmed";
  search: string;
}) {
  const { scope, search } = options;
  return usePages<MaskingRule>(
    async (cursor) => {
      const p = await query<AdminPage<MaskingRule>>("admin.product.data.masking", {
        limit: 50,
        scope,
        ...(search ? { query: search } : {}),
        ...(cursor === null ? {} : { cursor }),
      });
      return { rows: p.rows, next: p.nextCursor };
    },
    [scope, search],
  );
}

/* -------------------------------------------------------------------------
 * Flags and entitlement overrides
 * ---------------------------------------------------------------------- */

/**
 * These read the MONEY lane's routes, unchanged.
 *
 * `admin.flags.*` and `admin.entitlements.*` are that lane's prefixes and its
 * routes are complete, so the Experiments and Feature Flags page consumes them
 * rather than growing a second copy under `admin.product`. Adding a route under
 * somebody else's prefix is the kind of thing a review misses; this comment and
 * admin-product.test.ts are what stop it.
 */

export interface FeatureFlag {
  key: string;
  description: string;
  state: "off" | "on" | "targeted";
  rollout_percent: number;
  internal_only: boolean;
  killed_at: string | null;
  killed_by_label: string | null;
  killed_reason: string | null;
  updated_at: string | null;
  updated_by_label: string | null;
  /** Where in the source the flag is actually consulted, from the KNOWN_FLAGS
   *  map. Null when nothing reads it. */
  checkedAt: string | null;
  /** Whether anything reads this flag at all. A flag with no call site is a
   *  switch that looks like a control and is not one, which is the worst thing
   *  to discover halfway through an incident. */
  known: boolean;
}

export interface FlagTarget {
  id: string;
  flag_key: string;
  kind: "user" | "organization" | "project" | "repository" | "plan" | "environment";
  value: string;
  allow: boolean;
  org_id: string | null;
  reason: string | null;
  created_by_label: string | null;
  created_at: string;
}

interface FlagsAnswer {
  flags: FeatureFlag[];
  targets: FlagTarget[];
}

export function useFlags() {
  return useApi<FlagsAnswer>(() => query("admin.flags.list"), []);
}

/** The grant that moved a limit, as the server resolves it. Mirrors OverrideRef
 *  in src/entitlements.ts, with its two dates serialized. */
interface EntitlementOverride {
  id: string;
  scope: "global" | "organization" | "project" | "user";
  reason: string;
  ticket: string | null;
  grantedBy: string;
  grantedAt: string;
  /** Null is forever. Everything else stops applying on its own, which is what
   *  makes a grant a loan rather than a change of plan. */
  expiresAt: string | null;
}

export interface OrgEntitlement {
  key: string;
  value: number | boolean;
  /** What the plan alone would have said, so the screen can show both and a
   *  one-off grant is never mistaken for normal behaviour. */
  planValue: number | boolean;
  source: "plan" | "global" | "organization" | "project" | "user";
  unit: string | null;
  description: string;
  /** False when the catalog records that nothing enforces this limit yet. A
   *  number nothing checks is not a limit. */
  enforced: boolean;
  notEnforcedBecause: string | null;
  override: EntitlementOverride | null;
}

interface OrgEntitlements {
  org: { id: string; slug: string; plan: string };
  entitlements: OrgEntitlement[];
}

/** One organization's entitlements, with the grant that moved each one. Null
 *  orgId means nobody has picked an organization yet, and the hook does not
 *  fire: a route that needs an id is not a route to call with an empty one. */
export function useOrgEntitlements(orgId: string | null) {
  return useApi<OrgEntitlements | null>(
    async () =>
      orgId ? query<OrgEntitlements>("admin.entitlements.forOrganization", { orgId }) : null,
    [orgId ?? ""],
  );
}

export async function setFlag(input: {
  key: string;
  description: string;
  state: "off" | "on" | "targeted";
  rolloutPercent: number;
  internalOnly: boolean;
}) {
  return adminMutate<{ key: string }>("admin.flags.set", input);
}

/**
 * The kill switch, and it is a separate call from `setFlag` on purpose.
 *
 * Turning a flag off during an incident and turning it off because a rollout
 * ended are the same UPDATE and completely different events. The route records
 * them differently, at different severities, and the one worth finding six
 * months later is the first. A single "off" button here would have collapsed
 * that distinction back again on the way to the server.
 */
export async function killFlag(key: string, reason: string) {
  return adminMutate<{ killed: boolean }>("admin.flags.kill", { key, reason });
}

export async function targetFlag(input: {
  flagKey: string;
  kind: FlagTarget["kind"];
  value: string;
  allow: boolean;
  orgId: string | null;
  reason: string;
}) {
  return adminMutate<{ id: string }>("admin.flags.target", input);
}

export async function revokeOverride(id: string, reason: string) {
  return adminMutate<{ revoked: boolean }>("admin.entitlements.revoke", { id, reason });
}
