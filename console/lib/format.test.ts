// The console's formatting helpers.
//
// These are pure functions and they are the last thing anybody thinks to test,
// which is why one of them printed "-30d ago" on the line that tells a paying
// customer when they will next be charged. The direction of an interval is not
// a detail: "30d ago" and "in 30d" are opposite claims about a subscription.

import { test, describe } from "node:test";
import assert from "node:assert/strict";
import { ago, until, usd } from "./format.ts";

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

/*
 * `until`, the forward facing half of the same job.
 *
 * It decides the sentence beside a live impersonation, which is what tells an
 * operator whether to let one lapse or go and stop it. The clock is an argument
 * rather than read from the wall, for the reason the header of this file gives
 * about direction: a formatter that reads the wall clock is a formatter whose
 * test passes at whatever time it happened to run.
 */
describe("until", () => {
  const NOW = new Date("2026-09-02T12:00:00.000Z");
  const inMinutes = (n: number) => new Date(NOW.getTime() + n * 60_000).toISOString();

  test("something that has run out says so rather than counting backwards", () => {
    // The row stays in the live impersonation list until the next read, because
    // that list is filtered server side and the page does not re-query on a
    // timer. So "expired" is a state this string has to be able to say, and
    // "-3 minutes" would read as a defect in the portal rather than as a
    // session that has already stopped working.
    assert.equal(until(inMinutes(-3), NOW), "expired");
    assert.equal(until(inMinutes(0), NOW), "expired");
  });

  test("under a minute is words, not a zero", () => {
    // Flooring renders forty seconds as "0 minutes", which reads as expired and
    // is not. The one place rounding down is wrong is the answer closest to
    // zero.
    assert.equal(until(inMinutes(0.5), NOW), "under a minute");
  });

  test("one minute is singular, for the reason More says organization and organizations", () => {
    assert.equal(until(inMinutes(1), NOW), "1 minute");
    assert.equal(until(inMinutes(2), NOW), "2 minutes");
    assert.equal(until(inMinutes(59), NOW), "59 minutes");
  });

  test("an hour or more reads in hours, because 75 minutes is arithmetic the reader should not do", () => {
    assert.equal(until(inMinutes(60), NOW), "1 hour");
    assert.equal(until(inMinutes(120), NOW), "2 hours");
    assert.equal(until(inMinutes(75), NOW), "1h 15m");
  });

  test("an absent or unparseable instant says unknown rather than NaN", () => {
    assert.equal(until(null, NOW), "unknown");
    assert.equal(until("not a date", NOW), "unknown");
  });
});
