/**
 * The wire format, and the two things the transport was getting wrong about it.
 *
 * WHY THESE ARE IN A FILE WITH NO IMPORTS. Not tidiness: it is the only way
 * they can be tested at all. `console`'s unit tests are literally `node --test
 * lib/*.test.ts`, with no bundler and no `@/` alias, so a test cannot import
 * `lib/api.ts` or `lib/admin.ts`; both pull in React. Everything here is
 * dependency free and takes what it needs as arguments, so a test drives the
 * real code rather than parsing it. A structural test that greps the source for
 * a header name proves the string is present and nothing about when it is sent,
 * which is the half that was wrong.
 *
 * THE FIRST THING THAT WAS WRONG: `adminMutate` unwrapped nothing. `rest` is
 * for the handful of plain JSON endpoints, so it returns the body as it stands.
 * The operator router is tRPC, whose body is an envelope, so every operator
 * mutation returned `{result: {data: ...}}` typed as the thing inside it. The
 * two callers that existed never read the return value, so nothing failed:
 * `suspendTenant` was declared to answer `{suspended: boolean}` and answered an
 * object whose `suspended` was undefined. The first caller to read one, an
 * export that hands the reader a file, downloaded a file containing the word
 * "undefined".
 *
 * THE SECOND: `adminMutate` sent no token and carried a comment arguing it
 * did not need one, because the operator cookie is SameSite=Strict. The control
 * plane disagrees and always has: the `/trpc/*` middleware refuses every
 * non-GET request carrying a valid operator cookie unless it presents a
 * matching `x-antifailure-admin-csrf`, and `admincsrf.test.ts` asserts that
 * three ways. Nothing in this directory ever fetched the token, so every
 * operator mutation in the portal answered 403 and the two that existed,
 * suspending and resuming an organization, were buttons that could not work.
 *
 * The server is right on the merits too, which is why this is the side that
 * changed. SameSite is SITE scoped rather than origin scoped, so a subdomain an
 * attacker controls is inside it, and these are the highest value mutations in
 * the product.
 */

/**
 * Unwraps a tRPC response body.
 *
 * A query and a mutation answer `{result: {data: T}}`, and a caller that skips
 * this gets the envelope with the right TypeScript type on it, which is the
 * shape of bug that survives a compiler and a review.
 *
 * Optional all the way down rather than asserted, because a body that is not
 * the expected shape has already failed and throwing a TypeError here would
 * replace whatever the control plane said with a stack trace.
 */
export function trpcData<T>(body: unknown): T {
  return (body as { result?: { data?: T } } | null)?.result?.data as T;
}

/** The header the control plane demands. Its own name, not the product
 *  console's `x-antifailure-csrf`: two derivations over two different session
 *  secrets, and `admin-boundary.test.ts` pins that a token from one is not a
 *  valid token for the other. */
export const ADMIN_CSRF_HEADER = "x-antifailure-admin-csrf";

/**
 * Whether a rejection is the transport refusing the token, rather than the
 * route refusing the operator.
 *
 * BOTH HALVES MATTER. A 403 from the ROUTE is an operator whose role does not
 * hold the permission, and retrying that asks the same question again, gets the
 * same answer, and looks like a bug while being a correct refusal. So the
 * status alone is not the signal: `server.ts` names the header in the body of
 * the refusal it sends, which `admincsrf.test.ts` asserts, and that is what
 * tells the two apart.
 *
 * Duck typed rather than `instanceof ApiError`, because importing the error
 * class would import the aliased transport and this file would stop being
 * testable, which is the whole reason it exists. Status and message are the
 * contract either way.
 */
export function isAdminCsrfRefusal(err: unknown): boolean {
  const e = err as { status?: unknown; message?: unknown } | null;
  return (
    e !== null &&
    typeof e === "object" &&
    e.status === 403 &&
    typeof e.message === "string" &&
    e.message.toLowerCase().includes(ADMIN_CSRF_HEADER)
  );
}

export interface CsrfGuard {
  /** Sends a request with the token, fetching one first if there is none, and
   *  retrying ONCE if the transport refuses the token specifically. */
  send<T>(attempt: (token: string) => Promise<T>): Promise<T>;
  /** Drops the cached token. For sign-out, and for the test. */
  forget(): void;
}

/**
 * Holds the token and decides when to go back for a new one.
 *
 * ONE RETRY, NEVER A LOOP. The token is derived from the session, so a session
 * that was replaced while a page was open leaves a cached token that is wrong
 * forever. Without the retry the portal stops accepting mutations until
 * somebody reloads, and the symptom is a button that silently does nothing,
 * which is the hardest kind of failure to report. With a loop, a genuinely
 * refused request would hammer the control plane. A second refusal is the
 * answer.
 *
 * The cache is a closure rather than React state because the mutation helper is
 * called from event handlers all over the portal, and threading a token through
 * every one of them is how one of them ends up without it.
 */
export function createCsrfGuard(fetchToken: () => Promise<string | null>): CsrfGuard {
  let token: string | null = null;
  return {
    forget() {
      token = null;
    },
    async send<T>(attempt: (t: string) => Promise<T>): Promise<T> {
      if (token === null) token = await fetchToken();
      try {
        return await attempt(token ?? "");
      } catch (err) {
        if (!isAdminCsrfRefusal(err)) throw err;
        token = await fetchToken();
        return attempt(token ?? "");
      }
    },
  };
}
