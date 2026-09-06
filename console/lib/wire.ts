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
export function trpcData<T>(body: unknown, status = 200, headerRequestId: string | null = null): T {
  if (record(body) && Object.hasOwn(body, "error")) {
    const error = body.error;
    const message = typeof error === "string" ? error : record(error) ? error.message : undefined;
    const code = record(error) && record(error.data) ? error.data.code : undefined;
    if (typeof message === "string" && message.trim()) {
      throw new ApiError(
        message,
        status,
        typeof code === "string" ? code : "UNKNOWN",
        requestIdIn(body) ?? headerRequestId,
      );
    }
    throw incompleteResponse(status, headerRequestId);
  }
  if (!record(body) || !record(body.result) || !Object.hasOwn(body.result, "data") || body.result.data === undefined) {
    throw incompleteResponse(status, headerRequestId);
  }
  return body.result.data as T;
}

/**
 * The request id an error body carries, wherever this control plane puts it.
 *
 * Two places, because two things answer. A tRPC error carries it in
 * `error.data.requestId`, put there by the formatter. A raw route's refusal,
 * the 500 and the cross-site 403, carries it at the top level beside `error`.
 * Null when neither is there, and the caller then falls back to the
 * x-request-id header, which the server sets on every response.
 */
export function requestIdIn(body: unknown): string | null {
  if (!record(body)) return null;
  const error = body.error;
  if (record(error) && record(error.data) && typeof error.data.requestId === "string") return error.data.requestId;
  if (typeof body.requestId === "string") return body.requestId;
  return null;
}

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  /**
   * The control plane's id for the request that failed, the one on its log
   * line. Shown on the error card as the reference to quote, because for an
   * internal failure the message is a fixed sentence by design and the id is
   * the only thing that ties the card to the log. Null when the request never
   * reached the control plane, or when a response carried none.
   */
  readonly requestId: string | null;
  constructor(message: string, status: number, code: string, requestId: string | null = null) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.requestId = requestId;
  }
}

function incompleteResponse(status: number, requestId: string | null = null): ApiError {
  return new ApiError(
    "The control plane returned an incomplete response. Try again.",
    status,
    "INVALID_RESPONSE",
    requestId,
  );
}

/** Parsing and envelope validation are shared by queries and mutations. */
export async function trpcResponse<T>(response: Response): Promise<T> {
  const headerRequestId = response.headers.get("x-request-id");
  let body: unknown;
  try {
    body = await response.json();
  } catch {
    throw incompleteResponse(response.status, headerRequestId);
  }
  return trpcData<T>(body, response.status, headerRequestId);
}
