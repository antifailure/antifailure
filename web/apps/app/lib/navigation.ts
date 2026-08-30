// Telling a navigation apart from a failure.
//
// Its own module, with nothing imported, because `next/navigation` cannot be
// resolved outside the bundler and a predicate this important has to be
// testable by `node --test` without one.
/**
 * Whether an error is Next.js asking for a navigation rather than a failure.
 *
 * `redirect()` and `notFound()` work by throwing. The exception carries a
 * `digest` that the framework recognises on its way out of the render, and any
 * `catch` between the call and the framework swallows it. That is what
 * happened here: every page wrapped `requireActor` in a try/catch to turn an
 * unreachable API into a readable message, and the same catch turned "you are
 * not signed in, go to /login" into a full page error reading "The control
 * plane did not answer. Error: NEXT_REDIRECT".
 *
 * So a signed out visitor could not reach the sign-in page from anywhere in
 * the application. It was found by an agent, which saw a page with nothing on
 * it to press.
 *
 * Checked by digest rather than by importing Next's internal predicate,
 * because that predicate has moved between minor versions and the digest
 * format is what the framework itself matches on.
 */
export function isNavigation(err: unknown): boolean {
  if (typeof err !== "object" || err === null || !("digest" in err)) return false;
  const digest = (err as { digest?: unknown }).digest;
  return (
    typeof digest === "string" &&
    (digest.startsWith("NEXT_REDIRECT") || digest === "NEXT_NOT_FOUND")
  );
}
