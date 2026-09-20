import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

// Nothing anywhere drove main.ts until this file existed, and that is how a
// defect that made `af explore` impossible on every machine survived.
//
// The engine marshals its job document from Go, where a nil slice becomes
// null and an empty one becomes []. The exploration path never sets
// `workflows`, so every `af explore` run sent `"workflows": null`, and
// main.ts read `doc.workflows.length` before it looked at the goals. The
// command exited AF-AGT-003 with a TypeError every time.
//
// Both halves worked. The runner's own suite tests `explore()` directly, and
// the one Go test that reaches a real subprocess replaces `node` with a shell
// script. The document between them was never sent by anything but the
// product, and the product is what broke.
//
// So this drives the real entry point with the real document shapes. It needs
// no browser: a document with no goals and no workflows must produce two empty
// lists and exit cleanly, which is exactly the line that used to throw.

const here = dirname(fileURLToPath(import.meta.url));
const main = join(here, '..', 'src', 'main.ts');

interface RunnerOutput {
  results: unknown[];
  explorations: unknown[];
}

async function runMain(doc: Record<string, unknown>, environment = process.env): Promise<{
  code: number | null;
  stdout: string;
  stderr: string;
}> {
  const artifacts = mkdtempSync(join(tmpdir(), 'af-runner-main-'));
  const child = spawn(process.execPath, ['--experimental-strip-types', main], {
    stdio: ['pipe', 'pipe', 'pipe'],
    env: environment,
  });
  let stdout = '';
  let stderr = '';
  child.stdout.setEncoding('utf8');
  child.stderr.setEncoding('utf8');
  child.stdout.on('data', (c: string) => {
    stdout += c;
  });
  child.stderr.on('data', (c: string) => {
    stderr += c;
  });
  child.stdin.end(JSON.stringify({ artifacts, headless: true, ...doc }));
  const code: number | null = await new Promise((resolve) => {
    child.on('close', (c) => resolve(c));
  });
  return { code, stdout, stderr };
}

test('a document whose workflows are null is a document with no workflows', async () => {
  // This is the exact shape Go sends for every exploration. Before the fix it
  // produced "TypeError: Cannot read properties of null (reading 'length')"
  // and no output at all, which the engine reports as AF-AGT-003.
  const { code, stdout, stderr } = await runMain({
    base_url: 'http://127.0.0.1:1',
    workflows: null,
    goals: [],
    personas: null,
  });
  assert.equal(code, 0, `the runner exited ${code}: ${stderr}`);
  const doc = JSON.parse(stdout) as RunnerOutput;
  assert.deepEqual(doc.results, []);
  assert.deepEqual(doc.explorations, []);
});

test('a runner with no workflows does not claim it is using a model', async () => {
  const result = await runMain({
    base_url: 'http://127.0.0.1:1', workflows: [], goals: [], personas: [],
  }, { ...process.env, ANTHROPIC_API_KEY: 'AF_FAKE_UNUSED_MODEL_KEY' });
  if (result.code !== 0) throw new Error(result.stderr);
  assert.doesNotMatch(result.stderr, /reading pages with/);
});

test('a document with no workflows key at all is the same answer', async () => {
  const { code, stdout, stderr } = await runMain({
    base_url: 'http://127.0.0.1:1',
    goals: [],
  });
  assert.equal(code, 0, `the runner exited ${code}: ${stderr}`);
  const doc = JSON.parse(stdout) as RunnerOutput;
  assert.deepEqual(doc.results, []);
  assert.deepEqual(doc.explorations, []);
});

test('the runner still says nothing on stdout when the document is not JSON', async () => {
  // The engine treats silence as the runner's own failure and reports the
  // stderr, so this asserts the contract rather than a message: something on
  // stderr, nothing on stdout, and a non zero exit.
  const artifacts = mkdtempSync(join(tmpdir(), 'af-runner-main-'));
  const child = spawn(process.execPath, ['--experimental-strip-types', main], {
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  let stdout = '';
  let stderr = '';
  child.stdout.setEncoding('utf8');
  child.stderr.setEncoding('utf8');
  child.stdout.on('data', (c: string) => {
    stdout += c;
  });
  child.stderr.on('data', (c: string) => {
    stderr += c;
  });
  child.stdin.end('not a document at all');
  const code: number | null = await new Promise((resolve) => {
    child.on('close', (c) => resolve(c));
  });
  assert.notEqual(code, 0);
  assert.equal(stdout, '');
  assert.ok(stderr.includes('af-runner:'), stderr);
  assert.ok(artifacts.length > 0);
});

// The desktop surface, at the entry point.
//
// These exist because of the shape of the bug that was nearly shipped here.
// While the desktop driver was scaffolded, main.ts sent every non-web,
// non-terminal surface through assertAvailable, which threw. The moment the
// driver became available that same line stopped throwing and NOTHING took its
// place: `results` stayed the empty array it is initialised to and the run
// returned zero of everything with exit code zero. A surface that cannot be
// driven must fail, and a surface that can be driven must actually be driven.
// An available driver with no call site is the same green nothing as an
// unavailable one, and it is quieter.

test('a desktop run that names no application is refused, not reported as a clean run', async () => {
  const { code, stdout, stderr } = await runMain({
    base_url: 'http://127.0.0.1:1',
    surface: 'desktop',
    workflows: [{ name: 'anything', description: 'Do something.', expect: ['anything'] }],
    personas: [],
  });
  assert.notEqual(code, 0, `a desktop run with no application exited cleanly: ${stdout}`);
  assert.match(stderr, /names no application to drive/);
});

test('a desktop run reaches the desktop driver rather than returning nothing', async () => {
  // An Electron binary that does not exist. The point is not the launch, it is
  // that the run REACHED a driver: the failure has to come from the desktop
  // driver saying it could not start that application, which is a blocked
  // result with a reason, and not from an empty result list with a zero exit.
  const { code, stdout } = await runMain({
    base_url: 'http://127.0.0.1:1',
    surface: 'desktop',
    desktop: { kind: 'electron', executablePath: '/nonexistent/not/an/electron', timeoutMs: 5000 },
    workflows: [{ name: 'reaches the driver', description: 'Do something.', expect: ['anything'] }],
    personas: [],
  });
  const parsed = JSON.parse(stdout) as {
    results: { workflow: string; outcome: { verdict: string; cause: string; detail: string } }[];
    blocked: number; passed: number; failed: number;
  };
  // One result, for the one workflow. An empty list here is the whole defect.
  assert.equal(parsed.results.length, 1, `the desktop surface produced no result at all: ${stdout}`);
  assert.equal(parsed.results[0]!.workflow, 'reaches the driver');
  // Blocked, never failed: an application that will not start is the
  // environment's debt and not evidence about anything under test.
  assert.equal(parsed.results[0]!.outcome.verdict, 'blocked');
  assert.equal(parsed.blocked, 1);
  assert.equal(parsed.passed, 0);
  assert.match(parsed.results[0]!.outcome.detail, /did not start/);
  // Zero, and deliberately. exitCodeFor exits zero on blocked, because a
  // blocked run has already said what was missing and failing the build on it
  // would make an incomplete environment indistinguishable from a broken
  // application. The desktop surface obeys that rule like every other one,
  // which is the point of returning WorkflowResult rather than a shape of its
  // own: the exit code does not know which surface ran.
  assert.equal(code, 0, 'a blocked desktop run should exit zero, like every other blocked run');
});

// The mobile surfaces, at the entry point, guarding the one failure that is
// quieter than the scaffold it replaced.
//
// While a surface is scaffolded main.ts sends it through assertAvailable,
// which THROWS. The moment `available` becomes true that throw stops, and if
// no dispatch takes its place the run falls through with `results` still the
// empty array it was initialised to: zero passed, zero failed, and EXIT CODE
// ZERO, because exitCodeFor only fails a run when a verdict counts against the
// application. A customer would be told a mobile run passed by a run that
// drove nothing. These three cases are the ones that can reach that state.

test('an ios run with workflows reaches the driver rather than returning nothing', async () => {
  // A device that does not exist, so this needs no simulator and still proves
  // the dispatch was REACHED: reaching it is what tries to prepare the device
  // and fails. The empty-green path would instead exit zero with two empty
  // lists, which is exactly what this asserts against.
  const { code, stdout, stderr } = await runMain({
    surface: 'ios',
    base_url: 'http://unused.invalid',
    mobile: { id: 'dev.antifailure.probe', device: 'not-a-real-udid' },
    workflows: [{ name: 'sign in', description: 'Sign in.', expect: ['Welcome'] }],
    personas: [],
  });
  assert.notEqual(code, 0, 'a mobile run that could not reach a device exited zero');
  // AND THE FAILURE CAME FROM THE DRIVER, not from the guard that catches a
  // missing dispatch. Both exit non zero, so asserting only the exit code
  // cannot tell "the dispatch ran and the device was absent" from "there is no
  // dispatch at all", and mutation testing proved it: deleting the ios branch
  // left this test green because main.ts's own `driven` check refused the run
  // instead. The distinction is the whole point of the test.
  assert.doesNotMatch(stderr, /nothing in \S+ drives it/,
    'the ios branch is gone and only the missing-dispatch guard refused the run');
  if (stdout.trim()) {
    const out = JSON.parse(stdout) as RunnerOutput & { passed: number };
    assert.notDeepEqual(
      { results: out.results, passed: out.passed }, { results: [], passed: 0 },
      'the run reported an empty, passing document having driven nothing');
  }
});

test('an ios run with no workflows is refused rather than reported as passing', async () => {
  const { code, stdout } = await runMain({
    surface: 'ios',
    base_url: 'http://unused.invalid',
    mobile: { id: 'dev.antifailure.probe' },
    workflows: [],
    personas: [],
  });
  assert.notEqual(code, 0, 'a mobile run with nothing to drive exited zero');
  assert.equal(stdout.trim(), '', 'a refused run still emitted a result document');
});

test('an android run is refused loudly, because no run has ever driven it', async () => {
  // The scaffold's throw is a TRUE statement and a silent green is not, so a
  // surface nobody has driven keeps the loud refusal however complete its code
  // looks. This is what flips when somebody proves android, and it should flip
  // in the same commit as the run that proves it.
  const { code, stderr } = await runMain({
    surface: 'android',
    base_url: 'http://unused.invalid',
    mobile: { id: 'dev.antifailure.probe' },
    workflows: [{ name: 'sign in', description: 'Sign in.', expect: ['Welcome'] }],
    personas: [],
  });
  assert.notEqual(code, 0);
  assert.match(stderr, /not implemented yet|NOT yet proven/);
});
