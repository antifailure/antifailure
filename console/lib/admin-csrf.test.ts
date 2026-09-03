import { test } from "node:test";
import assert from "node:assert/strict";
import { ADMIN_CSRF_HEADER, isStaleToken } from "./admin-csrf.ts";

/*
 * The predicate that decides whether a refused operator mutation is retried.
 *
 * Not a security boundary: the server refuses either way. What it changes is
 * whether the console tries once more with a fresh token or reports the
 * refusal, and it is wrong in a different way in each direction. Too broad and
 * a permission refusal becomes a silent second attempt at something the
 * operator may not do. Too narrow and every operator write fails, with a
 * sentence about a header, the moment somebody signs out in another tab.
 */

test("only the forgery refusal is treated as a stale token", () => {
  // The two refusals the server can answer a 403 with are "this request needs
  // the x-antifailure-admin-csrf header" and "this needs the <permission>
  // permission". Refetching a token fixes the first and can never fix the
  // second, so a match on the header's own name is what separates them: no
  // permission string contains it.
  assert.equal(
    isStaleToken(`This operator request needs the ${ADMIN_CSRF_HEADER} header from the operator session endpoint.`),
    true,
  );
  assert.equal(
    isStaleToken("This needs the admin.impersonation.start permission, which your operator role does not have."),
    false,
  );
  assert.equal(isStaleToken("This operator request came from another site."), false);
});
