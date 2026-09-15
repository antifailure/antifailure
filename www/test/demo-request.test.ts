// The demo-request form's logic, against the inputs it actually meets.
//
// The form on /request-demo posts to POST /v1/leads, the same endpoint the
// enterprise form uses, and the server's validateLead is tested in the api
// suite. What is NEW and lives here is the client half: the guard that refuses
// a submission before the network, and the composition that folds the Harvey
// fields the leads table has no column for into the message with a "demo"
// source. Both are pure functions in lib/demo-request.ts so they can be run
// without a browser, and this is what runs them.

import { test } from "node:test";
import assert from "node:assert/strict";
import {
  composeDemoLead,
  looksLikeEmail,
  validateDemoRequest,
  type DemoRequestFields,
} from "../lib/demo-request";

/** A submission that is valid in every field, which each test then breaks in
 *  exactly one place so the assertion names the field it is about. */
function complete(overrides: Partial<DemoRequestFields> = {}): DemoRequestFields {
  return {
    firstName: "Ada",
    lastName: "Lovelace",
    email: "ada@analytical.example",
    company: "Analytical Engines",
    jobTitle: "Head of Platform",
    phone: "+44 20 7946 0000",
    orgType: "Enterprise",
    country: "United Kingdom",
    marketingOptIn: false,
    ...overrides,
  };
}

test("a complete request passes", () => {
  assert.equal(validateDemoRequest(complete()), null);
});

test("each required field, missing, is refused one at a time", () => {
  assert.match(validateDemoRequest(complete({ firstName: "  " }))!, /first name/i);
  assert.match(validateDemoRequest(complete({ lastName: "" }))!, /last name/i);
  assert.match(validateDemoRequest(complete({ email: "" }))!, /work email/i);
  assert.match(validateDemoRequest(complete({ company: "" }))!, /who you work for/i);
  assert.match(validateDemoRequest(complete({ jobTitle: "" }))!, /job title/i);
});

test("a malformed email is refused, a real one is not", () => {
  assert.match(validateDemoRequest(complete({ email: "ada-at-example" }))!, /email address/i);
  assert.match(validateDemoRequest(complete({ email: "ada@no-dot" }))!, /email address/i);
  assert.equal(validateDemoRequest(complete({ email: "a@b.co" })), null);
});

test("looksLikeEmail agrees with the server's permissive rule", () => {
  assert.equal(looksLikeEmail("ada@analytical.example"), true);
  assert.equal(looksLikeEmail("two@@at.example"), false);
  assert.equal(looksLikeEmail("no-at.example"), false);
  assert.equal(looksLikeEmail("trailing@dot."), false);
});

test("the select values must be from the offered lists", () => {
  assert.match(validateDemoRequest(complete({ orgType: "" }))!, /organization/i);
  assert.match(validateDemoRequest(complete({ orgType: "Wizard" }))!, /organization/i);
  assert.match(validateDemoRequest(complete({ country: "" }))!, /country/i);
  assert.match(validateDemoRequest(complete({ country: "Atlantis" }))!, /country/i);
});

test("phone is optional and its only rule is a length bound", () => {
  assert.equal(validateDemoRequest(complete({ phone: "" })), null);
  assert.match(validateDemoRequest(complete({ phone: "9".repeat(61) }))!, /phone/i);
});

test("compose folds the extra fields into the message and marks the source", () => {
  const lead = composeDemoLead(complete({ marketingOptIn: true, phone: "555" }));
  assert.equal(lead.source, "demo");
  assert.equal(lead.name, "Ada Lovelace");
  assert.equal(lead.email, "ada@analytical.example");
  assert.equal(lead.company, "Analytical Engines");
  assert.match(lead.message, /Job title: Head of Platform/);
  assert.match(lead.message, /Organization type: Enterprise/);
  assert.match(lead.message, /Country: United Kingdom/);
  assert.match(lead.message, /Phone: 555/);
  assert.match(lead.message, /Marketing opt-in: yes/);
});

test("compose records an absent phone and an absent opt-in honestly", () => {
  const lead = composeDemoLead(complete({ phone: "  ", marketingOptIn: false }));
  assert.match(lead.message, /Phone: not given/);
  assert.match(lead.message, /Marketing opt-in: no/);
});

test("a composed lead carries the four fields the server requires", () => {
  // The endpoint's validateLead refuses an empty name, email, company or
  // message, so a lead this composes must be non-empty in all four or the
  // accept path would reject a submission this said was valid.
  const lead = composeDemoLead(complete());
  assert.ok(lead.name.length > 0 && lead.email.length > 0);
  assert.ok(lead.company.length > 0 && lead.message.length > 0);
});
