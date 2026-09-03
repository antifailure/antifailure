/**
 * Where a tRPC answer actually is.
 *
 * ONE DEFINITION, because there were two and one of them was wrong. A tRPC
 * response is an envelope: the value the caller wants is at `result.data`, not
 * at the top level. `query` and `mutate` in lib/api.ts unwrap it. `rest` does
 * not, because it speaks to the plain JSON endpoints, where the body IS the
 * answer.
 *
 * The operator client called `rest` for a `/trpc/` path. So every operator
 * mutation resolved to `{result: {data: ...}}` and every field a caller read
 * off it was undefined. Creating an operator wrote the row, wrote its critical
 * audit entry, and left the panel showing its own form, because the branch that
 * renders the server's sentence about what it did was keyed on a field that was
 * never there. The obvious next move for whoever pressed the button is to press
 * it again.
 *
 * It survived because nothing read the return value of an operator mutation
 * until a page did, and because the two halves are individually correct: `rest`
 * is right about plain endpoints and `adminMutate` was right that it needed
 * `rest`'s header hook. Only the pair was wrong.
 *
 * So the knowledge lives here, in a module with no imports, which is also what
 * makes it the only part of the transport the console's test runner can
 * execute: the tests are literally `node --test lib/*.test.ts`, with no bundler
 * and no path aliases, so anything importing React or `@/lib/...` cannot be
 * tested at all.
 */

/** A tRPC response, as it arrives. */
export interface TrpcEnvelope<T> {
  result?: { data?: T };
}

/**
 * The answer inside a tRPC response.
 *
 * Returns undefined rather than throwing on a body with no `result`, which is
 * what `query` and `mutate` have always done: a 2xx with an unexpected shape is
 * a control plane bug, and the screens render "not measured" for a missing
 * value rather than replacing the page with a parse error.
 */
export function unwrapTrpc<T>(body: unknown): T {
  return (body as TrpcEnvelope<T> | null | undefined)?.result?.data as T;
}
