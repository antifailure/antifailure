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
import { query, rest, useApi, type ApiError } from "@/lib/api";

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

export function useTenants(search: string) {
  return useApi<AdminPage<Tenant>>(
    () => query("admin.tenants.list", { limit: 50, query: search || undefined }),
    [search],
  );
}

export function useOperators() {
  return useApi<Operator[]>(() => query("admin.operators.list"), []);
}

export function useAdminAudit(severity: string) {
  return useApi<AdminPage<AdminAuditEntry>>(
    () => query("admin.audit.list", { limit: 100, severity: severity || undefined }),
    [severity],
  );
}

/**
 * A tRPC mutation on the operator router.
 *
 * NO CSRF TOKEN, and that is a decision rather than an omission. The product's
 * console sends one because its session cookie is SameSite=Lax, which a
 * cross-site top-level POST still carries. The operator cookie is
 * SameSite=Strict, so a browser sends it on NO cross-site request of any kind
 * and there is nothing for a token to add. If that cookie's SameSite is ever
 * loosened, this is the line that has to grow a token.
 */
export async function adminMutate<T>(path: string, input: unknown): Promise<T> {
  return rest<T>(`/trpc/${path}`, { method: "POST", body: input });
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
}

export async function adminSignOut(): Promise<void> {
  await rest("/v1/admin/signout", { method: "POST" });
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
