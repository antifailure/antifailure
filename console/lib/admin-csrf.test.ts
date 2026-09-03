// The operator write path's cross-site token.
//
// This suite exists because the behaviour it pins was WRONG for the whole life
// of the operator portal and every check in the repository passed. The client
// argued in a comment that the token was unnecessary; the server refused every
// write that arrived without it; and no test on either side could see the other
// half. admincsrf.test.ts proves the server demands the header. Nothing proved
// the client sends it.
//
// So these assertions are deliberately about what goes ON THE WIRE, not about
// what the code intends. Every one of them inspects the headers an attempt was
// actually made with.
//
// The retry is a two event flow, so it is tested as one: not only "a 403 is
// retried" but every ordering a caller can reach, including the two that must
// NOT retry. A retry rule tested only on its happy path is how a refused write
// becomes a loop, or how a partly applied write gets applied twice.

import { test, describe } from "node:test";
import assert from "node:assert/strict";
import { ADMIN_CSRF_HEADER, createAdminCsrf, isForbidden } from "./admin-csrf.ts";

/** The shape lib/api.ts throws, narrowed to what the retry rule reads. */
function refusal(status: number) {
  return Object.assign(new Error(`refused with ${status}`), { status });
}

/** Records every set of headers an attempt was made with, in order. */
function recorder(outcomes: (Error | "ok")[]) {
  const sent: Record<string, string>[] = [];
  let call = 0;
  return {
    sent,
    get calls() {
      return call;
    },
    attempt: async (headers: Record<string, string>) => {
      sent.push(headers);
      const outcome = outcomes[call++] ?? "ok";
      if (outcome !== "ok") throw outcome;
      return "done";
    },
  };
}

describe("the operator write path attaches its own token", () => {
  test("the header is the admin one, never the tenant one", () => {
    // Pinned as a literal rather than compared to itself. The tenant header is
    // x-antifailure-csrf, one word shorter, and sending it produces a 403 that
    // reads like a permissions problem rather than a transport one.
    assert.equal(ADMIN_CSRF_HEADER, "x-antifailure-admin-csrf");
  });

  test("a write carries the token the session endpoint minted", async () => {
    const csrf = createAdminCsrf(async () => "token-one");
    const r = recorder(["ok"]);
    await csrf.send(r.attempt);
    assert.deepEqual(r.sent, [{ "x-antifailure-admin-csrf": "token-one" }]);
  });

  test("the token is read once and reused across writes", async () => {
    // A fetch per mutation is a second round trip on every operator action, and
    // the token does not change between them.
    let reads = 0;
    const csrf = createAdminCsrf(async () => {
      reads++;
      return "token-one";
    });
    const r = recorder(["ok", "ok", "ok"]);
    await csrf.send(r.attempt);
    await csrf.send(r.attempt);
    await csrf.send(r.attempt);
    assert.equal(reads, 1);
    assert.equal(r.calls, 3);
  });

  test("no token means no header, rather than an empty one", async () => {
    // The server's message distinguishes a missing header from a wrong one. An
    // empty string lands in the second bucket and misdirects whoever reads the
    // refusal.
    const csrf = createAdminCsrf(async () => null);
    const r = recorder(["ok"]);
    await csrf.send(r.attempt);
    assert.deepEqual(r.sent, [{}]);
  });
});

describe("the retry, in every ordering a caller can reach", () => {
  test("a 403 refetches the token and retries exactly once", async () => {
    // The one case the retry exists for: the cached token outlived the session
    // that minted it, which is what a sign out and sign in in the same tab
    // does.
    const tokens = ["stale", "fresh"];
    let reads = 0;
    const csrf = createAdminCsrf(async () => tokens[reads++] ?? null);
    const r = recorder([refusal(403), "ok"]);

    const result = await csrf.send(r.attempt);

    assert.equal(result, "done");
    assert.equal(r.calls, 2, "exactly two attempts, not a loop");
    assert.deepEqual(r.sent, [
      { "x-antifailure-admin-csrf": "stale" },
      { "x-antifailure-admin-csrf": "fresh" },
    ]);
  });

  test("a 403 with an unchanged token does not retry", async () => {
    // Asking the identical question gets the identical answer. Without this the
    // portal sends every genuinely refused write twice.
    const csrf = createAdminCsrf(async () => "same");
    const r = recorder([refusal(403), "ok"]);

    await assert.rejects(() => csrf.send(r.attempt), /refused with 403/);
    assert.equal(r.calls, 1);
  });

  test("a 403 with no token available does not retry", async () => {
    const csrf = createAdminCsrf(async () => null);
    const r = recorder([refusal(403), "ok"]);

    await assert.rejects(() => csrf.send(r.attempt), /refused with 403/);
    assert.equal(r.calls, 1);
  });

  test("a second 403 on the retry is raised, not retried again", async () => {
    const tokens = ["one", "two", "three"];
    let reads = 0;
    const csrf = createAdminCsrf(async () => tokens[reads++] ?? null);
    const r = recorder([refusal(403), refusal(403), "ok"]);

    await assert.rejects(() => csrf.send(r.attempt), /refused with 403/);
    assert.equal(r.calls, 2);
  });

  test("a 500 is raised without a refetch and without a retry", async () => {
    // Every operator mutation writes its audit entry inside its own
    // transaction, so a blind retry of a server error is a second recorded
    // action against the same account.
    let reads = 0;
    const csrf = createAdminCsrf(async () => {
      reads++;
      return "token-one";
    });
    const r = recorder([refusal(500), "ok"]);

    await assert.rejects(() => csrf.send(r.attempt), /refused with 500/);
    assert.equal(r.calls, 1);
    assert.equal(reads, 1, "the token was read once, for the first attempt only");
  });

  test("a 400 from the route itself is raised untouched", async () => {
    // The transport passing and the route refusing on its own terms is the
    // normal case for a bad input, and it must not look like a token problem.
    const csrf = createAdminCsrf(async () => "token-one");
    const r = recorder([refusal(400), "ok"]);

    await assert.rejects(() => csrf.send(r.attempt), /refused with 400/);
    assert.equal(r.calls, 1);
  });

  test("a network rejection carrying no status is raised untouched", async () => {
    // lib/api.ts turns an unreachable control plane into an error with status 0.
    const csrf = createAdminCsrf(async () => "token-one");
    const r = recorder([refusal(0), "ok"]);

    await assert.rejects(() => csrf.send(r.attempt), /refused with 0/);
    assert.equal(r.calls, 1);
  });
});

describe("forgetting a token whose session is gone", () => {
  test("the next write fetches again rather than sending the old one", async () => {
    // Sign-in and sign-out call this. Without it the first write after a
    // re-sign-in sends a token minted for the previous session, is refused, and
    // recovers on the retry: correct, and one pointless refusal slower than it
    // needs to be, at the moment somebody is most likely to be in a hurry.
    let minted = 0;
    const csrf = createAdminCsrf(async () => `token-${++minted}`);
    const sent: string[] = [];
    const attempt = async (headers: Record<string, string>) => {
      sent.push(headers[ADMIN_CSRF_HEADER]!);
      return "ok";
    };

    await csrf.send(attempt);
    await csrf.send(attempt);
    assert.deepEqual(sent, ["token-1", "token-1"], "the token is meant to be cached");

    csrf.forget();
    await csrf.send(attempt);
    assert.deepEqual(sent, ["token-1", "token-1", "token-2"]);
    assert.equal(minted, 2, "forgetting fetched exactly one more token");
  });

  test("forgetting twice with no write in between costs nothing", async () => {
    let minted = 0;
    const csrf = createAdminCsrf(async () => `token-${++minted}`);
    csrf.forget();
    csrf.forget();
    assert.equal(minted, 0, "forgetting must not fetch; it only drops");
  });
});

describe("recognising the server's refusal", () => {
  test("only a 403 counts", () => {
    assert.equal(isForbidden(refusal(403)), true);
    assert.equal(isForbidden(refusal(401)), false);
    assert.equal(isForbidden(refusal(500)), false);
  });

  test("something that is not an error at all does not count", () => {
    // The check is structural so this module needs no import. That is worth a
    // test, because a structural check is exactly the kind that quietly says
    // yes to the wrong shape.
    assert.equal(isForbidden(null), false);
    assert.equal(isForbidden(undefined), false);
    assert.equal(isForbidden("403"), false);
    assert.equal(isForbidden({ status: "403" }), false);
  });
});
