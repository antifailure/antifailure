import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { ModelPlanner, prompt, type ModelConfig } from '../src/model.ts';
import { Cassette, keyFor } from '../src/cassette.ts';
import { preamble, agentsFor, type Assignment, type ResolvedDiversity } from '../src/personality.ts';
import type { Snapshot, Workflow } from '../src/workflow.ts';

const config: ModelConfig = { provider: 'anthropic', apiKey: 'sk-secret-value-xyz', model: 'test' };

const workflow: Workflow = {
  name: 'subscribe',
  description: 'Choose a paid plan and complete checkout.',
  expect: ['The account shows the paid plan.'],
};

const snapshot: Snapshot = {
  url: 'https://app.test/pricing',
  title: 'Pricing',
  fields: [{ name: 'Card number', type: 'text', filled: false, required: false }],
  controls: ['Choose Pro', 'Back'],
  submits: [],
  unnamed: 0,
  text: 'Pricing. Free or Pro.',
};

function assignment(id: string, prompt: string, pacing = 'medium'): Assignment {
  return {
    workflow: 'subscribe',
    agentIndex: 0,
    personality: { id, name: id, reasoningPrompt: prompt },
    profile: {
      personaId: id, strategyArchetype: 'explorer', riskStyle: 'balanced',
      pacingStyle: pacing, attentionBias: 'cta_focused', errorResponseStyle: 'diagnostic',
      cognitiveStyle: 'business_operator', noveltyBias: 0.4, seed: 1, profileKey: `${id}:k`,
    },
  };
}

test('a personality preamble is prepended to the prompt, and no personality leaves it unchanged', () => {
  const plain = prompt(workflow, snapshot, []);
  const skeptic = prompt(workflow, snapshot, [], assignment('skeptic', 'You are a SKEPTIC.'));
  assert.ok(skeptic.startsWith('You are a SKEPTIC.'), 'the personality frames the task first');
  assert.ok(skeptic.includes('read the page to understand why before retrying'), 'the profile lens is in the preamble');
  assert.ok(skeptic.endsWith(plain), 'the rest of the prompt, the action grammar and the page, is unchanged');
  assert.notEqual(plain, skeptic, 'a personality changes the prompt');
});

test('each personality asking about one page produces its own cassette key, so N personalities record N answers', () => {
  const a = keyFor(prompt(workflow, snapshot, [], assignment('skeptic', 'You are a SKEPTIC.')), config);
  const b = keyFor(prompt(workflow, snapshot, [], assignment('fast_actor', 'You are a FAST ACTOR.')), config);
  const neutral = keyFor(prompt(workflow, snapshot, []), config);
  assert.notEqual(a, b, 'two personalities over one page hash to two keys');
  assert.notEqual(a, neutral, 'a personality differs from the neutral prompt');
});

test('the same personality and page replays the same recording, deterministically', () => {
  const first = keyFor(prompt(workflow, snapshot, [], assignment('skeptic', 'You are a SKEPTIC.')), config);
  const again = keyFor(prompt(workflow, snapshot, [], assignment('skeptic', 'You are a SKEPTIC.')), config);
  assert.equal(first, again, 'the key is a pure function of the prompt');
});

test('a replay with no recording for a personality is blocked, it does not call the network', async () => {
  const dir = mkdtempSync(join(tmpdir(), 'af-cassette-'));
  try {
    // Record two personalities over the same page.
    const rec = new Cassette(dir, 'record');
    let calls = 0;
    const recording = rec.wrap(async () => { calls++; return '{"action":"done","why":"ok"}'; });
    await recording(prompt(workflow, snapshot, [], assignment('skeptic', 'You are a SKEPTIC.')), config);
    await recording(prompt(workflow, snapshot, [], assignment('fast_actor', 'You are a FAST ACTOR.')), config);
    assert.equal(calls, 2, 'two personalities recorded two answers');
    assert.equal(readdirSync(dir).filter((f) => f.endsWith('.json')).length, 2, 'two recordings on disk');

    // A replay of a THIRD, unrecorded personality must throw rather than spend.
    const replay = new Cassette(dir, 'replay');
    let networkTouched = false;
    const replaying = replay.wrap(async () => { networkTouched = true; return 'live'; });
    await assert.rejects(
      () => replaying(prompt(workflow, snapshot, [], assignment('explorer', 'You are an EXPLORER.')), config),
      /No recorded answer/,
    );
    assert.equal(networkTouched, false, 'a missing recording never reaches the network');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('BYOK: the key is read and used on the model path, and never appears in the prompt', async () => {
  let seenPrompt = '';
  let seenConfig: ModelConfig | undefined;
  const complete = async (text: string, cfg: ModelConfig) => {
    seenPrompt = text; seenConfig = cfg; return '{"action":"click","target":"Choose Pro","why":"the paid plan"}';
  };
  const planner = new ModelPlanner(config, complete, undefined, assignment('skeptic', 'You are a SKEPTIC.'));
  const action = await planner.next(workflow, snapshot, []);
  assert.equal(action.kind, 'click', 'the model drove the action');
  assert.equal(seenConfig?.apiKey, 'sk-secret-value-xyz', 'the configured key reached the model call');
  assert.ok(!seenPrompt.includes('sk-secret-value-xyz'), 'the key never appears in the prompt the model receives');
  assert.ok(seenPrompt.startsWith('You are a SKEPTIC.'), 'the planner threaded the personality into the prompt');
});

test('agentsFor returns one neutral run with no plan, and the assigned agents with one', () => {
  assert.deepEqual(agentsFor(undefined, 'subscribe'), [undefined]);
  const plan: ResolvedDiversity = {
    seed: 's', diagnostics: { uniquenessScore: 1, strategyCount: 2, personaCount: 2 },
    assignments: { subscribe: [assignment('skeptic', 'a'), assignment('fast_actor', 'b')] },
  };
  assert.equal(agentsFor(plan, 'subscribe').length, 2, 'two agents for a workflow with two assignments');
  assert.deepEqual(agentsFor(plan, 'unlisted'), [undefined], 'a workflow with no assignment is one neutral run');
});

test('preamble ends by restating that only listed controls are available', () => {
  const text = preamble(assignment('edge_case', 'You are an EDGE-CASE EXPLORER.'));
  assert.ok(text.includes('never adds a control that is not listed'), 'the capability surface is restated');
});
