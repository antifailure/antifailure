/**
 * What the credentials list on /cli is, before any of it is a table.
 *
 * WHY THIS IS IN A FILE WITH NO IMPORTS, and it is the same reason lib/wire.ts
 * gives. The console's unit tests are literally `node --test lib/*.test.ts`,
 * with no bundler and no `@/` alias, so a test cannot import a page: the page
 * pulls in React and the component library and the import fails before a single
 * assertion runs. Logic that lives in a page is logic nothing can test.
 *
 * WHAT IS AT STAKE IN THE ONE FUNCTION BELOW. /cli decides which credentials a
 * person sees first and which it folds away behind a disclosure, and the rule
 * it must never break is that nothing live is folded away. That page is how
 * somebody notices a credential they did not expect, so a grouping bug that
 * tidied a live token off the screen would turn a cosmetic fix into a security
 * one. `stateOf` is what decides it, and it decides it here, where a test can
 * ask.
 */

/** One row of `tokens.list`, exactly as the control plane sends it. */
export interface TokenRow {
  id: string;
  name: string;
  prefix: string;
  kind: string;
  created_at: string;
  last_used_at: string | null;
  revoked_at: string | null;
  expires_at: string | null;
}

export type TokenState = "live" | "expired" | "revoked";

/**
 * Whether a credential can still act.
 *
 * Revoked beats expired, because a revoked credential is somebody's decision
 * and an expired one is a timer, and the reader is looking for the decision.
 *
 * An expiry exactly at `now` is expired. That is what the control plane does:
 * every route re-reads the row and refuses on `expires_at <= now`, so a page
 * that called the same instant live would show a credential as usable at the
 * moment it stopped being.
 */
export function stateOf(t: TokenRow, now: number): TokenState {
  if (t.revoked_at) return "revoked";
  if (t.expires_at && new Date(t.expires_at).getTime() <= now) return "expired";
  return "live";
}

/** What a `kind` is called on a screen a person reads. */
export function kindLabel(kind: string): string {
  if (kind === "cli") return "terminal";
  if (kind === "mcp") return "MCP client";
  if (kind === "oidc") return "workflow identity";
  return "engine token";
}

/**
 * One line of the history: either a single credential or one run's worth.
 *
 * `count` is one for everything that is not a workflow identity, and the line
 * then renders exactly as a row did before, prefix and all. A prefix is only
 * worth showing when there is one credential behind the line: it exists so
 * somebody can tell two of them apart and name one to `af token revoke`, and
 * neither is a thing you do to eight expired credentials at once.
 */
export interface HistoryLine {
  key: string;
  name: string;
  kind: string;
  state: TokenState;
  count: number;
  /** Only when this line stands for exactly one credential. */
  prefix: string | null;
  /** The newest created_at in the group, which is what it sorts on. */
  created: string;
  /** The newest last_used_at in the group, and how many of them have one. */
  lastUsed: string | null;
  used: number;
}

/**
 * Everything that can no longer act, grouped, newest first.
 *
 * LIVE ROWS ARE DROPPED HERE AND SHOWN IN FULL ELSEWHERE. This function only
 * ever returns dead credentials, which is the property the whole shape of the
 * page rests on, and it is the first thing the tests beside this file assert.
 *
 * Workflow identities are grouped on the name and the state together. The name
 * the control plane writes is `<owner>/<repo> run <id>`, so grouping on it is
 * grouping on the run, and it needs no parsing of a format this page does not
 * own. The state is in the key because a run whose credentials expired and a
 * run whose credentials were revoked are two different things to a reader, and
 * a group that mixed them could only report one badge for both.
 *
 * Nothing else is ever grouped. Two engine tokens can share a name, because
 * that is what a rotation is, and collapsing them would hide one credential
 * behind another that is not the same credential.
 */
export function groupHistory(rows: TokenRow[], now: number): HistoryLine[] {
  const lines: HistoryLine[] = [];
  const byRun = new Map<string, HistoryLine>();
  for (const t of rows) {
    const state = stateOf(t, now);
    if (state === "live") continue;
    const line: HistoryLine = {
      key: t.id,
      name: t.name,
      kind: t.kind,
      state,
      count: 1,
      prefix: t.prefix,
      created: t.created_at,
      lastUsed: t.last_used_at,
      used: t.last_used_at ? 1 : 0,
    };
    if (t.kind !== "oidc") {
      lines.push(line);
      continue;
    }
    const key = `${state}:${t.name}`;
    const existing = byRun.get(key);
    if (!existing) {
      line.key = key;
      byRun.set(key, line);
      lines.push(line);
      continue;
    }
    existing.count += 1;
    // The prefix stops being shown the moment a line stands for more than one
    // credential, rather than showing the first one and implying the rest.
    existing.prefix = null;
    if (t.created_at > existing.created) existing.created = t.created_at;
    if (t.last_used_at) {
      existing.used += 1;
      if (!existing.lastUsed || t.last_used_at > existing.lastUsed) {
        existing.lastUsed = t.last_used_at;
      }
    }
  }
  return lines.sort((a, b) => (a.created < b.created ? 1 : a.created > b.created ? -1 : 0));
}
