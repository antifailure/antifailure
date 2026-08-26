import { test } from 'node:test';
import assert from 'node:assert/strict';
import { matches, waitFor, InboxTimeout, type Message, type InboxSource } from '../src/inbox.ts';

// Partial with undefined allowed, because exactOptionalPropertyTypes makes
// "absent" and "explicitly undefined" different types and the tests need to
// say a field is missing.
type Over = { [K in keyof Message]?: Message[K] | undefined };

function message(over: Over = {}): Message {
  const base: Message = {
    seq: 1, at: '2026-06-01T12:00:00Z', provider: 'resend', kind: 'email',
    to: ['owner@example.test'], subject: 'Confirm your email',
    link: 'https://app.test/verify?token=abc', code: '481920',
  };
  // Assigned rather than spread, so a key set to undefined removes the field
  // instead of setting it to undefined, which exactOptionalPropertyTypes
  // treats as a different thing.
  const out: Record<string, unknown> = { ...base };
  for (const [key, value] of Object.entries(over)) {
    if (value === undefined) delete out[key];
    else out[key] = value;
  }
  return out as unknown as Message;
}

class Fake implements InboxSource {
  messages: Message[];
  constructor(messages: Message[]) { this.messages = messages; }
  async list(): Promise<readonly Message[]> { return this.messages; }
  add(m: Message) { this.messages = [...this.messages, m]; }
}

test('matching is by recipient, subject, link, and code', () => {
  const m = message();
  assert.ok(matches(m, { to: 'OWNER@EXAMPLE.TEST' }), 'a recipient is not case sensitive');
  assert.ok(matches(m, { subjectContains: 'confirm' }));
  assert.ok(matches(m, { hasLink: true, hasCode: true }));
  assert.ok(!matches(m, { to: 'someone.else@example.test' }));
  assert.ok(!matches(m, { subjectContains: 'invoice' }));
  assert.ok(!matches(message({ link: undefined }), { hasLink: true }));
  assert.ok(!matches(message({ code: undefined }), { hasCode: true }));
});

test('a message that already arrived is found without waiting', async () => {
  // The message is usually sent before anybody starts waiting for it, so a
  // wait that only looks forward passes on a slow machine and fails on a fast
  // one, which is the definition of a flaky test.
  const inbox = new Fake([message()]);
  const started = Date.now();
  const found = await waitFor(inbox, { to: 'owner@example.test' }, { timeoutMs: 5_000 });
  assert.equal(found.seq, 1);
  assert.ok(Date.now() - started < 200, 'it must not have slept');
});

test('a message that arrives later is found', async () => {
  const inbox = new Fake([]);
  setTimeout(() => inbox.add(message({ seq: 7 })), 60);
  const found = await waitFor(inbox, { hasCode: true }, { timeoutMs: 3_000, intervalMs: 20 });
  assert.equal(found.seq, 7);
});

test('the newest matching message wins', async () => {
  // A flow that sends the same message twice wants the one it just triggered,
  // not the one left over from the previous run.
  const inbox = new Fake([
    message({ seq: 1, code: '111111' }),
    message({ seq: 2, code: '222222' }),
  ]);
  const found = await waitFor(inbox, { hasCode: true }, { timeoutMs: 1_000 });
  assert.equal(found.code, '222222');
});

test('messages before a watermark are ignored', async () => {
  const inbox = new Fake([message({ seq: 1, code: '111111' })]);
  setTimeout(() => inbox.add(message({ seq: 2, code: '222222' })), 40);
  const found = await waitFor(inbox, { hasCode: true }, { timeoutMs: 2_000, intervalMs: 20, after: 1 });
  assert.equal(found.code, '222222');
});

test('a timeout says what was waited for and what did arrive', async () => {
  // Nine times out of ten the message is there and addressed to a different
  // persona, and a bare "no message came" sends somebody looking at the wrong
  // thing entirely.
  const inbox = new Fake([message({ to: ['someone.else@example.test'], subject: 'Welcome' })]);
  await assert.rejects(
    () => waitFor(inbox, { to: 'owner@example.test' }, { timeoutMs: 60, intervalMs: 10 }),
    (err: unknown) => {
      assert.ok(err instanceof InboxTimeout);
      assert.match(err.message, /to owner@example.test/);
      assert.match(err.message, /someone.else@example.test/);
      assert.match(err.message, /Welcome/);
      return true;
    },
  );
});

test('a timeout with nothing captured says so plainly', async () => {
  const inbox = new Fake([]);
  await assert.rejects(
    () => waitFor(inbox, { hasCode: true }, { timeoutMs: 40, intervalMs: 10 }),
    /Nothing was captured at all/,
  );
});
