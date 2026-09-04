"use client";

/**
 * The Operations lane's client: infrastructure, failures, email and the
 * switches.
 *
 * WHY A THIRD FILE AND NOT ADDITIONS TO admin.ts. Same reason admin.ts is not
 * additions to api.ts: the transport is shared and nothing else is. admin.ts
 * holds what every operator page needs, which is who is signed in and what they
 * may do. This holds four sections' worth of shapes that no other page reads,
 * and putting them there would make the module every page imports grow by the
 * size of the portal.
 *
 * The transport is reused unchanged. `query`, `usePages` and `useApi` come from
 * api.ts and `adminMutate` from admin.ts, because a second fetch wrapper is a
 * second place for the error shape, the credentials mode and the CSRF header to
 * drift, and the header is exactly the thing that has drifted here before.
 *
 * NOTHING IN THIS FILE INVENTS A NUMBER. Every shape it re-exports mirrors a
 * route that returns it. Where a question has no answer in this product, such
 * as whether a message was delivered, there is no type for it anywhere and the
 * page says so in words instead of showing a zero.
 *
 * The shapes and the pure helpers live in `operations-shapes.ts`, which imports
 * nothing, so `node --test lib/*.test.ts` can reach them. Everything there is
 * re-exported below, so a page still imports one module.
 */

import { query, useApi, usePages } from "@/lib/api";
import { adminMutate, type AdminPage } from "@/lib/admin";
import {
  FLEET_LIMIT,
  type BlastRadius,
  type ControlName,
  type ControlState,
  type EmailStatus,
  type EventRow,
  type FirewallSummary,
  type Finding,
  type FleetTeardownResult,
  type HealthReport,
  type LogsOverview,
  type SignInLink,
  type Teardown,
  type Twin,
  type TwinScope,
} from "@/lib/operations-shapes";

// Re-exported so a page imports one module and the contract with the control
// plane is still one file to reconcile. Same arrangement as load.ts over
// loadshapes.ts.
export * from "@/lib/operations-shapes";

/* -------------------------------------------------------------------------
 * System health, from admin/health.ts
 * ---------------------------------------------------------------------- */


export function useSystemHealth() {
  return useApi<HealthReport>(() => query("admin.infra.health"), []);
}

/* -------------------------------------------------------------------------
 * The fleet, from admin/fleet.ts
 * ---------------------------------------------------------------------- */


export function useTwins(scope: TwinScope) {
  return useApi<Twin[]>(
    () => query("admin.infra.twins", { scope, limit: FLEET_LIMIT }),
    [scope],
  );
}


export function useTeardowns(openOnly: boolean) {
  return useApi<Teardown[]>(
    () => query("admin.infra.teardowns", { open: openOnly, limit: FLEET_LIMIT }),
    [openOnly],
  );
}


/** What a fleet teardown would touch, computed rather than estimated. An
 *  operator confirming a blast radius written in prose is confirming somebody's
 *  recollection of what the query would return. */
export function teardownRadius(): Promise<BlastRadius> {
  return query("admin.infra.teardownRadius");
}


export function requestFleetTeardown(reason: string) {
  return adminMutate<FleetTeardownResult>("admin.infra.teardownFleet", { reason });
}

/* -------------------------------------------------------------------------
 * The egress firewall, from admin/firewall.ts
 * ---------------------------------------------------------------------- */


export function useFirewall() {
  return useApi<{ summary: FirewallSummary; findings: Finding[] }>(
    () => query("admin.infra.firewall"),
    [],
  );
}

/* -------------------------------------------------------------------------
 * The switches, from admin/controls.ts
 * ---------------------------------------------------------------------- */


export function useControls() {
  return useApi<ControlState[]>(() => query("admin.emergency.controls"), []);
}

/**
 * Engages or releases one switch.
 *
 * `reason` is required to engage and refused empty by the server as well as by
 * the form, because a switch that stops an installation with no reason recorded
 * is one the next person on call cannot safely release.
 */
export function setControl(name: ControlName, engaged: boolean, reason: string | null) {
  return adminMutate<ControlState>("admin.emergency.set", { name, engaged, reason });
}

/* -------------------------------------------------------------------------
 * Failures and the event stream
 * ---------------------------------------------------------------------- */


export function useLogsOverview(hours: string, orgId: string) {
  return useApi<LogsOverview>(
    () =>
      query("admin.operations.logs.overview", {
        hours: Number(hours),
        ...(orgId ? { orgId } : {}),
      }),
    [hours, orgId],
  );
}


/** usePages rather than useApi, because the route returns a cursor. A screen
 *  that reads one page of a keyset list and renders no footer tells the reader
 *  the list is complete, which is a confident wrong answer. */
export function useEventStream(hours: string, type: string, orgId: string) {
  return usePages<EventRow>(
    async (cursor) => {
      const page = await query<AdminPage<EventRow>>("admin.operations.logs.events", {
        hours: Number(hours),
        limit: 50,
        ...(type ? { type } : {}),
        ...(orgId ? { orgId } : {}),
        ...(cursor === null ? {} : { cursor }),
      });
      return { rows: page.rows, next: page.nextCursor };
    },
    [hours, type, orgId],
  );
}

/* -------------------------------------------------------------------------
 * Email
 * ---------------------------------------------------------------------- */


export function useEmailStatus(hours: string) {
  return useApi<EmailStatus>(
    () => query("admin.operations.email.status", { hours: Number(hours) }),
    [hours],
  );
}


export function useSignInLinks(hours: string, standing: string, search: string) {
  return usePages<SignInLink>(
    async (cursor) => {
      const page = await query<AdminPage<SignInLink>>("admin.operations.email.signInLinks", {
        hours: Number(hours),
        limit: 50,
        ...(standing ? { standing } : {}),
        ...(search ? { query: search } : {}),
        ...(cursor === null ? {} : { cursor }),
      });
      return { rows: page.rows, next: page.nextCursor };
    },
    [hours, standing, search],
  );
}
