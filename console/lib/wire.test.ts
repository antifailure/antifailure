import { test, describe } from "node:test";
import assert from "node:assert/strict";

import { ApiError, trpcData, trpcResponse } from "./wire.ts";

describe("the tRPC response boundary", () => {
  for (const [name, value] of Object.entries({ object: { suspended: true }, zero: 0, false: false, null: null, empty: "", collection: [] })) {
    test(`preserves valid ${name} data`, () => {
      assert.deepEqual(trpcData({ result: { data: value } }), value);
    });
  }

  for (const [name, body] of Object.entries({ empty: {}, missingData: { result: {} }, null: null, undefined: undefined, array: [], invalidResult: { result: "not data" }, undefinedData: { result: { data: undefined } } })) {
    test(`rejects ${name} envelope as an actionable error`, () => {
      assert.throws(() => trpcData(body), { name: "ApiError", code: "INVALID_RESPONSE", status: 200, message: "The control plane returned an incomplete response. Try again." });
    });
  }

  test("preserves the server's semantic error even on HTTP success", () => {
    assert.throws(() => trpcData({ error: { message: "Only an owner can change this.", data: { code: "FORBIDDEN" } } }), { message: "Only an owner can change this.", code: "FORBIDDEN" });
  });

  test("does not read a contradictory error envelope as success", () => {
    assert.throws(() => trpcData({ error: {}, result: { data: { saved: true } } }), ApiError);
  });

  test("a truncated JSON response fails with a retryable protocol error", async () => {
    await assert.rejects(trpcResponse(new Response('{"result":', { status: 200 })), { name: "ApiError", code: "INVALID_RESPONSE", status: 200 });
  });

  test("the HTTP reader returns the data inside the envelope", async () => {
    assert.deepEqual(await trpcResponse(Response.json({ result: { data: { changed: true } } })), { changed: true });
  });
});
