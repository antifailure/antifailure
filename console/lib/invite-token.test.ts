import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import {
  INVITE_TOKEN_KEY,
  forgetInviteToken,
  inviteToken,
  rememberInviteToken,
  type TokenStore,
} from "./invite-token.ts";

/** A storage that behaves, and one that throws the way a private window does. */
function store(initial: Record<string, string> = {}): TokenStore & { values: Record<string, string> } {
  const values = { ...initial };
  return {
    values,
    getItem: (key) => values[key] ?? null,
    setItem: (key, value) => {
      values[key] = value;
    },
    removeItem: (key) => {
      delete values[key];
    },
  };
}

function hostile(): TokenStore {
  return {
    getItem() {
      throw new DOMException("The operation is insecure.", "SecurityError");
    },
    setItem() {
      throw new DOMException("The operation is insecure.", "SecurityError");
    },
    removeItem() {
      throw new DOMException("The operation is insecure.", "SecurityError");
    },
  };
}

test("the token in the link is the one used, and it is kept for the sign-in trip", () => {
  const s = store();
  assert.equal(inviteToken("abc", s), "abc");
  rememberInviteToken(s, "abc");
  assert.equal(s.values[INVITE_TOKEN_KEY], "abc");
});

test("after sign-in the token comes back from the tab, not from the URL", () => {
  // This is the case the fix turns on: the control plane strips a token
  // parameter out of a return target, so the browser comes back to /invite with
  // nothing in the query and the invitation must still be acceptable.
  const s = store({ [INVITE_TOKEN_KEY]: "abc" });
  assert.equal(inviteToken(null, s), "abc");
  assert.equal(inviteToken("", s), "abc");
});

test("a token in the link wins over a stale one from an earlier invitation", () => {
  const s = store({ [INVITE_TOKEN_KEY]: "old" });
  assert.equal(inviteToken("new", s), "new");
});

test("no token anywhere is an empty string, which the page reports", () => {
  assert.equal(inviteToken(null, store()), "");
  assert.equal(inviteToken("   ", store()), "");
  assert.equal(inviteToken(null, null), "");
});

test("a storage that refuses is not an exception on a sign-in screen", () => {
  assert.equal(inviteToken(null, hostile()), "");
  assert.equal(inviteToken("abc", hostile()), "abc");
  rememberInviteToken(hostile(), "abc");
  forgetInviteToken(hostile());
});

test("the invite page asks to come back to /invite with no token in it", () => {
  // A structural guard, paired with the behavioural tests above and with the
  // stored-row assertions in the api suites. The control plane strips a token
  // parameter out of any return target, so a page that still put one there
  // would send people back to an invitation with no token and no way to accept.
  const page = readFileSync(new URL("../app/invite/page.tsx", import.meta.url), "utf8");
  assert.match(page, /redirect_to=\$\{encodeURIComponent\("\/invite"\)\}/);
  assert.doesNotMatch(
    page,
    /redirect_to=[^\n]*token=/,
    "the invite page is putting the raw token back into the return target",
  );
});

test("accepting forgets it", () => {
  const s = store({ [INVITE_TOKEN_KEY]: "abc" });
  forgetInviteToken(s);
  assert.equal(inviteToken(null, s), "");
});
