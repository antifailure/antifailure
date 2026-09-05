import { readFileSync } from "node:fs";
import { test } from "node:test";
import assert from "node:assert/strict";

const source = readFileSync(new URL("../components/Shell.tsx", import.meta.url), "utf8");

test("the mobile drawer returns focus to the control that opened it", () => {
  // The browser check proves the interaction. This guard keeps both ends of
  // the focus handoff on the same ref when the drawer is edited later.
  assert.deepEqual(
    [source.includes("ref={opener}"), source.includes("opener.current?.focus()")],
    [true, true],
  );
});
