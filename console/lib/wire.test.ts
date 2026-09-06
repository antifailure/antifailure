import { test, describe } from "node:test";
import assert from "node:assert/strict";

import { ApiError, requestIdIn, trpcData, trpcResponse } from "./wire.ts";

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

describe("the reference a person can quote", () => {
  // The formatter puts the id under error.data.requestId; the raw routes put
  // it at the top level beside error. Both are the same id as the header, and
  // the header is the fallback for a body that carries none.
  test("a tRPC error carries its request id onto the ApiError", () => {
    assert.throws(
      () => trpcData({ error: { message: "Something went wrong on the control plane.", data: { code: "INTERNAL_SERVER_ERROR", requestId: "0f4c-quote-me" } } }, 500),
      (e: unknown) => e instanceof ApiError && e.requestId === "0f4c-quote-me",
    );
  });

  test("a raw refusal carries its top-level request id", () => {
    assert.equal(requestIdIn({ error: { code: "AF-CP-004", message: "refused" }, requestId: "top-level-id" }), "top-level-id");
  });

  test("the body's id wins over the header, and the header stands in when the body has none", () => {
    assert.throws(
      () => trpcData({ error: { message: "no", data: { code: "FORBIDDEN", requestId: "from-body" } } }, 403, "from-header"),
      (e: unknown) => e instanceof ApiError && e.requestId === "from-body",
    );
    assert.throws(
      () => trpcData({ error: { message: "no", data: { code: "FORBIDDEN" } } }, 403, "from-header"),
      (e: unknown) => e instanceof ApiError && e.requestId === "from-header",
    );
  });

  test("an error with no id anywhere is null rather than a made-up string", () => {
    assert.equal(requestIdIn({ error: { message: "no" } }), null);
    assert.equal(requestIdIn("not a record"), null);
    assert.throws(() => trpcData({ error: { message: "no" } }), (e: unknown) => e instanceof ApiError && e.requestId === null);
  });

  test("the HTTP reader takes the id off the x-request-id header when the body is unusable", async () => {
    await assert.rejects(
      trpcResponse(new Response('{"result":', { status: 500, headers: { "x-request-id": "hdr-1" } })),
      (e: unknown) => e instanceof ApiError && e.code === "INVALID_RESPONSE" && e.requestId === "hdr-1",
    );
  });
});
