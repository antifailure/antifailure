import { test } from 'node:test';
import { execFileSync } from 'node:child_process';
import assert from 'node:assert/strict';
import { createServer, type Server } from 'node:http';
import { createServer as createNetServer } from 'node:net';
import { mkdtempSync } from 'node:fs';
import { readFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Session } from '../src/browser.ts';
import { run, type WorkflowResult } from '../src/execute.ts';
import { explore, type Goal } from '../src/explore.ts';
import { exitCodeFor } from '../src/verdict.ts';
import type { Planner, Workflow } from '../src/workflow.ts';
import type { Persona } from '../src/login.ts';

/** A tiny application with a real sign up form, served over HTTP.
 *
 * A real browser against a real server, because the whole value of this layer
 * is that it drives a page the way a person does, and a fake page proves
 * nothing about whether it can.
 */
function application(
  options: { readonly breakIt?: boolean; readonly serverError?: boolean } = {},
): {
  server: Server; url: Promise<string>;
} {
  const accounts = new Set<string>();
  const server = createServer((req, res) => {
    const url = new URL(req.url ?? '/', 'http://localhost');
    // The root page itself never renders, the way examples/next-app answered
    // every request once af init left its migrate key blank: the database
    // has no tables, and the page throws before it writes a single tag.
    if (options.serverError) {
      res.writeHead(500, { 'content-type': 'text/html' });
      res.end('<html><body>relation "customers" does not exist</body></html>');
      return;
    }
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

// The control plane's own showcase example, reproduced: a page that answers
// every request with a 500 because af init never wrote the migrate key that
// would have created its tables. Three rewrites of the workflow's expectation
// all came back UNVERIFIED against this, quoting the wording as the problem,
// because nothing ever looked at the response the real browser had already
// received. A real navigation through a real Session is what proves the fix
// reaches the actual code path and not just the unit around it.
test('a page that answers every request with a server error FAILS naming the status, not unverified', { timeout: 120_000 }, async () => {
  const { server, url } = application({ serverError: true });
  const baseURL = await url;
  const artifacts = mkdtempSync(join(tmpdir(), 'af-runner-'));
  try {
    const results = await run({
      baseURL, artifacts, workflows: [signUp], personas: nobody, attempts: 1,
    });
    const [result] = results;
    assert.equal(result!.outcome.verdict, 'fail', JSON.stringify(result!.outcome, null, 2));
    assert.equal(result!.outcome.cause, 'application-error');
    assert.match(result!.outcome.detail, /500/, 'the status has to be in the report, not just af logs');
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

/** An application shaped like this repository's own control plane console.
 *
 * Every detail below is one that was live when six Dogfood workflows came back
 * blocked, and each one on its own is enough to block all six:
 *
 *   - there is no /login. The console is a static export with a file per
 *     route, and every unknown path answers with its 404 page.
 *   - the email field's label carries its own hint text, so its accessible
 *     name is not "Email address" but "Email address We send a link that signs
 *     you in. No password."
 *   - the button says "Send a sign-in link".
 *
 * A fixture with a tidy `<label for>` and a button reading "Send link" passes
 * against a runner that cannot drive any real application, which is what the
 * fixture above this one was doing.
 */
function protectedConsole(): { server: Server; url: Promise<string>; sent: () => boolean } {
  let linked = false;
  // Whether the application has sent the sign-in mail yet.
  //
  // A captured inbox holds nothing until something sends something, and the
  // fake here used to hand the message over on every read, including the reads
  // that happen before the button is pressed. That is not an inbox, it is the
  // answer left on the table, and it is the one shape that hides the defect
  // this fixture's own comment is about: a magic link is single use, so a
  // second attempt that matches the first attempt's message follows a token
  // already spent.
  let sent = false;
  const server = createServer((req, res) => {
    const url = new URL(req.url ?? '/', 'http://localhost');
    const html = (code: number, body: string) => {
      res.writeHead(code, { 'content-type': 'text/html' });
      res.end(`<html><body>${body}</body></html>`);
    };
    if (req.method === 'POST' && url.pathname === '/auth/email') {
      sent = true;
      return html(200, '<h1>Check your mail</h1><p>A sign-in link is on its way.</p>');
    }
    if (url.pathname === '/auth/link') {
      linked = true;
      return html(200, '<h1>Environments</h1><p>Repository</p><a href="/logout">Sign out</a>');
    }
    if (url.pathname !== '/environments') {
      return html(404, '<h1>That page is not here</h1><p>The address does not match anything.</p>');
    }
    if (linked) {
      return html(200, '<h1>Environments</h1><p>Repository</p><a href="/logout">Sign out</a>');
    }
    return html(200, `<h1>Sign in</h1>
      <form method="POST" action="/auth/email">
        <label><span>Email address</span>
          <input name="email" type="email" required>
          <span>We send a link that signs you in. No password.</span>
        </label>
        <button type="submit">Send a sign-in link</button>
      </form>`);
  });
  const url = new Promise<string>((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      resolve(`http://127.0.0.1:${typeof addr === 'object' && addr ? addr.port : 0}`);
    });
  });
  return { server, url, sent: () => sent };
}

test('it signs in where the form actually is, not where /login would be', { timeout: 120_000 }, async () => {
  const { server, url, sent } = protectedConsole();
  const baseURL = await url;
  const artifacts = mkdtempSync(join(tmpdir(), 'af-runner-'));
  try {
    const results = await run({
      baseURL, artifacts, attempts: 1,
      workflows: [{
        name: 'sign-in-with-a-link',
        description: 'Ask for a sign-in link, follow it, and land signed in.',
        startPath: '/environments',
        expect: ['Repository'],
      }],
      personas: [{ name: 'owner', email: 'owner@example.test', login: 'magic_link' }],
      inbox: {
        async list() {
          if (!sent()) return [];
          return [{
            seq: 1, at: new Date().toISOString(), provider: 'resend', kind: 'email',
            to: ['owner@example.test'], subject: 'Your sign-in link',
            text: `Sign in: ${baseURL}/auth/link`,
            links: [`${baseURL}/auth/link`], link: `${baseURL}/auth/link`,
          }];
        },
      },
    });
    const [result] = results;
    assert.equal(result!.outcome.verdict, 'pass', JSON.stringify(result!.outcome, null, 2));
    assert.ok(
      result!.steps.some((s) => /Sign in as owner: Signed in/.test(s)),
      result!.steps.join(' | '),
    );
  } finally {
    server.close();
  }
});

test('no sign-in form anywhere is blocked, and says which paths it tried', { timeout: 120_000 }, async () => {
  // Every path answers with the 404 page, which is what /login did. The point
  // of the assertion is the message: `locator.fill: Timeout 10000ms exceeded`
  // names the regex the runner used and nothing a reader can act on.
  const server = createServer((_req, res) => {
    res.writeHead(404, { 'content-type': 'text/html' });
    res.end('<html><body><h1>That page is not here</h1></body></html>');
  });
  const baseURL = await new Promise<string>((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      resolve(`http://127.0.0.1:${typeof addr === 'object' && addr ? addr.port : 0}`);
    });
  });
  const artifacts = mkdtempSync(join(tmpdir(), 'af-runner-'));
  try {
    const results = await run({
      baseURL, artifacts, attempts: 1,
      workflows: [{
        name: 'sign-in-with-a-link',
        description: 'Ask for a sign-in link.',
        startPath: '/environments',
        expect: ['Repository'],
      }],
      personas: [{ name: 'owner', email: 'owner@example.test', login: 'magic_link' }],
      inbox: { async list() { return []; } },
    });
    const [result] = results;
    assert.equal(result!.outcome.verdict, 'blocked', JSON.stringify(result!.outcome, null, 2));
    const said = result!.steps.join(' | ');
    assert.match(said, /No sign-in form was found for owner/);
    assert.match(said, /\/environments/);
    assert.match(said, /\/login/);
  } finally {
    server.close();
  }
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

test('the snapshot never offers a control nobody could press', { timeout: 120_000 }, async () => {
  // The console's mobile menu button, which a desktop layout hides with
  // display: none. It was offered to the planner at desktop width, pressing it
  // could never work, and the click timed out and ended two of this
  // repository's own explorations as blocked with nothing explored.
  const server = createServer((_req, res) => {
    res.writeHead(200, { 'content-type': 'text/html' });
    res.end(`<html><head><title>Members</title></head><body>
      <header style="display: none">
        <button type="button" aria-label="Open the menu">&#9776;</button>
      </header>
      <h1>Members</h1>
      <a href="/invitations">Invitations</a>
    </body></html>`);
  });
  const baseURL = await new Promise<string>((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      resolve(`http://127.0.0.1:${typeof addr === 'object' && addr ? addr.port : 0}`);
    });
  });
  const session = await Session.open({ artifacts: mkdtempSync(join(tmpdir(), 'af-hidden-')) });
  try {
    await session.page().goto(`${baseURL}/`);
    const seen = await session.snapshot();
    // Both halves, so a snapshot that offered nothing at all cannot pass.
    assert.ok(seen.controls.includes('Invitations'), `the visible link is missing from ${seen.controls.join(', ')}`);
    assert.ok(!seen.controls.includes('Open the menu'),
      `a control hidden with display: none was offered: ${seen.controls.join(', ')}`);
  } finally {
    await session.close('hidden-control').catch(() => undefined);
    server.close();
  }
});

test('a press that starts a navigation which never finishes does not end the run', { timeout: 120_000 }, async () => {
  // Continue with GitHub, inside an environment that never lets github.com
  // answer. The click landed, Playwright then waited for the navigation it
  // started, the navigation never committed, and the ten second timeout threw
  // out of the press and ended a whole exploration as blocked with nothing
  // explored. Modelled by a host that accepts the connection and never replies.
  const silent = createNetServer((socket) => { socket.on('error', () => undefined); });
  const silentPort = await new Promise<number>((resolve) => {
    silent.listen(0, '127.0.0.1', () => {
      const addr = silent.address();
      resolve(typeof addr === 'object' && addr ? addr.port : 0);
    });
  });
  const server = createServer((_req, res) => {
    res.writeHead(200, { 'content-type': 'text/html' });
    res.end(`<html><head><title>Sign in</title></head><body>
      <h1>Sign in</h1>
      <a href="http://127.0.0.1:${silentPort}/login/oauth/authorize">Continue with GitHub</a>
      <a href="/email">Send a sign-in link</a>
    </body></html>`);
  });
  const baseURL = await new Promise<string>((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      resolve(`http://127.0.0.1:${typeof addr === 'object' && addr ? addr.port : 0}`);
    });
  });
  const session = await Session.open({ artifacts: mkdtempSync(join(tmpdir(), 'af-nowhere-')) });
  try {
    const page = session.page();
    await page.goto(`${baseURL}/`);
    await page.click(/^Continue with GitHub$/);
    // The press returned, and the page it left is still one the agent can
    // read and act on, which is what lets an exploration record it and move on.
    const seen = await session.snapshot();
    assert.ok(seen.controls.includes('Send a sign-in link'),
      `after the press the page offered: ${seen.controls.join(', ')}`);
  } finally {
    await session.close('nowhere').catch(() => undefined);
    server.close();
    silent.close();
  }
});

/** A form shaped like this repository's own careers form, served over HTTP.
 *
 * THE FAILURE THIS EXISTS FOR. Somebody filled in the careers form on
 * antifailure.dev and read "Could not reach the server." The obvious question
 * was why the agents had not found it, and the obvious answer was that they had
 * been pointed at an environment built from one commit, where the site and the
 * control plane necessarily agree. That answer was true and it was not the
 * whole of it. Pointed straight at the deployed site, the agent could not have
 * found it either, for four separate reasons, and every one of them is a
 * property of this form's SHAPE rather than of the target:
 *
 *  1. The required acknowledgment is a checkbox. A checkbox carries value="on"
 *     whether or not it is ticked, so the snapshot reported it as already
 *     filled and the planner skipped it.
 *  2. The role is a radio group, filled for the same wrong reason.
 *  3. "What have you built" is required and matches no known field shape, so it
 *     was left empty and the browser refused to submit the form at all.
 *  4. The button says "Send application", which is on no list of words that
 *     move a workflow forward, so nothing pressed it.
 *
 *  And after all four, the sentence the broken page shows was on no list of
 *  failure signals, so the run came back UNVERIFIED and exited zero.
 *
 * `reachable: false` is the deployed control plane refusing the request, which
 * is what a 404 from a version behind and a 403 from an unconfigured hostname
 * both look like from inside the page: the fetch rejects and the form shows its
 * own sentence.
 */
function careersForm(options: {
  readonly reachable: boolean;
  /** How long the control plane takes to answer. See the slow test below. */
  readonly answerAfterMs?: number;
}): {
  server: Server; url: Promise<string>; received: Record<string, unknown>[];
} {
  const received: Record<string, unknown>[] = [];
  const server = createServer((req, res) => {
    const url = new URL(req.url ?? '/', 'http://localhost');
    if (req.method === 'POST' && url.pathname === '/v1/applications') {
      if (!options.reachable) {
        // No CORS header and a refusal, which is what the browser turns into a
        // rejected fetch and the page turns into its banner. Modelled here as
        // a plain refusal because this server is same origin: what the test
        // needs is the page's failure branch, and the page takes it on any
        // response it cannot confirm.
        res.writeHead(404, { 'content-type': 'application/json' });
        res.end('{}');
        return;
      }
      let body = '';
      req.on('data', (c) => { body += c; });
      req.on('end', () => {
        received.push(JSON.parse(body || '{}') as Record<string, unknown>);
        setTimeout(() => {
          res.writeHead(201, { 'content-type': 'application/json' });
          res.end(JSON.stringify({ id: 'written-down', recorded: true }));
        }, options.answerAfterMs ?? 0);
      });
      return;
    }
    res.writeHead(200, { 'content-type': 'text/html' });
    res.end(`<html><body>
      <a href="/signin">Sign in</a>
      <h1>Careers</h1>
      <form id="apply">
        <fieldset>
          <legend>Which role</legend>
          <label for="r1"><input id="r1" type="radio" name="role" value="founding_engineer" required> Founding engineer</label>
          <label for="r2"><input id="r2" type="radio" name="role" value="founding_growth" required> Founding growth</label>
        </fieldset>
        <label for="n">Your name</label><input id="n" name="name" required>
        <label for="e">Email</label><input id="e" name="email" type="email" required>
        <label for="w">What have you built or grown, and why this role</label>
        <textarea id="w" name="why" required></textarea>
        <div hidden aria-hidden="true">
          <label for="hp">Company</label><input id="hp" name="website" tabindex="-1">
        </div>
        <label for="c"><input id="c" name="compensation" type="checkbox" required> I understand there is no salary for either role currently.</label>
        <button type="submit">Send application</button>
      </form>
      <div id="out"></div>
      <script>
        document.getElementById('apply').addEventListener('submit', async (event) => {
          event.preventDefault();
          const data = new FormData(event.target);
          const values = {
            name: data.get('name'), email: data.get('email'), role: data.get('role'),
            why: data.get('why'), website: data.get('website') || '',
            compensationAcknowledged: data.get('compensation') === 'on',
          };
          let response;
          try {
            response = await fetch('/v1/applications', {
              method: 'POST', headers: { 'content-type': 'application/json' },
              body: JSON.stringify(values),
            });
            if (!response.ok) throw new Error('refused');
            const recorded = await response.json();
            if (recorded.recorded !== true) throw new Error('unconfirmed');
          } catch {
            document.getElementById('out').textContent =
              'Could not reach the server. Check your connection and press it again; nothing you typed is lost.';
            return;
          }
          document.getElementById('apply').remove();
          document.getElementById('out').textContent =
            'It is written down. Your application is in the private queue a person reads, oldest first.';
        });
      </script>
    </body></html>`);
  });
  const url = new Promise<string>((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      resolve(`http://127.0.0.1:${typeof addr === 'object' && addr ? addr.port : 0}`);
    });
  });
  return { server, url, received };
}

const apply: Workflow = {
  name: 'apply-for-a-founding-role',
  description: 'Apply for a founding role and confirm the application is recorded.',
  startPath: '/careers',
  // Quoted, so the sentence is required on the page character for character.
  // This is what tools/sitesmoke sends, and the unquoted form is satisfied by
  // its own words being scattered about a page of prose.
  expect: ['"It is written down."'],
};

// Collected rather than asserted one at a time, and that is not a style either.
//
// `assert` throws on the first failure, so five assertions after a failing one
// are unreachable and still look alive. Driving a real browser costs eight
// seconds a run, so splitting them into six tests would cost a minute; keeping
// them in one test with a plain `assert` would mean every mutation of any of
// these lines reported the same first failure, and a mutation table built on
// that says nothing about which assertion catches which break.
function problem(into: string[], ok: boolean, said: string) {
  if (!ok) into.push(said);
}

test('it completes a form with a radio, a required acknowledgment and an unfamiliar field',
  { timeout: 120_000 }, async () => {
    const { server, url, received } = careersForm({ reachable: true });
    const baseURL = await url;
    const artifacts = mkdtempSync(join(tmpdir(), 'af-runner-'));
    try {
      const results = await run({
        baseURL, artifacts, workflows: [apply], personas: nobody, attempts: 1,
      });
      const result = results[0]!;
      const sent = (received[0] ?? {}) as Record<string, unknown>;
      const found: string[] = [];
      problem(found, result.outcome.verdict === 'pass',
        `the verdict is ${result.outcome.verdict}, not pass: ${result.outcome.detail}`);
      // The verdict alone would pass on a form that was never sent, because a
      // page that never changed shows no failure signal either. What proves
      // the agent completed it is the application the server received.
      problem(found, received.length === 1,
        `the server received ${received.length} applications, not one`);
      problem(found, sent.role === 'founding_engineer', 'no role was chosen from the radio group');
      problem(found, sent.compensationAcknowledged === true,
        'the required acknowledgment was not ticked');
      problem(found, String(sent.why ?? '').length > 0,
        'the required field nothing recognises was left empty');
      problem(found, sent.website === '', 'the agent filled the hidden honeypot field');
      problem(found, String(sent.email ?? '').includes('@'), 'no email address was typed');
      assert.deepEqual(found, [], `${found.join('\n')}\nsteps:\n${result.steps.join('\n')}`);
    } finally {
      server.close();
    }
  });

test('a form that cannot reach its server FAILS, and the report quotes what the page said',
  { timeout: 120_000 }, async () => {
    // The whole point of this one is the verdict. Before the failure signals
    // learned this sentence it came back UNVERIFIED, which exits zero, so a
    // careers form that could not reach its control plane produced a green run
    // with a sad line in a log.
    const { server, url, received } = careersForm({ reachable: false });
    const baseURL = await url;
    const artifacts = mkdtempSync(join(tmpdir(), 'af-runner-'));
    try {
      const results = await run({
        baseURL, artifacts, workflows: [apply], personas: nobody, attempts: 1,
      });
      const result = results[0]!;
      const found: string[] = [];
      problem(found, result.outcome.verdict === 'fail',
        `the verdict is ${result.outcome.verdict}, not fail`);
      problem(found, result.outcome.cause === 'expectation-not-met',
        `the cause is ${result.outcome.cause}, not expectation-not-met`);
      problem(found, /Could not reach the server/.test(result.outcome.detail),
        'the report does not quote the sentence the page showed');
      problem(found, exitCodeFor([result.outcome]) === 8,
        `the run exits ${exitCodeFor([result.outcome])}, not 8`);
      problem(found, received.length === 0, 'a refused server still recorded something');
      assert.deepEqual(found, [],
        `${found.join('\n')}\ndetail: ${result.outcome.detail}\nsteps:\n${result.steps.join('\n')}`);
    } finally {
      server.close();
    }
  });

test('a control plane that takes its time does not make the agent walk away',
  { timeout: 120_000 }, async () => {
    // THE FAILURE, and it is the one that turned a working production origin
    // red about one run in ten. A press returns the instant the click lands,
    // and this form disables its own fieldset for exactly as long as the
    // request takes. So the snapshot read a page with no fields and a button
    // labelled "Recording it", decided nothing there moved the workflow
    // forward, and followed the "Sign in" link in the header instead. The
    // agent had filled in a form and then navigated away from it, and the
    // run reported a careers page that offered nothing.
    //
    // `networkidle` cannot see this: it asks whether the CURRENT DOCUMENT has
    // already reached that state, and a client rendered page reached it
    // seconds ago and stays there. Nor can waiting for the rendered text to
    // stop changing, because the text is perfectly stable for the whole time
    // the page is at its least readable. What settles it is the request
    // itself, which is what the session now counts.
    //
    // A second and a half, which is slower than the deployed control plane on
    // a good day and well inside what it does on a bad one.
    const { server, url, received } = careersForm({ reachable: true, answerAfterMs: 1_500 });
    const baseURL = await url;
    const artifacts = mkdtempSync(join(tmpdir(), 'af-runner-'));
    try {
      const results = await run({
        baseURL, artifacts, workflows: [apply], personas: nobody, attempts: 1,
      });
      const result = results[0]!;
      const found: string[] = [];
      problem(found, result.outcome.verdict === 'pass',
        `the verdict is ${result.outcome.verdict}, not pass: ${result.outcome.detail}`);
      problem(found, received.length === 1,
        `the server received ${received.length} applications, not one`);
      problem(found, !result.steps.some((s) => /Press Sign in/.test(s)),
        'the agent left the form it had filled in and followed the header link');
      assert.deepEqual(found, [], `${found.join('\n')}\nsteps:\n${result.steps.join('\n')}`);
    } finally {
      server.close();
    }
  });

/** An application that reports, as the name of its only control, what the
 *  browser that opened it looks like: touch or a mouse, the window's width,
 *  and whether the user agent is a phone's. The heading is the path, so the
 *  page an exploration started on is readable from the page it stood on.
 *
 *  It says so through a control rather than through text because a control's
 *  name is what an exploration's journey records, which makes the device a
 *  fact in the result rather than something a test has to scrape. */
function deviceReporter(): { server: Server; url: Promise<string> } {
  const server = createServer((req, res) => {
    const path = new URL(req.url ?? '/', 'http://localhost').pathname;
    res.writeHead(200, { 'content-type': 'text/html' });
    res.end(`<!doctype html><html><head>
      <meta name="viewport" content="width=device-width, initial-scale=1">
      <title>${path}</title></head><body><h1>${path}</h1>
      <button type="button" id="report">Report</button>
      <script>
        document.getElementById('report').textContent = [
          'Report',
          navigator.maxTouchPoints > 0 ? 'touch' : 'mouse',
          String(window.innerWidth),
          /Mobile/.test(navigator.userAgent) ? 'phone' : 'desktop',
        ].join(' ');
      </script></body></html>`);
  });
  const url = new Promise<string>((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      resolve(`http://127.0.0.1:${typeof addr === 'object' && addr ? addr.port : 0}`);
    });
  });
  return { server, url };
}

const twoPersonas: Persona[] = [
  { name: 'owner', email: 'owner@example.test', login: 'none' },
  { name: 'viewer', email: 'viewer@example.test', login: 'none' },
];

const invoices: Goal = {
  name: 'invoices',
  goal: 'Download the latest invoice.',
  seed: 'invoices',
  maxSteps: 1,
};

test('an exploration pointed at a persona, a page and a phone is run exactly that way',
  { timeout: 120_000 }, async () => {
  const { server, url } = deviceReporter();
  const baseURL = await url;
  try {
    const [x] = await explore({
      baseURL,
      artifacts: mkdtempSync(join(tmpdir(), 'af-explore-')),
      goals: [{
        ...invoices,
        persona: 'viewer',
        startPath: '/settings/billing',
        viewport: { name: 'phone', width: 390, height: 844, mobile: true },
        steered: '--persona viewer --start /settings/billing --viewport phone',
      }],
      personas: twoPersonas,
    });
    assert.equal(x!.outcome.cause, 'explored', JSON.stringify(x!.outcome, null, 2));

    // The persona: the second one declared, not the first the runner falls
    // back to.
    assert.equal(x!.persona, 'viewer');
    assert.ok(x!.steps.some((s) => /^Sign in as viewer/.test(s)), x!.steps.join(' | '));

    // The page: the browser stood on it first, not on the front page.
    assert.equal(x!.startPath, '/settings/billing');
    assert.equal(x!.visited[0], `${baseURL}/settings/billing`);

    // The phone, as the page itself saw it: a touch screen, 390 wide, and a
    // phone's user agent. A window resized to 390 alone would say mouse and
    // desktop, which is the half of a phone a size cannot give.
    const click = x!.journey.find((m) => m.kind === 'click');
    assert.equal(click?.kind === 'click' ? click.control : '', 'Report touch 390 phone');
    assert.deepEqual(x!.viewport, { name: 'phone', width: 390, height: 844, mobile: true });

    // And the replay line points the same way.
    assert.match(x!.outcome.reproduction.join('\n'), /--seed invoices --persona viewer --start \/settings\/billing --viewport phone/);
  } finally {
    server.close();
  }
});

test('the same exploration unsteered runs as the first persona, from the front page, in the default window',
  { timeout: 120_000 }, async () => {
  // The control for the test above. Without it, a runner that ignored every
  // steering field and happened to open a phone would pass that test too.
  const { server, url } = deviceReporter();
  const baseURL = await url;
  try {
    const [x] = await explore({
      baseURL,
      artifacts: mkdtempSync(join(tmpdir(), 'af-explore-')),
      goals: [invoices],
      personas: twoPersonas,
    });
    assert.equal(x!.outcome.cause, 'explored', JSON.stringify(x!.outcome, null, 2));
    assert.equal(x!.persona, 'owner');
    assert.equal(x!.startPath, '/');
    assert.equal(x!.visited[0], `${baseURL}/`);
    const click = x!.journey.find((m) => m.kind === 'click');
    assert.equal(click?.kind === 'click' ? click.control : '', 'Report mouse 1280 desktop');
    assert.deepEqual(x!.viewport, { name: '', width: 1280, height: 800, mobile: false });
    assert.doesNotMatch(x!.outcome.reproduction.join('\n'), /--viewport|--persona|--start/);
  } finally {
    server.close();
  }
});

test('a viewport that is not a size is refused rather than explored on a desktop',
  { timeout: 120_000 }, async () => {
  const [x] = await explore({
    baseURL: 'http://127.0.0.1:1',
    artifacts: mkdtempSync(join(tmpdir(), 'af-explore-')),
    goals: [{ ...invoices, viewport: { name: 'phone', width: 0, height: 844, mobile: true } }],
    personas: nobody,
  });
  assert.equal(x!.outcome.verdict, 'blocked');
  assert.equal(x!.outcome.cause, 'runner-failure');
  assert.match(x!.missing[0]!, /not a size this runner can open/);
  assert.equal(x!.journey.length, 0, 'nothing was explored in a window nobody asked for');
});

test('on a phone the snapshot offers what the phone layout shows, and not what it hides',
  { timeout: 120_000 }, async () => {
  // The same defect as the test above, from the other side. A layout hides its
  // navigation below a breakpoint and shows a menu button instead, and an
  // exploration pointed at a phone must be offered the phone's controls. If it
  // were offered the desktop navigation it would press a link nobody on a phone
  // can see, and a steered viewport would be reporting the desktop's friction.
  const server = createServer((_req, res) => {
    res.writeHead(200, { 'content-type': 'text/html' });
    res.end(`<!doctype html><html><head>
      <meta name="viewport" content="width=device-width, initial-scale=1">
      <title>Plans</title>
      <style>
        .phone { display: none }
        @media (max-width: 600px) { .phone { display: inline } .desktop { display: none } }
      </style></head><body>
      <nav class="desktop"><a href="/pricing">Pricing</a></nav>
      <button class="phone" type="button" aria-label="Open the menu">&#9776;</button>
      <h1>Plans</h1>
      <a href="/invoices">Invoices</a>
    </body></html>`);
  });
  const baseURL = await new Promise<string>((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      resolve(`http://127.0.0.1:${typeof addr === 'object' && addr ? addr.port : 0}`);
    });
  });
  const offered = async (options: { viewport?: { width: number; height: number }; mobile?: boolean }) => {
    const session = await Session.open({ artifacts: mkdtempSync(join(tmpdir(), 'af-breakpoint-')), ...options });
    try {
      await session.page().goto(`${baseURL}/`);
      return (await session.snapshot()).controls;
    } finally {
      await session.close('breakpoint').catch(() => undefined);
    }
  };
  try {
    const phone = await offered({ viewport: { width: 390, height: 844 }, mobile: true });
    assert.ok(phone.includes('Invoices'), `the always visible link is missing on a phone: ${phone.join(', ')}`);
    assert.ok(phone.includes('Open the menu'), `the phone's menu button was not offered: ${phone.join(', ')}`);
    assert.ok(!phone.includes('Pricing'), `navigation the phone layout hides was offered: ${phone.join(', ')}`);

    // The control: the same page in the default window is the desktop layout.
    const desktop = await offered({});
    assert.ok(desktop.includes('Pricing'), `the desktop navigation is missing: ${desktop.join(', ')}`);
    assert.ok(!desktop.includes('Open the menu'), `the phone's menu button was offered on a desktop: ${desktop.join(', ')}`);
  } finally {
    server.close();
  }
});

/** An application whose every page says the account exists, after a delay.
 *
 *  The delay is the whole subject. Held with a timer rather than a busy page,
 *  so the browser is genuinely waiting on the network the way it waits on a
 *  slow deploy, and closeAllConnections ends the wait when a test is done. */
function slowWelcome(delayMs: number): { server: Server; url: Promise<string> } {
  const server = createServer((_req, res) => {
    const reply = () => {
      res.writeHead(200, { 'content-type': 'text/html' });
      res.end(`<html><body><h1>Welcome</h1>
        <p>Your account is created and you are signed in.</p></body></html>`);
    };
    if (delayMs > 0) setTimeout(reply, delayMs); else reply();
  });
  const url = new Promise<string>((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      resolve(`http://127.0.0.1:${typeof addr === 'object' && addr ? addr.port : 0}`);
    });
  });
  return { server, url };
}

test('a workflow slower than its declared time budget is blocked, names the budget, and is not retried',
  { timeout: 120_000 }, async () => {
  // Before this, budget.duration reached nothing and a page that never
  // answered held a workflow for the browser's own thirty second timeout on
  // every attempt, then judged whatever was on the screen.
  const { server, url } = slowWelcome(8_000);
  const baseURL = await url;
  try {
    const started = Date.now();
    const [result] = await run({
      baseURL, artifacts: mkdtempSync(join(tmpdir(), 'af-budget-')), attempts: 2,
      workflows: [{ ...signUp, name: 'slow-welcome', maxMs: 2_000 }],
      personas: nobody,
    });
    const elapsed = Date.now() - started;
    assert.equal(result!.outcome.verdict, 'blocked', JSON.stringify(result!.outcome, null, 2));
    assert.equal(result!.outcome.cause, 'budget-exhausted');
    assert.match(result!.outcome.detail, /time budget of 2s/);
    assert.equal(result!.outcome.attempts.length, 1, 'a second attempt started after the budget was spent');
    assert.ok(elapsed < 8_000, `the budget did not cap the workflow: it ran for ${elapsed} ms`);
  } finally {
    server.closeAllConnections();
    server.close();
  }
});

test('a workflow inside its declared time budget passes as it always did', { timeout: 120_000 }, async () => {
  // The control for the test above: the same page answered at once, under a
  // budget it fits, is a pass rather than a workflow the cap interrupted.
  const { server, url } = slowWelcome(0);
  const baseURL = await url;
  try {
    const [result] = await run({
      baseURL, artifacts: mkdtempSync(join(tmpdir(), 'af-budget-')), attempts: 1,
      workflows: [{ ...signUp, name: 'quick-welcome', maxMs: 20_000 }],
      personas: nobody,
    });
    assert.equal(result!.outcome.verdict, 'pass', JSON.stringify(result!.outcome, null, 2));
  } finally {
    server.close();
  }
});

/** A planner that opens the same page forever, so the only thing that can end
 *  an attempt is the step budget, and every step is one countable line. */
function reopening(url: string): Planner {
  return { next: async () => ({ kind: 'goto', url, why: 'again' }) };
}

/** runSteps drives a workflow with a declared step budget through the planner
 *  above and returns the result and how many steps it actually took. */
async function runSteps(
  page: string, maxSteps: number, attempts = 1,
): Promise<{ result: WorkflowResult; taken: number }> {
  const { server, url } = slowWelcome(0);
  const baseURL = await url;
  try {
    const [result] = await run({
      baseURL, artifacts: mkdtempSync(join(tmpdir(), 'af-steps-')), attempts,
      workflows: [{
        name: 'steps', description: 'Reopen the page until the budget ends.',
        expect: [page], maxSteps,
      }],
      personas: nobody,
      planner: reopening(`${baseURL}/`),
    });
    return { result: result!, taken: result!.steps.filter((s) => s.endsWith(': again')).length };
  } finally {
    server.close();
  }
}

test('a declared step budget of 5 stops at 5, not at the runner\'s own 40', { timeout: 120_000 }, async () => {
  const { result, taken } = await runSteps('The invoice was downloaded.', 5);
  assert.equal(taken, 5, result.steps.join(' | '));
  // Running out of steps with an expectation missing is not a pass and not a
  // plain failure: nothing contradicted the expectation, the budget ran out.
  assert.equal(result.outcome.verdict, 'blocked', JSON.stringify(result.outcome, null, 2));
  assert.equal(result.outcome.cause, 'budget-exhausted');
  assert.match(result.outcome.detail, /^Stopped at its budget of 5 steps: the page it reached does not show what was expected\./);
});

test('a declared step budget of 60 is not silently capped at the runner\'s 40', { timeout: 240_000 }, async () => {
  const { result, taken } = await runSteps('The invoice was downloaded.', 60);
  assert.equal(taken, 60, `the workflow took ${taken} steps`);
  assert.match(result.outcome.detail, /budget of 60 steps/);
});

test('a workflow that uses every step and shows everything it expected passes', { timeout: 120_000 }, async () => {
  // The page says the account exists on every visit, so when the steps run out
  // the workflow did reach what it was asked to reach, and it passed.
  const { result, taken } = await runSteps('The account is created and the session is signed in.', 2);
  assert.ok(taken <= 2, result.steps.join(' | '));
  assert.equal(result.outcome.verdict, 'pass', JSON.stringify(result.outcome, null, 2));
});

/** browsersBelow is every Chromium process descended from pid. */
function browsersBelow(pid: number): number[] {
  let children: number[] = [];
  try {
    children = execFileSync('pgrep', ['-P', String(pid)], { encoding: 'utf8' })
      .split('\n').map((l) => Number(l.trim())).filter((n) => n > 0);
  } catch {
    // pgrep exits 1 when there are none.
  }
  return children.flatMap((c) => {
    let command = '';
    try { command = execFileSync('ps', ['-o', 'command=', '-p', String(c)], { encoding: 'utf8' }); } catch { /* gone */ }
    return [...(/chrom/i.test(command) ? [c] : []), ...browsersBelow(c)];
  });
}

test('a budget spent while the browser is still starting leaves no browser running', { timeout: 120_000 }, async () => {
  // The attempt is raced against the time budget, and the race can end while
  // Chromium is still launching. The launch finished after the race with
  // nothing left to close the browser it produced, so a workflow stopped at
  // its budget left a browser and its video recorder running for the rest of
  // the run. Checked in this process rather than through main.ts, because
  // main.ts exits, and Playwright closes its browsers when the process exits,
  // which hides the leak from anything that only watches the process end.
  const { server, url } = slowWelcome(8_000);
  const baseURL = await url;
  try {
    const [result] = await run({
      baseURL, artifacts: mkdtempSync(join(tmpdir(), 'af-late-launch-')), attempts: 1,
      workflows: [{ ...signUp, name: 'late-launch', maxMs: 50 }],
      personas: nobody,
    });
    assert.equal(result!.outcome.cause, 'budget-exhausted', JSON.stringify(result!.outcome, null, 2));
    let left = browsersBelow(process.pid);
    for (let i = 0; i < 50 && left.length > 0; i++) {
      await new Promise((resolve) => setTimeout(resolve, 100));
      left = browsersBelow(process.pid);
    }
    assert.deepEqual(left, [], 'a browser launched for a workflow its budget stopped is still running');
  } finally {
    server.closeAllConnections();
    server.close();
  }
});

test('ordering 2, run for real: a step budget is retried, and spent on every attempt it is blocked', { timeout: 120_000 }, async () => {
  // A step budget is per attempt, so running out of steps does not end the
  // retry loop the way a spent time budget does. Both attempts run, and only
  // then is the workflow blocked with the budget named.
  const { result } = await runSteps('The invoice was downloaded.', 2, 2);
  assert.equal(result.outcome.attempts.length, 2, JSON.stringify(result.outcome, null, 2));
  assert.equal(result.outcome.verdict, 'blocked');
  assert.equal(result.outcome.cause, 'budget-exhausted');
  assert.match(result.outcome.detail, /budget of 2 steps/);
});

test('ordering 5, run for real: a real failure, then the time budget running out on the retry, is a failure', { timeout: 120_000 }, async () => {
  // The first attempt's three requests are answered with HTTP 500, which is
  // the application failing. Every later request waits eight seconds, so the
  // retry is still waiting when the six second budget runs out. The failure
  // the first attempt saw must not be hidden by the budget on the second.
  let served = 0;
  const server = createServer((_req, res) => {
    served++;
    if (served <= 3) {
      res.writeHead(500, { 'content-type': 'text/html' });
      res.end('<html><body><h1>Internal error</h1></body></html>');
      return;
    }
    setTimeout(() => {
      res.writeHead(200, { 'content-type': 'text/html' });
      res.end('<html><body><h1>Welcome</h1></body></html>');
    }, 8_000);
  });
  const baseURL = await new Promise<string>((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      resolve(`http://127.0.0.1:${typeof addr === 'object' && addr ? addr.port : 0}`);
    });
  });
  try {
    const [result] = await run({
      baseURL, artifacts: mkdtempSync(join(tmpdir(), 'af-ordering-5-')), attempts: 2,
      workflows: [{
        name: 'fail-then-slow', description: 'Reopen the page until the budget ends.',
        expect: ['The invoice was downloaded.'], maxSteps: 2, maxMs: 6_000,
      }],
      personas: nobody,
      planner: reopening(`${baseURL}/`),
    });
    const causes = result!.outcome.attempts.map((a) => a.cause);
    assert.deepEqual(causes, ['application-error', 'budget-exhausted'], JSON.stringify(result!.outcome, null, 2));
    assert.equal(result!.outcome.verdict, 'fail');
    assert.equal(result!.outcome.cause, 'application-error');
  } finally {
    server.closeAllConnections();
    server.close();
  }
});
