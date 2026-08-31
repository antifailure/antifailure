// Recording and replaying model answers.
//
// The properties that matter are the ones that decide whether a scheduled run
// costs money and whether it can lie:
//
//   a replay must never reach the network, including on a miss
//   a miss must be visible, not a quiet fall back to a cheaper planner
//   two different pages must not share one recording
//   two different models must not share one recording
//   recording twice in one run must ask once
//
// Each of those is a test below, and each of them is a way this could have
// been written that would look fine and be wrong.

import { describe, it, before, after } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, readdirSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Cassette, CassetteMiss, cassetteFromEnvironment, keyFor } from '../src/cassette.ts';
import type { ModelConfig } from '../src/model.ts';

const anthropic: ModelConfig = {
  provider: 'anthropic',
  apiKey: 'not-a-real-key',
  model: 'claude-sonnet-5',
};
const openai: ModelConfig = { provider: 'openai', apiKey: 'not-a-real-key', model: 'gpt-4.1' };

describe('a cassette', () => {
  let dir: string;

  before(() => {
    dir = mkdtempSync(join(tmpdir(), 'af-cassette-'));
  });
  after(() => {
    rmSync(dir, { recursive: true, force: true });
  });

  it('records an answer and replays it without asking again', async () => {
    let asked = 0;
    const record = new Cassette(dir, 'record').wrap(async () => {
      asked += 1;
      return '{"action":"click","target":"Send link","why":"it is the only button"}';
    });

    const first = await record('a page with a Send link button', anthropic);
    assert.equal(asked, 1);
    assert.match(first, /Send link/);

    const replay = new Cassette(dir, 'replay').wrap(async () => {
      throw new Error('a replay reached the network');
    });
    const second = await replay('a page with a Send link button', anthropic);
    assert.equal(second, first, 'the replayed answer is not the recorded one');
    assert.equal(asked, 1, 'replaying asked the model again');
  });

  it('asks once when the same page comes round twice in one recording run', async () => {
    // Two identical pages in one run are one question. Asking twice costs
    // twice and can answer differently, which would put two answers under one
    // key and make the recording depend on which one landed last.
    const fresh = mkdtempSync(join(tmpdir(), 'af-cassette-once-'));
    try {
      let asked = 0;
      const record = new Cassette(fresh, 'record').wrap(async () => {
        asked += 1;
        return `answer ${asked}`;
      });
      const a = await record('the same page', anthropic);
      const b = await record('the same page', anthropic);
      assert.equal(asked, 1, `the model was asked ${asked} times for one page`);
      assert.equal(a, b);
    } finally {
      rmSync(fresh, { recursive: true, force: true });
    }
  });

  it('refuses a miss rather than reaching the network', async () => {
    let asked = 0;
    const replay = new Cassette(dir, 'replay').wrap(async () => {
      asked += 1;
      return 'this must never happen';
    });

    await assert.rejects(
      () => replay('a page nobody recorded', anthropic),
      (err: unknown) => {
        assert.ok(err instanceof CassetteMiss, `threw ${err} instead of a CassetteMiss`);
        // The message has to say what to do. A miss on a schedule is read by
        // somebody who did not write the cassette.
        assert.match(err.message, /re-record/i);
        return true;
      },
    );
    assert.equal(asked, 0, 'a miss called the model, which is what recorded mode exists to prevent');
  });

  it('does not replay one page as another', async () => {
    const replay = new Cassette(dir, 'replay').wrap(async () => 'never');
    // One character different is a different page and must be a miss. A key
    // built from a subset of the prompt would hit here, and a wrong hit looks
    // like a pass.
    await assert.rejects(() => replay('a page with a Send link button.', anthropic), CassetteMiss);
  });

  it('does not replay one model as another', async () => {
    const replay = new Cassette(dir, 'replay').wrap(async () => 'never');
    await assert.rejects(
      () => replay('a page with a Send link button', openai),
      CassetteMiss,
      'an answer recorded from one model was replayed as another',
    );
    assert.notEqual(
      keyFor('a page', anthropic),
      keyFor('a page', openai),
      'two providers share a key, so a recording made with one replays as the other',
    );
  });

  it('writes a recording a person can read', async () => {
    const files = readdirSync(dir).filter((f) => f.endsWith('.json'));
    assert.ok(files.length > 0);
    const body = JSON.parse(readFileSync(join(dir, files[0]!), 'utf8')) as Record<string, unknown>;
    for (const field of ['model', 'provider', 'prompt', 'response', 'recordedAt']) {
      assert.ok(field in body, `a recording does not carry ${field}, so a stale one is unreadable`);
    }
    // The prompt in full, because a directory of hashes nobody can read is a
    // directory nobody can review.
    assert.equal(typeof body.prompt, 'string');
    assert.ok((body.prompt as string).length > 0);
  });

  it('counts what it did, so a run can say whether the cassette was used at all', async () => {
    const fresh = mkdtempSync(join(tmpdir(), 'af-cassette-count-'));
    try {
      const cassette = new Cassette(fresh, 'record');
      const record = cassette.wrap(async () => 'an answer');
      await record('page one', anthropic);
      await record('page two', anthropic);
      await record('page one', anthropic);
      assert.equal(cassette.written, 2);
      assert.equal(cassette.read, 1);
      assert.equal(cassette.size(), 2);
    } finally {
      rmSync(fresh, { recursive: true, force: true });
    }
  });
});

describe('reading a cassette out of the environment', () => {
  it('is off when no directory is named', () => {
    assert.equal(cassetteFromEnvironment({}), undefined);
    assert.equal(cassetteFromEnvironment({ AF_MODEL_CASSETTE_MODE: 'record' }), undefined);
  });

  it('replays by default, because the other default spends money on a schedule', () => {
    const dir = mkdtempSync(join(tmpdir(), 'af-cassette-env-'));
    try {
      const cassette = cassetteFromEnvironment({ AF_MODEL_CASSETTE: dir });
      assert.equal(cassette?.mode, 'replay');
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });

  it('records only when asked in words', () => {
    const dir = mkdtempSync(join(tmpdir(), 'af-cassette-env2-'));
    try {
      const cassette = cassetteFromEnvironment({
        AF_MODEL_CASSETTE: dir,
        AF_MODEL_CASSETTE_MODE: 'record',
      });
      assert.equal(cassette?.mode, 'record');
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });

  it('stops on a mode it does not recognise rather than guessing', () => {
    // Guessing here means choosing between spending money and not running.
    assert.throws(
      () => cassetteFromEnvironment({ AF_MODEL_CASSETTE: '/tmp/x', AF_MODEL_CASSETTE_MODE: 'yes' }),
      /record.*replay/,
    );
  });
});
