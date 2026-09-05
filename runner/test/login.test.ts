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

  /** Which paths show the sign-in form. Every path, by default, which is the
   *  application that answers a protected route with its sign-in screen. */
  signInAt: readonly string[] | null = null;

  async goto(url: string) { this.visited.push(url); this.current = url; }
  async fill(field: RegExp, value: string) { this.filled[field.source] = value; }
  checked: string[] = [];
  async check(field: RegExp) { this.checked.push(field.source); }
  async has(_field: RegExp) {
    if (this.signInAt === null) return true;
    return this.signInAt.some((p) => this.current.endsWith(p));
  }
  /** What the application does when a control is pressed.
   *
   * A sign-in message is captured in response to the request, never before it.
   * Modelling that ordering here is what the tests below are about: an inbox
   * seeded up front describes an application that sent the message before
   * anybody asked, which no application does. */
  onClick: (() => void) | undefined;

  async click(control: RegExp) {
    this.clicked.push(control.source);
    this.onClick?.();
  }
  async waitForAny(patterns: readonly RegExp[]): Promise<RegExp | null> {
    if (this.outcome === null) return null;
    return patterns.find((p) => p.source === this.outcome!.source) ?? null;
  }
  async text() { return ''; }
  url() { return this.current; }
}

class FakeInbox implements InboxSource {
  messages: Message[];
  /** What has been sent but not captured yet, and how many more reads it
   *  takes to arrive.
   *
   * Delivery is not instant and the difference is the whole bug: a poll that
   * happens before the new message lands sees only the old one. An inbox that
   * hands over the fresh message on the first read cannot express that, and a
   * test written against one passes whether or not the floor is there. */
  #inFlight: { readonly reads: number; readonly message: Message } | undefined;
  #reads = 0;

  constructor(messages: Message[] = []) { this.messages = messages; }

  async list(): Promise<readonly Message[]> {
    this.#reads++;
    if (this.#inFlight && this.#reads > this.#inFlight.reads) {
      this.messages.push(this.#inFlight.message);
      this.#inFlight = undefined;
    }
    return this.messages;
  }

  /** deliver captures one message immediately, for a flow with nothing to
   *  race against. */
  deliver(message: Message) { this.messages.push(message); }

  /** deliverAfter captures one message once the inbox has been read that many
   *  more times, which is what a message in flight looks like from here. */
  deliverAfter(reads: number, message: Message) {
    this.#reads = 0;
    this.#inFlight = { reads, message };
  }
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
  const inbox = new FakeInbox();
  page.onClick = () => inbox.deliver({
    seq: 3, at: '', provider: 'resend', kind: 'email',
    to: ['owner@example.test'], subject: 'Your sign in link',
    link: 'https://app.test/auth/callback?token=xyz',
  });

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
  const inbox = new FakeInbox();
  page.onClick = () => inbox.deliver({
    seq: 2, at: '', provider: 'resend', kind: 'email',
    to: ['owner@example.test'], subject: 'Your code', code: '481920',
  });

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

test('a retry does not follow the link the previous attempt already spent', async () => {
  // A magic link is single use, and waitFor deliberately looks at what already
  // arrived before it waits: right for a message that need only exist, wrong
  // for one that has to be new. Without a floor read before the button is
  // pressed, a second attempt matched the first attempt's message and followed
  // a token the application had already burned, and the application answered
  // "This sign-in link is no longer valid."
  //
  // It was a race rather than a certainty, which is how it survived. Driving
  // this repository's own six workflows against a real environment produced
  // two that signed in and four that did not, out of one code path in one run.
  const spent = 'https://app.test/auth/callback?token=already-used';
  const fresh = 'https://app.test/auth/callback?token=asked-for-just-now';
  const inbox = new FakeInbox([{
    seq: 7, at: '', provider: 'resend', kind: 'email',
    to: ['owner@example.test'], subject: 'Sign in', link: spent,
  }]);
  const page = new FakePage(/welcome back/i);
  // One read later, so the first poll sees only the spent message. That is the
  // race as it actually happened, and a delivery on the first read would pass
  // with or without the fix.
  page.onClick = () => inbox.deliverAfter(1, {
    seq: 8, at: '', provider: 'resend', kind: 'email',
    to: ['owner@example.test'], subject: 'Sign in', link: fresh,
  });

  const result = await signIn(page, { ...owner, login: 'magic_link' }, {
    baseURL: 'https://app.test', inbox, timeoutMs: 1_000,
  });

  assert.equal(result.ok, true);
  assert.ok(page.visited.includes(fresh), 'followed the link it just asked for');
  assert.ok(!page.visited.includes(spent), 'did not follow the spent one');
});

test('a code the previous attempt already used is not entered again', async () => {
  // The same rule, for the same reason: a one time code is spent the same way
  // a link is.
  const inbox = new FakeInbox([{
    seq: 4, at: '', provider: 'resend', kind: 'email',
    to: ['owner@example.test'], subject: 'Your code', code: '111111',
  }]);
  const page = new FakePage(/dashboard/i);
  page.onClick = () => inbox.deliverAfter(1, {
    seq: 5, at: '', provider: 'resend', kind: 'email',
    to: ['owner@example.test'], subject: 'Your code', code: '222222',
  });

  const result = await signIn(page, { ...owner, login: 'email_code' }, {
    baseURL: 'https://app.test', inbox, timeoutMs: 1_000,
  });

  assert.equal(result.ok, true);
  assert.equal(page.filled[FIELD.code.source], '222222');
});
