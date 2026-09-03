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
import { ApiError, query, rest, useApi, usePages } from "@/lib/api";
// Re-exported so callers have one import for the operator client. The two
// constants live in a module with no dependencies because that is the only
// kind this package can unit test: see lib/admin-csrf.ts.
import { ADMIN_CSRF_HEADER, isStaleToken } from "@/lib/admin-csrf";

export { ADMIN_CSRF_HEADER, isStaleToken };

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
 * The operator's forgery token, fetched once and remembered.
 *
 * WHY IT HAS TO BE FETCHED AT ALL. This function replaces a comment that argued
 * no token was needed, because the operator cookie is SameSite=Strict and a
 * browser sends it on no cross-site request. The argument is half right and the
 * half it misses is the half that matters: SameSite is SITE scoped rather than
 * ORIGIN scoped, so a subdomain an attacker controls is inside it. The server
 * has always disagreed and always refused. `web/apps/api/src/server.ts` refuses
 * every non-GET /trpc/* request carrying a live `af_admin_session` cookie
 * unless it presents a matching token, and `admincsrf.test.ts` pins that in
 * three ways. So the comment was a claim and the 403 was the fact: every
 * operator mutation this console has ever sent was refused, which is why the
 * portal had a working Suspend button that suspended nothing.
 *
 * The token is derived from the cookie, and the cookie is HttpOnly, so a page
 * cannot compute it. `GET /v1/admin/session` returns it to whoever already
 * holds the cookie, which is exactly what a forgery token is: not a secret, but
 * a value a page on another origin cannot read because it cannot make this
 * request and see the answer.
 *
 * Cached in a module variable rather than fetched per mutation, and refreshed
 * on the one refusal that means it went stale. See adminMutate.
 */
let cachedAdminCsrf: string | null = null;

async function adminCsrfToken(force = false): Promise<string> {
  if (cachedAdminCsrf !== null && !force) return cachedAdminCsrf;
  const session = await rest<{ signedIn: boolean; csrfToken?: string }>("/v1/admin/session");
  cachedAdminCsrf = session.signedIn && session.csrfToken ? session.csrfToken : null;
  if (cachedAdminCsrf === null) {
    // A 401 rather than a network error, and the message says what to do. The
    // alternative is sending the mutation without a token and letting the
    // server answer with a sentence about a header, which describes the
    // transport to somebody whose session simply ended.
    throw new ApiError(
      "Your operator session has ended. Sign in again to continue.",
      401,
      "UNAUTHORIZED",
    );
  }
  return cachedAdminCsrf;
}

/** Dropped when a session begins or ends, because a token derived from one
 *  cookie is refused by the next one, and a stale token in this variable would
 *  turn a fresh sign-in into a 403 nobody could explain. */
export function forgetAdminCsrf(): void {
  cachedAdminCsrf = null;
}

/**
 * A tRPC mutation on the operator router.
 *
 * RETRIED EXACTLY ONCE, and only on the refusal that names the header. The
 * cached token belongs to a specific operator session, so signing out in
 * another tab, or an operator session expiring and being replaced, leaves this
 * variable holding a value the server will refuse. One refetch and one retry
 * turns that into a mutation that works; a loop would turn a genuinely refused
 * request into a hammer. Every other 403, including "your operator role does
 * not have this permission", is passed straight through, because refetching a
 * token cannot help with a permission.
 */
export async function adminMutate<T>(path: string, input: unknown): Promise<T> {
  return adminPost<T>(`/trpc/${path}`, input);
}

/**
 * A POST to any operator endpoint, carrying the token.
 *
 * Separate from adminMutate because not every operator write is a procedure.
 * Two of them cannot be: starting and ending an impersonation both end in a
 * Set-Cookie for the CUSTOMER's session cookie, and a procedure that exists to
 * set a cookie is a procedure pretending to be a route. They are plain JSON
 * endpoints under /v1/admin/, they are behind the same operator cookie, and the
 * server demands the same header on them, so they need this and not a third
 * copy of it.
 */
export async function adminPost<T>(path: string, body?: unknown): Promise<T> {
  const send = (csrf: string) =>
    rest<T>(path, {
      method: "POST",
      body: body ?? {},
      headers: { [ADMIN_CSRF_HEADER]: csrf },
    });
  try {
    return await send(await adminCsrfToken());
  } catch (err) {
    if (err instanceof ApiError && err.status === 403 && isStaleToken(err.message)) {
      return send(await adminCsrfToken(true));
    }
    throw err;
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
  // Dropped BEFORE the request as well as after it. A failed sign-in can still
  // have replaced the cookie, and a token from the previous session refuses
  // silently rather than loudly.
  forgetAdminCsrf();
  await rest("/v1/admin/signin", { method: "POST", body: { email, password } });
  forgetAdminCsrf();
}

/**
 * Ends the operator session.
 *
 * It also ends any impersonation this session holds and clears the customer
 * cookie, which the server does rather than this function: see the block on
 * POST /v1/admin/signout in server.ts. That matters here because this is what
 * the portal's impersonation refusal screen calls, so the one button an
 * operator can reach from inside an impersonation actually gets them out
 * rather than leaving a borrowed cookie in the browser.
 */
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
