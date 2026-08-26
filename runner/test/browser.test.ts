import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer, type Server } from 'node:http';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { run } from '../src/execute.ts';
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
