import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  ModelPlanner, parseDecision, prompt, fromEnvironment, type ModelConfig,
} from '../src/model.ts';
import { DeterministicPlanner, freshIdentity, type Snapshot, type Workflow } from '../src/workflow.ts';

const config: ModelConfig = { provider: 'anthropic', apiKey: 'not-a-real-key', model: 'test' };

const workflow: Workflow = {
  name: 'subscribe',
  description: 'Choose a paid plan and complete checkout.',
  expect: ['The account shows the paid plan.'],
};

const snapshot: Snapshot = {
  url: 'https://app.test/pricing',
  title: 'Pricing',
  fields: [{ name: 'Card number', type: 'text', filled: false }],
  controls: ['Choose Pro', 'Back'],
  unnamed: 0,
  text: 'Pricing. Free or Pro.',
};

function answering(text: string) {
  return async () => text;
}

test('it refuses a control that is not on the page', async () => {
  // A click on the wrong control produces a result that looks like an
  // application failure and is not one, so a name the page does not have is
  // refused rather than approximated.
  const planner = new ModelPlanner(config, answering('{"action":"click","target":"Buy now","why":"go"}'));
  const action = await planner.next(workflow, snapshot, []);
  assert.equal(action.kind, 'stuck');
  assert.match(action.why, /"Buy now", which is not on this page/);
  assert.match(action.why, /Choose Pro/, 'it says what is there instead');
});

test('it refuses a field that is not on the page', async () => {
  const planner = new ModelPlanner(config, answering('{"action":"fill","target":"CVC","value":"123","why":"x"}'));
  const action = await planner.next(workflow, snapshot, []);
  assert.equal(action.kind, 'stuck');
  assert.match(action.why, /not a field on this page/);
});

test('it carries out an action the page actually offers', async () => {
  const planner = new ModelPlanner(config, answering('{"action":"click","target":"Choose Pro","why":"the paid plan"}'));
  const action = await planner.next(workflow, snapshot, []);
  assert.equal(action.kind, 'click');
  assert.ok(action.kind === 'click' && action.control.test('Choose Pro'));
});

test('an unreachable model falls back rather than failing the workflow', async () => {
  // A model that cannot be reached is not evidence about the application.
  const failing = async () => { throw new Error('connection refused'); };
  const planner = new ModelPlanner(config, failing, new DeterministicPlanner(freshIdentity('t')));
  const action = await planner.next(workflow, snapshot, []);
  assert.equal(action.kind, 'fill', 'the deterministic planner took over');
});

test('an unreachable model with no fallback is stuck, which is blocked', async () => {
  const failing = async () => { throw new Error('connection refused'); };
  const planner = new ModelPlanner(config, failing);
  const action = await planner.next(workflow, snapshot, []);
  assert.equal(action.kind, 'stuck');
  assert.match(action.why, /connection refused/);
});

test('a page that already satisfies the workflow never reaches the model', async () => {
  // Every request not made is a second and a fraction of a cent nobody spends.
  let called = false;
  const planner = new ModelPlanner(config, async () => { called = true; return '{}'; });
  const action = await planner.next(workflow, { ...snapshot, text: 'The account shows the paid plan.' }, []);
  assert.equal(action.kind, 'done');
  assert.equal(called, false);
});

test('it reads JSON out of prose and out of a code fence', () => {
  // A model wraps its answer often enough that refusing it would waste a step.
  assert.equal(parseDecision('Sure! {"action":"done","why":"finished"}')?.action, 'done');
  assert.equal(parseDecision('```json\n{"action":"stuck","why":"nothing"}\n```')?.action, 'stuck');
  assert.equal(parseDecision('{"action":"click","target":"Go","why":"x"}')?.target, 'Go');
});

test('anything without a usable action is no decision at all', () => {
  assert.equal(parseDecision('I think you should click the button'), undefined);
  assert.equal(parseDecision('{"thinking":"hmm"}'), undefined);
  assert.equal(parseDecision('{"action":"delete_everything","why":"x"}'), undefined);
  assert.equal(parseDecision(''), undefined);
});

test('the prompt carries the accessibility snapshot and no markup', () => {
  // No HTML, no cookies, no storage. What a person navigating with a screen
  // reader gets is enough to decide from, and it keeps whatever is in the DOM
  // out of somebody else's logs.
  const text = prompt(workflow, snapshot, []);
  assert.match(text, /Choose a paid plan/);
  assert.match(text, /- Card number \(text\)/);
  assert.match(text, /- Choose Pro/);
  assert.match(text, /example\.test/, 'it is told which test data to use');
  assert.ok(!/</.test(text.replace(/[<>]/g, (m) => (m === '<' ? '<' : '>')).replace(/<exact[^>]*>|<what to type>|<one sentence>|<why[^>]*>/g, '')),
    'no markup leaks into the prompt');
});

test('a key is read from the environment, and no key is not an error', () => {
  assert.equal(fromEnvironment({}), undefined, 'no key is the normal case');
  const anthropic = fromEnvironment({ ANTHROPIC_API_KEY: 'k' });
  assert.equal(anthropic?.provider, 'anthropic');
  const openai = fromEnvironment({ OPENAI_API_KEY: 'k' });
  assert.equal(openai?.provider, 'openai');
  const pinned = fromEnvironment({ ANTHROPIC_API_KEY: 'k', AF_MODEL: 'claude-opus-5' });
  assert.equal(pinned?.model, 'claude-opus-5');
});
