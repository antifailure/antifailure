import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  DeterministicPlanner, answerFor, failureSentence, freshIdentity, judge, judgeAll,
  type Action, type Snapshot, type Workflow,
} from '../src/workflow.ts';

/** A page, in the terms a decision is made in. */
function page(over: Partial<Snapshot> = {}): Snapshot {
  return {
    url: 'https://antifailure.dev/careers',
    title: 'Careers',
    fields: [],
    controls: [],
    submits: [],
    unnamed: 0,
    text: '',
    ...over,
  };
}

/** The careers form as the snapshot sees it, before anything is answered. */
function careersFields(over: { readonly filled?: readonly string[] } = {}) {
  const filled = new Set(over.filled ?? []);
  return [
    { name: 'Founding engineer', type: 'radio', filled: filled.has('role'), required: true },
    { name: 'Founding growth', type: 'radio', filled: filled.has('role'), required: true },
    { name: 'Your name', type: 'text', filled: filled.has('Your name'), required: true },
    { name: 'Email', type: 'email', filled: filled.has('Email'), required: true },
    { name: 'Link to your work (optional)', type: 'url', filled: filled.has('link'), required: false },
    {
      name: 'What have you built or grown, and why this role',
      type: 'textarea', filled: filled.has('why'), required: true,
    },
    {
      name: 'I understand there is no salary for either role currently.',
      type: 'checkbox', filled: filled.has('ack'), required: true,
    },
  ];
}

const apply: Workflow = {
  name: 'apply-for-a-founding-role',
  description: 'Apply for a founding role.',
  expect: ['"It is written down."'],
};

function planner() {
  return new DeterministicPlanner(freshIdentity('apply-for-a-founding-role'));
}

async function next(workflow: Workflow, snapshot: Snapshot, history: Action[] = []) {
  return planner().next(workflow, snapshot, history);
}

// A required acknowledgment is ticked, not typed into and not skipped.
//
// A checkbox carries value="on" whether or not anybody ticked it, so the
// snapshot used to report every one of them as already filled. The planner
// skips filled fields, so this repository's own careers form could not be
// completed by its own agent: the box that says "I understand there is no
// salary" was never ticked and the browser refused to submit the form.
test('a required acknowledgment is chosen', async () => {
  const action = await next(apply, page({
    fields: careersFields({ filled: ['role', 'Your name', 'Email', 'why'] }),
    submits: ['Send application'],
  }));
  assert.equal(action.kind, 'check');
  assert.match('I understand there is no salary for either role currently.',
    (action as { field: RegExp }).field);
});

// A required choice is made, and only one option of it.
test('a required radio group is chosen', async () => {
  const action = await next(apply, page({
    fields: careersFields({ filled: ['Your name', 'Email', 'why', 'ack'] }),
    submits: ['Send application'],
  }));
  assert.equal(action.kind, 'check');
  assert.match('Founding engineer', (action as { field: RegExp }).field);
});

// A checkbox is never typed into.
//
// "Email me about releases" matches the email rule in the value table, and
// `fill` on a checkbox throws, so a page with one would have ended the run as
// a runner failure.
test('a checkbox whose name matches a value rule is still chosen and not typed into', async () => {
  const action = await next(apply, page({
    fields: [{ name: 'Email me about releases', type: 'checkbox', filled: false, required: true }],
    submits: ['Send'],
  }));
  assert.equal(action.kind, 'check');
});

// An optional checkbox is left alone.
//
// An agent that ticks every box on a page has subscribed somebody to a
// newsletter to see what happens.
test('an optional checkbox is not ticked', async () => {
  const action = await next(apply, page({
    fields: [{ name: 'Send me the newsletter', type: 'checkbox', filled: false, required: false }],
    submits: ['Send'],
  }));
  assert.equal(action.kind, 'click');
});

// A required field nothing recognises is still answered.
//
// "What have you built or grown, and why this role" matches no shape any
// application shares, and a required field left empty is a form the browser
// refuses to send. The agent pressed the button and watched nothing happen.
test('a required field nothing recognises is answered anyway', async () => {
  const action = await next(apply, page({
    fields: careersFields({ filled: ['role', 'Your name', 'Email', 'ack'] }),
    submits: ['Send application'],
  }));
  assert.equal(action.kind, 'fill');
  assert.match('What have you built or grown, and why this role',
    (action as { field: RegExp }).field);
  assert.match((action as { value: string }).value, /Antifailure agent/);
});

// An optional field nothing recognises is left empty.
test('an optional field nothing recognises is left empty', async () => {
  const action = await next(apply, page({
    fields: [{ name: 'Anything else', type: 'text', filled: false, required: false }],
    submits: ['Send'],
  }));
  assert.equal(action.kind, 'click');
});

// What the workflow said to type beats what the planner would have guessed.
test('an answer the workflow wrote down wins over the value table', async () => {
  const answered: Workflow = { ...apply, answers: { 'Link to your work': 'https://x.test/' } };
  const action = await next(answered, page({
    fields: careersFields({ filled: ['role', 'Your name', 'Email', 'why', 'ack'] }),
    submits: ['Send application'],
  }));
  assert.equal(action.kind, 'fill');
  assert.equal((action as { value: string }).value, 'https://x.test/');
});

test('the longest matching answer wins, and a name it does not match is untouched', () => {
  const w: Workflow = {
    ...apply,
    answers: { 'Email': 'a@example.test', 'Email me a copy': 'yes' },
  };
  assert.equal(answerFor(w, 'Email me a copy at'), 'yes');
  assert.equal(answerFor(w, 'Email address'), 'a@example.test');
  assert.equal(answerFor(w, 'Your name'), undefined);
  assert.equal(answerFor(apply, 'Email'), undefined);
});

// The form's own submit control is pressed when no known word matches.
//
// "Send application" is on no list of words that move a workflow forward, so
// the agent filled the whole form in and then declared itself stuck in front of
// the only control that mattered.
test('the control that submits the form is pressed when no known word matches', async () => {
  const action = await next(apply, page({
    fields: careersFields({ filled: ['role', 'Your name', 'Email', 'why', 'ack'] }),
    controls: ['Skip to content', 'Send application', 'Apply to join'],
    submits: ['Send application'],
  }), [{ kind: 'fill', field: /^Your name$/, value: 'x', why: 'y' }]);
  assert.equal(action.kind, 'click');
  assert.match('Send application', (action as { control: RegExp }).control);
});

// A form that has been filled in is finished by pressing what SENDS it.
//
// THE FAILURE. Once the document's own submit controls were consulted, the
// word list was still consulted FIRST, and this site's header carries a "Sign
// in" link that `^(sign in|log in|login)$` matches. So the agent filled in
// every field of the careers form, ignored "Send application", followed the
// header link, and reported that the careers page offered nothing.
test('a filled in form is not abandoned for a link in the site header', async () => {
  const action = await next(apply, page({
    fields: careersFields({ filled: ['role', 'Your name', 'Email', 'why', 'ack'] }),
    controls: ['Sign in', 'Send application'],
    submits: ['Send application'],
  }), [{ kind: 'check', field: /^ack$/, why: 'y' }]);
  assert.equal(action.kind, 'click');
  assert.match('Send application', (action as { control: RegExp }).control);
});

// And once it has been sent as often as it is going to be, the answer is stuck.
//
// Wandering off left the run's LAST page as the sign-in screen, so the failure
// was reported against a page with nothing to do with the workflow and the
// error banner the form had just shown was gone from the evidence, the
// screenshot and the quoted sentence.
test('a form whose submit control is spent is stuck, not somewhere else', async () => {
  const pressed: Action[] = [
    { kind: 'check', field: /^ack$/, why: 'y' },
    { kind: 'click', control: /^Try it again$/, why: 'y' },
    { kind: 'click', control: /^Try it again$/, why: 'y' },
  ];
  const action = await next(apply, page({
    fields: careersFields({ filled: ['role', 'Your name', 'Email', 'why', 'ack'] }),
    controls: ['Sign in', 'Try it again'],
    submits: ['Try it again'],
  }), pressed);
  assert.equal(action.kind, 'stuck');
});

// Before anything is answered, the words still win.
//
// A pricing page with a search box offers a submit control that sends nobody
// anywhere, and "Choose Pro" is the control that moves a checkout forward.
test('before anything is answered the known words still win', async () => {
  const action = await next(apply, page({
    controls: ['Search', 'Continue'],
    submits: ['Search'],
  }));
  assert.equal(action.kind, 'click');
  assert.match('Continue', (action as { control: RegExp }).control);
});

// A quoted expectation is required on the page character for character.
//
// THE CASE THAT FOUND THIS. The control plane's refusal of a work link with
// credentials scores six of its seven meaningful words against the careers page
// BEFORE the form is touched: `public`, `link`, `use`, `credentials` and an
// install command containing `https` are all already on it. Unquoted, the
// expectation was met before the agent did anything, and the workflow passed in
// one step against a form it never submitted.
test('a quoted expectation is not satisfied by its own words being scattered about', () => {
  const scattered =
    'Use the quickstart. A public repository is welcome. Link to your work. ' +
    'curl -fsSL https://antifailure.dev/install.sh | sh. We never ask for credentials.';
  const sentence = 'Use a public http or https link without credentials.';
  assert.equal(judge(`"${sentence}"`, scattered), 'unmet');
  assert.equal(judge(`"${sentence}"`, `Something. ${sentence} Try it again`), 'met');
  // And the unquoted form is exactly the reading that made this necessary.
  assert.equal(judge(sentence, scattered), 'met');
});

test('a quoted expectation is met across a line break and a run of spaces', () => {
  assert.equal(judge('"It is written down."', 'Careers\nIt is\n  written   down.\nKeep this'), 'met');
});

// Never unclear. The whole point of quoting a sentence is that its absence is
// an answer, and unclear exits zero.
test('a quoted expectation that is absent is unmet, never unclear', () => {
  assert.equal(judge('"It is written down."', 'Could not reach the server.'), 'unmet');
  assert.equal(judgeAll(['"It is written down."'], 'anything else at all'), 'unmet');
});

// The page telling the agent its request never arrived is a failure signal.
//
// It was on no list, so `judge` returned unclear, the attempt came back
// page-unreadable, the verdict was UNVERIFIED, and the runner exited zero over
// a careers form that did not work.
test('a page that says it could not reach the server is a failure, not an unreadable page', () => {
  const banner = 'Could not reach the server. Check your connection and press it again; ' +
    'nothing you typed is lost.';
  assert.equal(judge('The application is recorded', banner), 'unmet');
  assert.equal(judge('The application is recorded', 'Unable to reach the server.'), 'unmet');
  assert.equal(judge('The application is recorded', 'A network error stopped this.'), 'unmet');
  // The contracted forms, which are what most front ends actually write and
  // which the "could not X" rule cannot see. Asserted separately because two
  // patterns that both match "could not reach" make each other untestable: a
  // mutation of either stays green while the other covers for it.
  assert.equal(judge('The application is recorded', "We couldn't reach the server."), 'unmet');
  assert.equal(judge('The application is recorded', 'Cannot reach the server.'), 'unmet');
  assert.equal(judge('The application is recorded', "Can't reach the server."), 'unmet');
});

// And the sentence itself comes back, so a report can quote it.
test('the sentence a failure signal matched is what comes back', () => {
  const page = 'Careers\nApply to join\n' +
    'Could not reach the server. Check your connection and press it again.\n' +
    'Try it again';
  assert.equal(failureSentence(page), 'Could not reach the server.');
  assert.equal(failureSentence('Everything is fine here.'), undefined);
});

// An ordinary page must not read as a failure.
//
// A signal list that matches prose is a check that cannot say yes, which is
// the same defect as one that cannot say no wearing different clothes.
test('an ordinary page carries no failure signal', () => {
  assert.equal(failureSentence(
    'Two founding roles. Read the source first. Your application is in the private queue.',
  ), undefined);
  assert.equal(judge('The application is recorded', 'It is written down. Keep this reference.'),
    'unclear');
});
