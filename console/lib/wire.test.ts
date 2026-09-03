// The operator portal's mutations send a token, and they send the right one.
//
// WHY A TEST RATHER THAN A CODE REVIEW. This is the defect the portal survey
// names first among the things a build agent gets wrong, and it had already
// shipped: `adminMutate` sent no CSRF header and carried a comment arguing none
// was needed, while `server.ts` refused every operator mutation without one and
// `admincsrf.test.ts` asserted that refusal three ways. Both files were correct
// about themselves and the product was broken between them. The two mutations
// that existed, suspending and resuming an organization, could not work, and
// nothing anywhere went red.
//
// So the behaviour is pinned here, on the console side, in the only directory
// whose tests actually run: `npm test` in console/ is literally
// `node --test lib/*.test.ts`, and a test file anywhere else in this project is
// decoration.
//
// The four cases are the four ways this goes wrong, not four ways it goes
// right: no token sent at all, the wrong header name, a stale token that can
// never recover, and a retry on a refusal that is not about the token, which
// would turn one honest "your role cannot do this" into two.

import { test, describe } from "node:test";
import assert from "node:assert/strict";
import {
  ADMIN_CSRF_HEADER,
  createCsrfGuard,
  isAdminCsrfRefusal,
  trpcData,
} from "./wire.ts";

/** The refusal `server.ts` sends when the header is missing or wrong. The
 *  message names the header, which is what makes it distinguishable from the
 *  route refusing the operator, and admincsrf.test.ts asserts the body matches
 *  /x-antifailure-admin-csrf/. */
function transportRefusal() {
  return Object.assign(new Error(`This request needs the ${ADMIN_CSRF_HEADER} header.`), {
    status: 403,
    code: "FORBIDDEN",
  });
}

/** The refusal the ROUTE sends when the operator's role does not hold the
 *  permission. The same status, deliberately, because that is the pair the
 *  retry has to tell apart. */
function permissionRefusal() {
  return Object.assign(
    new Error("This needs the admin.audit.export permission, which your operator role does not have."),
    { status: 403, code: "FORBIDDEN" },
  );
}

describe("the operator CSRF token", () => {
  test("a mutation sends the token, under the operator header name", async () => {
    let fetched = 0;
    const guard = createCsrfGuard(async () => {
      fetched += 1;
      return "token-one";
    });
    const sent: string[] = [];
    const answer = await guard.send(async (token) => {
      sent.push(token);
      return "ok";
    });
    assert.equal(answer, "ok");
    assert.deepEqual(sent, ["token-one"], "the mutation was sent with no token");
    assert.equal(fetched, 1);
    // The name is the other half. The product console's header is
    // x-antifailure-csrf, derived from a different session secret, and
    // admin-boundary.test.ts pins that one is not valid as the other.
    assert.equal(ADMIN_CSRF_HEADER, "x-antifailure-admin-csrf");
  });

  test("the token is fetched once and reused, not fetched per mutation", async () => {
    // Not an optimisation. A round trip to /v1/admin/session before every
    // mutation is a second request that can fail on its own, and a suspend
    // button that fails because the session probe failed reports the wrong
    // thing to the operator.
    let fetched = 0;
    const guard = createCsrfGuard(async () => {
      fetched += 1;
      return `token-${fetched}`;
    });
    const sent: string[] = [];
    const send = () =>
      guard.send(async (token) => {
        sent.push(token);
        return null;
      });
    await send();
    await send();
    await send();
    assert.equal(fetched, 1);
    assert.deepEqual(sent, ["token-1", "token-1", "token-1"]);
  });

  test("a stale token recovers on the next mutation instead of failing forever", async () => {
    // The failure this retry exists for: the session was replaced while the
    // page was open, so the cached token is wrong from now on. Without the
    // retry the portal stops accepting mutations until somebody reloads, and
    // the symptom is a button that silently does nothing.
    let fetched = 0;
    const guard = createCsrfGuard(async () => {
      fetched += 1;
      return `token-${fetched}`;
    });
    const sent: string[] = [];
    const answer = await guard.send(async (token) => {
      sent.push(token);
      if (token === "token-1") throw transportRefusal();
      return "recovered";
    });
    assert.equal(answer, "recovered");
    assert.deepEqual(sent, ["token-1", "token-2"], "the refused mutation was not retried with a fresh token");
    assert.equal(fetched, 2);
  });

  test("it retries once, not until it gives up", async () => {
    // A loop here would hammer the control plane with a request it is refusing
    // on purpose. A second refusal is the answer.
    let attempts = 0;
    const guard = createCsrfGuard(async () => "always-stale");
    await assert.rejects(
      () =>
        guard.send(async () => {
          attempts += 1;
          throw transportRefusal();
        }),
      /x-antifailure-admin-csrf/,
    );
    assert.equal(attempts, 2, "the guard retried more or less than once");
  });

  test("a permission refusal is not retried, because asking twice gets the same no", async () => {
    // The same status code as the transport refusal, which is why the message
    // is part of the signal. Retrying this one turns a correct refusal into two
    // requests and makes an honest "your role cannot do this" look like a bug.
    let attempts = 0;
    const guard = createCsrfGuard(async () => "token-one");
    await assert.rejects(
      () =>
        guard.send(async () => {
          attempts += 1;
          throw permissionRefusal();
        }),
      /admin\.audit\.export/,
    );
    assert.equal(attempts, 1, "a permission refusal was retried as though it were a stale token");
  });

  test("only the transport refusal is recognised as one", () => {
    assert.equal(isAdminCsrfRefusal(transportRefusal()), true);
    assert.equal(isAdminCsrfRefusal(permissionRefusal()), false);
    // A network failure carries status 0 in this console's error shape, and a
    // retry would send the mutation twice against a control plane that may
    // have received the first one.
    assert.equal(isAdminCsrfRefusal(Object.assign(new Error("unreachable"), { status: 0 })), false);
    assert.equal(isAdminCsrfRefusal(null), false);
    assert.equal(isAdminCsrfRefusal(undefined), false);
    assert.equal(isAdminCsrfRefusal("403"), false);
  });

  test("forgetting the token makes the next mutation fetch a new one", async () => {
    // Signing out ends the session the token was derived from, so keeping it
    // would leave the next operator to sign in on this page sending the
    // previous one's.
    let fetched = 0;
    const guard = createCsrfGuard(async () => {
      fetched += 1;
      return `token-${fetched}`;
    });
    await guard.send(async () => null);
    guard.forget();
    const sent: string[] = [];
    await guard.send(async (token) => {
      sent.push(token);
      return null;
    });
    assert.deepEqual(sent, ["token-2"]);
  });
});

describe("the tRPC envelope", () => {
  // The defect this catches shipped and was invisible. adminMutate returned the
  // envelope with the inner type declared on it, so `suspendTenant` promised
  // `{suspended: boolean}` and delivered an object whose `suspended` was
  // undefined. Nothing failed, because neither caller read the answer. The
  // first one that did downloaded a file containing the word "undefined".
  test("the data is what comes out, not the envelope", () => {
    assert.deepEqual(trpcData({ result: { data: { suspended: true } } }), { suspended: true });
    assert.equal(trpcData<number>({ result: { data: 0 } }), 0);
  });

  test("a body that is not an envelope yields undefined rather than throwing", () => {
    // The control plane has already said something went wrong by this point,
    // and a TypeError here would replace its message with a stack trace.
    assert.equal(trpcData({}), undefined);
    assert.equal(trpcData({ result: {} }), undefined);
    assert.equal(trpcData(null), undefined);
    assert.equal(trpcData(undefined), undefined);
  });
});

describe("the console actually sends it", () => {
  // The unit above proves the guard. This proves the guard is WIRED: a helper
  // that is correct and uncalled is the shape of every defect this project
  // keeps finding. lib/admin.ts cannot be imported here, because it pulls in
  // React and the "@/" alias and node --test has neither, so this reads the
  // source. It is a weaker check and it is checking a different thing: not what
  // the code does, but that the code is reached.
  test("adminMutate passes the header through the guard", async () => {
    const { readFileSync } = await import("node:fs");
    const source = readFileSync(new URL("./admin.ts", import.meta.url), "utf8");
    assert.match(
      source,
      /trpcData</,
      "lib/admin.ts no longer unwraps the tRPC envelope, so every operator mutation answers the envelope",
    );
    assert.match(
      source,
      /createCsrfGuard\(/,
      "lib/admin.ts no longer builds a CSRF guard, so its mutations send no token",
    );
    assert.match(
      source,
      /headers: \{ \[ADMIN_CSRF_HEADER\]: token \}/,
      "adminMutate no longer puts the token in the request headers",
    );
    assert.doesNotMatch(
      source,
      /NO CSRF TOKEN/,
      "the comment claiming no token is needed is back, and it is the claim that was wrong",
    );
  });
});
