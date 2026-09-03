"use client";

/**
 * The Developer Platform lane's client.
 *
 * A third module beside admin.ts rather than additions to it, for the reason
 * admin.ts is a second module beside api.ts: the transport is REUSED and
 * nothing else is. Every hook here goes through `query`, `usePages` and
 * `adminMutate`, so there is no second fetch wrapper for the error shape, the
 * credentials mode or the CSRF header to drift in.
 *
 * EVERY PAGED LIST USES usePages, without exception. Three screens in this
 * console already read one page of a cursor paged route and told the reader the
 * list was complete when it was showing a third of it. On this lane that would
 * be an operator concluding a credential does not exist because it sorted past
 * the cut, which is a confident wrong answer about a security surface.
 */

import { query, useApi, usePages, type ApiError } from "@/lib/api";
import { adminMutate, type AdminPage } from "@/lib/admin";

export type { ApiError };

/* -------------------------------------------------------------------------
 * Repositories and pull requests
 * ---------------------------------------------------------------------- */

export interface AdminRepository {
  id: string;
  orgId: string;
  orgSlug: string;
  fullName: string;
  defaultBranch: string;
  private: boolean;
  archived: boolean;
  createdAt: string;
  openPullRequests: number;
  /** Null when no pull request has ever been recorded for it, which is not the
   *  same as zero open ones and is rendered differently. */
  lastPullRequestAt: string | null;
}

export interface AdminRepositoryDetail {
  id: string;
  orgId: string;
  orgSlug: string;
  orgName: string;
  fullName: string;
  defaultBranch: string;
  private: boolean;
  githubId: string | null;
  archived: boolean;
  createdAt: string;
  installations: { accountLogin: string; accountType: string; suspended: boolean }[];
}

export interface AdminGenerationSummary {
  state: string;
  detail: string | null;
  attempt: number;
  headSha: string | null;
  verdict: unknown;
  updatedAt: string | null;
}

export interface AdminPullRequest {
  id: string;
  number: number;
  title: string | null;
  state: string;
  draft: boolean;
  fromFork: boolean;
  headRef: string;
  baseRef: string;
  headRepository: string;
  headSha: string;
  approvedSha: string | null;
  approvedBy: string | null;
  approvedAt: string | null;
  /** Whether the approval is for the commit that is currently at the head. A
   *  fork approval that outlived a push is an approval of code nobody looked
   *  at, so the two are reported apart rather than as one boolean. */
  approvalCoversHead: boolean;
  openedAt: string | null;
  updatedAt: string;
  latestGeneration: AdminGenerationSummary | null;
}

export interface AdminGeneration {
  id: string;
  headSha: string;
  attempt: number;
  state: string;
  detail: string | null;
  checkRunId: string | null;
  workflowRunId: string | null;
  reportedBy: string | null;
  envId: string | null;
  verdict: unknown;
  supersededBy: string | null;
  queuedAt: string | null;
  startedAt: string | null;
  finishedAt: string | null;
  deadlineAt: string | null;
  updatedAt: string;
}

export function useRepositories(search: string) {
  return usePages<AdminRepository>(
    async (cursor) => {
      const page = await query<AdminPage<AdminRepository>>("admin.platform.repositories.list", {
        limit: 50,
        ...(search ? { query: search } : {}),
        ...(cursor === null ? {} : { cursor }),
      });
      return { rows: page.rows, next: page.nextCursor };
    },
    [search],
  );
}

export function usePullRequests(repositoryId: string, state: string) {
  return usePages<AdminPullRequest>(
    async (cursor) => {
      const page = await query<AdminPage<AdminPullRequest>>(
        "admin.platform.repositories.pullRequests",
        {
          repositoryId,
          limit: 50,
          ...(state ? { state } : {}),
          ...(cursor === null ? {} : { cursor }),
        },
      );
      return { rows: page.rows, next: page.nextCursor };
    },
    [repositoryId, state],
  );
}

export function useRepository(repositoryId: string) {
  return useApi<AdminRepositoryDetail>(
    () => query("admin.platform.repositories.get", { repositoryId }),
    [repositoryId],
  );
}

/**
 * Every generation for one pull request.
 *
 * useApi rather than usePages because the route is bounded at fifty rather than
 * paged, and it is bounded because the schema carries a UNIQUE on
 * (pull_request_id, head_sha): fifty rows means fifty distinct heads. A `More`
 * footer here would offer a page that does not exist.
 *
 * The empty string is a real argument. A hook cannot be called conditionally,
 * so the panel that is closed asks for nothing and the route is never called,
 * which is what the guard in the caller is for.
 */
export function useGenerations(pullRequestId: string) {
  return useApi<AdminGeneration[]>(
    () =>
      pullRequestId === ""
        ? Promise.resolve([])
        : query("admin.platform.repositories.generations", { pullRequestId }),
    [pullRequestId],
  );
}

/* -------------------------------------------------------------------------
 * Credentials
 * ---------------------------------------------------------------------- */

/** live, expired or revoked, decided by the server so this file and the route
 *  cannot disagree about which of expired and revoked wins. */
export type { CredentialStanding } from "@/lib/platform-format";
import type { CredentialStanding } from "@/lib/platform-format";

export interface AdminCredential {
  id: string;
  orgId: string;
  orgSlug: string;
  name: string;
  /** The first twelve characters, which is what the customer's own console
   *  shows them. There is no field on this type that holds the value, because
   *  only the hash is stored and no route in this product can return one. */
  prefix: string;
  kind: string;
  scopes: string[];
  createdAt: string;
  lastUsedAt: string | null;
  expiresAt: string | null;
  revokedAt: string | null;
  expired: boolean;
  standing: CredentialStanding;
  createdBy: string | null;
  /** Who the credential acts AS, which is null for a machine token on purpose:
   *  a machine is not a person, and putting its actions in somebody's audit
   *  trail is what the null prevents. */
  actsAs: string | null;
  bindingRepository: string | null;
}

export interface AdminBinding {
  id: string;
  orgId: string;
  orgSlug: string;
  repository: string;
  createdAt: string;
  lastUsedAt: string | null;
  revokedAt: string | null;
  createdBy: string | null;
  liveTokens: number;
}

export function useCredentials(search: string, kind: string, liveOnly: boolean) {
  return usePages<AdminCredential>(
    async (cursor) => {
      const page = await query<AdminPage<AdminCredential>>("admin.platform.keys.list", {
        limit: 50,
        liveOnly,
        ...(search ? { query: search } : {}),
        ...(kind ? { kind } : {}),
        ...(cursor === null ? {} : { cursor }),
      });
      return { rows: page.rows, next: page.nextCursor };
    },
    [search, kind, liveOnly],
  );
}

export function useBindings(search: string) {
  return usePages<AdminBinding>(
    async (cursor) => {
      const page = await query<AdminPage<AdminBinding>>("admin.platform.keys.bindings", {
        limit: 50,
        ...(search ? { query: search } : {}),
        ...(cursor === null ? {} : { cursor }),
      });
      return { rows: page.rows, next: page.nextCursor };
    },
    [search],
  );
}

export interface RevokeResult {
  revoked: boolean;
  alreadyRevoked: boolean;
  /** Said by the route rather than written here. Two sentences that mean the
   *  same thing today are two sentences that disagree after somebody edits
   *  one, and this is the sentence an operator repeats to the customer. */
  effect: string;
}

export async function revokeCredential(tokenId: string, reason: string) {
  return adminMutate<RevokeResult & { prefix: string }>("admin.platform.keys.revoke", {
    tokenId,
    reason,
  });
}

export async function revokeBinding(bindingId: string, reason: string) {
  return adminMutate<RevokeResult & { repository: string; tokensRevoked: number }>(
    "admin.platform.keys.revokeBinding",
    { bindingId, reason },
  );
}

/* -------------------------------------------------------------------------
 * Integrations
 * ---------------------------------------------------------------------- */

export interface AdminInstallation {
  id: string;
  orgId: string;
  orgSlug: string;
  installationId: string;
  accountLogin: string;
  accountType: string;
  suspended: boolean;
  createdAt: string;
  repositories: number;
  lastDeliveryAt: string | null;
}

export interface AdminDelivery {
  id: string;
  /** Null for a delivery about an account this installation has never seen.
   *  Kept rather than filtered out: those are the rows worth reading when
   *  somebody reports that their events go nowhere. */
  orgSlug: string | null;
  account: string | null;
  event: string;
  action: string | null;
  receivedAt: string;
  handledAt: string | null;
  outcome: string | null;
}

export function useInstallations(search: string) {
  return usePages<AdminInstallation>(
    async (cursor) => {
      const page = await query<AdminPage<AdminInstallation>>(
        "admin.platform.integrations.installations",
        {
          limit: 50,
          ...(search ? { query: search } : {}),
          ...(cursor === null ? {} : { cursor }),
        },
      );
      return { rows: page.rows, next: page.nextCursor };
    },
    [search],
  );
}

export function useDeliveries(source: string, unhandledOnly: boolean, search: string) {
  return usePages<AdminDelivery>(
    async (cursor) => {
      const page = await query<AdminPage<AdminDelivery>>("admin.platform.integrations.deliveries", {
        source,
        unhandledOnly,
        limit: 50,
        ...(search ? { query: search } : {}),
        ...(cursor === null ? {} : { cursor }),
      });
      return { rows: page.rows, next: page.nextCursor };
    },
    [source, unhandledOnly, search],
  );
}

/* -------------------------------------------------------------------------
 * MCP
 * ---------------------------------------------------------------------- */

export interface McpSurface {
  /** Whether this control plane records anything about MCP at all. Sent by the
   *  server rather than assumed here, so the day a write path exists the page
   *  changes because the server changed. */
  recordsAnything: boolean;
  why: string;
  tools: { name: string; does: string; refuses: string; servedBy: string }[];
  registeredIn: string;
  unknownFieldRefusal: string;
  command: string;
  documentation: string;
}

export function useMcpSurface() {
  return useApi<McpSurface>(() => query("admin.platform.mcp.surface"), []);
}

/* -------------------------------------------------------------------------
 * Re-exported so a page imports one module
 * ---------------------------------------------------------------------- */

/**
 * The pure helpers live in lib/platform-format.ts and are re-exported here.
 *
 * Split for one reason and it is not tidiness: console unit tests are literally
 * `node --test lib/*.test.ts`, so a test can only reach a module that loads
 * outside a browser. This file imports React hooks; that one imports nothing at
 * all, which is what makes the three decisions in it testable. Everything a
 * page needs still arrives from one import.
 */
export {
  standingTone,
  approvalWording,
  shortSha,
  deliveryStanding,
} from "@/lib/platform-format";
