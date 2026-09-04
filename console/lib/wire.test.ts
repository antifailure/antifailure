// The tRPC envelope, and the unwrap that was missing from it.
//
// The token half of this suite moved to lib/admin-csrf.test.ts, which asserts
// the same properties and several more. What is left here is the half that
// module does not cover: `mutate` answers the body the control plane sent, and
// a caller reading a field off it is reading the field it asked for rather than
// a field of the envelope around it.

import { test, describe } from "node:test";
import assert from "node:assert/strict";

import { trpcData } from "./wire.ts";

describe("the envelope", () => {
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
