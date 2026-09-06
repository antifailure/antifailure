import { test } from 'node:test';
import assert from 'node:assert/strict';
import { finalJudgement, sessionsFor } from '../src/execute.ts';
import type { Persona } from '../src/login.ts';
import type { Snapshot, Workflow } from '../src/workflow.ts';

// Which sessions a workflow holds, and in what order.
//
// One browser, two cookies, is the state the control plane's own launch night
// bug needed and the state no workflow could reach: a workflow signed in as
// one persona, so the operator transport check that fired on customer
// mutations whenever the operator cookie was PRESENT was never exercised with
// both cookies present. `personas` names every session; these say what the
// runner makes of the list.

const owner: Persona = { name: 'owner', email: 'owner@example.test', login: 'magic_link' };
const operator: Persona = {
  name: 'operator', email: 'operator@example.test', login: 'password', password: 'x',
};
const declared = [owner, operator];

test('the list form signs in every persona named, in the order named', () => {
  const got = sessionsFor({ personas: ['operator', 'owner'] }, declared);
  assert.equal(got.missing, undefined);
  assert.deepEqual(got.personas.map((p) => p.name), ['operator', 'owner']);
});

test('the list form wins over a single persona when both are present', () => {
  const got = sessionsFor({ persona: 'owner', personas: ['operator', 'owner'] }, declared);
  assert.deepEqual(got.personas.map((p) => p.name), ['operator', 'owner']);
});

test('a name in the list that matches no persona is reported, not skipped', () => {
  // Skipping it would run the workflow with one session and report against
  // the application whatever that state produced.
  const got = sessionsFor({ personas: ['operator', 'onwer'] }, declared);
  assert.equal(got.missing, 'onwer');
  assert.deepEqual(got.personas, []);
});

test('the single form keeps its fallback to the first declared persona', () => {
  assert.deepEqual(sessionsFor({}, declared).personas.map((p) => p.name), ['owner']);
  assert.deepEqual(sessionsFor({ persona: 'operator' }, declared).personas.map((p) => p.name), ['operator']);
  // An unknown single name falls back to the first persona, as it always has;
  // the manifest validator is what refuses the misspelling. Only a manifest
  // with no personas at all leaves the name unresolved.
  assert.deepEqual(sessionsFor({ persona: 'nobody' }, declared).personas.map((p) => p.name), ['owner']);
  assert.equal(sessionsFor({ persona: 'nobody' }, []).missing, 'nobody');
  assert.deepEqual(sessionsFor({}, []).personas, []);
});

test('an empty list means the single form, not no sign in at all', () => {
  assert.deepEqual(sessionsFor({ persona: 'operator', personas: [] }, declared).personas.map((p) => p.name), ['operator']);
});

// The failure a stranger following the docs actually hit: af init wrote a
// manifest missing its migrate key, the health check ran SELECT 1 and passed,
// and the page itself answered every request with a 500 and the words "relation
// customers does not exist". Three rewrites of the workflow's expectation all
// came back UNVERIFIED, with a note blaming the wording, because judgement ran
// on the page's text and never looked at the status the runner already had.

const emptySnapshot: Snapshot = {
  url: 'http://127.0.0.1:9/', title: '', fields: [], controls: [], submits: [],
  unnamed: 0, text: 'Internal Server Error',
};

const readOrders: Workflow = {
  name: 'read-the-spend-by-customer',
  description: 'Open the orders page and check a customer is on it.',
  expect: ['Katherine Johnson', 'Customer Email Orders Spent'],
};

test('a page answering with a server error is a failure naming the status and the page, not an unread expectation', () => {
  const result = finalJudgement(
    readOrders, { ...emptySnapshot, status: 500 }, 'Nothing moved the workflow forward.', []);
  assert.equal(result.cause, 'application-error');
  assert.match(result.detail, /500/, 'the status has to be in the sentence');
  assert.match(result.detail, new RegExp(emptySnapshot.url.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')),
    'the page has to be in the sentence');
});

test('a page answering with a client error is also a failure, not an unread expectation', () => {
  const result = finalJudgement(
    readOrders, { ...emptySnapshot, status: 404 }, 'Nothing moved the workflow forward.', []);
  assert.equal(result.cause, 'application-error');
  assert.match(result.detail, /404/);
});

test('a page that answered 200 and matches nothing is still unverified, not a failure', () => {
  // The status check must not swallow the real UNVERIFIED case: a page that
  // rendered fine and simply does not carry the expectation's words is
  // page-unreadable, exactly as it always was.
  const result = finalJudgement(
    readOrders, { ...emptySnapshot, status: 200, text: 'No customers yet.' },
    'Nothing moved the workflow forward.', []);
  assert.equal(result.cause, 'page-unreadable');
});

test('a page with no status yet (nothing has navigated) falls through to the text judgement', () => {
  const result = finalJudgement(
    readOrders, { ...emptySnapshot, status: undefined, text: 'No customers yet.' },
    'Nothing moved the workflow forward.', []);
  assert.equal(result.cause, 'page-unreadable');
});
