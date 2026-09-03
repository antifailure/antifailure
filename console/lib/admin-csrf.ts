/**
 * The two facts about the operator forgery token that have to match the server.
 *
 * ITS OWN MODULE, AND WHY. `console/lib/admin.ts` is a React client: it imports
 * `react` and `@/lib/api`, and the console's unit tests are literally
 * `node --test lib/*.test.ts`, which resolves neither the alias nor a hook. So
 * anything in that file is untestable here, and these two are exactly the parts
 * worth testing, because both are a copy of something the server decides.
 *
 * The header name is spelled on both sides and a disagreement refuses every
 * operator mutation. The predicate decides whether a 403 gets one silent retry
 * with a fresh token or is reported to the operator, and it is wrong in a
 * different way in each direction: too broad and a permission refusal becomes a
 * quiet second attempt, too narrow and every write fails after somebody signs
 * out in another tab.
 *
 * Nothing is imported here on purpose. A dependency would take the test with
 * it.
 */

/** What `web/apps/api/src/server.ts` demands on every non-GET /trpc/* request
 *  carrying a live operator cookie. Spelled once here and once there. */
export const ADMIN_CSRF_HEADER = "x-antifailure-admin-csrf";

/**
 * Whether a 403 is the server saying the token was missing or wrong, rather
 * than saying this operator may not do this.
 *
 * Matched on the header's own name, which is the one string both forgery
 * refusals put in their message and which no permission refusal contains. A
 * permission is named `admin.something.verb` and can never look like this.
 */
export function isStaleToken(message: string): boolean {
  return message.includes(ADMIN_CSRF_HEADER);
}
