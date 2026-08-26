import { test } from 'node:test';
import assert from 'node:assert/strict';
import { signIn, FIELD, CONTROL, type Page, type Persona } from '../src/login.ts';
import type { InboxSource, Message } from '../src/inbox.ts';

/** FakePage records what was typed and pressed, and answers with whatever the
 *  test says the application would show. */
class FakePage implements Page {
  filled: Record<string, string> = {};
  clicked: string[] = [];
  visited: string[] = [];
  outcome: RegExp | null;
  current = 'https://app.test/login';

  constructor(outcome: RegExp | null) { this.outcome = outcome; }

  async goto(url: string) { this.visited.push(url); this.current = url; }
  async fill(field: RegExp, value: string) { this.filled[field.source] = value; }
  async click(control: RegExp) { this.clicked.push(control.source); }
  async waitForAny(patterns: readonly RegExp[]): Promise<RegExp | null> {
    if (this.outcome === null) return null;
    return patterns.find((p) => p.source === this.outcome!.source) ?? null;
  }
  async text() { return ''; }
  url() { return this.current; }
}

class FakeInbox implements InboxSource {
  messages: Message[];
  constructor(messages: Message[]) { this.messages = messages; }
  async list(): Promise<readonly Message[]> { return this.messages; }
}

const owner: Persona = {
  name: 'owner', email: 'owner@example.test', password: 'correct horse', login: 'password',
};

test('a password sign in fills both fields and submits', async () => {
  const page = new FakePage(/dashboard/i);
  const result = await signIn(page, owner, { baseURL: 'https://app.test' });

  assert.equal(result.ok, true);
  assert.equal(page.filled[FIELD.email.source], 'owner@example.test');
  assert.equal(page.filled[FIELD.password.source], 'correct horse');
  assert.deepEqual(page.clicked, [CONTROL.signIn.source]);
  assert.equal(page.visited[0], 'https://app.test/login');
});

test('a refused password is a failure, not a blocked run', async () => {
  // The application said no. That is the application's answer and it counts.
  const page = new FakePage(/invalid|incorrect|wrong password/i);
  const result = await signIn(page, owner, { baseURL: 'https://app.test' });
  assert.equal(result.ok, false);
  assert.equal(result.blocked, false);
  assert.match(result.detail, /refused by the application/);
});

test('a persona with no password is blocked, not failed', async () => {
  // A manifest that has not finished is not a broken application, and
  // reporting it as a failing test points at the wrong file.
  const page = new FakePage(/dashboard/i);
  // Written out rather than spread with undefined, because
  // exactOptionalPropertyTypes makes an absent field and an undefined one
  // different types, and the case under test is the absent one.
  const noPassword: Persona = { name: owner.name, email: owner.email, login: 'password' };
  const result = await signIn(page, noPassword, { baseURL: 'https://app.test' });
  assert.equal(result.ok, false);
  assert.equal(result.blocked, true);
  assert.match(result.detail, /has none set/);
});

test('a page that says nothing is reported as saying nothing', async () => {
  // "The page never said" is a different problem from "the password was
  // wrong", and collapsing them sends somebody to the wrong place.
  const page = new FakePage(null);
  const result = await signIn(page, owner, { baseURL: 'https://app.test' });
  assert.equal(result.ok, false);
  assert.equal(result.blocked, false);
  assert.match(result.detail, /neither a signed in state nor an error/);
});

test('a magic link is read from the inbox and followed', async () => {
  const page = new FakePage(/welcome back/i);
  const inbox = new FakeInbox([{
    seq: 3, at: '', provider: 'resend', kind: 'email',
    to: ['owner@example.test'], subject: 'Your sign in link',
    link: 'https://app.test/auth/callback?token=xyz',
  }]);

  const result = await signIn(page, { ...owner, login: 'magic_link' }, {
    baseURL: 'https://app.test', inbox, timeoutMs: 1_000,
  });
  assert.equal(result.ok, true);
  assert.equal(page.filled[FIELD.email.source], 'owner@example.test');
  assert.deepEqual(page.clicked, [CONTROL.sendLink.source]);
  assert.ok(page.visited.includes('https://app.test/auth/callback?token=xyz'));
});

test('a magic link that never arrives is blocked and says what did arrive', async () => {
  const page = new FakePage(/dashboard/i);
  const inbox = new FakeInbox([{
    seq: 1, at: '', provider: 'resend', kind: 'email',
    to: ['someone.else@example.test'], subject: 'Welcome',
  }]);

  const result = await signIn(page, { ...owner, login: 'magic_link' }, {
    baseURL: 'https://app.test', inbox, timeoutMs: 60,
  });
  assert.equal(result.blocked, true);
  assert.match(result.detail, /someone.else@example.test/);
});

test('a one time code is read from the inbox and entered', async () => {
  const page = new FakePage(/dashboard/i);
  const inbox = new FakeInbox([{
    seq: 2, at: '', provider: 'resend', kind: 'email',
    to: ['owner@example.test'], subject: 'Your code', code: '481920',
  }]);

  const result = await signIn(page, { ...owner, login: 'email_code' }, {
    baseURL: 'https://app.test', inbox, timeoutMs: 1_000,
  });
  assert.equal(result.ok, true);
  assert.equal(page.filled[FIELD.code.source], '481920');
});

test('a code strategy with no inbox is blocked', async () => {
  const page = new FakePage(/dashboard/i);
  const result = await signIn(page, { ...owner, login: 'sms_code' }, { baseURL: 'https://app.test' });
  assert.equal(result.blocked, true);
  assert.match(result.detail, /no inbox is available/);
});

test('a persona that does not sign in is left alone', async () => {
  const page = new FakePage(null);
  const result = await signIn(page, { ...owner, login: 'none' }, { baseURL: 'https://app.test' });
  assert.equal(result.ok, true);
  assert.deepEqual(page.visited, [], 'it must not even open a sign in page');
});

test('fields are matched by accessible name, anchored', () => {
  // Anchored, because "email" unanchored matches a newsletter box on a
  // marketing page and signs the agent up for a mailing list instead.
  assert.ok(FIELD.email.test('Email address'));
  assert.ok(FIELD.email.test('email'));
  assert.ok(!FIELD.email.test('Email me about new features'));
  assert.ok(FIELD.password.test('Password'));
  assert.ok(!FIELD.password.test('Forgot your password?'));
  assert.ok(CONTROL.signIn.test('Sign in'));
  assert.ok(!CONTROL.signIn.test('Sign in with Google'));
});
