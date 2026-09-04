/**
 * The wire format: the one thing the transport was getting wrong about it.
 *
 * WHY THIS IS IN A FILE WITH NO IMPORTS. Not tidiness: it is the only way it
 * can be tested at all. The console's unit tests are literally `node --test
 * lib/*.test.ts`, with no bundler and no `@/` alias, so a test cannot import
 * `lib/api.ts`, which pulls in React. This takes what it needs as an argument,
 * so a test drives the real code rather than parsing it.
 *
 * WHAT WAS WRONG: `mutate` serves the tRPC routers, whose body is an envelope,
 * and a caller that skips the unwrap gets the envelope carrying the TypeScript
 * type of the thing inside it. That is a bug a compiler cannot see. The two
 * operator callers that existed never read the return value, so nothing failed:
 * `suspendTenant` was declared to answer `{suspended: boolean}` and answered an
 * object whose `suspended` was undefined. The first caller to read one, an
 * export that hands the reader a file, downloaded a file containing the word
 * "undefined".
 *
 * The token half of what this module used to hold is in lib/admin-csrf.ts. Two
 * modules were written for that problem in parallel, and only one of them can
 * be the one that runs.
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
