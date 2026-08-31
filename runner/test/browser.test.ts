import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer, type Server } from 'node:http';
import { mkdtempSync } from 'node:fs';
import { readFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { run } from '../src/execute.ts';
import { explore, type Goal } from '../src/explore.ts';
import { exitCodeFor } from '../src/verdict.ts';
import type { Workflow } from '../src/workflow.ts';
import type { Persona } from '../src/login.ts';

/** A tiny application with a real sign up form, served over HTTP.
 *
 * A real browser against a real server, because the whole value of this layer
 * is that it drives a page the way a person does, and a fake page proves
 * nothing about whether it can.
 */
function application(options: { readonly breakIt?: boolean } = {}): {
  server: Server; url: Promise<string>;
} {
  const accounts = new Set<string>();
  const server = createServer((req, res) => {
    const url = new URL(req.url ?? '/', 'http://localhost');
    if (req.method === 'POST' && url.pathname === '/signup') {
      let body = '';
      req.on('data', (c) => { body += c; });
      req.on('end', () => {
        const form = new URLSearchParams(body);
        const email = form.get('email') ?? '';
        if (options.breakIt) {
          res.writeHead(200, { 'content-type': 'text/html' });
          res.end('<html><body><h1>Something went wrong</h1></body></html>');
          return;
        }
        accounts.add(email);
        res.writeHead(200, { 'content-type': 'text/html' });
        res.end(`<html><body><h1>Welcome</h1>
          <p>Your account is created and you are signed in.</p>
          <p>Signed in as ${email}</p>
          <a href="/logout">Sign out</a></body></html>`);
      });
      return;
    }
    res.writeHead(200, { 'content-type': 'text/html' });
    res.end(`<html><body><h1>Create your account</h1>
      <form method="POST" action="/signup">
        <label for="email">Email address</label>
        <input id="email" name="email" type="email" required>
        <label for="password">Password</label>
        <input id="password" name="password" type="password" required>
        <button type="submit">Create account</button>
      </form></body></html>`);
  });

  const url = new Promise<string>((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      resolve(`http://127.0.0.1:${typeof addr === 'object' && addr ? addr.port : 0}`);
    });
  });
  return { server, url };
}

const signUp: Workflow = {
  name: 'sign-up',
  description: 'Sign up for a new account with a fresh email address.',
  expect: ['The account is created and the session is signed in.'],
};

const nobody: Persona[] = [{ name: 'visitor', email: 'visitor@example.test', login: 'none' }];

test('it drives a real sign up form and passes', { timeout: 120_000 }, async () => {
  const { server, url } = application();
  const baseURL = await url;
  const artifacts = mkdtempSync(join(tmpdir(), 'af-runner-'));
  try {
    const results = await run({
      baseURL, artifacts, workflows: [signUp], personas: nobody, attempts: 1,
    });
    assert.equal(results.length, 1);
    const [result] = results;
    assert.equal(result!.outcome.verdict, 'pass', JSON.stringify(result!.outcome, null, 2));
    // It found the fields by their labels and pressed the button by its name.
    assert.ok(result!.steps.some((s) => /Fill.*[Ee]mail/.test(s)), result!.steps.join(' | '));
    assert.ok(result!.steps.some((s) => /Press.*[Cc]reate account/.test(s)));
    // Evidence is captured on a pass too, because the run that failed is the
    // one that is hardest to reproduce and nobody knows in advance.
    assert.ok(result!.evidence.screenshot, 'a screenshot is captured');
    assert.ok(result!.evidence.trace, 'a trace is captured');
  } finally {
    server.close();
  }
});

test('an application error is a failure, with steps to reproduce it', { timeout: 120_000 }, async () => {
  const { server, url } = application({ breakIt: true });
  const baseURL = await url;
  const artifacts = mkdtempSync(join(tmpdir(), 'af-runner-'));
  try {
    const results = await run({
      baseURL, artifacts, workflows: [signUp], personas: nobody, attempts: 1,
    });
    const [result] = results;
    assert.equal(result!.outcome.verdict, 'fail', JSON.stringify(result!.outcome, null, 2));
    assert.ok(result!.outcome.reproduction.length > 0, 'a failure comes with steps to follow');
    assert.match(result!.outcome.reproduction.join('\n'), /Expected:/);
  } finally {
    server.close();
  }
});

test('a page that cannot be reached is blocked, not failed', { timeout: 120_000 }, async () => {
  // Nothing is listening. That is the runner or the environment, not the
  // application, and reporting it as a failing test would point at the wrong
  // thing entirely.
  const artifacts = mkdtempSync(join(tmpdir(), 'af-runner-'));
  const results = await run({
    baseURL: 'http://127.0.0.1:1', artifacts,
    workflows: [signUp], personas: nobody, attempts: 1,
  });
  assert.equal(results[0]!.outcome.verdict, 'blocked');
  assert.equal(results[0]!.outcome.cause, 'runner-failure');
});

/** A small application with friction planted in it, served over HTTP.
 *
 * Every one of the four things this test asserts is a real page defect that a
 * declared workflow could not report: the workflow would have been written
 * against the route that works, passed, and said nothing about the button on
 * the front page that does nothing.
 */
function shop(): { server: Server; url: Promise<string> } {
  const page = (title: string, body: string) =>
    `<html><head><title>${title}</title></head><body>${body}</body></html>`;

  const server = createServer((req, res) => {
    const url = new URL(req.url ?? '/', 'http://localhost');
    res.writeHead(200, { 'content-type': 'text/html' });
    switch (url.pathname) {
      case '/plans':
        res.end(page('Plans', `<h1>Plans</h1><p>Free and paid.</p>
          <a href="/checkout">Upgrade to paid plan</a>
          <a href="/">Home</a>`));
        return;
      case '/checkout':
        res.end(page('Checkout', `<h1>Checkout</h1>
          <form method="GET" action="/done">
            <label for="email">Email address</label>
            <input id="email" name="email" type="email">
            <label for="card">Card number</label>
            <input id="card" name="card" type="text">
            <button type="submit">Pay now</button>
          </form>`));
        return;
      case '/done':
        res.end(page('Done', `<h1>Done</h1>
          <p>Your workspace is on the paid plan.</p>`));
        return;
      case '/help':
        // A page with nowhere to go from it.
        res.end(page('Help', `<h1>Help</h1><p>Read the manual.</p>`));
        return;
      default:
        res.end(page('Home', `<h1>Home</h1><p>Welcome.</p>
          <button type="button">Upgrade plan</button>
          <a href="/plans">Plans</a>
          <a href="/help">Help</a>
          <a href="/settings"><img src="data:image/gif;base64,R0lGODlhAQABAAAAACw="></a>`));
        return;
    }
  });

  const url = new Promise<string>((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      resolve(`http://127.0.0.1:${typeof addr === 'object' && addr ? addr.port : 0}`);
    });
  });
  return { server, url };
}

const upgrade: Goal = {
  name: 'upgrade',
  goal: 'Upgrade the workspace to the paid plan.',
  seed: 'browser-replay',
  maxSteps: 20,
};

test('it explores a real application, finds friction, and replays from the seed',
  { timeout: 240_000 }, async () => {
  const { server, url } = shop();
  const baseURL = await url;
  try {
    const first = await explore({
      baseURL,
      artifacts: mkdtempSync(join(tmpdir(), 'af-explore-')),
      goals: [upgrade], personas: nobody,
    });
    const [run1] = first;
    assert.ok(run1, 'one goal produces one exploration');

    // An exploration never counts against the change. Nobody declared what
    // this application should do on the pages it wandered onto.
    assert.equal(run1.outcome.verdict, 'pass', JSON.stringify(run1.outcome, null, 2));
    assert.equal(run1.outcome.cause, 'explored');
    assert.equal(exitCodeFor([run1.outcome]), 0);

    // It found its way to the paid plan through the route that works.
    assert.equal(run1.reached, true, run1.steps.join(' | '));

    // The button on the front page that does nothing, named and located.
    const inert = run1.findings.find((f) => f.kind === 'no_effect');
    assert.ok(inert, `no no_effect finding in ${run1.findings.map((f) => f.kind).join(', ')}`);
    assert.equal(inert.control, 'Upgrade plan');
    assert.equal(inert.url, `${baseURL}/`);

    // The link whose only content is an image with no alt text.
    const nameless = run1.findings.find((f) => f.kind === 'unnamed_control');
    assert.ok(nameless, 'the unlabelled link was not reported');
    assert.match(nameless.detail, /no accessible name/);

    // Evidence, the same as a declared workflow gets.
    assert.ok(run1.evidence.trace, 'a trace is captured');
    assert.ok(run1.evidence.screenshot, 'a screenshot is captured');
    assert.match(run1.outcome.reproduction.join('\n'), /--seed browser-replay/);

    // And the whole path replays. This is the claim that matters: a finding
    // arrives with a seed that reproduces the session it came from, rather
    // than with a trace of a browser that no longer exists.
    const second = await explore({
      baseURL,
      artifacts: mkdtempSync(join(tmpdir(), 'af-explore-')),
      goals: [upgrade], personas: nobody,
    });
    const [run2] = second;
    assert.deepEqual(run2!.journey, run1.journey);
    assert.deepEqual(run2!.findings, run1.findings);
    assert.deepEqual(run2!.visited, run1.visited);
  } finally {
    server.close();
  }
});

test('an exploration that cannot reach the application is blocked, not passed',
  { timeout: 120_000 }, async () => {
  // The same rule the declared runner follows. A run that explored nothing
  // must never read as a run that found nothing.
  const results = await explore({
    baseURL: 'http://127.0.0.1:1',
    artifacts: mkdtempSync(join(tmpdir(), 'af-explore-')),
    goals: [upgrade], personas: nobody,
  });
  assert.equal(results[0]!.outcome.verdict, 'blocked');
  assert.equal(results[0]!.findings.length, 0);
  assert.equal(results[0]!.missing.length, 1);
  assert.match(results[0]!.missing[0]!, /Nothing was explored/);
});

test('the snapshot reads rendered text, never markup', async () => {
  // The documented promise is that the model sees the accessibility snapshot
  // and never the page's raw HTML, and it is a promise people decide to trust
  // this product on. The prompt is checked in model.test.ts; this checks the
  // one line upstream of it, because a snapshot that captured markup would
  // keep every assertion there passing and break the claim anyway.
  //
  // Structural rather than behavioural, deliberately. What is being guarded is
  // that a specific call is not reached for, and there is no page that could
  // demonstrate its absence.
  const source = await readFile(new URL('../src/browser.ts', import.meta.url), 'utf8');
  assert.match(source, /locator\('body'\)\.innerText\(\)/,
    'the snapshot no longer reads rendered text');
  for (const forbidden of ['innerHTML', 'outerHTML', '.content()', 'documentElement.outerHTML']) {
    assert.ok(!source.includes(forbidden),
      `browser.ts reads ${forbidden}, so the page's markup can reach the model`);
  }
});
