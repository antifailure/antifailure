"use client";

/**
 * The operator portal's client.
 *
 * WHY THIS IS A SECOND FILE AND NOT ADDITIONS TO api.ts. The two portals speak
 * to the same control plane over the same transport, and everything else about
 * them is different: a different cookie, a different session table, a different
 * permission catalog, and a different answer to "who is this". One module
 * holding both would make `useSession()` a function whose meaning depends on
 * which page imported it, and the page that gets it wrong is the one that shows
 * a customer's console to an operator or the reverse.
 *
 * So the transport is REUSED, deliberately, and nothing else is: query, rest
 * and useApi come from api.ts unchanged, because a second fetch wrapper is a
 * second place for the error shape, the credentials mode and the CSRF header to
 * drift.
 */

import { createContext, useContext } from "react";
import { query, rest, useApi, usePages, ApiError } from "@/lib/api";

/** Every permission string the platform catalog defines. Kept as a plain string
 *  rather than a union mirrored from the server: a union here would have to be
 *  edited every time a lane adds a permission, and the copy that goes stale is
 *  the one that silently hides a navigation entry. */
export type AdminPermission = string;

export interface AdminMe {
  adminUserId: string;
  /** The operator's display name. */
  label: string;
  email: string;
  role: string;
  /** True when this session is acting as a customer. The gate refuses every
   *  admin procedure while it is set, so the portal shows the reason rather
   *  than a wall of failed panels. */
  impersonating: boolean;
  /**
   * What this operator may do, sent by the server rather than derived here.
   *
   * The navigation hides an entry whose permission is absent. Deriving that
   * from the role name would mean this file keeps a second copy of
   * ADMIN_ROLE_PERMISSIONS, and the copy that drifts is the one that shows a
   * link to a page the server will refuse.
   */
  permissions: AdminPermission[];
}

/** One page of anything the operator router lists. Matches pageOf on the
 *  server, which returns the rows plus the cursor for the next call. */
export interface AdminPage<Row> {
  rows: Row[];
  nextCursor: string | null;
}

export interface Tenant {
  id: string;
  slug: string;
  name: string;
  plan: string;
  createdAt: string;
  suspended: boolean;
  suspendedReason: string | null;
  members: number;
  environments: number;
}

export interface Operator {
  id: string;
  email: string;
  name: string;
  role: string;
  /** The permanent root operator, which the database refuses to delete,
   *  demote or suspend. Shown so nobody wastes an incident trying. */
  isRoot: boolean;
  suspended: boolean;
  lastSignedInAt: string | null;
  /** Whether a password has ever been set. An operator who CANNOT sign in
   *  otherwise looks identical to one who can, and on this screen that is the
   *  difference between an account that is waiting for setup and one that is
   *  live. */
  provisioned: boolean;
}

export interface TenantDetail extends Tenant {
  members: number;
  environments: number;
}

export interface AdminAuditEntry {
  seq: number;
  actor: string;
  action: string;
  targetType: string;
  targetId: string | null;
  /** Null for an installation-wide action, which is the whole reason the
   *  operator chain is a separate table. Rendered as a dash, never as a blank
   *  cell that reads like missing data. */
  organization: string | null;
  severity: "info" | "notice" | "high" | "critical";
  detail: unknown;
  occurredAt: string;
}

/** Reads the signed-in operator. Null when nobody is signed in, which is a
 *  state and not an error: the portal shows its sign-in screen. */
export function useAdminMe() {
  return useApi<AdminMe>(() => query("admin.me"), []);
}

/**
 * Tenants, paged.
 *
 * usePages rather than useApi, and that is a correctness fix rather than a
 * feature. The route returns 50 rows and a cursor; reading only the first page
 * showed 50 organizations in a table that looked complete, so an operator
 * searching for an account that sorted past the cut would be told, confidently,
 * that it does not exist. See the comment on More: a list that quietly shows a
 * third of the rows is worse than one that looks broken, because the reader
 * acts on it.
 */
export function useTenants(search: string) {
  return usePages<Tenant>(
    async (cursor) => {
      const page = await query<AdminPage<Tenant>>("admin.tenants.list", {
        limit: 50,
        ...(search ? { query: search } : {}),
        ...(cursor === null ? {} : { cursor }),
      });
      return { rows: page.rows, next: page.nextCursor };
    },
    [search],
  );
}

/** One tenant by slug, for the detail screen. Its own single-page read rather
 *  than paging the whole list and searching it client side: the list route
 *  filters server side, so asking for the one is one query rather than many. */
export function useTenant(slug: string) {
  return useApi<AdminPage<Tenant>>(
    () => query("admin.tenants.list", { limit: 50, query: slug }),
    [slug],
  );
}

export function useOperators() {
  return useApi<Operator[]>(() => query("admin.operators.list"), []);
}

export function useAdminAudit(severity: string) {
  return usePages<AdminAuditEntry>(
    async (cursor) => {
      const page = await query<AdminPage<AdminAuditEntry>>("admin.audit.list", {
        limit: 100,
        ...(severity ? { severity } : {}),
        ...(cursor === null ? {} : { cursor }),
      });
      return { rows: page.rows, next: page.nextCursor };
    },
    [severity],
  );
}

/**
 * The header the control plane wants on every operator mutation.
 *
 * NOT the tenant one. `rest` and `mutate` in api.ts send
 * `x-antifailure-csrf`, which the operator guard does not look at, so a route
 * that goes through those is refused exactly as if it sent nothing. Written
 * here as its own constant for that reason: the two names differ by five
 * characters and the failure they produce is identical.
 */
const ADMIN_CSRF_HEADER = "x-antifailure-admin-csrf";

/**
 * The operator CSRF token, fetched once and kept.
 *
 * WHY THIS EXISTS AT ALL. The comment that used to stand here argued that no
 * token was needed, because the operator cookie is SameSite=Strict and a
 * browser sends it on no cross-site request of any kind. That reasoning is
 * sound and it does not matter: server.ts refuses every non-GET `/trpc/*`
 * request carrying a valid operator cookie unless it presents a matching
 * `x-antifailure-admin-csrf`, and admincsrf.test.ts pins that three ways. So
 * every operator mutation in this console was answered with 403. The buttons
 * were there, the routes were there, the audit rows would have been written,
 * and the request never reached any of it.
 *
 * That is the exact failure this product exists to catch: a capability that is
 * defined, wired to a button, and not effective. It survived because both
 * halves are individually correct and nobody ran the pair.
 *
 * The server's own comment says why the reasoning was wrong: SameSite is SITE
 * scoped, so a subdomain an attacker controls is inside it.
 *
 * WHY IT IS FETCHED RATHER THAN REMEMBERED FROM SIGN-IN. The cookie is
 * HttpOnly and the token is derived from it, so a page cannot compute it and a
 * reload loses whatever the sign-in response carried. A console that held only
 * that could mutate exactly once per sign-in, which is a guard that looks like
 * it works and fails on the first refresh.
 *
 * The PROMISE is cached rather than the value, so two mutations fired together
 * make one request instead of two.
 */
let csrfPromise: Promise<string | null> | null = null;

async function adminCsrfToken(): Promise<string | null> {
  csrfPromise ??= rest<{ signedIn: boolean; csrfToken?: string }>("/v1/admin/session")
    .then((s) => s.csrfToken ?? null)
    .catch(() => {
      // Not cached as a failure. A network blip while fetching the token would
      // otherwise poison every mutation for the life of the page.
      csrfPromise = null;
      return null;
    });
  return csrfPromise;
}

/**
 * Forgets the token, because the session it was derived from is gone.
 *
 * Called on sign-in and sign-out. Without it, signing out and back in inside
 * one page leaves the old token cached against a new session, and every
 * mutation after that is refused with a message about a header that is present
 * and simply belongs to somebody else.
 */
function forgetAdminCsrf(): void {
  csrfPromise = null;
}

/**
 * A tRPC mutation on the operator router.
 *
 * Retries ONCE on a refusal, after forgetting the token. The token is derived
 * from the session, so the one case that matters is a session that was
 * replaced under a page that is still open: the first attempt is refused, the
 * second fetches the current token and succeeds. It retries once and not in a
 * loop, because a refusal that survives a fresh token is a real refusal and
 * hiding it behind retries would turn a 403 into a hang.
 */
export async function adminMutate<T>(path: string, input: unknown): Promise<T> {
  const send = async () => {
    const csrf = await adminCsrfToken();
    return rest<T>(`/trpc/${path}`, {
      method: "POST",
      body: input,
      headers: csrf ? { [ADMIN_CSRF_HEADER]: csrf } : undefined,
    });
  };
  try {
    return await send();
  } catch (err) {
    if ((err as ApiError).status !== 403) throw err;
    forgetAdminCsrf();
    return send();
  }
}

export async function suspendTenant(orgId: string, reason: string) {
  return adminMutate<{ suspended: boolean; effect: string }>("admin.tenants.suspend", {
    orgId,
    reason,
  });
}

export async function resumeTenant(orgId: string) {
  return adminMutate<{ suspended: boolean }>("admin.tenants.resume", { orgId });
}

/**
 * Signs an operator in.
 *
 * A plain JSON endpoint rather than tRPC, for the reason the product's own
 * sign-in is: the thing it returns is a Set-Cookie, and a procedure that exists
 * to set a cookie is a procedure pretending to be a route.
 */
export async function adminSignIn(email: string, password: string): Promise<void> {
  await rest("/v1/admin/signin", { method: "POST", body: { email, password } });
  // The new session has a new token, and the old one is now wrong rather than
  // merely stale: it would be sent, refused, and the refusal would name a
  // header that is right there in the request.
  forgetAdminCsrf();
}

export async function adminSignOut(): Promise<void> {
  await rest("/v1/admin/signout", { method: "POST" });
  forgetAdminCsrf();
}

/** The operator, shared by the chrome and every page under it, so a navigation
 *  does not refetch who is signed in on every click. */
export const AdminContext = createContext<{
  me: AdminMe | null;
  status: "loading" | "ready" | "error";
  error: ApiError | null;
  reload: () => void;
} | null>(null);

export function useAdminContext() {
  const ctx = useContext(AdminContext);
  if (!ctx) throw new Error("useAdminContext outside the operator shell");
  return ctx;
}

/** Whether the operator holds a permission. Absent means the entry is hidden,
 *  and the server refuses it anyway: the navigation is a convenience and never
 *  the enforcement. */
export function operatorMay(me: AdminMe | null, permission: AdminPermission): boolean {
  return me?.permissions.includes(permission) ?? false;
}
