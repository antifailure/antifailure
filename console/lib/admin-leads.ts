"use client";

/**
 * The enterprise leads lane's client: the private queue of people who asked to
 * buy.
 *
 * A lane file of its own rather than more of admin-administration.ts, for the
 * reason that file gives and the recruitment lane repeats: the transport is
 * shared and nothing else is. `query` and `adminMutate` come from api.ts and
 * admin.ts, so the error shape, the credentials mode and the operator CSRF
 * header have one home and cannot drift per lane.
 *
 * WHAT IS IN HERE IS NOT CUSTOMER DATA. A lead is a person who filled in the
 * "talk to us" form, has no account, and gave us their name, their company and
 * a message on the understanding that somebody reads it and replies. It is not
 * joined to a tenant, it is not in analytics, and the two permissions that
 * reach it are held by owner alone, the same as recruitment.
 *
 * THE PROCEDURE PATHS ARE STRINGS, and that is the seam admin-leads.test.ts
 * guards. This file names admin.administration.leads.list and .handle; a rename
 * on the router leaves the console compiling and answering 404 the first time
 * an operator presses a button, so that test reads the control plane's own
 * source and asserts both names exist.
 */

import { query, usePages } from "@/lib/api";
import { adminMutate } from "@/lib/admin";

export interface Lead {
  id: string;
  email: string;
  name: string;
  company: string;
  /** Null when the person did not say. 0035 stores "unknown" as null, never as
   *  zero, so there is one representation to render rather than two. */
  seats: number | null;
  message: string;
  /** Which page it came from, kept so a route producing nothing is visible as a
   *  route producing nothing rather than as an absence. */
  source: string;
  createdAt: string;
  /** Null until an operator marks it handled. That is what the queue filter
   *  splits on, so it is nullable here for the same reason it is in the
   *  column. */
  handledAt: string | null;
  handledNote: string | null;
}

export interface LeadCursor {
  id: string;
  createdAt: string;
}

export interface LeadPage {
  rows: Lead[];
  /** Null on the last page. A cursor is (createdAt, id) rather than an offset,
   *  so marking a lead handled does not shift the next page underneath the
   *  operator who is reading it. */
  nextCursor: LeadCursor | null;
}

/**
 * One queue, a page at a time, with the rows already on screen kept.
 *
 * `usePages` rather than `useApi`, so "show more" adds fifty rows instead of
 * blanking the fifty the operator is reading. Its cursor is a string and this
 * route's cursor is a pair, so the pair travels as JSON through it, the same
 * adaptation the recruitment lane makes and for the same reason.
 *
 * A KEYSET CURSOR RATHER THAN AN OFFSET, and this page depends on it. An
 * operator marks leads handled while reading the waiting queue, which removes
 * them from it, and an offset would shift every later row up underneath them so
 * "show more" would silently skip one. `(created_at, id)` names a position in
 * the order rather than a distance from the start, so removing the row the
 * cursor names cannot lose the next one. leads-admin.test.ts proves it against
 * a real deletion of the cursor row.
 */
export function useLeads(handled: boolean) {
  return usePages<Lead>(
    async (cursor) => {
      const page = await query<LeadPage>("admin.administration.leads.list", {
        handled,
        ...(cursor === null ? {} : { cursor: JSON.parse(cursor) as LeadCursor }),
      });
      return {
        rows: page.rows,
        next: page.nextCursor === null ? null : JSON.stringify(page.nextCursor),
      };
    },
    [handled],
  );
}

export function handleLead(id: string, note?: string) {
  return adminMutate<{ handled: true }>("admin.administration.leads.handle", {
    id,
    ...(note ? { note } : {}),
  });
}
