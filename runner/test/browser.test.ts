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
