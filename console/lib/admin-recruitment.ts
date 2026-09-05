"use client";

/**
 * The recruitment lane's client: the private queue of applications for the
 * founding roles.
 *
 * A fourth lane file rather than more of admin-administration.ts, for the
 * reason that file gives for not being more of admin.ts: the transport is
 * shared and nothing else is. The transport IS reused, unchanged. `query` and
 * `adminMutate` come from api.ts and admin.ts, so there is no second place for
 * the error shape, the credentials mode or the operator CSRF header to drift.
 *
 * WHAT IS IN HERE IS NOT CUSTOMER DATA. Everything else this console reads
 * belongs to an organization. An application belongs to a person who is not a
 * customer, has no account, and gave us their name and their introduction on
 * the understanding that an operator reads it and then it expires. It is not
 * joined to a tenant, it is not in analytics, and the two permissions that
 * reach it are held by `owner` alone.
 *
 * THE ROLE VOCABULARY IS CLOSED, AND THE CONSOLE HAS TO KNOW IT. `role` arrives
 * as a string and the migration constrains it to exactly two values with a
 * CHECK. That list is in lib/recruitment-roles.ts rather than here, because it
 * is a rule and this file cannot be executed by the console's test runner:
 * admin-recruitment.test.ts reads the migration and asserts the two agree in
 * both directions. See the header of that module for what a rule in an
 * unrunnable file has already cost this console.
 */

import { query, usePages } from "@/lib/api";
import { adminMutate } from "@/lib/admin";

// Re-exported so a page imports one module for this lane. The values live in
// their own import-free file because that is the only kind this console's test
// runner can execute; see the header there.
export { APPLICATION_ROLES, roleLabel, type ApplicationRole } from "@/lib/recruitment-roles";

export interface Application {
  id: string;
  name: string;
  email: string;
  role: string;
  /** Empty string when the applicant gave no link, never null: the column is
   *  NOT NULL DEFAULT ''. */
  projectUrl: string;
  why: string;
  createdAt: string;
  /** Null until an operator marks it read. That is what the queue filter
   *  splits on, so it is nullable here for the same reason it is in the
   *  column. */
  reviewedAt: string | null;
}

export interface ApplicationCursor {
  id: string;
  createdAt: string;
}

export interface ApplicationPage {
  rows: Application[];
  /** Null on the last page. A cursor is (createdAt, id) rather than an offset,
   *  so removing an application does not shift the next page underneath the
   *  operator who is reading it. */
  nextCursor: ApplicationCursor | null;
}

/**
 * One queue, a page at a time, with the rows already on screen kept.
 *
 * `usePages` rather than `useApi`, which is the difference between "show more"
 * adding fifty rows and blanking the fifty the operator is reading. Its cursor
 * is a string and this route's cursor is a pair, so the pair travels as JSON
 * through it: that hook's own comment says each caller adapts its own route,
 * because the list routes in this portal page three different ways.
 *
 * A KEYSET CURSOR RATHER THAN AN OFFSET, and this page depends on that
 * property. An operator deletes applications while reading the list, and an
 * offset would shift every later row up underneath them, so pressing "show
 * more" would silently skip one. `(created_at, id)` names a position in the
 * order rather than a distance from the start, so removing the row the cursor
 * names cannot lose the next one. recruitment.test.ts proves it against a real
 * deletion of the cursor row.
 */
export function useApplications(reviewed: boolean) {
  return usePages<Application>(
    async (cursor) => {
      const page = await query<ApplicationPage>("admin.administration.applications.list", {
        reviewed,
        ...(cursor === null ? {} : { cursor: JSON.parse(cursor) as ApplicationCursor }),
      });
      return {
        rows: page.rows,
        next: page.nextCursor === null ? null : JSON.stringify(page.nextCursor),
      };
    },
    [reviewed],
  );
}

export function reviewApplication(id: string) {
  return adminMutate<{ reviewed: true }>("admin.administration.applications.review", { id });
}

export function removeApplication(id: string) {
  return adminMutate<{ removed: true }>("admin.administration.applications.remove", { id });
}
