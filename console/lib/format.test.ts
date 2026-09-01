// The console's formatting helpers.
//
// These are pure functions and they are the last thing anybody thinks to test,
// which is why one of them printed "-30d ago" on the line that tells a paying
// customer when they will next be charged. The direction of an interval is not
// a detail: "30d ago" and "in 30d" are opposite claims about a subscription.

import { test, describe } from "node:test";
import assert from "node:assert/strict";
import { ago, usd } from "./format.ts";

describe("how far away a time is", () => {
  const now = Date.now();
  const at = (ms: number) => new Date(now + ms).toISOString();

  test("something that has happened reads as the past", () => {
    assert.equal(ago(at(-30 * 60_000)), "30m ago");
    assert.equal(ago(at(-5 * 3_600_000)), "5h ago");
    assert.equal(ago(at(-30 * 86_400_000)), "30d ago");
  });

  test("something that has not happened yet reads as the future", () => {
    // The defect, in the three units it could have reached. A renewal date is
    // a future date, and the sign was being printed rather than read: the
    // number came out negative and the word after it still said "ago".
    assert.equal(ago(at(30 * 60_000)), "in 30m");
    assert.equal(ago(at(5 * 3_600_000)), "in 5h");
    assert.equal(ago(at(30 * 86_400_000)), "in 30d");
  });

  test("no sign is ever printed, in either direction", () => {
    for (const offset of [-1, 1]) {
      for (const unit of [90_000, 5 * 3_600_000, 30 * 86_400_000]) {
        const rendered = ago(at(offset * unit));
        assert.equal(rendered.includes("-"), false, `"${rendered}" carries a sign`);
      }
    }
  });

  test("either side of this instant is just now, not a rounding artifact", () => {
    assert.equal(ago(at(-1_000)), "just now");
    assert.equal(ago(at(1_000)), "just now");
  });

  test("nothing to say about nothing", () => {
    assert.equal(ago(null), "");
    assert.equal(ago(undefined), "");
    assert.equal(ago("not a date"), "");
  });
});

describe("money", () => {
  test("a whole number of dollars keeps its cents", () => {
    assert.equal(usd(7500), "$7,500.00");
  });
  test("an absent amount is not zero, because zero is a real price", () => {
    assert.equal(usd(null), "--");
    assert.equal(usd(undefined), "--");
    assert.equal(usd(0), "$0.00");
  });
});
