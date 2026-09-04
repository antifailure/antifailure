import { test } from "node:test";
import assert from "node:assert/strict";
import { ROLE_PERMISSIONS, may, mayReadAnalytics } from "./roles.ts";

/*
 * The invariants the lapsed-exits screen is built on.
 *
 * This table is a copy of ROLE_PERMISSIONS in web/apps/api/src/permissions.ts,
 * and the console cannot import the original: it is a separate build with its
 * own lockfile. So the copy is where the drift would happen, and these are the
 * two facts the screen would render wrongly if it drifted. Neither is a
 * security boundary, and that is the point. The server refuses regardless; what
 * a stale copy breaks is which controls a person is OFFERED, which on this
 * screen is the difference between a way out and a dead end.
 */

test("every role can close its own account", () => {
  // The exits page reads under account.close for exactly this reason. If any
  // role lost it, that role would reach a page with no exit on it at all, and
  // an admin holding data.export and sessions.manage would have no route to
  // either. Written per role rather than as a loop so a failure names the one
  // that broke.
  for (const role of Object.keys(ROLE_PERMISSIONS)) {
    assert.equal(may(role, "account.close"), true, `${role} cannot close its account`);
  }
});

test("billing is the owner's alone, which is why the lapsed screen makes the Plan link conditional", () => {
  assert.equal(may("owner", "billing.manage"), true);
  for (const role of ["admin", "member", "viewer"]) {
    assert.equal(may(role, "billing.manage"), false, `${role} unexpectedly holds billing.manage`);
  }
});

test("the four exits are held by the roles the screen renders them for", () => {
  assert.equal(may("admin", "data.export"), true);
  assert.equal(may("admin", "sessions.manage"), true);
  // The one that decided where the exempt read had to live: an admin on a
  // lapsed plan holds two exits and NOT this, so a read under
  // organization.delete would have left them with no page.
  assert.equal(may("admin", "organization.delete"), false);
  assert.equal(may("member", "data.export"), false);
  assert.equal(may("member", "sessions.manage"), false);
  assert.equal(may("viewer", "sessions.manage"), false);
});

test("an unknown or absent role is refused rather than defaulted", () => {
  // The failure this codebase kept hitting today is an absent answer read as a
  // satisfied condition. A session with no role must light up no control.
  assert.equal(may(null, "account.close"), false);
  assert.equal(may(undefined, "data.export"), false);
  assert.equal(may("", "organization.delete"), false);
  assert.equal(may("superuser", "billing.manage"), false);
});

test("analytics needs both the operator organization and a permitted role", () => {
  assert.deepEqual(
    [
      mayReadAnalytics({ analyticsOperator: true, role: "owner" }),
      mayReadAnalytics({ analyticsOperator: true, role: "admin" }),
      mayReadAnalytics({ analyticsOperator: true, role: "member" }),
      mayReadAnalytics({ analyticsOperator: true, role: "viewer" }),
      mayReadAnalytics({ analyticsOperator: false, role: "owner" }),
      mayReadAnalytics({ role: "owner" }),
    ],
    [true, true, false, false, false, false],
  );
});
