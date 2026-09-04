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
 * HTTP success does not prove that this envelope exists. Returning undefined
 * made the hook report ready while Loaded kept a skeleton on screen forever.
 * Refuse a missing envelope with the error the existing retry UI understands.
 * Null, false, zero and empty collections remain valid endpoint results.
 */
export function trpcData<T>(body: unknown, status = 200): T {
  if (record(body) && Object.hasOwn(body, "error")) {
    const error = body.error;
    const message = typeof error === "string" ? error : record(error) ? error.message : undefined;
    const code = record(error) && record(error.data) ? error.data.code : undefined;
    if (typeof message === "string" && message.trim()) {
      throw new ApiError(message, status, typeof code === "string" ? code : "UNKNOWN");
    }
    throw incompleteResponse(status);
  }
  if (!record(body) || !record(body.result) || !Object.hasOwn(body.result, "data") || body.result.data === undefined) {
    throw incompleteResponse(status);
  }
  return body.result.data as T;
}

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  constructor(message: string, status: number, code: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

function incompleteResponse(status: number): ApiError {
  return new ApiError("The control plane returned an incomplete response. Try again.", status, "INVALID_RESPONSE");
}

/** Parsing and envelope validation are shared by queries and mutations. */
export async function trpcResponse<T>(response: Response): Promise<T> {
  let body: unknown;
  try {
    body = await response.json();
  } catch {
    throw incompleteResponse(response.status);
  }
  return trpcData<T>(body, response.status);
}
