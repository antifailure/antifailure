import { describe, test } from "node:test";
import assert from "node:assert/strict";
import { approvalDestination, consentParameters, pendingConsent } from "./mcp-consent.ts";

const org = "organization-one";
const pending = {
  clientName: "Development assistant", scopes: ["mcp:read", "mcp:write"],
  organization: org, organizationName: "Example team",
  redirectUri: "https://client.example/callback", expiresInDays: 90,
};

describe("the consent request the person actually approves", () => {
  test("preserves encoded OAuth values without changing their meaning", () => {
    assert.deepEqual(Object.fromEntries(consentParameters("client_id=one&state=a%2Bb%26c&scope=mcp%3Aread+mcp%3Awrite")), { client_id: "one", state: "a+b&c", scope: "mcp:read mcp:write" });
  });
  test("refuses duplicated parameters before approval can collapse them", () => {
    assert.throws(() => consentParameters("client_id=one&client_id=two"), /repeats a parameter/);
  });
  test("shows the server's actual client, organization and permissions", () => {
    assert.deepEqual(pendingConsent(pending, org), pending);
  });
  test("a read-only request never gains a write scope", () => {
    assert.deepEqual(pendingConsent({ ...pending, scopes: ["mcp:read"] }, org).scopes, ["mcp:read"]);
  });
  test("a malformed optional organization name is not rendered as an object", () => {
    assert.equal(pendingConsent({ ...pending, organizationName: {} }, org).organizationName, undefined);
  });
  for (const [name, value] of Object.entries({
    missing: null,
    client: { ...pending, clientName: " " },
    organization: { ...pending, organization: "another-organization" },
    emptyScopes: { ...pending, scopes: [] },
    unknownScope: { ...pending, scopes: ["admin:all"] },
    repeatedScope: { ...pending, scopes: ["mcp:read", "mcp:read"] },
    lifetime: { ...pending, expiresInDays: 365 },
  })) {
    test(`refuses incomplete consent: ${name}`, () => {
      assert.throws(() => pendingConsent(value, org), /incomplete or does not belong/);
    });
  }
  test("an invalid pending address produces a human error", () => {
    assert.throws(() => pendingConsent({ ...pending, redirectUri: "not a url" }, org), /invalid return address/);
  });
  for (const [name, redirectUri] of Object.entries({ script: "javascript:alert(1)", credentials: "https://user:pass@client.example/callback", fragment: "https://client.example/callback#other" })) {
    test(`refuses unsafe pending address: ${name}`, () => {
      assert.throws(() => pendingConsent({ ...pending, redirectUri }, org), /unsupported return address/);
    });
  }
});

describe("approval returns only to the address and request shown", () => {
  const uri = "https://client.example/callback?client=desktop";
  const valid = `${uri}&code=issued-code&state=a%2Bb%26c`;
  test("preserves the registered query and encoded state", () => {
    assert.equal(approvalDestination({ redirect: valid }, uri, "a+b&c"), valid);
  });
  test("a request without state does not invent one", () => {
    assert.equal(approvalDestination({ redirect: `${uri}&code=issued-code` }, uri, null), `${uri}&code=issued-code`);
  });
  test("an incomplete approval cannot navigate", () => {
    assert.throws(() => approvalDestination({}, uri, null), /response was incomplete/);
  });
  test("an invalid approval address produces a human error", () => {
    assert.throws(() => approvalDestination({ redirect: "not a url" }, uri, null), /invalid address/);
  });
  for (const [name, redirect] of Object.entries({
    origin: valid.replace("client.example", "different.example"),
    path: valid.replace("/callback", "/other"),
    registeredQuery: valid.replace("client=desktop", "client=other"),
    duplicateRegisteredQuery: valid + "&client=other",
    extraParameter: valid + "&next=https%3A%2F%2Fdifferent.example",
    missingCode: valid.replace("&code=issued-code", ""),
    emptyCode: valid.replace("issued-code", ""),
    repeatedCode: valid + "&code=other",
    wrongState: valid.replace("a%2Bb%26c", "other"),
    repeatedState: valid + "&state=a%2Bb%26c",
    credentials: valid.replace("https://", "https://user:pass@"),
    fragment: valid + "#other",
  })) {
    test(`rejects a different approval: ${name}`, () => {
      assert.throws(() => approvalDestination({ redirect }, uri, "a+b&c"), /different address or request/);
    });
  }
  test("an unsolicited state cannot replace a request without state", () => {
    assert.throws(() => approvalDestination({ redirect: valid }, uri, null), /different address or request/);
  });
});
