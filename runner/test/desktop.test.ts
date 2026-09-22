// The desktop surface: the adapter that turns an accessibility tree into the
// snapshot the planner already consumes, the loop that drives it, and both of
// the real applications it was built against.
//
// Four layers, and each is tested against the thing it actually has to
// survive. The adapter is pure, so it is tested directly. The Electron reader
// is tested against the shapes Chrome DevTools Protocol really sends, taken
// from a dump of a running application rather than invented. The loop is
// tested by driving the SHIPPED runDesktop over a scripted surface, for the
// reason runner/src/explore.ts gives about its own Surface interface: a test
// that drove a copy of the loop would prove the copy correct and say nothing
// about what ships. And then the two real applications, because everything
// above this line could be right while the driver still cannot open a window.
//
// The real application tests are the only ones here that cannot run
// everywhere, and they say so rather than passing quietly. Native
// accessibility is a macOS permission a person grants in System Settings, and
// an Electron binary is not something this package depends on. When either is
// missing the test SKIPS with the reason named, which reads in the log as "not
// checked" and never as "checked and fine".

import { test, type TestContext } from 'node:test';
import assert from 'node:assert/strict';
import { createServer, type Server } from 'node:net';
import { mkdtempSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  locate, normalizeRole, snapshotFrom, walk, filledOf, chosen, type AxNode,
} from '../src/drivers/ax.ts';
import { treeFrom } from '../src/drivers/electron.ts';
import { runDesktop, desktop, withEnvironmentAddress, type DesktopApp } from '../src/drivers/desktop.ts';
import {
  AxError, trusted, screenIsLocked, GRANT_INSTRUCTION, LOCKED_SCREEN,
} from '../src/drivers/macax.ts';
import { driverFor } from '../src/drivers/driver.ts';
import { socketSink, decode, type LiveEvent } from '../src/live.ts';
import type { AxSurface } from '../src/drivers/surface.ts';
import type { Snapshot } from '../src/workflow.ts';

const here = dirname(fileURLToPath(import.meta.url));

const WHERE = { url: 'desktop://fixture', title: 'Fixture' };

/** A sign-in screen as an accessibility tree, in the macOS spelling. */
function signInTree(): AxNode {
  return {
    role: 'AXWindow', name: 'Ledger',
    children: [
      { role: 'AXStaticText', name: 'Sign in to Ledger' },
      { role: 'AXTextField', name: 'Email address', required: true },
      { role: 'AXTextField', name: 'Password', required: true, value: '' },
      { role: 'AXCheckBox', name: 'I accept the terms', required: true, checked: false },
      { role: 'AXButton', name: 'Sign in', isDefault: true },
      { role: 'AXLink', name: 'Forgot your password' },
      // An icon button with no label: a screen reader announces nothing and
      // no planner can press it.
      { role: 'AXButton', name: '' },
      { role: 'AXButton', name: 'Delete everything', enabled: false },
    ],
  };
}

test('the adapter turns an accessibility tree into the shape a browser produces', () => {
  const snapshot = snapshotFrom(signInTree(), WHERE);

  assert.equal(snapshot.url, 'desktop://fixture');
  assert.equal(snapshot.title, 'Fixture');
  assert.deepEqual(snapshot.fields, [
    { name: 'Email address', type: 'textbox', filled: false, required: true },
    { name: 'Password', type: 'textbox', filled: false, required: true },
    { name: 'I accept the terms', type: 'checkbox', filled: false, required: true },
  ]);
  // A disabled control is not offered: pressing one is a click that goes
  // nowhere and reports as a timeout rather than as the truth.
  assert.deepEqual(snapshot.controls, ['Sign in', 'Forgot your password']);
  // The submit comes from the platform's own default button, never a word list.
  assert.deepEqual(snapshot.submits, ['Sign in']);
  // The unlabelled button is counted rather than dropped: the count is the
  // only evidence that the application offers something nobody can reach.
  assert.equal(snapshot.unnamed, 1);
  assert.match(snapshot.text, /Sign in to Ledger/);
});

test('a radio is filled when its group has been answered, not when this option is the answer', () => {
  const tree: AxNode = {
    role: 'AXWindow', name: 'Plan',
    children: [{
      role: 'AXRadioGroup', name: 'Plan',
      children: [
        { role: 'AXRadioButton', name: 'Free', required: true, checked: false },
        { role: 'AXRadioButton', name: 'Pro', required: true, checked: true },
      ],
    }],
  };
  const snapshot = snapshotFrom(tree, WHERE);
  // BOTH options read as answered, because the group has been answered. Read
  // per option, a planner that skips filled fields would tick Free after Pro,
  // which in a radio group is changing its mind rather than making progress.
  assert.deepEqual(
    snapshot.fields.map((f) => [f.name, f.filled]),
    [['Free', true], ['Pro', true]],
  );

  const untouched = snapshotFrom({
    role: 'AXWindow', name: 'Plan',
    children: [{
      role: 'AXRadioGroup', name: 'Plan',
      children: [
        { role: 'AXRadioButton', name: 'Free', required: true, checked: false },
        { role: 'AXRadioButton', name: 'Pro', required: true, checked: false },
      ],
    }],
  }, WHERE);
  assert.deepEqual(
    untouched.fields.map((f) => [f.name, f.filled]),
    [['Free', false], ['Pro', false]],
  );
});

test('normalizeRole reduces every platform spelling of a role to one token', () => {
  assert.equal(normalizeRole('AXButton'), 'button');
  assert.equal(normalizeRole('button'), 'button');
  assert.equal(normalizeRole('XCUIElementTypeButton'), 'button');
  // The three spellings of a text field all have to reach the same rule, or a
  // workflow written for the web stops filling anything on the desktop.
  assert.equal(normalizeRole('AXTextField'), 'textbox');
  assert.equal(normalizeRole('textbox'), 'textbox');
  assert.equal(normalizeRole('XCUIElementTypeSecureTextField'), 'textbox');
  assert.equal(normalizeRole('AXRadioButton'), 'radio');
  assert.equal(normalizeRole('AXStaticText'), 'text');
});

test('locate finds an element by the name a screen reader announces, and refuses the rest', () => {
  const tree = signInTree();
  assert.equal(locate(tree, /^Email address$/i, 'field')?.role, 'AXTextField');
  assert.equal(locate(tree, /^Sign in$/i, 'control')?.role, 'AXButton');
  // A control is not a field and a field is not a control, so a click cannot
  // land on the text box that shares a label with the button beside it.
  assert.equal(locate(tree, /^Sign in$/i, 'field'), undefined);
  assert.equal(locate(tree, /^Nothing here$/i, 'control'), undefined);
  // A disabled control is not a match.
  assert.equal(locate(tree, /^Delete everything$/i, 'control'), undefined);
});

test('the visible text does not repeat a line the tree announced twice', () => {
  // An accessibility tree announces a heading and then the static text inside
  // it. Repeated, a six word screen reads as twelve, and an expectation is
  // judged against this string.
  const tree: AxNode = {
    role: 'AXWindow', name: 'Ledger',
    children: [
      { role: 'heading', name: 'Welcome back', children: [{ role: 'StaticText', name: 'Welcome back' }] },
      { role: 'button', name: 'Continue', children: [{ role: 'StaticText', name: 'Continue' }] },
    ],
  };
  assert.equal(snapshotFrom(tree, WHERE).text, 'Welcome back\nContinue');
});

test('an expectation cannot be met by what the agent itself typed into a field', () => {
  // The accessibility tree's version of a pseudo terminal echoing its input.
  // The only occurrence of the expected words on this screen is the value an
  // agent put in the box; the application said nothing. A snapshot that
  // reported that value as visible text would let a workflow satisfy itself,
  // which is a check that cannot say no.
  const typed = snapshotFrom({
    role: 'AXWindow', name: 'Ledger',
    children: [
      { role: 'AXStaticText', name: 'Sign in to Ledger' },
      { role: 'AXTextField', name: 'Email address', value: 'Welcome back' },
      { role: 'AXCheckBox', name: 'I accept the terms', checked: true },
    ],
  }, WHERE);
  assert.doesNotMatch(typed.text, /Welcome back/,
    `a typed value reached the text expectations are judged against: ${JSON.stringify(typed.text)}`);
  // The field is still a field, and still reads as answered. Only the text
  // changed, because only the text is evidence.
  assert.equal(typed.fields[0]?.filled, true);

  // And the other half, which is what stops this being a blunt instrument: a
  // value that is NOT a field's is still visible text. On macOS a static text
  // carries its words in AXValue rather than AXTitle, so dropping values
  // wholesale would empty the text of a native screen.
  const rendered = snapshotFrom({
    role: 'AXWindow', name: 'Ledger',
    children: [{ role: 'AXStaticText', name: '', value: 'Welcome back' }],
  }, WHERE);
  assert.match(rendered.text, /Welcome back/);
});

test('walk and filledOf answer about the element the planner is looking at', () => {
  const nodes = [...walk(signInTree())].map((n) => n.node.name);
  assert.equal(nodes[0], 'Ledger');
  assert.ok(nodes.includes('I accept the terms'));
  assert.equal(filledOf({ role: 'AXCheckBox', name: 'x', checked: true }, undefined), true);
  assert.equal(filledOf({ role: 'AXCheckBox', name: 'x', checked: false }, undefined), false);
  assert.equal(filledOf({ role: 'AXTextField', name: 'x', value: 'ada' }, undefined), true);
  assert.equal(filledOf({ role: 'AXTextField', name: 'x' }, undefined), false);
  assert.equal(chosen({ role: 'AXCheckBox', name: 'x' }), true);
  assert.equal(chosen({ role: 'AXTextField', name: 'x' }), false);
});

// Chrome DevTools Protocol sends its accessibility properties as STRINGS, and
// these fixtures are the shapes a running Electron application really
// produced, dumped from Accessibility.getFullAXTree rather than invented.

test('the Electron reader reads the string valued properties the protocol really sends', () => {
  const tree = treeFrom([
    { nodeId: '1', role: { value: 'RootWebArea' }, name: { value: '' }, childIds: ['2', '3', '4'] },
    {
      nodeId: '2', role: { value: 'textbox' }, name: { value: 'Email address' },
      properties: [{ name: 'required', value: { value: 'true' } }],
    },
    {
      nodeId: '3', role: { value: 'checkbox' }, name: { value: 'Remember me' },
      properties: [{ name: 'checked', value: { value: 'true' } }],
    },
    {
      nodeId: '4', role: { value: 'button' }, name: { value: 'Sign in' },
      properties: [{ name: 'disabled', value: { value: 'true' } }],
    },
  ], new Set());
  const kids = tree.children ?? [];
  assert.equal(kids[0]?.required, true);
  // "true" as a string, not true as a boolean. Read as a boolean this is
  // false for a ticked box, and the planner ticks it forever.
  assert.equal(kids[1]?.checked, true);
  assert.equal(kids[2]?.enabled, false);
});

test('the Electron reader reads aria-busy in the numeric shape Chromium really sends it', () => {
  // MEASURED, and the shape is the point: a region with aria-busy="true" came
  // back from a real Electron window as {type: "boolean", value: 1}, a NUMBER,
  // where checked and required arrive as strings. A reader that expected the
  // string "true" read every busy screen as idle and nothing would ever say so.
  const tree = treeFrom([
    { nodeId: '1', role: { value: 'RootWebArea' }, name: { value: 'Ledger' }, childIds: ['2', '3', '4'] },
    {
      nodeId: '2', role: { value: 'region' }, name: { value: 'Journal' },
      properties: [{ name: 'busy', value: { value: 1 } }],
    },
    {
      nodeId: '3', role: { value: 'region' }, name: { value: 'Totals' },
      properties: [{ name: 'busy', value: { value: 'true' } }],
    },
    {
      // Hidden from screen readers, so not the application saying anything.
      nodeId: '4', ignored: true, role: { value: 'none' },
      properties: [{ name: 'busy', value: { value: 1 } }],
    },
  ], new Set());
  const kids = tree.children ?? [];
  assert.equal(kids[0]?.busy, true);
  assert.equal(kids[1]?.busy, true);
  assert.equal(kids[2]?.busy, undefined);
  assert.equal(snapshotFrom(tree, WHERE).busy, true);
  // And a screen whose platform said nothing is the shape it always was.
  assert.equal('busy' in snapshotFrom(signInTree(), WHERE), false);
});

test('the Electron reader marks a required checkbox Chromium reports only as invalid', () => {
  // MEASURED against a real form: Chromium publishes required=true on a
  // required text input and publishes nothing of the kind on a required
  // checkbox. What it publishes there is invalid="true" until the box is
  // ticked. Read literally, every mandatory acknowledgment is optional, the
  // planner leaves it alone, and the form is refused by a box it was never
  // willing to tick.
  const tree = treeFrom([
    { nodeId: '1', role: { value: 'RootWebArea' }, name: { value: '' }, childIds: ['2', '3'] },
    {
      nodeId: '2', role: { value: 'checkbox' }, name: { value: 'I accept the terms' },
      properties: [
        { name: 'invalid', value: { value: 'true' } },
        { name: 'checked', value: { value: 'false' } },
      ],
    },
    // A text field whose typed value is wrong is invalid and is NOT required.
    // The inference is scoped to the roles Chromium leaves required off.
    {
      nodeId: '3', role: { value: 'textbox' }, name: { value: 'Email address' },
      properties: [{ name: 'invalid', value: { value: 'true' } }],
    },
  ], new Set());
  const kids = tree.children ?? [];
  assert.equal(kids[0]?.required, true, 'a required checkbox was read as optional');
  assert.equal(kids[1]?.required, undefined, 'an invalid text field was read as required');
});

test('the Electron reader keeps an ignored node so everything below it survives', () => {
  // A form wrapped in a presentational div is entirely inside an ignored node.
  // A reader that dropped it would report an empty screen.
  const tree = treeFrom([
    { nodeId: '1', role: { value: 'RootWebArea' }, name: { value: '' }, childIds: ['2'] },
    { nodeId: '2', role: { value: 'none' }, name: { value: '' }, ignored: true, childIds: ['3'] },
    { nodeId: '3', role: { value: 'button' }, name: { value: 'Sign in' } },
  ], new Set(['Sign in']));
  const snapshot = snapshotFrom(tree, WHERE);
  assert.deepEqual(snapshot.controls, ['Sign in']);
  // And the document's own answer to which control submits reaches the tree.
  assert.deepEqual(snapshot.submits, ['Sign in']);
});

test('the Electron reader survives a cycle rather than walking one forever', () => {
  const tree = treeFrom([
    { nodeId: '1', role: { value: 'RootWebArea' }, name: { value: '' }, childIds: ['2'] },
    { nodeId: '2', role: { value: 'button' }, name: { value: 'Loop' }, childIds: ['1'] },
  ], new Set());
  assert.deepEqual(snapshotFrom(tree, WHERE).controls, ['Loop']);
});

// The loop. Driven over a scripted surface, so the loop under test is the loop
// that ships rather than a copy of it.

/** scripted is a surface whose screens are given in advance.
 *
 * It moves to the next screen when a control is PRESSED and not when a field
 * is typed into, which is how an application behaves and is load bearing here:
 * a surface that advanced on every action would answer the planner's first
 * fill with the signed in screen, the planner would declare itself done, and
 * the test would pass without the submit control ever being pressed. That is
 * exactly what the first version of this helper did. */
function scripted(screens: readonly Snapshot[]): AxSurface & { readonly acted: string[] } {
  const acted: string[] = [];
  let at = 0;
  return {
    acted,
    async snapshot() { return screens[at]!; },
    async fill(field, value) { acted.push(`fill ${field.source} ${value}`); },
    async check(field) { acted.push(`check ${field.source}`); },
    async click(control) {
      acted.push(`click ${control.source}`);
      if (at < screens.length - 1) at++;
    },
    async close() { /* nothing to release */ },
  };
}

function screen(over: Partial<Snapshot>): Snapshot {
  return {
    url: 'desktop://fixture', title: 'Fixture',
    fields: [], controls: [], submits: [], unnamed: 0, text: '',
    ...over,
  };
}

test('the registry reports the desktop surface as available', () => {
  assert.equal(driverFor('desktop').available, true);
  assert.equal(desktop.available, true);
});

test('runDesktop passes when the screen shows what the workflow expected', async () => {
  const surface = scripted([
    screen({
      fields: [{ name: 'Email address', type: 'textbox', filled: false, required: true }],
      controls: ['Sign in'], submits: ['Sign in'], text: 'Sign in to Ledger',
    }),
    screen({ text: 'Welcome back. Your balance is 42 pounds.' }),
  ]);
  const results = await runDesktop({
    app: { kind: 'electron', executablePath: 'unused' },
    open: async () => surface,
    workflows: [{
      name: 'sign in',
      description: 'Sign in and confirm you land on a signed in screen.',
      expect: ['Welcome back'],
      answers: { 'Email address': 'ada@example.test' },
    }],
  });
  assert.equal(results.length, 1);
  assert.equal(results[0]!.outcome.verdict, 'pass');
  // It typed into the field by its accessible name and pressed the control by
  // its accessible name. No selector was involved.
  assert.ok(surface.acted.some((a) => a.startsWith('fill ^Email address$')),
    `the field was never filled: ${surface.acted.join(' | ')}`);
  assert.ok(surface.acted.some((a) => a.startsWith('click ^Sign in$')),
    `the control was never pressed: ${surface.acted.join(' | ')}`);
  assert.ok(results[0]!.steps.some((s) => s.startsWith('Fill Email address')));
});

// The address tests. A desktop client of a service has to be told which
// service to talk to, and before this it never was: an Electron application
// under a rehearsal reached whatever its own configuration named. These drive
// the SHIPPED runDesktop and read the application it actually handed to open,
// so they are about what launches and not about a helper in isolation.
test('runDesktop launches an Electron application with the environment address as AF_BASE_URL', async () => {
  let opened: DesktopApp | undefined;
  const results = await runDesktop({
    app: { kind: 'electron', executablePath: 'unused' },
    baseURL: 'http://127.0.0.1:39000',
    open: async (app) => { opened = app; return scripted([screen({ text: 'transfer.posted' })]); },
    workflows: [{ name: 'read', description: 'Read the journal.', expect: ['transfer.posted'] }],
  });
  assert.equal(results[0]!.outcome.verdict, 'pass');
  assert.ok(opened, 'open was never called');
  assert.equal(opened.kind, 'electron');
  assert.equal(opened.kind === 'electron' ? opened.env?.['AF_BASE_URL'] : undefined, 'http://127.0.0.1:39000');
});

test('the address is merged over the runner environment, not substituted for it', async () => {
  // Playwright REPLACES a child's whole environment when it is handed one, so
  // an Electron process given only AF_BASE_URL would start with no PATH and no
  // HOME and fail in a way that reads as the application's fault.
  const app = withEnvironmentAddress({ kind: 'electron', executablePath: 'unused' }, 'http://127.0.0.1:39000');
  assert.ok(app.kind === 'electron');
  assert.equal(app.env?.['PATH'], process.env['PATH']);
  assert.equal(app.env?.['HOME'], process.env['HOME']);
});

test('the environment address wins over one the developer shell exported', () => {
  // The run's own environment is the only address a rehearsal may send an
  // application to. A stale export in somebody's shell pointing at production
  // must not survive into the launch.
  const app = withEnvironmentAddress(
    { kind: 'electron', executablePath: 'unused', env: { AF_BASE_URL: 'https://ledger.example.com' } },
    'http://127.0.0.1:39000',
  );
  assert.ok(app.kind === 'electron');
  assert.equal(app.env?.['AF_BASE_URL'], 'http://127.0.0.1:39000');
});

test('a native application is not handed an address it could never receive', () => {
  // Launch Services starts a macOS application with the session's environment,
  // so a variable set here would be dropped. Returning the target untouched is
  // the honest answer; appearing to pass it would be dead wiring.
  const mac: DesktopApp = { kind: 'macos', name: 'Notes' };
  assert.equal(withEnvironmentAddress(mac, 'http://127.0.0.1:39000'), mac);
});

test('with no environment behind the run, the application is launched as it was', () => {
  const app: DesktopApp = { kind: 'electron', executablePath: 'unused' };
  assert.equal(withEnvironmentAddress(app, undefined), app);
  assert.equal(withEnvironmentAddress(app, ''), app);
});

test('runDesktop fails when the screen does not show what the workflow expected', async () => {
  const surface = scripted([
    screen({ controls: ['Sign in'], submits: ['Sign in'], text: 'Sign in to Ledger' }),
    screen({ text: 'Sign in to Ledger' }),
  ]);
  const results = await runDesktop({
    app: { kind: 'electron', executablePath: 'unused' },
    open: async () => surface,
    settle: FAST,
    workflows: [{
      name: 'a deliberately wrong expectation',
      description: 'Sign in.',
      // Quoted, so its absence is an answer rather than a shrug.
      expect: ['"Your order has shipped."'],
      maxSteps: 4,
    }],
  });
  // FAIL, not blocked and not unverified. This is the arm that shows the
  // instrument can say no.
  assert.equal(results[0]!.outcome.verdict, 'fail');
  assert.equal(results[0]!.outcome.cause, 'expectation-not-met');
  // And it says what was missing and what was there, without claiming an
  // error nothing on this screen showed.
  const detail = results[0]!.outcome.detail;
  assert.ok(detail.startsWith('"Your order has shipped." was not found.'), detail);
  assert.ok(!/shows an error|showed a failure/i.test(detail), `an error was invented: ${detail}`);
  assert.ok(detail.includes('No error was showing. Instead it showed: "Sign in to Ledger"'), detail);
});

test('runDesktop still says error when the screen genuinely shows one', async () => {
  const surface = scripted([
    screen({ controls: ['Sign in'], submits: ['Sign in'], text: 'Sign in to Ledger' }),
    screen({ text: 'Could not connect to the ledger service.' }),
  ]);
  const results = await runDesktop({
    app: { kind: 'electron', executablePath: 'unused' },
    open: async () => surface,
    settle: FAST,
    workflows: [{
      name: 'a sign in the application broke',
      description: 'Sign in.',
      expect: ['"Your order has shipped."'],
      maxSteps: 4,
    }],
  });
  assert.equal(results[0]!.outcome.cause, 'expectation-not-met');
  assert.ok(results[0]!.outcome.detail.includes(
    'The page shows an error rather than what was expected. It says: "Could not connect to the ledger service."'),
  results[0]!.outcome.detail);
});

// Loading. The failure these answer: an Electron ledger client that fetches
// its journal in the main process was judged on the skeleton it draws while
// the answer is in flight, and FAILED with "transfer.posted" not found in front
// of a ledger holding thousands of them. See runner/src/drivers/settle.ts.

/** FAST is the patience a test can afford: a short budget, and a quiet period
 *  short enough that a scripted screen's reads are milliseconds apart. */
const FAST = { budgetMs: 500, quietMs: 10 } as const;

/** The ledger client's loading screen, as its tree reads before the IPC
 *  answer lands: the heading, the words it shows while reading, the empty
 *  table's caption and header. Quoted from the failing run's own detail. */
const LOADING = screen({
  title: 'Ledger',
  text: 'Journal\nReading the ledger\nThe newest journal entries\nSEQ\nEVENT\nACCOUNT\nAMOUNT\nRECORDED',
});

function journal(event: string): Snapshot {
  return screen({
    title: 'Ledger',
    text: `Journal\nhttp://127.0.0.1:39000\n2 entries, newest first\nSEQ\nEVENT\n2\n${event}\n1\n${event}`,
  });
}

/** loadingThen is a surface that reads as `loading` for its first `reads`
 *  snapshots and as `loaded` after, which is what a client waiting on a fetch
 *  looks like to something reading its tree. Counted in reads rather than
 *  milliseconds so the test says the same thing on a loaded machine. */
function loadingThen(reads: number, loading: Snapshot, loaded: Snapshot): AxSurface & { readonly reads: () => number } {
  let n = 0;
  return {
    reads: () => n,
    async snapshot() { n++; return n <= reads ? loading : loaded; },
    async fill() { throw new Error('nothing on a journal is typed into'); },
    async check() { throw new Error('nothing on a journal is ticked'); },
    async click() { throw new Error('nothing on a journal is pressed'); },
    async close() { /* nothing to release */ },
  };
}

const READ_THE_JOURNAL = {
  name: 'read the journal',
  description: 'Open the journal and confirm posted transfers are listed.',
  expect: ['"transfer.posted"'],
} as const;

test('a screen that is still loading is judged once it has loaded, not on its loading screen', async () => {
  // Loading for six reads: longer than the first look, so the planner meets
  // the loading screen, finds nothing to press and says stuck. That stuck is
  // the moment the old loop turned into a FAIL.
  const surface = loadingThen(6, LOADING, journal('transfer.posted'));
  const results = await runDesktop({
    app: { kind: 'electron', executablePath: 'unused' },
    open: async () => surface,
    settle: FAST,
    workflows: [READ_THE_JOURNAL],
  });
  assert.equal(results[0]!.outcome.verdict, 'pass', results[0]!.outcome.detail);
  assert.ok(surface.reads() > 6, `the screen was never read after it loaded: ${surface.reads()} reads`);
});

test('a screen that loads into something to press is driven, not judged', async () => {
  // The loaded screen is not the answer yet: it offers a control the workflow
  // has to press. So a changed screen goes back to the planner, and judging it
  // on arrival would fail a workflow one press from passing.
  let pressed = false;
  let reads = 0;
  const surface: AxSurface = {
    async snapshot() {
      if (pressed) return journal('transfer.posted');
      reads++;
      return reads <= 4 ? LOADING
        : screen({ title: 'Ledger', controls: ['Show posted transfers'], text: 'Journal\n3000 entries' });
    },
    async fill() { /* never reached */ },
    async check() { /* never reached */ },
    async click() { pressed = true; },
    async close() { /* nothing to release */ },
  };
  const results = await runDesktop({
    app: { kind: 'electron', executablePath: 'unused' },
    open: async () => surface,
    settle: FAST,
    workflows: [{ ...READ_THE_JOURNAL, description: 'Press Show posted transfers.' }],
  });
  assert.equal(results[0]!.outcome.verdict, 'pass', results[0]!.outcome.detail);
  assert.ok(pressed, 'the loaded screen was judged instead of driven');
});

test('a screen that finishes loading without the expectation still fails, on the loaded screen', async () => {
  // The arm that proves waiting cannot manufacture a pass. The journal loads
  // and holds nothing but reversals, and the verdict is about THAT screen:
  // quoted, and not the loading screen before it.
  const surface = loadingThen(6, LOADING, journal('transfer.reversed'));
  const results = await runDesktop({
    app: { kind: 'electron', executablePath: 'unused' },
    open: async () => surface,
    settle: FAST,
    workflows: [READ_THE_JOURNAL],
  });
  const { verdict, cause, detail } = results[0]!.outcome;
  assert.equal(verdict, 'fail', detail);
  assert.equal(cause, 'expectation-not-met');
  assert.ok(detail.startsWith('"transfer.posted" was not found.'), detail);
  assert.ok(detail.includes('transfer.reversed'), `the loaded screen is not the one judged: ${detail}`);
  assert.ok(!detail.includes('Reading the ledger'), `the loading screen was judged: ${detail}`);
  assert.match(detail, /had stopped changing, so it was judged finished\.$/);
});

test('a screen that never stops changing is judged on its last read, and the verdict says so', async () => {
  // A clock, a counter, a feed: something that never holds still. It must not
  // hold a run forever, and a verdict on it must not pretend it was finished.
  // Still for its first two reads, so the first look settles on the loading
  // screen, and then moving forever: the verdict has to be about the last
  // read the watch took and not about the screen the planner gave up on.
  let n = 0;
  const surface: AxSurface = {
    async snapshot() {
      n++;
      return screen({ title: 'Ledger', text: n <= 2 ? 'Journal\nReading the ledger' : `Journal\nReading the ledger\nTick ${n}` });
    },
    async fill() { /* never reached */ },
    async check() { /* never reached */ },
    async click() { /* never reached */ },
    async close() { /* nothing to release */ },
  };
  const started = Date.now();
  const results = await runDesktop({
    app: { kind: 'electron', executablePath: 'unused' },
    open: async () => surface,
    settle: { budgetMs: 400, quietMs: 10 },
    workflows: [READ_THE_JOURNAL],
  });
  const elapsed = Date.now() - started;
  const { verdict, detail } = results[0]!.outcome;
  assert.equal(verdict, 'fail', detail);
  assert.match(detail, /The screen was still changing when it was judged/);
  // The LAST read is the one judged, not the first and not a stale one.
  assert.ok(detail.includes(`Tick ${n}"`), `judged a read other than the last (${n}): ${detail}`);
  // And bounded: one budget for the attempt, not one per question.
  assert.ok(elapsed < 400 * 1.75, `an attempt spent ${elapsed} ms against a 400 ms budget`);
});

test('the last press of a spent step budget is judged on what it loaded, not on its loading screen', async () => {
  // One step: press "Open journal", and the step budget is gone. The press is
  // exactly the thing whose effect has had the least time to draw, so the
  // verdict the budget produces gets the same patience as any other.
  let pressed = false;
  let after = 0;
  const surface: AxSurface = {
    async snapshot() {
      if (!pressed) return screen({ title: 'Ledger', controls: ['Open journal'], text: 'Ledger' });
      after++;
      return after <= 4 ? LOADING : journal('transfer.posted');
    },
    async fill() { /* never reached */ },
    async check() { /* never reached */ },
    async click() { pressed = true; },
    async close() { /* nothing to release */ },
  };
  const results = await runDesktop({
    app: { kind: 'electron', executablePath: 'unused' },
    open: async () => surface,
    settle: FAST,
    workflows: [{ ...READ_THE_JOURNAL, description: 'Press Open journal.', maxSteps: 1 }],
  });
  assert.ok(pressed, 'the control was never pressed');
  assert.equal(results[0]!.outcome.verdict, 'pass', results[0]!.outcome.detail);
});

test('a screen that is already still costs one extra read, not a wait', async () => {
  const surface = loadingThen(0, LOADING, journal('transfer.posted'));
  const started = Date.now();
  const results = await runDesktop({
    app: { kind: 'electron', executablePath: 'unused' },
    open: async () => surface,
    settle: { budgetMs: 10_000, quietMs: 10 },
    workflows: [READ_THE_JOURNAL],
  });
  assert.equal(results[0]!.outcome.verdict, 'pass', results[0]!.outcome.detail);
  // Two reads before the planner's first decision, and the pass needs no more.
  assert.equal(surface.reads(), 2);
  assert.ok(Date.now() - started < 1_000, 'a still screen was waited on');
});

test('runDesktop blocks, never fails, when the application could not be opened', async () => {
  const results = await runDesktop({
    app: { kind: 'macos', name: 'Nonesuch' },
    open: async () => { throw new AxError(GRANT_INSTRUCTION); },
    workflows: [{ name: 'anything', description: 'Do something.', expect: ['anything'] }],
  });
  // A permission nobody granted is the environment's debt, not the
  // application's, and charging it to the application is how people learn to
  // ignore these results.
  assert.equal(results[0]!.outcome.verdict, 'blocked');
  assert.equal(results[0]!.outcome.cause, 'environment-incomplete');
  assert.match(results[0]!.outcome.detail, /System Settings/);
});

test('runDesktop refuses a plan that asks to open a url rather than doing nothing', async () => {
  const results = await runDesktop({
    app: { kind: 'electron', executablePath: 'unused' },
    open: async () => scripted([screen({ text: 'Ledger' })]),
    planner: {
      async next() { return { kind: 'goto', url: 'https://example.test', why: 'a model said so' }; },
    },
    workflows: [{ name: 'navigate', description: 'Go somewhere.', expect: ['anything'] }],
  });
  // Blocked with the reason said out loud. Answered with a silent no-op, a
  // model would spend every remaining step opening a url that will never open.
  assert.equal(results[0]!.outcome.verdict, 'blocked');
  assert.match(results[0]!.outcome.detail, /no address bar/);
});

function collector(): Promise<{ path: string; lines: () => LiveEvent[]; close: () => void; server: Server }> {
  const dir = mkdtempSync(join(tmpdir(), 'af-desk-'));
  const path = join(dir, 'l.sock');
  let buffer = '';
  const server = createServer((s) => { s.setEncoding('utf8'); s.on('data', (c) => { buffer += c; }); });
  return new Promise((resolve) => {
    server.listen(path, () => resolve({
      path,
      lines: () => buffer.split('\n').map(decode).filter((e): e is LiveEvent => !!e),
      close: () => server.close(),
      server,
    }));
  });
}

test('runDesktop streams the agent lifecycle and its steps to a watcher', async () => {
  const c = await collector();
  try {
    const sink = socketSink(c.path);
    await runDesktop({
      app: { kind: 'electron', executablePath: 'unused' },
      live: sink,
      open: async () => scripted([
        screen({ controls: ['Continue'], text: 'Ledger' }),
        screen({ text: 'Welcome back' }),
      ]),
      workflows: [{
        name: 'streamed', description: 'Press Continue.', expect: ['Welcome back'], maxSteps: 4,
      }],
    });
    // close() is what flushes. A desktop run over a scripted surface finishes
    // inside one turn of the event loop, which is faster than the socket
    // connects, so there is nothing to poll for until it has.
    await sink.close();
    // Polled after the close, because close() is what flushes a run that
    // finished before its socket connected, and because a write leaving this
    // process is not the same event as the server having read it.
    const deadline = Date.now() + 2_000;
    const ended = () => c.lines().some(
      (e) => e.t === 'agent' && (e as { state: string }).state === 'ended');
    while (!ended() && Date.now() < deadline) await new Promise((r) => setTimeout(r, 10));

    const events = c.lines();
    const states = events.filter((e) => e.t === 'agent').map((e) => (e as { state: string }).state);
    assert.ok(states.includes('connecting'), `states were ${states.join(', ')}`);
    assert.ok(states.includes('live'), `states were ${states.join(', ')}`);
    assert.ok(states.includes('ended'), `states were ${states.join(', ')}`);
    const agent = events.find(
      (e) => e.t === 'agent' && (e as { surface: string }).surface === 'desktop');
    assert.ok(agent, 'the desktop agent did not declare its surface');
    const steps = events.filter((e) => e.t === 'step').map((e) => (e as { text: string }).text);
    assert.ok(steps.some((s) => s.startsWith('Press Continue')),
      `the cast did not stream to the watcher: ${steps.join(' | ')}`);
  } finally {
    // In a finally, so a failed assertion does not leave a listening server
    // behind: the test runner then waits on an event loop that never empties,
    // and one red assertion reads as a hung suite.
    c.close();
  }
});

// The real applications.
//
// These two are the difference between a driver that is correct on paper and
// one that has opened a window. Neither can run everywhere, and each says
// which step it is missing rather than passing quietly.
//
// AND A SKIP IS NOT ALLOWED TO BE THE LAST WORD ON A MACHINE THAT CLAIMS TO
// SUPPORT THIS. `AF_REQUIRE_DESKTOP` turns every reason below into a FAILURE
// instead of a skip, following the precedent tools/emulatorcheck sets with
// AF_REQUIRE_DOCKER and the api harness sets with AF_REQUIRE_DATABASE. Set it
// on a macOS host with the Accessibility grant and an Electron binary, and
// "this could not run" stops being an acceptable answer. Without it the skip
// carries its reason, which is what keeps a Linux continuous integration run
// honest rather than red for a surface it could never drive.

/** cannotCheck ends a test that could not look, loudly or quietly.
 *
 * Quietly by default, because a skip that names its reason is the honest
 * answer for a machine that genuinely cannot run the check. Loudly under
 * AF_REQUIRE_DESKTOP, because a machine that says it supports the desktop
 * surface and then skips its only real proof is reporting a pass it did not
 * earn, and a green count is exactly what nobody re reads. */
function cannotCheck(t: TestContext, reason: string): void {
  if (process.env['AF_REQUIRE_DESKTOP']) {
    assert.fail(
      `AF_REQUIRE_DESKTOP is set, so this cannot be skipped: ${reason}`,
    );
  }
  t.skip(`NOT CHECKED: ${reason}`);
}

/** electronBinary finds an Electron runtime to drive, or says why there is none.
 *
 * Electron is deliberately NOT a dependency of this package: it is a hundred
 * megabytes, the runner does not ship it, and the applications this driver
 * targets bring their own. So a machine that wants to run this test points
 * AF_ELECTRON_BINARY at one. */
function electronBinary(): { readonly path: string } | { readonly absent: string } {
  const named = process.env['AF_ELECTRON_BINARY'];
  if (named && existsSync(named)) return { path: named };
  if (named) {
    return { absent: `AF_ELECTRON_BINARY is set to ${named} and there is no such file.` };
  }
  return {
    absent:
      'no Electron runtime. This package does not depend on Electron, so a real ' +
      'Electron application can only be driven where one exists. Set AF_ELECTRON_BINARY to an ' +
      'Electron binary (node_modules/electron/dist/Electron.app/Contents/MacOS/Electron on ' +
      'macOS) and run this suite again.',
  };
}

/** fixtureApp is the Electron application this driver is proven against.
 *
 * Real files under test/fixtures/ledger rather than a string written to a
 * temporary directory, and the reason is that it has two jobs. It is the
 * fixture, so its accessible names are the interface this test drives by. It
 * is also the application a person WATCHES being driven, so it is built to be
 * looked at, and a sign-in screen assembled out of concatenated markup inside
 * a test file could never be either reviewed or designed.
 *
 * The names it must keep are "Email address", "Password", "I accept the
 * terms", "Sign in" and "Forgot your password", plus the acknowledgment being
 * really `required`. Its own comment says so, next to the markup, which is
 * where somebody about to rename one of them will actually be.
 */
function fixtureApp(): string {
  return join(here, 'fixtures', 'ledger');
}

test('the Electron surface drives a real Electron application, and says no when it should',
  async (t: TestContext) => {
    const binary = electronBinary();
    if ('absent' in binary) return cannotCheck(t, binary.absent);
    const app: DesktopApp = {
      kind: 'electron', executablePath: binary.path, args: [fixtureApp()], timeoutMs: 30_000,
    };

    const passed = await runDesktop({
      app,
      workflows: [{
        name: 'sign in',
        description: 'Sign in and confirm you land on a signed in screen.',
        expect: ['Welcome back'],
        answers: { 'Email address': 'ada@example.test', 'Password': 'correct-horse' },
        maxSteps: 12,
      }],
    });
    assert.equal(passed[0]!.outcome.verdict, 'pass', passed[0]!.outcome.detail);
    // It did the whole thing through accessible names: two fields typed, one
    // required acknowledgment ticked, one submit pressed.
    const steps = passed[0]!.steps.join(' | ');
    assert.match(steps, /Fill Email address/);
    assert.match(steps, /Choose I accept the terms/);
    assert.match(steps, /Press Sign in/);

    const failed = await runDesktop({
      app,
      workflows: [{
        name: 'a deliberately wrong expectation',
        description: 'Sign in.',
        expect: ['"Your order has shipped."'],
        answers: { 'Email address': 'ada@example.test', 'Password': 'correct-horse' },
        maxSteps: 12,
      }],
    });
    assert.equal(failed[0]!.outcome.verdict, 'fail', failed[0]!.outcome.detail);
    assert.equal(failed[0]!.outcome.cause, 'expectation-not-met');
  });

/** ledgerAnswering serves /journal the way the ledger behind the failing run
 *  did, after `delayMs`, with rows of one event, so a real Electron client
 *  spends that long on its loading screen. */
async function ledgerAnswering(event: string, delayMs: number) {
  const { createServer: createHttp } = await import('node:http');
  const rows = JSON.stringify(Array.from({ length: 200 }, (_, i) => ({ seq: 200 - i, event })));
  const server = createHttp((req, res) => {
    if (req.url !== '/journal') { res.writeHead(404).end(); return; }
    setTimeout(() => res.writeHead(200, { 'content-type': 'application/json' }).end(rows), delayMs);
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', () => resolve()));
  return { url: `http://127.0.0.1:${(server.address() as { port: number }).port}`, close: () => server.close() };
}

test('the Electron surface judges a real client once its journal has loaded, and still says no',
  async (t: TestContext) => {
    const binary = electronBinary();
    if ('absent' in binary) return cannotCheck(t, binary.absent);
    // The failing run's own shape: a main process fetch the page cannot see,
    // held back a second and a half. Before settle.ts this was a FAIL with the
    // loading screen quoted as what the application showed.
    const app: DesktopApp = {
      kind: 'electron', executablePath: binary.path,
      args: [join(here, 'fixtures', 'journal')], timeoutMs: 30_000,
    };
    const workflow = {
      name: 'read the journal',
      description: 'Open the journal and confirm posted transfers are listed.',
      expect: ['"transfer.posted"'],
    };

    const posted = await ledgerAnswering('transfer.posted', 1_500);
    try {
      const results = await runDesktop({ app, baseURL: posted.url, workflows: [workflow] });
      assert.equal(results[0]!.outcome.verdict, 'pass', results[0]!.outcome.detail);
    } finally {
      posted.close();
    }

    const reversed = await ledgerAnswering('transfer.reversed', 1_500);
    try {
      const results = await runDesktop({ app, baseURL: reversed.url, workflows: [workflow] });
      const { verdict, detail } = results[0]!.outcome;
      assert.equal(verdict, 'fail', detail);
      assert.ok(detail.includes('transfer.reversed'), `the loaded journal is not what was judged: ${detail}`);
      assert.ok(!detail.includes('Reading the ledger'), `the loading screen was judged: ${detail}`);
    } finally {
      reversed.close();
    }
  });

test('the native surface refuses a locked screen as itself, not as an empty application',
  async (t: TestContext) => {
    if (process.platform !== 'darwin') {
      return cannotCheck(t, `the lock is a macOS state and this is ${process.platform}.`);
    }
    // Both states are a pass here, because both are the reader answering
    // honestly about something it can check. What would be a failure is a
    // locked screen read as an application with no controls on it, and the
    // assertion below is that the two answers agree: the driver refuses
    // exactly when the session says it is locked, and not otherwise.
    const locked = await screenIsLocked();
    const results = await runDesktop({
      // Deliberately an application that is not running and has no bundle to
      // launch. On a locked screen the answer must be the LOCK, decided
      // before anything else is attempted; anything later in openMac would
      // answer that there is no such application, which is true and is not
      // the reason. That is what makes the up-front check provable rather
      // than merely present.
      app: { kind: 'macos', name: 'NoSuchApplicationOnThisMac', readyTimeoutMs: 2_000 },
      workflows: [{ name: 'probe', description: 'Look at the screen.', expect: ['anything'] }],
    });
    const detail = results[0]!.outcome.detail;
    if (locked) {
      assert.equal(results[0]!.outcome.verdict, 'blocked');
      assert.match(detail, /screen is locked/);
      // And it says so at once rather than reporting a slow application.
      assert.doesNotMatch(detail, /never opened a window/);
    } else {
      assert.doesNotMatch(detail, /screen is locked/);
    }
  });

test('the native surface drives a real macOS application through AXUIElement',
  async (t: TestContext) => {
    if (process.platform !== 'darwin') {
      return cannotCheck(t,
        `the native surface drives macOS applications through AXUIElement and ` +
        `this is ${process.platform}.`);
    }
    if (!(await trusted())) return cannotCheck(t, GRANT_INSTRUCTION);
    // A locked screen has the permission and none of the answers, so this is
    // asked separately. Skipped with the reason rather than failed, because
    // the driver refusing a locked screen is the driver working.
    if (await screenIsLocked()) return cannotCheck(t, LOCKED_SCREEN);

    const app: DesktopApp = {
      kind: 'macos', bundlePath: '/System/Applications/TextEdit.app', name: 'TextEdit',
      readyTimeoutMs: 30_000,
    };
    const passed = await runDesktop({
      app,
      workflows: [{
        name: 'start a new document',
        description: 'Press New Document to start writing, and confirm a blank document opens.',
        expect: ['Untitled'],
        maxSteps: 6,
      }],
    });
    assert.equal(passed[0]!.outcome.verdict, 'pass', passed[0]!.outcome.detail);
    assert.match(passed[0]!.steps.join(' | '), /Press New Document/);

    const failed = await runDesktop({
      app,
      workflows: [{
        name: 'a deliberately wrong expectation',
        description: 'Press New Document to start writing.',
        expect: ['"Your order has shipped."'],
        maxSteps: 6,
      }],
    });
    assert.equal(failed[0]!.outcome.verdict, 'fail', failed[0]!.outcome.detail);
    assert.equal(failed[0]!.outcome.cause, 'expectation-not-met');
  });
