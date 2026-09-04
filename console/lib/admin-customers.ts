"use client";

/**
 * The Customers lane's client, beside lib/admin.ts rather than inside it.
 *
 * Same reason lib/admin-money.ts gives, and the same reason lib/admin.ts gives
 * for not being part of api.ts: the transport is reused and nothing else is.
 * `query`, `usePages`, `useApi`, `adminMutate` and `adminPost` come from there
 * unchanged, so the error shape, the credentials mode and the forgery header
 * have one definition. What lives here is this lane's shapes and its calls, so
 * three sections can be read and changed without touching the file every other
 * lane imports.
 */

import { query, useApi, usePages } from "@/lib/api";
import { adminMutate, adminPost, type AdminPage } from "@/lib/admin";

/* -------------------------------------------------------------------------
 * People
 * ---------------------------------------------------------------------- */

export interface AdminUserRow {
  id: string;
  githubLogin: string;
  email: string;
  name: string | null;
  createdAt: string;
  suspended: boolean;
  suspendedReason: string | null;
  /** How many organizations this account belongs to. Zero is a real answer and
   *  a common one: somebody signed in and never created or joined anything. */
  organizations: number;
}

/**
 * Every account on the installation, paged.
 *
 * usePages rather than useApi, which is a correctness requirement rather than a
 * preference: the route returns fifty rows and a cursor, and a screen that read
 * one page would tell an operator searching for an account that sorted past the
 * cut, confidently, that it does not exist. Three screens in this console
 * already shipped that bug.
 */
export function useAdminUsers(search: string) {
  return usePages<AdminUserRow>(
    async (cursor) => {
      const page = await query<AdminPage<AdminUserRow>>("admin.users.list", {
        limit: 50,
        ...(search ? { query: search } : {}),
        ...(cursor === null ? {} : { cursor }),
      });
      return { rows: page.rows, next: page.nextCursor };
    },
    [search],
  );
}

export interface AdminSessionRow {
  id: string;
  orgId: string | null;
  orgSlug: string | null;
  createdAt: string;
  lastSeenAt: string;
  expiresAt: string;
  revoked: boolean;
  ip: string | null;
  userAgent: string | null;
}

/** Every session one account holds. Not paged on the server, so not paged
 *  here: the route returns at most a hundred and says so. */
export function useUserSessions(userId: string | null) {
  return useApi<AdminSessionRow[]>(
    () => (userId ? query<AdminSessionRow[]>("admin.sessions.list", { userId }) : Promise.resolve([])),
    [userId],
  );
}

export function suspendUser(userId: string, reason: string) {
  return adminMutate<{ suspended: boolean; effect: string }>("admin.users.suspend", {
    userId,
    reason,
  });
}

export function restoreUser(userId: string) {
  return adminMutate<{ suspended: boolean }>("admin.users.restore", { userId });
}

export function revokeUserSession(sessionId: string, reason: string) {
  return adminMutate<{ revoked: boolean }>("admin.sessions.revoke", { sessionId, reason });
}

/* -------------------------------------------------------------------------
 * One organization
 * ---------------------------------------------------------------------- */

export interface TenantMember {
  userId: string;
  role: string;
  githubLogin: string;
  email: string;
  name: string | null;
}

export interface TenantDetail {
  id: string;
  slug: string;
  name: string;
  plan: string;
  createdAt: string;
  suspended: boolean;
  suspendedReason: string | null;
  suspendedBy: string | null;
  members: TenantMember[];
}

/**
 * One organization by id, with its people.
 *
 * `admin.tenants.get` rather than filtering `admin.tenants.list` by slug, which
 * is what this page did before. The list route returns a member COUNT and this
 * one returns the members, and the count without the names answers none of the
 * questions somebody opens a tenant to ask. It takes an id, so the page reads
 * the slug from its address, finds the row in the list, and asks for the id.
 */
export function useTenantDetail(orgId: string | null) {
  return useApi<TenantDetail | null>(
    () => (orgId ? query<TenantDetail>("admin.tenants.get", { orgId }) : Promise.resolve(null)),
    [orgId],
  );
}

export function setTenantPlan(orgId: string, plan: string, reason: string) {
  return adminMutate<{ plan: string }>("admin.tenants.setPlan", { orgId, plan, reason });
}

/* -------------------------------------------------------------------------
 * What an operator wrote down
 * ---------------------------------------------------------------------- */

export type NoteSubject = "user" | "organization" | "repository";

export interface SupportNote {
  id: string;
  body: string;
  /** The operator's address, from the row rather than a join, because their
   *  own account may be gone by the time anybody reads this. */
  author: string;
  createdAt: string;
  /** Set on a note that was taken back. Retracted notes are still returned,
   *  because the taking back is itself worth being able to see. */
  retractedAt: string | null;
}

export function useNotes(subjectType: NoteSubject, subjectId: string | null) {
  return usePages<SupportNote>(
    async (cursor) => {
      if (!subjectId) return { rows: [], next: null };
      const page = await query<AdminPage<SupportNote>>("admin.customers.notes.list", {
        subjectType,
        subjectId,
        limit: 50,
        ...(cursor === null ? {} : { cursor }),
      });
      return { rows: page.rows, next: page.nextCursor };
    },
    [subjectType, subjectId],
  );
}

export function addNote(subjectType: NoteSubject, subjectId: string, body: string) {
  return adminMutate<{ id: string; createdAt: string }>("admin.customers.notes.add", {
    subjectType,
    subjectId,
    body,
  });
}

export function retractNote(id: string, reason: string) {
  return adminMutate<{ retracted: boolean }>("admin.customers.notes.retract", { id, reason });
}

/* -------------------------------------------------------------------------
 * Impersonation
 * ---------------------------------------------------------------------- */

export interface LiveImpersonation {
  sessionId: string;
  userId: string;
  githubLogin: string;
  email: string;
  orgId: string | null;
  orgSlug: string | null;
  /** The operator inside the account, by the address the audit chain names
   *  them under. */
  operator: string;
  reason: string;
  auditSeq: number;
  startedAt: string;
  /** When it stops on its own. Never null: the route refuses to mint a session
   *  without a bound. */
  endsAt: string;
}

export interface ImpersonationEvent {
  seq: number;
  operator: string;
  action: string;
  targetId: string | null;
  organization: string | null;
  detail: Record<string, unknown> | null;
  occurredAt: string;
}

/**
 * Who is inside a customer account, and who has been.
 *
 * Both halves in one call, because they come from two tables and the page needs
 * both to answer anything. A finished impersonation leaves no live row by
 * design, so a screen showing only the live list would say "nobody is
 * impersonating anybody", which is true and useless to somebody asking whether
 * anybody had.
 */
export function useImpersonations() {
  return useApi<{ live: LiveImpersonation[]; recent: ImpersonationEvent[] }>(
    () => query("admin.customers.impersonation.list"),
    [],
  );
}

export interface ImpersonationStarted {
  impersonating: true;
  userId: string;
  githubLogin: string;
  label: string;
  orgId: string | null;
  orgSlug: string | null;
  auditSeq: number;
  endsAt: string;
  /** The sentence the route composed, shown rather than reworded. Two
   *  sentences that mean the same thing today are two sentences that disagree
   *  after somebody edits one. */
  effect: string;
}

/**
 * Steps into a customer account.
 *
 * A plain endpoint rather than a procedure, because what it returns is a
 * Set-Cookie for the CUSTOMER's session. After it resolves this browser IS that
 * account, so the caller reloads rather than rendering anything: every operator
 * panel on the page is about to start answering "this session is impersonating
 * a customer", and re-rendering them one at a time is a screen full of failures
 * with one cause.
 */
export function startImpersonation(input: {
  userId: string;
  orgId: string | null;
  reason: string;
  minutes: number;
}) {
  return adminPost<ImpersonationStarted>("/v1/admin/impersonation/start", input);
}

/**
 * Steps back out.
 *
 * `ended` says whether THIS call is what ended it, which is not the same as
 * whether the session is impersonating afterwards. It is false on a second
 * press and on a tab that reloaded after the first one worked, and neither of
 * those is an error: the caller's goal has been achieved.
 */
export function endImpersonation() {
  return adminPost<{ impersonating: false; ended: boolean; revoked: number }>(
    "/v1/admin/impersonation/end",
  );
}
