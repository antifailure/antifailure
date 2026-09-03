// Where a tRPC answer is, pinned.
//
// This suite exists because the console had TWO answers to that question and
// one of them was wrong for as long as the operator portal has existed.
// `query` and `mutate` unwrapped `result.data`; `adminMutate` sent a `/trpc/`
// path through `rest`, which returns the body as it arrives. So every operator
// mutation resolved to the envelope, and every field a caller read off one was
// undefined.
//
// It was invisible for a specific, repeatable reason: nothing read the return
// value of an operator mutation until a page did. The two writes that existed,
// suspending and resuming an organization, threw their result away. A bug that
// only shows up when somebody uses the answer is a bug that ships.
//
// So the knowledge is one function in a module with no imports, which is also
// what makes it testable: the console's tests are literally
// `node --test lib/*.test.ts`, with no bundler and no path aliases, so a module
// importing React or `@/lib/...` cannot be executed here at all. That is not a
// detail. It is why the one piece of the operator client with a rule in it went
// unchecked for its whole life.

import { test, describe } from "node:test";
import assert from "node:assert/strict";
import { unwrapTrpc } from "./trpc-envelope.ts";

describe("unwrapping a tRPC response", () => {
  test("the answer is at result.data, not at the top level", () => {
    const body = { result: { data: { id: "abc", effect: "the account exists" } } };
    assert.deepEqual(unwrapTrpc<{ id: string; effect: string }>(body), {
      id: "abc",
      effect: "the account exists",
    });
  });

  test("the envelope itself is never returned", () => {
    // The regression this file exists for, stated as its own assertion. The
    // failure was not an exception: it was a caller reading `.effect` off an
    // object whose only key is `result`, getting undefined, and rendering the
    // branch for "nothing happened" over a write that had happened.
    const body = { result: { data: { effect: "done" } } };
    const out = unwrapTrpc<{ effect?: string; result?: unknown }>(body);
    assert.equal(out.effect, "done");
    assert.equal(out.result, undefined, "the envelope leaked through as the answer");
  });

  test("a falsy answer survives", () => {
    // `false`, `0` and `""` are answers. An implementation using `||` or a
    // truthiness check would turn each of them into undefined, and a route
    // returning `{provisioned: false}` would read as a route that returned
    // nothing.
    assert.equal(unwrapTrpc<boolean>({ result: { data: false } }), false);
    assert.equal(unwrapTrpc<number>({ result: { data: 0 } }), 0);
    assert.equal(unwrapTrpc<string>({ result: { data: "" } }), "");
    assert.equal(unwrapTrpc<null>({ result: { data: null } }), null);
  });

  test("an array answer comes back as an array", () => {
    // admin.operators.list returns a bare array rather than a page, so this is
    // the shape the most sensitive list in the product arrives in.
    assert.deepEqual(unwrapTrpc<number[]>({ result: { data: [1, 2, 3] } }), [1, 2, 3]);
  });

  test("a body with no result is undefined rather than a throw", () => {
    // What query and mutate have always done. A 2xx with an unexpected shape is
    // a control plane bug, and the screens render a missing value rather than
    // replacing the page with a parse error.
    assert.equal(unwrapTrpc({}), undefined);
    assert.equal(unwrapTrpc({ result: {} }), undefined);
    assert.equal(unwrapTrpc(null), undefined);
    assert.equal(unwrapTrpc(undefined), undefined);
    assert.equal(unwrapTrpc("not json at all"), undefined);
  });
});
