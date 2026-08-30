import { test } from 'node:test';
import assert from 'node:assert/strict';
import { FakeClock } from '../src/clock.ts';
import {
  pursue, allKinds, destructive, signature,
  type Goal, type Kind, type Move, type Surface,
} from '../src/explore.ts';
import type { Snapshot } from '../src/workflow.ts';

// A scripted application, driven through the same Surface the browser is.
//
// The loop under test is the shipped loop: exploreOne wraps a Session in this
// interface and hands it to the same pursue(). A test that drove a copy would
// prove the copy correct and leave the thing that emits findings untested,
// which is the defect this repository keeps finding in its own work.

interface Screen {
  readonly title: string;
  readonly text: string;
  readonly controls?: readonly string[];
  readonly fields?: readonly { name: string; type: string }[];
  readonly unnamed?: number;
  /** where a control leads. A control with no entry here does nothing. */
  readonly links?: Readonly<Record<string, string>>;
  /** how far the clock moves when a control on this screen is pressed. */
  readonly costMs?: number;
}

class Site implements Surface {
  #at: string;
  readonly #screens: Readonly<Record<string, Screen>>;
  readonly #clock: FakeClock;
  readonly #filled = new Set<string>();

  constructor(screens: Readonly<Record<string, Screen>>, clock: FakeClock, start = '/') {
    this.#screens = screens;
    this.#clock = clock;
    this.#at = start;
  }

  async snapshot(): Promise<Snapshot> {
    const screen = this.#screens[this.#at];
    if (!screen) throw new Error(`the test site has no screen at ${this.#at}`);
    return {
      url: this.#at,
      title: screen.title,
      fields: (screen.fields ?? []).map((f) => ({
        name: f.name, type: f.type, filled: this.#filled.has(`${this.#at}|${f.name}`),
      })),
      controls: screen.controls ?? [],
      unnamed: screen.unnamed ?? 0,
      text: screen.text,
    };
  }

  async goto(url: string): Promise<void> {
    this.#at = url;
  }

  async fill(field: string, _value: string): Promise<void> {
    this.#filled.add(`${this.#at}|${field}`);
  }

  async click(control: string): Promise<void> {
    const screen = this.#screens[this.#at]!;
    this.#clock.advance(screen.costMs ?? 10);
    const to = screen.links?.[control];
    if (to) this.#at = to;
  }
}

function goal(over: Partial<Goal> = {}): Goal {
  return {
    name: 'upgrade',
    goal: 'Upgrade the workspace to the paid plan.',
    seed: 'seed-one',
    startPath: '/',
    maxSteps: 20,
    slowMs: 3_000,
    ...over,
  };
}

function kinds(findings: readonly { kind: Kind }[]): Kind[] {
  return findings.map((f) => f.kind);
}

test('a control that does nothing is reported, naming the control and the step', async () => {
  const clock = new FakeClock();
  const site = new Site({
    '/': {
      title: 'Home', text: 'Nothing much here.',
      controls: ['Save preferences'],
    },
  }, clock);

  const { explorer } = await pursue(goal(), site, clock);

  const finding = explorer.findings.find((f) => f.kind === 'no_effect');
  assert.ok(finding, `expected a no_effect finding, got ${kinds(explorer.findings).join(', ')}`);
  assert.equal(finding.control, 'Save preferences');
  assert.equal(finding.url, '/');
  assert.equal(finding.step, 0);
  assert.equal(finding.confidence, 'high');
  // The evidence has to be specific enough to go and look at.
  assert.match(finding.detail, /Save preferences/);
  assert.ok(finding.fix.length > 0, 'every finding says what to do about it');
});

test('a slow step is reported with the reading and the threshold', async () => {
  const clock = new FakeClock();
  const site = new Site({
    '/': { title: 'Home', text: 'Home.', controls: ['Billing'], links: { Billing: '/billing' }, costMs: 5_000 },
    '/billing': { title: 'Billing', text: 'Billing.' },
  }, clock);

  const { explorer } = await pursue(goal({ slowMs: 3_000 }), site, clock);

  const finding = explorer.findings.find((f) => f.kind === 'slow_response');
  assert.ok(finding, `expected slow_response, got ${kinds(explorer.findings).join(', ')}`);
  assert.equal(finding.control, 'Billing');
  assert.equal(finding.measuredMs, 5_000);
  assert.match(finding.detail, /5000 ms/);
  assert.match(finding.detail, /3000 ms/);
});

test('a step under the threshold is not reported as slow', async () => {
  // The negative control. Without it the detector could fire on everything and
  // the test above would still pass.
  const clock = new FakeClock();
  const site = new Site({
    '/': { title: 'Home', text: 'Home.', controls: ['Billing'], links: { Billing: '/billing' }, costMs: 2_999 },
    '/billing': { title: 'Billing', text: 'Billing.' },
  }, clock);

  const { explorer } = await pursue(goal({ slowMs: 3_000 }), site, clock);
  assert.equal(explorer.findings.filter((f) => f.kind === 'slow_response').length, 0);
});

test('interactive elements with no accessible name are reported once per page', async () => {
  const clock = new FakeClock();
  const site = new Site({
    '/': { title: 'Home', text: 'Home.', controls: ['Billing'], unnamed: 3, links: { Billing: '/billing' } },
    '/billing': { title: 'Billing', text: 'Billing.', controls: ['Home'], links: { Home: '/' } },
  }, clock);

  const { explorer } = await pursue(goal(), site, clock);

  const found = explorer.findings.filter((f) => f.kind === 'unnamed_control');
  assert.equal(found.length, 1, 'reported once per page, not once per visit');
  assert.equal(found[0]!.url, '/');
  assert.match(found[0]!.detail, /3 interactive elements/);
});

test('a page with nothing left to try is a dead end, and the run goes back', async () => {
  const clock = new FakeClock();
  const site = new Site({
    '/': {
      title: 'Home', text: 'Home.',
      controls: ['Settings', 'Docs'],
      links: { Settings: '/settings', Docs: '/docs' },
    },
    '/settings': { title: 'Settings', text: 'Settings.' },
    '/docs': { title: 'Docs', text: 'Docs.' },
  }, clock);

  const { explorer, journey } = await pursue(goal(), site, clock);

  const finding = explorer.findings.find((f) => f.kind === 'dead_end');
  assert.ok(finding, `expected a dead_end, got ${kinds(explorer.findings).join(', ')}`);
  assert.match(finding.detail, /no control and no field/);
  // Having found the dead end it navigated back rather than stopping, and
  // pressed the control it had not tried.
  assert.ok(
    journey.some((m) => m.kind === 'goto' && m.url === '/'),
    `expected a backtrack to /, journey was ${JSON.stringify(journey)}`,
  );
  const pressed = journey.filter((m) => m.kind === 'click').map((m) => m.control);
  assert.deepEqual([...pressed].sort(), ['Docs', 'Settings']);
});

test('coming back to a page already left is reported with both steps', async () => {
  const clock = new FakeClock();
  const site = new Site({
    '/': { title: 'Home', text: 'Home.', controls: ['Plans'], links: { Plans: '/plans' } },
    '/plans': { title: 'Plans', text: 'Plans.', controls: ['Home'], links: { Home: '/' } },
  }, clock);

  const { explorer } = await pursue(goal(), site, clock);

  const finding = explorer.findings.find((f) => f.kind === 'revisit');
  assert.ok(finding, `expected a revisit, got ${kinds(explorer.findings).join(', ')}`);
  assert.equal(finding.confidence, 'medium');
  assert.match(finding.detail, /step 0/);
});

test('a goal that is never visible is reported, and one that is is not', async () => {
  const clock = new FakeClock();
  const nowhere = new Site({
    '/': { title: 'Home', text: 'Nothing about billing here.' },
  }, clock);
  const arrives = new Site({
    '/': { title: 'Home', text: 'Home.', controls: ['Plans'], links: { Plans: '/plans' } },
    '/plans': {
      title: 'Plans', text: 'Upgrade the workspace to the paid plan. Done.',
    },
  }, clock);

  const missed = await pursue(goal(), nowhere, clock);
  assert.ok(kinds(missed.explorer.findings).includes('goal_unreached'));
  assert.equal(missed.explorer.reached, false);

  const reached = await pursue(goal(), arrives, clock);
  assert.equal(reached.explorer.reached, true);
  assert.ok(!kinds(reached.explorer.findings).includes('goal_unreached'));
});

test('a control that looks destructive is never pressed, and the refusal is recorded', async () => {
  const clock = new FakeClock();
  const site = new Site({
    '/': {
      title: 'Home', text: 'Home.',
      controls: ['Delete workspace', 'Sign out', 'Plans'],
      links: { 'Delete workspace': '/gone', 'Sign out': '/bye', Plans: '/plans' },
    },
    '/plans': { title: 'Plans', text: 'Plans.' },
    '/gone': { title: 'Gone', text: 'Everything was removed.' },
    '/bye': { title: 'Bye', text: 'You are signed out.' },
  }, clock);

  const { explorer, journey } = await pursue(goal(), site, clock);

  const pressed = journey.filter((m) => m.kind === 'click').map((m) => m.control);
  assert.ok(!pressed.includes('Delete workspace'), `it pressed ${pressed.join(', ')}`);
  assert.ok(!pressed.includes('Sign out'), `it pressed ${pressed.join(', ')}`);
  // An unexplored corner has to read as unexplored rather than as clean.
  assert.equal(explorer.missing.length, 2);
  assert.ok(explorer.missing.some((m) => m.includes('Delete workspace')));
  assert.ok(explorer.missing.some((m) => m.includes('Sign out')));
});

test('the same seed twice takes the same path and finds the same things', async () => {
  // The whole reason there is a seeded generator. An exploration that found
  // something and cannot be replayed is a bug report nobody can act on.
  const run = async (seed: string) => {
    const clock = new FakeClock();
    const site = new Site(maze(), clock);
    const { explorer, journey } = await pursue(goal({ seed, maxSteps: 30 }), site, clock);
    return { journey, findings: explorer.findings, visited: explorer.visited };
  };

  const first = await run('replay-me');
  const second = await run('replay-me');

  assert.deepEqual(second.journey, first.journey);
  assert.deepEqual(second.findings, first.findings);
  assert.deepEqual(second.visited, first.visited);
  assert.ok(first.journey.length > 5, 'the path is long enough for a difference to show');
});

test('a different seed takes a different path', async () => {
  // Without this, the test above would pass for an explorer that ignores the
  // seed entirely, which is exactly the trap: a determinism claim is easy to
  // satisfy by having no randomness in the decision at all, and then the
  // exploration only ever walks one route.
  const journeys = new Set<string>();
  for (let i = 0; i < 8; i++) {
    const clock = new FakeClock();
    const site = new Site(maze(), clock);
    const { journey } = await pursue(goal({ seed: `seed-${i}`, maxSteps: 30 }), site, clock);
    journeys.add(JSON.stringify(journey));
  }
  assert.ok(journeys.size > 1, 'every seed produced the identical path');
});

/** maze is a site with several equally irrelevant routes, so the tie break
 *  the seed decides is the thing that chooses. */
function maze(): Record<string, Screen> {
  return {
    '/': {
      title: 'Home', text: 'Home.',
      controls: ['Alpha', 'Bravo', 'Charlie'],
      links: { Alpha: '/a', Bravo: '/b', Charlie: '/c' },
    },
    '/a': { title: 'A', text: 'A.', controls: ['Delta', 'Echo'], links: { Delta: '/d', Echo: '/e' } },
    '/b': { title: 'B', text: 'B.', controls: ['Foxtrot'], links: { Foxtrot: '/f' } },
    '/c': { title: 'C', text: 'C.', controls: ['Golf'], links: { Golf: '/g' } },
    '/d': { title: 'D', text: 'D.' },
    '/e': { title: 'E', text: 'E.' },
    '/f': { title: 'F', text: 'F.' },
    '/g': { title: 'G', text: 'G.' },
  };
}

test('a control the goal names is preferred over one it does not', async () => {
  // Personality is not what this layer models, but relevance is: somebody
  // trying to upgrade a plan presses the control that says plan.
  const clock = new FakeClock();
  const site = new Site({
    '/': {
      title: 'Home', text: 'Home.',
      controls: ['About us', 'Upgrade plan', 'Careers'],
      links: { 'About us': '/about', 'Upgrade plan': '/upgrade', Careers: '/jobs' },
    },
    '/about': { title: 'About', text: 'About.' },
    '/upgrade': { title: 'Upgrade', text: 'Choose a paid plan.' },
    '/jobs': { title: 'Jobs', text: 'Jobs.' },
  }, clock);

  const { journey } = await pursue(goal({ maxSteps: 4 }), site, clock);
  const firstClick = journey.find((m: Move) => m.kind === 'click');
  assert.equal(firstClick?.kind === 'click' ? firstClick.control : '', 'Upgrade plan');
});

test('a form is filled before the button under it is pressed', async () => {
  const clock = new FakeClock();
  const site = new Site({
    '/': {
      title: 'Sign up', text: 'Create an account.',
      fields: [{ name: 'Email address', type: 'email' }, { name: 'Password', type: 'password' }],
      controls: ['Create account'],
      links: { 'Create account': '/welcome' },
    },
    '/welcome': { title: 'Welcome', text: 'Welcome.' },
  }, clock);

  const { journey } = await pursue(goal({ maxSteps: 6 }), site, clock);
  const order = journey.map((m) => m.kind);
  assert.deepEqual(order, ['goto', 'fill', 'fill', 'click']);
  const filled = journey.filter((m) => m.kind === 'fill');
  // The values come from the shared table, so a workflow compiled from this
  // path types what the declared runner would type.
  assert.match(filled[0]!.kind === 'fill' ? filled[0]!.value : '', /@example\.test$/);
});

test('every kind the taxonomy lists is one a finding can carry', () => {
  // AllKinds is what the documentation page and the Go mirror are checked
  // against, so a kind added to the type and not to the list would drift
  // silently.
  const listed = allKinds();
  assert.equal(new Set(listed).size, listed.length, 'no kind is listed twice');
  const all: Kind[] = [
    'no_effect', 'dead_end', 'revisit', 'unnamed_control', 'slow_response', 'goal_unreached',
  ];
  assert.deepEqual([...listed].sort(), [...all].sort());
});

test('destructive names the controls it should and nothing else', () => {
  for (const yes of [
    'Sign out', 'Log out', 'Delete project', 'Remove member', 'Revoke token',
    'Cancel subscription', 'Deactivate account',
  ]) {
    assert.ok(destructive(yes), `${yes} should be left alone`);
  }
  for (const no of ['Upgrade plan', 'Continue', 'Save', 'Sign in', 'Cancel']) {
    // "Cancel" on its own closes a dialog and is safe; "Cancel subscription"
    // is not. The rule has to tell them apart or half the application becomes
    // unexplorable.
    assert.ok(!destructive(no), `${no} should be explorable`);
  }
});

test('a page signature changes when anything a person would notice changes', () => {
  const base: Snapshot = {
    url: '/a', title: 'A', fields: [], controls: ['One'], unnamed: 0, text: 'hello',
  };
  const same = signature(base);
  assert.equal(signature({ ...base }), same);
  assert.notEqual(signature({ ...base, url: '/b' }), same);
  assert.notEqual(signature({ ...base, title: 'B' }), same);
  assert.notEqual(signature({ ...base, controls: ['One', 'Two'] }), same);
  assert.notEqual(signature({ ...base, unnamed: 1 }), same);
  assert.notEqual(signature({ ...base, text: 'goodbye' }), same);
  assert.notEqual(
    signature({ ...base, fields: [{ name: 'Email', type: 'email', filled: false }] }), same);
  // Control order is the DOM's business, not the page's meaning.
  assert.equal(signature({ ...base, controls: ['One'] }), same);
});

// The four things running this against a real application found, each with a
// test so it stays found.

test('a dead end is a page with no way onward, not one whose controls were tried', async () => {
  // The first version reported a dead end whenever nothing was left to press,
  // so every run ended by reporting one on its own front page. A finding that
  // fires on every run regardless of the application is one people skip.
  const clock = new FakeClock();
  const site = new Site({
    '/': { title: 'Home', text: 'Home.', controls: ['Docs'], links: { Docs: '/docs' } },
    '/docs': { title: 'Docs', text: 'Docs.', controls: ['Home'], links: { Home: '/' } },
  }, clock);

  const { explorer } = await pursue(goal(), site, clock);
  assert.equal(
    explorer.findings.filter((f) => f.kind === 'dead_end').length, 0,
    `every page here leads somewhere: ${JSON.stringify(explorer.findings, null, 2)}`,
  );
});

test('a page whose only way onward is destructive is a dead end', async () => {
  const clock = new FakeClock();
  const site = new Site({
    '/': { title: 'Home', text: 'Home.', controls: ['Danger'], links: { Danger: '/gone' } },
    '/gone': { title: 'Gone', text: 'Gone.', controls: ['Delete everything'] },
  }, clock);

  const { explorer } = await pursue(goal(), site, clock);
  const found = explorer.findings.filter((f) => f.kind === 'dead_end');
  assert.equal(found.length, 1, JSON.stringify(explorer.findings.map((f) => f.kind)));
  assert.equal(found[0]!.url, '/gone');
  assert.match(found[0]!.detail, /must not press/);
});

test('a page with no control and no field says exactly that', async () => {
  const clock = new FakeClock();
  const site = new Site({
    '/': { title: 'Home', text: 'Home.', controls: ['Help'], links: { Help: '/help' } },
    '/help': { title: 'Help', text: 'Read the manual.' },
  }, clock);

  const { explorer } = await pursue(goal(), site, clock);
  const found = explorer.findings.find((f) => f.kind === 'dead_end');
  assert.ok(found);
  assert.equal(found.url, '/help');
  assert.match(found.detail, /no control and no field/);
  // The first version's sentence read "It offers nothing at all, and every one
  // of those was already tried", which refers to nothing.
  assert.doesNotMatch(found.detail, /every one of those/);
});

test('what the agent typed is removed from every url it reports', async () => {
  // A form submitted with GET puts every field in the address bar, and that
  // address travels into a finding, a compiled description and a pull request
  // comment. The agent knows exactly what it typed, which is what makes the
  // removal precise rather than a guess at what looks sensitive.
  const clock = new FakeClock();
  const site = new Site({
    '/': {
      title: 'Sign up', text: 'Sign up.',
      fields: [{ name: 'Email address', type: 'email' }],
      controls: ['Continue'],
      links: { Continue: '/done?e=af.leaky%40example.test' },
    },
    '/done?e=af.leaky%40example.test': { title: 'Done', text: 'Thanks.' },
  }, clock);

  const { explorer } = await pursue(goal({ seed: 'leaky' }), site, clock);
  const everything = JSON.stringify({
    findings: explorer.findings, visited: explorer.visited,
  });
  assert.ok(!everything.includes('af.leaky'), everything);
  assert.ok(everything.includes('[typed]'), everything);
});

test('a goal that was never reached names the words that never appeared', async () => {
  // Whether a goal was reached is decided by matching its words against the
  // page, so a goal describing where somebody started can be satisfied and
  // still read as unreached. Naming the missing words is what tells that case
  // apart from a genuinely missing feature in a second.
  const clock = new FakeClock();
  const site = new Site({
    '/': { title: 'Home', text: 'Your workspace is on the paid plan.' },
  }, clock);

  const { explorer } = await pursue(
    goal({ goal: 'Upgrade the workspace from the free plan to the paid one.' }), site, clock);

  const found = explorer.findings.find((f) => f.kind === 'goal_unreached');
  assert.ok(found);
  assert.match(found.detail, /never appeared anywhere/);
  for (const word of ['upgrade', 'free']) {
    assert.match(found.detail, new RegExp(word), `${word} was nowhere and is not named`);
  }
  // And the words that were on the page are not listed as missing.
  assert.doesNotMatch(found.detail.split('never appeared anywhere:')[1] ?? '', /workspace|paid/);
});
