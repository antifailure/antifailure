import { readFileSync } from "node:fs";
import { test } from "node:test";
import assert from "node:assert/strict";

const source = readFileSync(new URL("../app/(app)/plan/page.tsx", import.meta.url), "utf8");

test("the Plan page does not decide access from the cached client role", () => {
  // The behavioral half is in the API auth suite: all three procedures return
  // owner data to a session whose browser snapshot still says member. This
  // guard keeps the page from putting that older answer back in front of the
  // live procedure results.
  assert.equal(
    source.includes("session.data?.role") || source.includes("Your role cannot see this"),
    false,
  );
});
