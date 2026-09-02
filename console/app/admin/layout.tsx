"use client";

import { AdminContext, useAdminMe } from "@/lib/admin";
import { AdminShell } from "@/components/AdminShell";

/**
 * The operator portal's own route group.
 *
 * Its own group rather than a section inside (app), and that is not filing:
 * (app) mounts SessionProvider and Shell, which resolve the CUSTOMER session
 * and render a customer's navigation. An operator page underneath that would
 * fetch the wrong identity, show the wrong chrome, and refuse for reasons about
 * the wrong account.
 *
 * The operator is resolved ONCE here rather than per page, for the reason the
 * console's own layout gives: a layout survives a client-side navigation and a
 * page does not, so resolving it in the pages would refetch and blank the whole
 * window, rail included, on every click.
 */
export default function AdminLayout({ children }: { children: React.ReactNode }) {
  const state = useAdminMe();
  return (
    <AdminContext.Provider
      value={{ me: state.data, status: state.status, error: state.error, reload: state.reload }}
    >
      <AdminShell>{children}</AdminShell>
    </AdminContext.Provider>
  );
}
