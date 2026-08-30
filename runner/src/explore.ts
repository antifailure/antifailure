// An exploration is a goal without a script.
//
// A workflow says what to do and what proves it happened, and the runner's job
// is to reach a declared outcome. An exploration says only "upgrade to the
// paid plan" and then wanders: it reads the page, chooses somewhere to go,
// goes there, and writes down every place the application cost it effort. It
// answers the question a declared workflow cannot ask, which is "nothing
// broke, so why would somebody give up here".
//
// Three rules shape everything below.
//
// It is reproducible. Every choice comes from a seeded generator and every
// duration from an injected clock, so the same seed against the same
// application takes the same path, step for step. An exploration that found
// something and cannot be replayed is a bug report nobody can act on, and an
// unseeded Math.random in the decision path is the whole difference.
//
// It never counts against the change. Nobody declared what this application
// was supposed to do on the pages it wandered onto, so a friction finding is
// an observation, not a defect in the pull request. The verdict is PASS and
// the exit code is zero, always. See VERDICT_FOR_CAUSE in verdict.ts.
//
// It does not break things. An exploration signs in as a real persona with
// real permissions on a real branch. It will not press a control that looks
// like it destroys state, and it says in `missing` which ones it refused, so
// an unexplored corner reads as unexplored rather than as clean.

import { Session } from './browser.ts';
import { signIn, type Persona } from './login.ts';
import type { InboxSource } from './inbox.ts';
import { systemClock, type Clock } from './clock.ts';
import { Seeded } from './random.ts';
import {
  freshIdentity, identityValueFor, judge, keywords,
  type Identity, type Snapshot,
} from './workflow.ts';
import { classify, type Attempt, type Cause, type Outcome } from './verdict.ts';

/** What an exploration is asked to pursue. */
export interface Goal {
  readonly name: string;
  /** goal is the sentence, written the way somebody would say it out loud.
   *  "Upgrade from the free plan to the paid one." */
  readonly goal: string;
  readonly persona?: string;
  readonly startPath?: string;
  readonly maxSteps?: number;
  /** seed decides every choice. The same seed takes the same path. */
  readonly seed: string;
  /** slowMs is how long a step may take before it is reported as friction. */
  readonly slowMs?: number;
}

/** Kind names one way an application costs somebody effort without failing.
 *
 * Six of them, and every one is decided from something the runner actually
 * measured rather than from a judgement about what a user would feel. That
 * constraint is why there is no "confusion" and no "frustration" here: this
 * layer can see a control that did nothing and a page it came back to twice,
 * and it cannot see a person's patience. A taxonomy naming things it cannot
 * observe would produce findings nobody can check.
 */
export type Kind =
  /** A control was activated and nothing about the page changed. */
  | 'no_effect'
  /** A page offered nothing that had not already been tried. */
  | 'dead_end'
  /** The path came back to a page it had already left. */
  | 'revisit'
  /** The page carries interactive elements with no accessible name. */
  | 'unnamed_control'
  /** A step took longer than the goal allows. */
  | 'slow_response'
  /** The whole exploration never found what it was looking for. */
  | 'goal_unreached';

/** AllKinds is every kind, in the order the documentation lists them.
 *
 * Kept so the docs page and the code cannot drift, the way
 * insights.AllRules() is. A test walks this list against the Go mirror and
 * against the published page. */
export function allKinds(): readonly Kind[] {
  return [
    'no_effect', 'dead_end', 'revisit',
    'unnamed_control', 'slow_response', 'goal_unreached',
  ];
}

/** How sure the run is. Two values, because a third would be a guess.
 *
 * `high` means the runner measured it: the page did not change, the element
 * had no name, the step took this many milliseconds. `medium` means it
 * inferred it from the goal's words, which is a heuristic and says so. */
export type Confidence = 'high' | 'medium';

/** Finding is one thing an exploration ran into.
 *
 * Every field exists so that somebody can go and look. `url` and `control`
 * locate it the way a person would search for it, and `step` indexes the
 * journey, so the finding opens where it happened rather than somewhere in a
 * trace. A finding that says "users hesitate on this page" and names neither
 * is a complaint, not a report.
 */
export interface Finding {
  readonly kind: Kind;
  readonly url: string;
  /** control is the accessible name of the element, when one element is
   *  responsible. Absent for a finding about a whole page or a whole run. */
  readonly control?: string;
  readonly step: number;
  readonly confidence: Confidence;
  /** detail says what happened. fix says what to do about it. */
  readonly detail: string;
  readonly fix: string;
  /** measuredMs is the duration behind a slow_response, so the threshold and
   *  the reading can both be shown. Zero elsewhere. */
  readonly measuredMs: number;
}

/** Move is one concrete thing the exploration did.
 *
 * Accessible names as plain strings rather than the RegExp an Action carries,
 * because this is what crosses the boundary into Go and gets compiled into a
 * declared workflow. A pattern that serialises to its own source and has to be
 * parsed back is a shape nobody can hand-edit. */
export type Move =
  | { readonly kind: 'goto'; readonly url: string }
  | { readonly kind: 'fill'; readonly field: string; readonly value: string }
  | { readonly kind: 'click'; readonly control: string };

/** Exploration is what one goal produced. */
export interface Exploration {
  readonly name: string;
  readonly goal: string;
  readonly seed: string;
  readonly outcome: Outcome;
  /** reached says whether the goal's own words ever appeared on a page. */
  readonly reached: boolean;
  /** steps is the prose account, the same shape a workflow's is. */
  readonly steps: readonly string[];
  /** journey is the same path in a form that can be replayed and compiled. */
  readonly journey: readonly Move[];
  readonly findings: readonly Finding[];
  readonly visited: readonly string[];
  /** missing names what was not explored, and why. An exploration that
   *  refused half the application must never read as a clean bill of health. */
  readonly missing: readonly string[];
  readonly evidence: {
    readonly video?: string;
    readonly trace?: string;
    readonly screenshot?: string;
    readonly console: readonly string[];
    readonly failed: readonly string[];
  };
  readonly durationMs: number;
}

/** Controls an exploration will not press.
 *
 * It runs as a real persona against a real branch with real permissions, and
 * a wandering agent that presses "Delete workspace" on step three has removed
 * the thing every later step would have explored. Signing out is the same
 * defect in a quieter form: every page after it is the logged out one, and the
 * run reports an application with no features.
 *
 * Refusals are recorded rather than silent, so the report says which corners
 * were left alone.
 */
const DESTRUCTIVE: readonly RegExp[] = [
  /^(sign out|log out|logout|sign off)$/i,
  /\b(delete|destroy|erase|wipe)\b/i,
  /\b(deactivate|close|cancel) (account|subscription|plan|workspace|organisation|organization)\b/i,
  /\bremove\b/i,
  /\brevoke\b/i,
];

const DEFAULT_STEPS = 40;
const DEFAULT_SLOW_MS = 3_000;

/** Explorer is the decision and detection half, with no browser in it.
 *
 * Separated so that the taxonomy can be tested against a scripted sequence of
 * pages in milliseconds rather than against a real browser, and so that the
 * reproducibility test can prove two runs of a seed agree without launching
 * chromium twice. The browser half is exploreOne below.
 */
export class Explorer {
  readonly #goal: Goal;
  readonly #identity: Identity;
  readonly #clock: Clock;
  readonly #rng: Seeded;
  readonly #slowMs: number;
  readonly #goalWords: readonly string[];

  /** offered is every control seen at a URL, unioned across visits. */
  readonly #offered = new Map<string, string[]>();
  readonly #pressed = new Set<string>();
  readonly #filled = new Set<string>();
  readonly #exhausted = new Set<string>();
  /** signatures maps a page's shape to the step it was first seen at. */
  readonly #signatures = new Map<string, number>();
  readonly #reportedRevisit = new Set<string>();
  readonly #reportedUnnamed = new Set<string>();
  readonly #visited: string[] = [];

  readonly #findings: Finding[] = [];
  readonly #missing: string[] = [];
  readonly #refused = new Set<string>();

  #reached = false;

  constructor(goal: Goal, clock: Clock = systemClock) {
    this.#goal = goal;
    this.#clock = clock;
    this.#rng = new Seeded(goal.seed);
    // Derived from the seed rather than from the wall clock, which is the
    // trade this design makes on purpose: the same seed types the same email
    // address, so replaying a sign up against a stateful application is the
    // second sign up of the same person and the application is right to refuse
    // it. Freshness and replayability cannot both be had from one seed, and
    // replayability is the one an exploration exists for.
    this.#identity = freshIdentity(goal.seed);
    this.#slowMs = goal.slowMs ?? DEFAULT_SLOW_MS;
    this.#goalWords = keywords(goal.goal);
  }

  get findings(): readonly Finding[] { return this.#findings; }
  get missing(): readonly string[] { return this.#missing; }
  get visited(): readonly string[] { return this.#visited; }
  get reached(): boolean { return this.#reached; }
  get identity(): Identity { return this.#identity; }

  /** observe records what the page itself says, before anything is done to it.
   *
   * Returns true when the goal is visibly satisfied, which ends the run. */
  observe(snapshot: Snapshot, step: number): boolean {
    const url = snapshot.url;
    if (!this.#visited.includes(url)) this.#visited.push(url);

    const seen = this.#offered.get(url) ?? [];
    for (const control of snapshot.controls) {
      if (!seen.includes(control)) seen.push(control);
    }
    this.#offered.set(url, seen);

    // Every page the exploration stands on is registered here rather than in
    // settle, and the first step it was seen at wins. Registering only what an
    // action landed on left the starting page out of the map entirely, so a
    // path that went out and came straight back to where it began was the one
    // loop the revisit detector could not see.
    const sig = signature(snapshot);
    if (!this.#signatures.has(sig)) this.#signatures.set(sig, step);

    if (snapshot.unnamed > 0 && !this.#reportedUnnamed.has(url)) {
      this.#reportedUnnamed.add(url);
      this.#findings.push({
        kind: 'unnamed_control',
        url,
        step,
        confidence: 'high',
        detail:
          `${snapshot.unnamed} interactive ${snapshot.unnamed === 1 ? 'element has' : 'elements have'} ` +
          `no accessible name on this page, so neither a screen reader nor an agent can say what ` +
          `${snapshot.unnamed === 1 ? 'it does' : 'they do'}.`,
        fix:
          'Give each one an aria-label, a title, or visible text. An icon on its own is a control ' +
          'only for somebody who can see it.',
        measuredMs: 0,
      });
    }

    if (judge(this.#goal.goal, snapshot.text) === 'met') {
      this.#reached = true;
      return true;
    }
    return false;
  }

  /** decide chooses the next move, or undefined when there is nothing left.
   *
   * Filling comes before pressing for the same reason it does in the declared
   * planner: a form submitted with a field still empty produces a validation
   * error that looks exactly like a broken application.
   */
  decide(snapshot: Snapshot, step: number): Move | undefined {
    const url = snapshot.url;

    for (const field of snapshot.fields) {
      if (field.filled) continue;
      const key = `${url}|${field.name}`;
      if (this.#filled.has(key)) continue;
      const value = identityValueFor(field.name, this.#identity);
      if (value === undefined) continue;
      this.#filled.add(key);
      return { kind: 'fill', field: field.name, value };
    }

    const candidates = this.#candidates(url);
    if (candidates.length > 0) {
      const control = this.#choose(candidates);
      this.#pressed.add(`${url}|${control}`);
      return { kind: 'click', control };
    }

    // Nothing here that has not been tried. That is a dead end from where the
    // exploration is standing, and it is worth saying so even though the run
    // carries on: a page a person reaches with a goal in mind and cannot
    // advance from is the shape of "nothing broke and I left".
    this.#exhausted.add(url);
    this.#findings.push({
      kind: 'dead_end',
      url,
      step,
      confidence: 'high',
      detail:
        `Nothing on this page moves the goal forward. It offers ` +
        `${describe(snapshot)}, and every one of those was already tried.`,
      fix:
        'If somebody can reach this page while trying to ' +
        `${lowerFirst(this.#goal.goal)} then it needs a way onward from here, or a way back that ` +
        'says where they are.',
      measuredMs: 0,
    });

    // Back to the earliest page that still has something unexplored, chosen by
    // visit order rather than at random so that the path stays readable and
    // the same seed produces the same backtrack. Navigation is by URL rather
    // than by the browser's history, because history depends on how the page
    // was reached and a replay would not reproduce it.
    for (const previous of this.#visited) {
      if (previous === url) continue;
      if (this.#candidates(previous).length === 0) continue;
      return { kind: 'goto', url: previous };
    }
    return undefined;
  }

  /** settle records what the move did.
   *
   * `elapsedMs` is measured by the caller from the injected clock, so a test
   * decides how slow a page was rather than having to make one genuinely slow.
   */
  settle(
    move: Move, before: Snapshot, after: Snapshot, elapsedMs: number, step: number,
  ): void {
    if (elapsedMs > this.#slowMs) {
      this.#findings.push({
        kind: 'slow_response',
        url: before.url,
        ...(move.kind === 'click' ? { control: move.control } : {}),
        step,
        confidence: 'high',
        detail:
          `${describeMove(move)} took ${elapsedMs} ms, above the ${this.#slowMs} ms this goal ` +
          `allows a single step.`,
        fix:
          'Either make it answer faster, or show something while it works. A control that looks ' +
          'inert for three seconds gets pressed again.',
        measuredMs: elapsedMs,
      });
    }

    const beforeSig = signature(before);
    const afterSig = signature(after);

    if (move.kind === 'click' && beforeSig === afterSig) {
      this.#findings.push({
        kind: 'no_effect',
        url: before.url,
        control: move.control,
        step,
        confidence: 'high',
        detail:
          `Pressing "${move.control}" changed nothing: same address, same controls, same fields, ` +
          `same text.`,
        fix:
          'Either it should do something and does not, or it should say why it cannot. A control ' +
          'that silently does nothing is the one people press four times.',
        measuredMs: 0,
      });
    }

    const first = this.#signatures.get(afterSig);
    // Only when the page was left and came back. A page whose first sighting
    // is this same step is the one the move started from, which is a no_effect
    // and already reported above; counting it here as well would make one
    // problem read as two.
    if (first !== undefined && first < step && !this.#reportedRevisit.has(afterSig)) {
      this.#reportedRevisit.add(afterSig);
      this.#findings.push({
        kind: 'revisit',
        url: after.url,
        ...(move.kind === 'click' ? { control: move.control } : {}),
        step,
        confidence: 'medium',
        detail:
          `${describeMove(move)} arrived back at the page from step ${first}, unchanged. The path ` +
          `to ${lowerFirst(this.#goal.goal)} loops through here.`,
        fix:
          'Somebody following this route ends up where they started with nothing to show for it. ' +
          'Check where this control is meant to lead.',
        measuredMs: 0,
      });
    }
  }

  /** refuse records a control the exploration would not press. */
  refuse(control: string, url: string): void {
    if (this.#refused.has(control)) return;
    this.#refused.add(control);
    this.#missing.push(
      `Did not press "${control}" on ${url}: it looks like it destroys state, and an exploration ` +
      `must not remove the thing the rest of the run would have looked at.`,
    );
  }

  /** finish records what the run as a whole did not manage. */
  finish(step: number, lastURL: string): void {
    if (this.#reached) return;
    this.#findings.push({
      kind: 'goal_unreached',
      url: lastURL,
      step,
      confidence: 'medium',
      detail:
        `${step} ${step === 1 ? 'step' : 'steps'} across ` +
        `${this.#visited.length} ${this.#visited.length === 1 ? 'page' : 'pages'} and nothing ` +
        `visible ever said ${lowerFirst(this.#goal.goal)} had happened.`,
      fix:
        'Either the path is longer than the budget, or it is not discoverable from where this ' +
        'started. Raise the budget or set start_path closer, and if neither helps, that is the ' +
        'finding.',
      measuredMs: 0,
    });
  }

  /** now is the injected clock, so the caller measures with the same one. */
  get clock(): Clock { return this.#clock; }

  #candidates(url: string): string[] {
    const offered = this.#offered.get(url) ?? [];
    return offered.filter((c) => !this.#pressed.has(`${url}|${c}`) && !destructive(c));
  }

  /** choose picks among the candidates, preferring the ones the goal names.
   *
   * A control whose name shares a word with the goal is where a person would
   * go, so it goes first. Among equals the seeded generator decides, and the
   * list is sorted before it does, because a set iterated in insertion order
   * would make the choice depend on the order the DOM happened to be in.
   */
  #choose(candidates: readonly string[]): string {
    let best = -1;
    let tied: string[] = [];
    for (const control of [...candidates].sort()) {
      const score = this.#relevance(control);
      if (score > best) {
        best = score;
        tied = [control];
      } else if (score === best) {
        tied.push(control);
      }
    }
    return this.#rng.pick(tied) ?? tied[0]!;
  }

  #relevance(control: string): number {
    if (this.#goalWords.length === 0) return 0;
    const name = control.toLowerCase();
    return this.#goalWords.filter((w) => name.includes(w)).length;
  }
}

/** destructive reports whether a control should be left alone. */
export function destructive(control: string): boolean {
  return DESTRUCTIVE.some((p) => p.test(control));
}

/** signature is a page's shape, for deciding whether anything changed.
 *
 * The visible text is included by its hash rather than in full, because a page
 * that differs only in a timestamp is a different page and the exploration
 * should say so, and because carrying whole documents in a set would make the
 * memory of a long run the size of the site.
 */
export function signature(s: Snapshot): string {
  return [
    s.url,
    s.title,
    [...s.controls].sort().join(''),
    s.fields.map((f) => `${f.name}:${f.filled}`).sort().join(''),
    String(s.unnamed),
    hash(s.text),
  ].join('');
}

/** hash is FNV-1a over the page text, in hex. Not a checksum anybody verifies:
 *  it exists so two identical pages compare equal cheaply. */
function hash(s: string): string {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h.toString(16);
}

function describe(snapshot: Snapshot): string {
  const parts: string[] = [];
  if (snapshot.fields.length > 0) {
    parts.push(`fields ${snapshot.fields.map((f) => f.name).join(', ')}`);
  }
  if (snapshot.controls.length > 0) {
    parts.push(`controls ${snapshot.controls.slice(0, 8).join(', ')}`);
  }
  return parts.length > 0 ? parts.join(' and ') : 'nothing at all';
}

/** describeMove is one move, in the words a person would use. */
export function describeMove(move: Move): string {
  switch (move.kind) {
    case 'goto': return `Opening ${move.url}`;
    case 'fill': return `Filling ${move.field}`;
    case 'click': return `Pressing "${move.control}"`;
  }
}

function lowerFirst(s: string): string {
  const trimmed = s.trim().replace(/\.$/, '');
  return trimmed.charAt(0).toLowerCase() + trimmed.slice(1);
}

/** Surface is the page an exploration drives.
 *
 * An interface rather than the Session directly, so that the loop below is the
 * loop a test runs. A test that drove a copy of this logic against scripted
 * pages would prove the copy correct and nothing else, which is how a
 * taxonomy ends up tested and the thing that emits it does not.
 */
export interface Surface {
  snapshot(): Promise<Snapshot>;
  goto(url: string): Promise<void>;
  fill(field: string, value: string): Promise<void>;
  click(control: string): Promise<void>;
}

/** Pursuit is what driving one goal produced, before the verdict is decided. */
export interface Pursuit {
  readonly explorer: Explorer;
  readonly steps: readonly string[];
  readonly journey: readonly Move[];
  /** taken is how many steps were spent, which the goal_unreached finding
   *  reports and a budget is judged against. */
  readonly taken: number;
  readonly lastURL: string;
}

/** pursue drives one goal against one surface until it runs out of steps,
 *  reaches the goal, or has nothing left it has not tried. */
export async function pursue(
  goal: Goal, surface: Surface, clock: Clock = systemClock,
): Promise<Pursuit> {
  const explorer = new Explorer(goal, clock);
  const steps: string[] = [];
  const journey: Move[] = [];

  const start = goal.startPath ?? '/';
  await surface.goto(start);
  journey.push({ kind: 'goto', url: start });
  steps.push(`Open ${start}`);

  const limit = goal.maxSteps ?? DEFAULT_STEPS;
  let lastURL = start;
  let step = 0;
  for (; step < limit; step++) {
    const before = await surface.snapshot();
    lastURL = before.url;
    // Refusals are recorded from what the page offers, not from what was
    // chosen, so the report names every destructive control the exploration
    // walked past rather than only the ones it happened to reach for.
    for (const control of before.controls) {
      if (destructive(control)) explorer.refuse(control, before.url);
    }
    if (explorer.observe(before, step)) {
      steps.push(`The page now says the goal is met: ${before.url}`);
      break;
    }

    const move = explorer.decide(before, step);
    if (!move) {
      steps.push('Nothing left to explore that had not already been tried.');
      break;
    }

    const at = clock.monotonicMs();
    switch (move.kind) {
      case 'fill':
        await surface.fill(move.field, move.value);
        break;
      case 'click':
        await surface.click(move.control);
        break;
      case 'goto':
        await surface.goto(move.url);
        break;
    }
    journey.push(move);
    steps.push(describeMove(move));

    const after = await surface.snapshot();
    explorer.settle(move, before, after, clock.monotonicMs() - at, step);
    lastURL = after.url;
  }

  explorer.finish(step, lastURL);
  return { explorer, steps, journey, taken: step, lastURL };
}

/** Everything an exploration run needs. */
export interface ExploreJob {
  readonly baseURL: string;
  readonly artifacts: string;
  readonly goals: readonly Goal[];
  readonly personas: readonly Persona[];
  readonly inbox?: InboxSource;
  readonly clock?: Clock;
  readonly headless?: boolean;
}

/** explore pursues every goal and returns what each one found. */
export async function explore(job: ExploreJob): Promise<Exploration[]> {
  const out: Exploration[] = [];
  for (const goal of job.goals) {
    out.push(await exploreOne(job, goal));
  }
  return out;
}

async function exploreOne(job: ExploreJob, goal: Goal): Promise<Exploration> {
  const clock = job.clock ?? systemClock;
  const started = clock.monotonicMs();
  const signIns: string[] = [];
  let pursuit: Pursuit | undefined;
  let evidence: Exploration['evidence'] = { console: [], failed: [] };
  let attempt: Attempt;
  let session: Session | undefined;

  try {
    session = await Session.open({
      artifacts: job.artifacts,
      ...(job.headless === undefined ? {} : { headless: job.headless }),
    });
    const page = session.page();

    const persona = job.personas.find((p) => p.name === goal.persona) ?? job.personas[0];
    if (goal.persona && !persona) {
      throw new Environment(
        `This exploration runs as ${goal.persona}, and no persona by that name is declared.`,
      );
    }
    if (persona) {
      const login = await signIn(page, persona, {
        baseURL: job.baseURL,
        ...(job.inbox ? { inbox: job.inbox } : {}),
      });
      signIns.push(`Sign in as ${persona.name}: ${login.detail}`);
      // A sign in that did not work is the environment's problem or the
      // application's, and either way the exploration has nothing to explore.
      // Reporting it as an exploration that found no friction would be the
      // worst of both answers.
      if (!login.ok) throw new Environment(login.detail);
    }

    const browser = session;
    pursuit = await pursue(
      { ...goal, startPath: join(job.baseURL, goal.startPath ?? '/') },
      {
        snapshot: () => browser.snapshot(),
        goto: (url) => page.goto(url),
        fill: (field, value) => page.fill(anchor(field), value),
        click: (control) => page.click(anchor(control)),
      },
      clock,
    );
    attempt = {
      cause: 'explored',
      detail: summarise(pursuit.explorer, pursuit.taken),
      durationMs: clock.monotonicMs() - started,
    };
  } catch (err) {
    // The same split the declared runner makes. A browser that would not open
    // is ours; a persona that could not sign in is the environment's; neither
    // is evidence about the application.
    attempt = {
      cause: err instanceof Environment ? 'environment-incomplete' : 'runner-failure',
      detail: err instanceof Error ? err.message : String(err),
      durationMs: clock.monotonicMs() - started,
    };
  } finally {
    if (session) {
      evidence = await session.close(`explore-${goal.name}`).catch(() => evidence);
    }
  }

  const outcome = classify([attempt]);
  const journey = pursuit?.journey ?? [];
  return {
    name: goal.name,
    goal: goal.goal,
    seed: goal.seed,
    outcome: {
      ...outcome,
      reproduction: reproduction(goal, journey, outcome.cause),
    },
    reached: pursuit?.explorer.reached ?? false,
    steps: [...signIns, ...(pursuit?.steps ?? [])],
    journey,
    findings: pursuit?.explorer.findings ?? [],
    visited: pursuit?.explorer.visited ?? [],
    // An exploration that never started explored nothing, and saying so is the
    // difference between "no friction here" and "nobody looked".
    missing: pursuit
      ? pursuit.explorer.missing
      : [`Nothing was explored: ${attempt.detail}`],
    evidence,
    durationMs: clock.monotonicMs() - started,
  };
}

/** Environment marks a failure the environment owes rather than the runner. */
class Environment extends Error {}

function summarise(explorer: Explorer, steps: number): string {
  const n = explorer.findings.length;
  const reached = explorer.reached ? 'reached the goal' : 'did not reach the goal';
  if (n === 0) {
    return `Explored ${explorer.visited.length} pages in ${steps} steps and ${reached}, ` +
      `with nothing to report.`;
  }
  return `Explored ${explorer.visited.length} pages in ${steps} steps, ${reached}, and found ` +
    `${n} ${n === 1 ? 'thing' : 'things'} worth looking at.`;
}

/** reproduction turns the journey into steps somebody can follow.
 *
 * Written for a person and for a rerun at once: the seed is on the first line
 * because `af explore --seed` replays the whole path, and the moves are listed
 * because somebody reading a pull request wants to know what happened without
 * running anything.
 */
export function reproduction(
  goal: Goal, journey: readonly Move[], cause: Cause,
): readonly string[] {
  if (cause !== 'explored') {
    return [
      `Bring the environment up with af up, then run:`,
      `af explore --only ${goal.name}`,
    ];
  }
  return [
    `Bring the environment up with af up, then replay this exact path:`,
    `af explore --only ${goal.name} --seed ${goal.seed}`,
    `Or follow it by hand:`,
    ...journey.map((m, i) => `${i + 1}. ${describeMove(m)}`),
  ];
}

/** anchor turns an accessible name into a pattern matching it exactly.
 *
 * The same shape as workflow.ts's anchored, and separate on purpose: a Move
 * carries a plain string across the JSON boundary and the pattern is built at
 * the point of use, so nothing has to serialise a RegExp. */
export function anchor(name: string): RegExp {
  return new RegExp('^' + name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '$', 'i');
}

function join(base: string, path: string): string {
  if (/^https?:\/\//.test(path)) return path;
  return base.replace(/\/+$/, '') + '/' + path.replace(/^\/+/, '');
}
