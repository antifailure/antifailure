"use client";

/**
 * The Security & Governance lane's client.
 *
 * The transport is REUSED and nothing else is, which is the rule lib/admin.ts
 * states for itself: `query`, `usePages` and `adminMutate` come from there
 * unchanged, because a second fetch wrapper is a second place for the error
 * shape, the credentials mode and the CSRF header to drift, and the header is
 * the one that was already wrong once.
 *
 * EVERY LIST HERE IS PAGED WITH usePages, without exception. Three screens in
 * this console already read one page of a route that returns a cursor and told
 * the reader the list was complete when it was showing a third of it. On this
 * lane that failure has a sharper edge than usual: a credential inventory that
 * silently stops at fifty is a page that says there is nothing else to look at.
 */

import { query, useApi, usePages } from "@/lib/api";
import { adminMutate, type AdminPage } from "@/lib/admin";

/* -------------------------------------------------------------------------
 * Security Center
 * ---------------------------------------------------------------------- */

export type CredentialKind = "engine_token" | "provider_key" | "oidc_binding";
export type CredentialFlag = "never_used" | "idle" | "unrotated";
export type CredentialState = "live" | "flagged" | "expired" | "revoked";

export interface Credential {
  id: string;
  kind: CredentialKind;
  organization: string;
  orgId: string;
  /** The name somebody gave it, the provider, or the repository. */
  label: string;
  /** The non-secret identifier: a token prefix or a key's last four. Null for a
   *  binding, which has no such thing rather than an unknown one. */
  handle: string | null;
  createdBy: string | null;
  createdAt: string;
  lastUsedAt: string | null;
  expiresAt: string | null;
  rotatedAt: string | null;
  revokedAt: string | null;
  state: "live" | "expired" | "revoked";
  flags: CredentialFlag[];
}

export interface Posture {
  thresholds: { staleDays: number; unusedGraceDays: number };
  engineTokens: {
    live: number;
    expired: number;
    revoked: number;
    neverExpiring: number;
    neverUsed: number;
    idle: number;
  };
  providerKeys: { live: number; revoked: number; unrotated: number };
  oidcBindings: { live: number; revoked: number; neverUsed: number; idle: number };
  sso: {
    connections: number;
    enabled: number;
    bypassable: number;
    breakGlassOutstanding: number;
    breakGlassUsed: number;
  };
  operators: {
    total: number;
    unprovisioned: number;
    suspended: number;
    liveSessions: number;
    impersonatingSessions: number;
  };
  impersonations: {
    id: string;
    operator: string;
    actingAs: string | null;
    reason: string | null;
    startedAt: string;
    expiresAt: string;
  }[];
  severeEvents: {
    seq: number;
    actor: string;
    action: string;
    organization: string | null;
    severity: string;
    occurredAt: string;
  }[];
}

export interface SsoConnection {
  id: string;
  orgId: string;
  organization: string;
  kind: string;
  displayName: string;
  enabled: boolean;
  enforced: boolean;
  defaultRole: string;
  certificates: number;
  breakGlassOutstanding: number;
  breakGlassUsed: number;
  createdAt: string;
  updatedAt: string;
}

/** The whole posture in one read. One route rather than six, so the page cannot
 *  render five correct panels beside one that failed, which is the state a
 *  reader has no way to detect. */
export function usePosture() {
  return useApi<Posture>(() => query("admin.security.posture"), []);
}

export function useCredentials(kind: string, state: CredentialState) {
  return usePages<Credential>(
    async (cursor) => {
      const p = await query<AdminPage<Credential>>("admin.security.credentials", {
        limit: 50,
        state,
        ...(kind ? { kind } : {}),
        ...(cursor === null ? {} : { cursor }),
      });
      return { rows: p.rows, next: p.nextCursor };
    },
    [kind, state],
  );
}

export function useSso() {
  return usePages<SsoConnection>(
    async (cursor) => {
      const p = await query<AdminPage<SsoConnection>>("admin.security.sso", {
        limit: 50,
        ...(cursor === null ? {} : { cursor }),
      });
      return { rows: p.rows, next: p.nextCursor };
    },
    [],
  );
}

/* -------------------------------------------------------------------------
 * Data Governance
 * ---------------------------------------------------------------------- */

export type DeletionStep =
  | "stop_work"
  | "cancel_subscription"
  | "await_entitlement_end"
  | "revoke_credentials"
  | "export"
  | "purge"
  | "done"
  | "cancelled";

export interface Deletion {
  id: string;
  orgId: string;
  organization: string;
  slug: string;
  requestedBy: string;
  requestedAt: string;
  reason: string | null;
  step: DeletionStep;
  waitingUntil: string | null;
  purgedAt: string | null;
  cancelledAt: string | null;
  revoked: {
    at: string;
    engineTokens: number;
    providerKeys: number;
    sessions: number;
    installations: number;
  } | null;
  export: {
    available: boolean;
    expiresAt: string | null;
    sizeBytes: number | null;
    downloads: number;
    destroyedAt: string | null;
  } | null;
  lastError: { at: string; step: string; message: string } | null;
  attempts: number;
}

export interface MaskingRule {
  id: string;
  orgId: string;
  organization: string;
  repository: string;
  table: string;
  column: string;
  transform: string;
  reason: string | null;
  confirmed: boolean;
  updatedAt: string;
}

export interface OrphanedAccount {
  id: string;
  githubLogin: string;
  email: string | null;
  name: string | null;
  createdAt: string;
}

export interface SubjectLocation {
  table: string;
  column: string;
  onDelete: "cascade" | "set null" | "set default" | "restrict" | "no action";
  rows: number;
  /** The count stopped at the ceiling rather than at the end, so the real
   *  number is this or larger. Never rendered as an exact figure. */
  atLeast: boolean;
}

export interface SubjectAnswer {
  subject: {
    id: string;
    githubLogin: string;
    email: string | null;
    name: string | null;
    createdAt: string;
    organizations: { slug: string; role: string }[];
  } | null;
  candidates: { id: string; githubLogin: string; email: string | null; name: string | null }[];
  map: SubjectLocation[] | null;
  retained: { table: string; column: string; why: string }[] | null;
  erasure: {
    perSubject: string;
    perOrganization: string;
    residue: string;
    retention: string;
  };
  countCeiling: number;
}

export interface ErasureStatement {
  erasure: { perSubject: string; perOrganization: string; residue: string; retention: string };
  retained: { table: string; column: string; why: string }[];
  countCeiling: number;
}

/** What can and cannot be erased, with nobody named. Its own read so the page
 *  can render its caveats without looking a person up, which would record
 *  somebody in the operator log every time the page loaded. */
export function useErasure() {
  return useApi<ErasureStatement>(() => query("admin.security.erasure"), []);
}

export function useDeletions(state: "open" | "stuck" | "finished" | "all") {
  return usePages<Deletion>(
    async (cursor) => {
      const p = await query<AdminPage<Deletion>>("admin.security.deletions", {
        limit: 50,
        state,
        ...(cursor === null ? {} : { cursor }),
      });
      return { rows: p.rows, next: p.nextCursor };
    },
    [state],
  );
}

export function useMasking(confirmed: string) {
  return usePages<MaskingRule>(
    async (cursor) => {
      const p = await query<AdminPage<MaskingRule>>("admin.security.masking", {
        limit: 50,
        ...(confirmed === "" ? {} : { confirmed: confirmed === "yes" }),
        ...(cursor === null ? {} : { cursor }),
      });
      return { rows: p.rows, next: p.nextCursor };
    },
    [confirmed],
  );
}

export function useOrphanedAccounts() {
  return usePages<OrphanedAccount>(
    async (cursor) => {
      const p = await query<AdminPage<OrphanedAccount>>("admin.security.orphanedAccounts", {
        limit: 50,
        ...(cursor === null ? {} : { cursor }),
      });
      return { rows: p.rows, next: p.nextCursor };
    },
    [],
  );
}

/**
 * One subject lookup.
 *
 * NOT A HOOK ON EVERY KEYSTROKE. Reading a person's data map writes an audit
 * entry naming that person, at notice severity, which is the entry a data
 * protection request is answered from. A lookup that fired as somebody typed
 * would fill the operator log with people nobody meant to open, and the entries
 * that matter would be buried among them. So this is called on submit, from a
 * real form, and never from a `useApi` whose dependency is an input value.
 */
export async function lookupSubject(input: { userId?: string; query?: string }) {
  return query<SubjectAnswer>("admin.security.subject", input);
}

export interface Exported {
  filename: string;
  contentType: string;
  document: string;
}

export async function exportSubject(userId: string, reason: string) {
  return adminMutate<Exported & { subject: string; locations: number }>(
    "admin.security.subjectExport",
    { userId, reason },
  );
}

/* -------------------------------------------------------------------------
 * The audit chain
 * ---------------------------------------------------------------------- */

export interface ChainReport {
  ok: boolean;
  entries: number;
  firstSeq: number | null;
  lastSeq: number | null;
  head: string | null;
  problems: { seq: number; kind: "altered" | "broken_link"; detail: string }[];
}

export interface ChainExport extends Exported {
  format: "json" | "csv";
  entryCount: number;
  /** The export stopped at its ceiling rather than at the end of the range. A
   *  truncated file read as the whole chain is somebody telling a regulator
   *  that this is everything. */
  truncated: boolean;
  firstSeq: number | null;
  lastSeq: number | null;
  verification: {
    ok: boolean;
    entriesWalked: number;
    problems: { seq: number; kind: string; detail: string }[];
  };
}

export async function verifyChain() {
  return query<ChainReport>("admin.audit.verify", {});
}

export async function exportChain(input: {
  format: "json" | "csv";
  limit: number;
  severity?: string;
}) {
  return adminMutate<ChainExport>("admin.audit.export", input);
}

/* -------------------------------------------------------------------------
 * Handing the reader a file
 * ---------------------------------------------------------------------- */

/**
 * Saves a document the control plane produced.
 *
 * A BLOB AND AN OBJECT URL, not a link to an endpoint, and the reason is the
 * shape of this console rather than a preference. The export is a MUTATION,
 * because producing a file of every operator action is an act the log has to
 * record, and a browser cannot navigate to a POST. So the page asks for the
 * document, gets it back, and saves what it was given.
 *
 * The object URL is revoked on the next frame. Not revoking it holds the whole
 * document in memory for the life of the tab, and an operator exporting ten
 * thousand audit entries a few times over an afternoon is the case where that
 * stops being free.
 */
export function saveDocument(file: { filename: string; contentType: string; document: string }) {
  const url = URL.createObjectURL(new Blob([file.document], { type: file.contentType }));
  const a = document.createElement("a");
  a.href = url;
  a.download = file.filename;
  a.style.display = "none";
  document.body.appendChild(a);
  a.click();
  a.remove();
  // requestAnimationFrame rather than an immediate revoke: Safari has not
  // started reading the blob by the time click() returns, and revoking under it
  // produces a download that fails with no error anywhere.
  requestAnimationFrame(() => URL.revokeObjectURL(url));
}
