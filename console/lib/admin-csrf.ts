/**
 * The operator portal's cross-site token, as something a test can drive.
 *
 * WHY THIS IS ITS OWN MODULE. The console's unit tests are literally
 * `node --test lib/*.test.ts`, with no bundler and no path aliases, so a module
 * that imports React or `@/lib/api` cannot be tested at all. lib/admin.ts is
 * both. That is exactly how the bug this file fixes survived: the one piece of
 * the operator client with a rule in it lived in the one file nothing could
 * execute, and the rule it carried was a paragraph of reasoning about what the
 * server requires, with nothing checking whether the server agreed.
 *
 * It did not. Every operator mutation the console has ever sent was answered
 * 403, because web/apps/api/src/server.ts refuses a non-GET under /trpc/ that
 * carries a resolving af_admin_session cookie without a matching
 * x-antifailure-admin-csrf header. admincsrf.test.ts has asserted that on the
 * server side the whole time.
 *
 * So the rule moved here, where it has no imports at all and admin-csrf.test.ts
 * runs it. What stays in admin.ts is the wiring: the real fetch, and the real
 * endpoint.
 */

/** The header the control plane demands on every operator write.
 *
 *  NOT the tenant header. They are two sessions with two tokens, and sending
 *  the tenant one produces a 403 that reads like a permissions problem, which
 *  is the most expensive way for this to fail. */
export const ADMIN_CSRF_HEADER = "x-antifailure-admin-csrf";

export interface AdminCsrf {
  /** The current token, fetching it once and keeping it. */
  token(refresh?: boolean): Promise<string | null>;
  /**
   * Runs one operator write with the token attached, retrying once if the
   * server refuses it.
   *
   * `attempt` is handed the headers to send rather than the token, so a caller
   * cannot attach it under the wrong name.
   */
  send<T>(attempt: (headers: Record<string, string>) => Promise<T>): Promise<T>;
  /**
   * Drops the cached token, because the session it belongs to is gone.
   *
   * Called on sign-in and on sign-out. `send` already recovers from a stale
   * token by refetching after a 403, so this is not what makes the flow
   * correct; it is what stops the first write after a re-sign-in paying a
   * pointless refusal to discover something the client already knew. Signing
   * out and back in inside one tab is the case, and it is the one where an
   * operator is most likely to be in a hurry.
   */
  forget(): void;
}

/** Whether a rejection is the server refusing the token, structurally, so this
 *  module needs no import to recognise ApiError. */
export function isForbidden(error: unknown): boolean {
  return (
    typeof error === "object" &&
    error !== null &&
    "status" in error &&
    (error as { status: unknown }).status === 403
  );
}

/**
 * The token, cached, and the one retry that is worth making.
 *
 * ONE RETRY, ON 403 ONLY, AND ONLY WHEN THE TOKEN ACTUALLY CHANGED. Each of
 * those three conditions is load bearing:
 *
 *   More than one retry turns a genuinely refused write into a loop against a
 *   server that is already saying no.
 *
 *   Retrying anything but a 403 retries a write that may have partly happened.
 *   Every operator mutation in this product records an audit entry inside its
 *   own transaction, so a blind retry of a 500 is a second recorded action.
 *
 *   Retrying with the SAME token asks the identical question and gets the
 *   identical answer. The refetch is only useful when the cached token outlived
 *   the session that minted it, which is what happens when somebody signs out
 *   and back in without reloading the tab.
 */
export function createAdminCsrf(read: () => Promise<string | null>): AdminCsrf {
  // Module scoped rather than component scoped on purpose: the token belongs to
  // the cookie, not to a screen. Two panels mutating at once send one token,
  // and unmounting a page does not throw it away.
  let cached: string | null = null;

  async function token(refresh = false): Promise<string | null> {
    if (cached !== null && !refresh) return cached;
    cached = await read();
    return cached;
  }

  return {
    token,
    forget() {
      cached = null;
    },
    async send<T>(attempt: (headers: Record<string, string>) => Promise<T>): Promise<T> {
      const first = await token();
      try {
        // An absent token sends no header rather than an empty one. The server
        // distinguishes a missing header from a wrong one in its message, and
        // an empty string would land in the second bucket and mislead whoever
        // reads the refusal.
        return await attempt(first ? { [ADMIN_CSRF_HEADER]: first } : {});
      } catch (error) {
        if (!isForbidden(error)) throw error;
        const fresh = await token(true);
        if (fresh === null || fresh === first) throw error;
        return attempt({ [ADMIN_CSRF_HEADER]: fresh });
      }
    },
  };
}
