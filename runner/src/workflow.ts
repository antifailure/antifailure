// A workflow is a sentence, not a script.
//
// "Sign up for a new account with a fresh email address, complete every
// required field, and confirm that you land on a signed in page." That is what
// somebody writes, and it is what somebody would tell a colleague. Turning it
// into clicks is the runner's job, and doing it from the accessibility tree
// rather than from selectors is what keeps it working when the markup changes
// and failing when the labels disappear, which is the right way round.
//
// The planner is an interface because there are two of them. With a model key
// the plan comes from a model reading the page. Without one, a deterministic
// planner handles the shapes that are the same in every application: fill the
// form, press the obvious button, follow the link. The second is not a toy: it
// covers sign up, sign in, and checkout in most applications, and it runs with
// no key, no network, and no cost.

import type { Page } from './login.ts';

/** What a workflow asks for. */
export interface Workflow {
  readonly name: string;
  readonly description: string;
  readonly persona?: string;
  /** expect are the sentences that have to be true at the end. */
  readonly expect: readonly string[];
  /** startPath is where to begin. Empty starts at the root. */
  readonly startPath?: string;
  readonly maxSteps?: number;
}

/** One thing to do next. */
export type Action =
  | { readonly kind: 'fill'; readonly field: RegExp; readonly value: string; readonly why: string }
  | { readonly kind: 'click'; readonly control: RegExp; readonly why: string }
  | { readonly kind: 'goto'; readonly url: string; readonly why: string }
  | { readonly kind: 'done'; readonly why: string }
  | { readonly kind: 'stuck'; readonly why: string };

/** What the page looks like right now, in the terms a decision is made in. */
export interface Snapshot {
  readonly url: string;
  readonly title: string;
  /** fields are the form fields, by accessible name. */
  readonly fields: readonly { readonly name: string; readonly type: string; readonly filled: boolean }[];
  /** controls are the buttons and links, by accessible name. */
  readonly controls: readonly string[];
  /** unnamed counts the interactive elements that have no accessible name at
   *  all, so they appear in neither `controls` nor `fields`.
   *
   *  Counted rather than dropped silently because the count is the only
   *  evidence that a page offers something an agent, and a screen reader,
   *  cannot reach. An exploration reports it; the planners ignore it, because
   *  there is nothing they could press. */
  readonly unnamed: number;
  /** text is the visible text, which the expectations are checked against. */
  readonly text: string;
}

/** Decides what to do next. */
export interface Planner {
  next(workflow: Workflow, snapshot: Snapshot, history: readonly Action[]): Promise<Action>;
}

/** Values a deterministic planner types into fields.
 *
 * Chosen so that a human reading the database afterwards can tell at a glance
 * that a row came from an agent rather than from a person, and so that nothing
 * here could ever be mistaken for a real address, card, or phone number.
 */
export interface Identity {
  readonly email: string;
  readonly password: string;
  readonly name: string;
  readonly phone: string;
  readonly card: string;
  readonly postcode: string;
}

/** freshIdentity makes one that is unique per run. */
export function freshIdentity(seed: string): Identity {
  const tag = seed.replace(/[^a-z0-9]/gi, '').toLowerCase().slice(0, 12) || 'agent';
  return {
    // A reserved domain, so a message to it cannot reach anybody even if the
    // capture rule is ever removed.
    email: `af.${tag}@example.test`,
    password: `Antifailure-${tag}-1!`,
    name: `Agent ${tag}`,
    // The reserved test range, which no telephone network routes.
    phone: '+1 555 0100',
    // Stripe's own test card, which every sandbox and mock accepts and no
    // real processor will charge.
    card: '4242424242424242',
    postcode: 'SW1A 1AA',
  };
}

/** Field shapes a deterministic planner recognises.
 *
 * The order is the priority: a more specific name is tried first, so
 * "confirm password" is filled with the password rather than treated as an
 * unknown field and skipped.
 */
const FIELD_VALUES: readonly { readonly match: RegExp; readonly pick: (id: Identity) => string }[] = [
  { match: /confirm.*(password)|password.*confirm|repeat password/i, pick: (i) => i.password },
  { match: /password/i, pick: (i) => i.password },
  { match: /e-?mail/i, pick: (i) => i.email },
  { match: /card number|credit card|^card$/i, pick: (i) => i.card },
  { match: /(post|zip).?code/i, pick: (i) => i.postcode },
  { match: /phone|mobile|telephone/i, pick: (i) => i.phone },
  { match: /full name|your name|^name$|first name|last name|company/i, pick: (i) => i.name },
];

/** identityValueFor returns what to type into a field with this name, or
 *  undefined when nothing here recognises it.
 *
 *  Exported so that the exploratory planner types the same values the declared
 *  one does. Two tables that agree today are two tables that disagree later,
 *  and the symptom would be a form an exploration can fill and a workflow
 *  compiled from it cannot. */
export function identityValueFor(field: string, identity: Identity): string | undefined {
  return FIELD_VALUES.find((r) => r.match.test(field))?.pick(identity);
}

/** Controls a deterministic planner will press, most likely first. */
const PROGRESS_CONTROLS: readonly RegExp[] = [
  /^(sign up|create account|get started|register)$/i,
  /^(subscribe|upgrade|choose plan|select plan)$/i,
  /^(pay|pay now|complete|place order|confirm|checkout)$/i,
  /^(continue|next|submit|save)$/i,
  /^(sign in|log in|login)$/i,
];

/** DeterministicPlanner drives the shapes every application shares.
 *
 * It fills what it recognises, presses the control that moves forward, and
 * stops when the expectations are met or when it has nothing left to try. It
 * is not clever and does not pretend to be: when it gets stuck it says so, and
 * a stuck run is BLOCKED rather than a failure, because the runner not knowing
 * what to click is not evidence about the application.
 */
export class DeterministicPlanner implements Planner {
  readonly #identity: Identity;

  constructor(identity: Identity) {
    this.#identity = identity;
  }

  async next(
    workflow: Workflow, snapshot: Snapshot, history: readonly Action[],
  ): Promise<Action> {
    if (meetsAll(workflow.expect, snapshot.text)) {
      return { kind: 'done', why: 'Every expectation is visible on the page.' };
    }

    // Fill before pressing, always. A form submitted with a field still empty
    // produces a validation error that looks exactly like a broken
    // application, and the agent then reports a failure it caused itself.
    for (const field of snapshot.fields) {
      if (field.filled) continue;
      const rule = FIELD_VALUES.find((r) => r.match.test(field.name));
      if (!rule) continue;
      const already = history.some(
        (a) => a.kind === 'fill' && a.field.source === anchored(field.name).source,
      );
      if (already) continue;
      return {
        kind: 'fill',
        field: anchored(field.name),
        value: rule.pick(this.#identity),
        why: `${field.name} is empty and this workflow needs it filled.`,
      };
    }

    for (const pattern of PROGRESS_CONTROLS) {
      const control = snapshot.controls.find((c) => pattern.test(c));
      if (!control) continue;
      const pressedBefore = history.filter(
        (a) => a.kind === 'click' && a.control.source === anchored(control).source,
      ).length;
      // Pressing the same button twice is how an agent loops forever on a page
      // that did not change. Twice is allowed, because one retry is often
      // exactly right after a field was filled.
      if (pressedBefore >= 2) continue;
      return {
        kind: 'click',
        control: anchored(control),
        why: `${control} is the control that moves this workflow forward.`,
      };
    }

    // Deliberately does not name a verdict. This said "reported as blocked"
    // while finalJudgement decides between blocked and unverified from whether
    // the page contradicted the expectation, so a run that came back
    // unverified printed a row saying unverified, a summary saying unverified,
    // and this sentence saying blocked. The planner cannot know which it will
    // be, and a verdict named in two places is one that will disagree with
    // itself.
    return {
      kind: 'stuck',
      why:
        `Nothing on this page moves the workflow forward. It offers ${describe(snapshot)}. ` +
        `The runner not knowing what to press is not evidence about the application, so it is ` +
        `not counted against it.`,
    };
  }
}

/** anchored turns an accessible name into a pattern that matches it exactly. */
export function anchored(name: string): RegExp {
  return new RegExp('^' + name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '$', 'i');
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

/** What a checker concluded about an expectation.
 *
 * Three answers rather than two, and the third is the honest one. An
 * expectation is a sentence written for a person: "The account shows the paid
 * plan after checkout completes" is satisfied by a page saying "Pro plan,
 * active", and no amount of word matching decides that reliably.
 *
 * So without a model the checker says met when it is sure, unmet when the page
 * is actively saying the opposite, and unclear the rest of the time. Unclear
 * becomes UNVERIFIED, which is exactly right: the workflow ran, nothing broke,
 * and nobody should claim it proved anything.
 */
export type Judgement = 'met' | 'unmet' | 'unclear';

/** Signals that a page is showing a failure rather than a result. */
const FAILURE_SIGNALS: readonly RegExp[] = [
  /\bsomething went wrong\b/i,
  /\bunexpected error\b/i,
  /\binternal server error\b/i,
  /\bpayment (failed|declined)\b/i,
  /\bcould not (complete|process)\b/i,
  /\b(500|502|503)\b.*\berror\b/i,
];

/** judge decides one expectation against the page. */
export function judge(expectation: string, text: string): Judgement {
  const haystack = normalize(text);
  const words = keywords(expectation);
  if (words.length === 0) return 'unclear';

  const hits = words.filter((w) => haystack.includes(w)).length;
  const ratio = hits / words.length;

  // Sure enough to call it met. Not all of them, because an expectation
  // carries connective words no page repeats, and requiring all would mean
  // writing expectations for the matcher instead of for a person.
  if (ratio >= 0.66) return 'met';

  // The page is showing a failure, which is a real answer rather than an
  // absence of one.
  if (FAILURE_SIGNALS.some((p) => p.test(text))) return 'unmet';

  return 'unclear';
}

/** judgeAll combines the expectations.
 *
 * One unmet expectation decides the whole thing, because a workflow that half
 * worked did not work. One unclear expectation makes the whole thing unclear,
 * because a result that rests on a guess is not a result.
 */
export function judgeAll(expectations: readonly string[], text: string): Judgement {
  if (expectations.length === 0) return 'unclear';
  const judgements = expectations.map((e) => judge(e, text));
  if (judgements.includes('unmet')) return 'unmet';
  if (judgements.includes('unclear')) return 'unclear';
  return 'met';
}

/** meetsAll reports whether every expectation is definitely satisfied. */
export function meetsAll(expectations: readonly string[], text: string): boolean {
  return judgeAll(expectations, text) === 'met';
}

const STOP_WORDS = new Set([
  'the', 'a', 'an', 'is', 'are', 'was', 'were', 'be', 'been', 'and', 'or', 'that', 'this',
  'it', 'its', 'on', 'in', 'at', 'to', 'of', 'for', 'with', 'as', 'after', 'before',
  'then', 'not', 'no', 'shows', 'show', 'should', 'must', 'will', 'can', 'confirm',
  'rather', 'than', 'back', 'you', 'your', 'completes', 'complete', 'arrives',
]);

/** keywords pulls the words that carry meaning out of an expectation. */
export function keywords(expectation: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of normalize(expectation).split(/\s+/)) {
    const word = raw.replace(/[^a-z0-9]/g, '');
    if (word.length < 3 || STOP_WORDS.has(word) || seen.has(word)) continue;
    seen.add(word);
    out.push(word);
  }
  return out;
}

function normalize(s: string): string {
  return s.toLowerCase().replace(/\s+/g, ' ');
}

/** Re-exported so a caller does not need two imports to drive a page. */
export type { Page };
