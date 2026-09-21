import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  BUDGET_SPENT, budgetDetail, finalJudgement, sessionsFor, withinBudget,
} from '../src/execute.ts';
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

// The report's table cell, and the reason this number is written down here.
//
// `oneLine` in engine/internal/report/report.go caps a workflow row's detail at
// 120 characters and appends an ellipsis. Everything past it reaches nobody:
// the full text survives only on the `Got:` line inside a collapsed details
// block, which is where somebody looks once they already suspect something.
// This is the runner's half of that contract, and report_test.go's
// TestMarkdown_ACellCapKeepsALeadingQuotedName is the engine's half, which
// pins the number so this one cannot be written against a cap that moved.
const CELL = 120;

/** The planner's sentence, which used to lead the detail and is 183 characters
 *  on its own, so nothing behind it survived the cell. */
const STUCK = 'Nothing on this page moves the workflow forward. It offers nothing at all. '
  + 'The runner not knowing what to press is not evidence about the application, '
  + 'so it is not counted against it.';

const readablePage: Snapshot = {
  url: 'http://127.0.0.1:46000/orders', title: 'orders', fields: [], controls: [],
  submits: [], unnamed: 0, status: 200, text: '[]',
};

// THE FAILURE THIS CLOSES. An expectation that could never match any page was
// named at character 295 of a 489 character detail, behind a cap of 120, so the
// sentence that said which expectation was at fault existed for nobody reading
// the row. Worse, the sentence that DID lead said nothing on this page moves
// the workflow forward, about a page that may be showing exactly what was asked
// for, which points the reader at their application when the fault is in their
// manifest.
test('an expectation that could never match is named inside the report cell, not behind it', () => {
  const result = finalJudgement(
    { name: 'w', description: 'd', expect: ['is it up'] }, readablePage, STUCK, []);
  assert.equal(result.cause, 'page-unreadable');
  assert.ok(
    result.detail.slice(0, CELL).includes('"is it up"'),
    `the cell does not name the expectation: ${result.detail.slice(0, CELL)}`,
  );
});

// The quoted name leads the sentence rather than closing it, and this is the
// case that decides that. A sentence that leads and then truncates before
// naming which expectation tells somebody there is a problem and not what it
// is, which is worse than the folded version it replaces.
test('a long expectation still survives the cell whole', () => {
  const long = 'is it not that this was as it was before and was it not that this is as it is';
  assert.ok(long.length > 70, 'the case has to be long enough to be at risk');
  const result = finalJudgement(
    { name: 'w', description: 'd', expect: [long] }, readablePage, STUCK, []);
  assert.ok(
    result.detail.slice(0, CELL).includes(`"${long}"`),
    `the cell carries only part of the expectation: ${result.detail.slice(0, CELL)}`,
  );
});

// Only an expectation that could NEVER be met earns the front of the cell. One
// that simply was not met is a different fact, and leading with this sentence
// on every unverified workflow would change every report in the product.
test('a workflow whose expectations are all matchable still leads with the planner', () => {
  const result = finalJudgement(
    { name: 'w', description: 'd', expect: ['total_cents'] }, readablePage, STUCK, []);
  assert.equal(result.cause, 'page-unreadable');
  assert.ok(result.detail.startsWith(STUCK), `the detail was rewritten: ${result.detail}`);
  assert.ok(!result.detail.includes('could never match any page'));
});

// Leading in the cell must not take anything away from where the text already
// correctly appears. The details block prints the whole detail, so everything
// the folded version said is still said.
test('leading in the cell keeps the rest of the sentence for the details block', () => {
  const result = finalJudgement(
    { name: 'w', description: 'd', expect: ['is it up'] }, readablePage, STUCK, []);
  assert.ok(result.detail.includes('no word this can look for'), result.detail);
  assert.ok(result.detail.includes('Quote a string to require it exactly'), result.detail);
  assert.ok(result.detail.includes(STUCK), 'the planner\'s own sentence is still there');
  assert.ok(result.detail.includes('nothing confirms it either'), result.detail);
});

// AN UNMET EXPECTATION IS NOT AN ERROR UNLESS SOMETHING SHOWED ONE.
//
// Every expectation-not-met used to be explained as "The page shows an error
// rather than what was expected", because judge answers unmet both for a
// failure banner and for a quoted string that is simply absent. The second is
// what a deliberately impossible expectation produces, and on 2026-09-21 it was
// reported that way twice over healthy screens: an iOS journal showing every
// entry, and a terminal menu drawn in full. finalJudgement is the path web,
// desktop, iOS and Android all share, so these two cases hold for all four;
// test/mobile.test.ts and test/desktop.test.ts drive the same function from
// their own snapshots.

const healthyJournal: Snapshot = {
  url: 'http://127.0.0.1:46000/journal', title: 'Journal', fields: [], controls: [],
  submits: [], unnamed: 0, status: 200,
  text: 'Journal\nMonday: walked to the harbour.\nTuesday: finished the book.',
};

test('a quoted expectation missing from a healthy page names the expectation and claims no error', () => {
  const result = finalJudgement(
    { name: 'w', description: 'd', expect: ['"Entry for Sunday"'] }, healthyJournal, STUCK, []);
  assert.equal(result.cause, 'expectation-not-met', 'the verdict must not change');
  assert.ok(result.detail.slice(0, CELL).includes('"Entry for Sunday" was not found.'),
    `the cell does not name the missing expectation: ${result.detail.slice(0, CELL)}`);
  assert.ok(!/shows an error|showed a failure/i.test(result.detail),
    `the detail claims an error on a healthy page: ${result.detail}`);
  assert.ok(result.detail.includes('No error was showing. Instead it showed: "Journal Monday: walked to the harbour.'),
    `the detail does not say what was showing: ${result.detail}`);
  assert.ok(result.detail.endsWith(STUCK), 'the planner\'s own sentence is kept, after the facts');
});

test('several missing expectations are all named, and a met one is not', () => {
  const result = finalJudgement(
    { name: 'w', description: 'd', expect: ['"Journal"', '"Entry for Sunday"', '"Entry for Saturday"'] },
    healthyJournal, STUCK, []);
  assert.equal(result.cause, 'expectation-not-met');
  assert.ok(result.detail.startsWith('"Entry for Sunday" and "Entry for Saturday" were not found.'),
    result.detail);
});

test('a page genuinely showing an error still says so, quotes it and names what was missing', () => {
  const broken = { ...healthyJournal, text: 'Journal\nSomething went wrong. Please try later.' };
  const result = finalJudgement(
    { name: 'w', description: 'd', expect: ['"Entry for Sunday"'] }, broken, STUCK, []);
  assert.equal(result.cause, 'expectation-not-met');
  assert.ok(result.detail.includes(
    'The page shows an error rather than what was expected. It says: "Something went wrong."'),
  result.detail);
  assert.ok(result.detail.includes('"Entry for Sunday" was not found.'), result.detail);
  assert.ok(!result.detail.includes('No error was showing'), result.detail);
});

test('a budget detail names the budget, how far in, the attempt and the last step', () => {
  // The next question about a workflow that ran out of time is whether it was
  // stuck or merely slow, and the last step taken is what answers it.
  assert.match(
    budgetDetail(2_000, 2_500, 1, ['Open http://127.0.0.1/', 'Press Pay now: the form is complete']),
    /^Stopped at its time budget of 2s, 2\.5s into the workflow on attempt 1, after: Press Pay now: the form is complete\./,
  );
  assert.match(
    budgetDetail(600_000, 600_000, 2, []),
    /^Stopped at its time budget of 10m, 10m into the workflow on attempt 2, before it took a single step\./,
  );
});

test('withinBudget settles to the work when it finishes in time, and to the spent marker when it does not', async () => {
  assert.equal(await withinBudget(Promise.resolve('done'), 1_000), 'done');
  assert.equal(await withinBudget(new Promise<string>(() => undefined), 20), BUDGET_SPENT);
});
