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
  /** answers are what to type into fields this workflow names, keyed by any
   *  part of a field's accessible name and matched case insensitively.
   *
   *  A table, not a script. It says what an answer IS, never what order to do
   *  things in or which control to press, so a workflow with answers is still
   *  a sentence with some of its nouns pinned down rather than a sequence of
   *  clicks. The planner still decides everything else.
   *
   *  It exists for two things the shared shapes cannot cover. A field whose
   *  label nothing recognises, "How many seats" or "VAT number", is otherwise
   *  filled with a sentence saying an agent typed it, which is right for free
   *  text and wrong for a number. And a run against a DEPLOYED application
   *  sometimes has to submit an answer the application will refuse: proving
   *  that a form reaches its server must not mean filing a real job
   *  application in a queue a person reads. See tools/sitesmoke, which is the
   *  caller this was added for.
   */
  readonly answers?: Readonly<Record<string, string>>;
}

/** One thing to do next. */
export type Action =
  | { readonly kind: 'fill'; readonly field: RegExp; readonly value: string; readonly why: string }
  /** check chooses a checkbox or a radio. Separate from `fill` because the
   *  browser refuses to type into either, and because the two are different
   *  decisions: typing an answer and agreeing to a term are not the same act
   *  even when they are the same element to a selector. */
  | { readonly kind: 'check'; readonly field: RegExp; readonly why: string }
  | { readonly kind: 'click'; readonly control: RegExp; readonly why: string }
  | { readonly kind: 'goto'; readonly url: string; readonly why: string }
  | { readonly kind: 'done'; readonly why: string }
  | { readonly kind: 'stuck'; readonly why: string };

/** What the page looks like right now, in the terms a decision is made in. */
export interface Snapshot {
  readonly url: string;
  readonly title: string;
  /** fields are the form fields, by accessible name.
   *
   *  `filled` means answered rather than non-empty: a ticked checkbox and a
   *  radio group with a chosen option are filled, an untouched one is not.
   *  `required` is the browser's own `required`, which is what decides
   *  whether a field nothing here recognises still has to be answered before
   *  the form will submit at all. */
  readonly fields: readonly {
    readonly name: string; readonly type: string;
    readonly filled: boolean; readonly required: boolean;
  }[];
  /** controls are the buttons and links, by accessible name. */
  readonly controls: readonly string[];
  /** submits are the controls that submit a form, by accessible name.
   *
   *  A subset of `controls`, kept apart so a planner has an answer on a form
   *  whose button says something no list of words could have predicted. */
  readonly submits: readonly string[];
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
  /** statement is what goes into a required field nothing here recognises.
   *
   *  A form does not submit while a required field is empty, and the browser
   *  refuses it without a round trip, so a planner that only fills the shapes
   *  it knows presses the button and watches nothing happen. It then reports a
   *  page that proved nothing, on a form it never actually sent. The value
   *  says what wrote it, so a row that reaches a person is obviously an
   *  agent's rather than somebody's real answer. */
  readonly statement: string;
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
    statement:
      'This text was typed by an Antifailure agent driving this form, not by a person. ' +
      'Nothing here is a real answer.',
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

/** Field types that are chosen rather than typed into. */
const CHOSEN_TYPES = new Set(['checkbox', 'radio']);

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

    // What this workflow said to type, before anything this planner guesses.
    // An answer the caller wrote down beats a shape this file recognises,
    // always: the caller can see the application and this table cannot.
    for (const field of snapshot.fields) {
      if (field.filled || CHOSEN_TYPES.has(field.type)) continue;
      const answer = answerFor(workflow, field.name);
      if (answer === undefined) continue;
      if (history.some(
        (a) => a.kind === 'fill' && a.field.source === anchored(field.name).source)) continue;
      return {
        kind: 'fill',
        field: anchored(field.name),
        value: answer,
        why: `${field.name} is answered by this workflow.`,
      };
    }

    // Fill before pressing, always. A form submitted with a field still empty
    // produces a validation error that looks exactly like a broken
    // application, and the agent then reports a failure it caused itself.
    for (const field of snapshot.fields) {
      if (field.filled) continue;
      // A checkbox named "Email me about releases" matches the email rule, and
      // typing into a checkbox throws. Chosen fields are handled below.
      if (CHOSEN_TYPES.has(field.type)) continue;
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

    // Then the fields nothing above recognised but the browser will not let
    // the form be submitted without. Before the choices below rather than
    // after, because typing is the cheaper action to redo if a page rerenders.
    for (const field of snapshot.fields) {
      if (field.filled || !field.required) continue;
      if (CHOSEN_TYPES.has(field.type)) continue;
      if (FIELD_VALUES.some((r) => r.match.test(field.name))) continue;
      if (answerFor(workflow, field.name) !== undefined) continue;
      if (history.some(
        (a) => a.kind === 'fill' && a.field.source === anchored(field.name).source)) continue;
      return {
        kind: 'fill',
        field: anchored(field.name),
        value: this.#identity.statement,
        why:
          `${field.name} is required and empty, and nothing here recognises what it wants. ` +
          `A required field left empty is a form the browser refuses to send.`,
      };
    }

    // Then the acknowledgments and the option groups.
    //
    // Required only, deliberately. A checkbox that is not required is a
    // preference, and an agent that ticks every optional box on a page is
    // subscribing somebody to a newsletter to see what happens. A radio group
    // is always answered when it is required and left alone when it is not,
    // for the same reason: choosing an option nobody asked for is a decision,
    // not progress.
    for (const field of snapshot.fields) {
      if (field.filled || !field.required || !CHOSEN_TYPES.has(field.type)) continue;
      if (history.some(
        (a) => a.kind === 'check' && a.field.source === anchored(field.name).source)) continue;
      return {
        kind: 'check',
        field: anchored(field.name),
        why: field.type === 'radio'
          ? `${field.name} is one option of a required choice that has not been made.`
          : `${field.name} is a required acknowledgment and it is not ticked.`,
      };
    }

    // Pressing the same control twice is how an agent loops forever on a page
    // that did not change. Twice is allowed, because one retry is often
    // exactly right after a field was filled.
    const pressedBefore = (control: string) => history.filter(
      (a) => a.kind === 'click' && a.control.source === anchored(control).source,
    ).length;
    const submit = snapshot.submits.find((c) => pressedBefore(c) < 2);
    const known = snapshot.controls.find(
      (c) => PROGRESS_CONTROLS.some((p) => p.test(c)) && pressedBefore(c) < 2);

    // A form that has been filled in is finished by pressing what SENDS it,
    // and by nothing else on the page.
    //
    // Two failures, one ordering. PROGRESS_CONTROLS is a list of the words
    // that move the shapes every application shares, and it can never be
    // finished: "Send application" is the button on this repository's own
    // careers form and matches none of them, so the agent filled the form in
    // completely and then declared itself stuck in front of the only control
    // that mattered. Adding the document's own submit controls fixed that and
    // exposed the second half, which is worse: this site's header carries a
    // "Sign in" link, `^(sign in|log in|login)$` matches it, and the word list
    // is consulted first. So the agent filled in every field of the careers
    // form, ignored "Send application", followed the header link to the sign
    // in page, and reported that the careers page offered nothing. It had
    // filled in a form and then navigated away from it.
    //
    // So: once anything has been typed or chosen, the submit control wins.
    // Before that, the words win, because a page with a search box and a
    // "Choose Pro" button offers a submit control that sends nobody anywhere.
    //
    // AND ONCE A FORM HAS BEEN SENT AS OFTEN AS IT IS GOING TO BE, THE ANSWER
    // IS STUCK RATHER THAN SOMEWHERE ELSE. That is not a refinement of the
    // rule above, it is the half that makes the report usable. Wandering off
    // to the header link left the run's LAST page as the sign-in screen, so
    // the failure was reported against a page that had nothing to do with the
    // workflow and the error banner the form had just shown was gone from the
    // evidence, the screenshot and the quoted sentence. A page offering a
    // submit control that has already been pressed twice offers this workflow
    // nothing, and saying so is the honest end. A flow whose next step is not
    // a submit control at all, a wizard with a "Continue" link, still falls
    // through to the word list.
    const answeredSomething = history.some((a) => a.kind === 'fill' || a.kind === 'check');
    const control = answeredSomething
      ? (submit ?? (snapshot.submits.length > 0 ? undefined : known))
      : (known ?? submit);
    if (control) {
      return {
        kind: 'click',
        control: anchored(control),
        why: control === submit && answeredSomething
          ? `${control} sends the form this workflow has just filled in.`
          : `${control} is the control that moves this workflow forward.`,
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

/** answerFor returns what this workflow said to type into a field, if it said.
 *
 * Matched as a case insensitive substring of the accessible name rather than
 * as an exact string, because an accessible name is computed from everything
 * inside a wrapping label and carries hint text nobody would think to repeat:
 * "Link to your work (optional)" is the name of a field somebody would key as
 * "Link to your work". The longest key that matches wins, so a page with both
 * "Email" and "Email me a copy" can be answered separately.
 */
export function answerFor(workflow: Workflow, field: string): string | undefined {
  if (!workflow.answers) return undefined;
  const name = field.toLowerCase();
  let best: string | undefined;
  let bestKey = '';
  for (const [key, value] of Object.entries(workflow.answers)) {
    if (!key || !name.includes(key.toLowerCase())) continue;
    if (key.length <= bestKey.length) continue;
    bestKey = key;
    best = value;
  }
  return best;
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

/** Signals that a page is showing a failure rather than a result.
 *
 * THE ONE THAT WAS MISSING, and it is the sentence this repository's own site
 * shows when its forms cannot reach their control plane. Somebody filled in
 * the careers form on antifailure.dev and read "Could not reach the server."
 * Nothing here matched it, so `judge` returned `unclear`, the attempt came
 * back `page-unreadable`, the verdict was UNVERIFIED, and the runner exited
 * zero over a form that did not work. An agent that drives a page and cannot
 * recognise the page telling it the request never arrived is an agent that
 * cannot say no about the failure it is most likely to meet.
 *
 * Written as the shapes a front end actually renders rather than as the one
 * sentence: "could not reach", "unable to reach", "network error", "check your
 * connection", "try again" alongside a refusal. Each is a sentence a page
 * shows INSTEAD of a result, which is the whole membership rule here. A page
 * that merely mentions an error somewhere in its help text does not match,
 * because every pattern requires the wording a failure banner uses.
 */
const FAILURE_SIGNALS: readonly RegExp[] = [
  /\bsomething went wrong\b/i,
  /\bunexpected error\b/i,
  /\binternal server error\b/i,
  /\bpayment (failed|declined)\b/i,
  /\bcould not (complete|process|record|confirm|reach|connect)\b/i,
  /\b(could|can)(\s+not|not|n?['’]t) reach\b/i,
  /\b(unable|failed) to (reach|connect|load|send|submit)\b/i,
  /\bnetwork error\b/i,
  /\bcheck your connection\b/i,
  /\b(500|502|503)\b.*\berror\b/i,
];

/** failureSentence returns the sentence a failure signal matched, or nothing.
 *
 * The point of it is the report. `expectation-not-met` used to be reported as
 * "The page shows an error rather than what was expected", which tells a
 * reader that something is wrong and not one word about what. The sentence the
 * page actually showed is the difference between a red mark somebody has to
 * reproduce and a red mark somebody can act on, and between two failures that
 * look identical in a summary: a control plane that has no such route and a
 * control plane that refused this hostname both break the same form, and the
 * page says something different about each.
 */
export function failureSentence(text: string): string | undefined {
  // Split on sentence ends and on line breaks, because a banner is usually its
  // own block and often carries no full stop at all.
  const pieces = text.split(/\n+|(?<=[.!?])\s+/).map((p) => p.trim()).filter(Boolean);
  for (const piece of pieces) {
    if (FAILURE_SIGNALS.some((p) => p.test(piece))) {
      return piece.length > 300 ? piece.slice(0, 297) + '...' : piece;
    }
  }
  // A signal that spans the pieces this split produced still has to be
  // reportable, so fall back to saying which pattern matched the whole page
  // rather than returning nothing and losing the only evidence there is.
  const whole = FAILURE_SIGNALS.find((p) => p.test(text));
  return whole ? text.slice(0, 297) : undefined;
}

/** An expectation asking for a string exactly, rather than for its sense.
 *
 * `"It is written down."`, quotes included. Anything between a leading and a
 * trailing double quote is required on the page character for character, up to
 * case and run of whitespace.
 *
 * WHY THIS EXISTS. The word ratio below is right for an expectation written as
 * a sentence about the product, and it is badly wrong for a sentence a page
 * either shows or does not. Two thirds of the meaningful words is a low bar on
 * a page with four thousand characters of prose on it: "Use a public http or
 * https link without credentials", the control plane's own refusal, scores six
 * of seven against the UNSUBMITTED careers page, because `public`, `link`,
 * `use`, `credentials` and an install command containing `https` are all
 * already there. The expectation was met before the agent touched the form.
 *
 * A page that renders a specific sentence when something works and a different
 * one when it does not is the ordinary case for a form, and for that case
 * there is nothing to infer. Quoting it says so, and turns a miss into `unmet`
 * rather than `unclear`: a string is present or absent, and there is no third
 * answer to hedge towards.
 */
function verbatim(expectation: string): string | undefined {
  const quoted = /^\s*"([\s\S]+)"\s*$/.exec(expectation);
  return quoted?.[1];
}

/** judge decides one expectation against the page. */
export function judge(expectation: string, text: string): Judgement {
  const haystack = normalize(text);
  const exact = verbatim(expectation);
  if (exact !== undefined) {
    // Never `unclear`. The whole point of quoting a sentence is that its
    // absence is an answer, and the run that found this needed exactly that:
    // "Could not reach the server" is on the page instead, and reporting the
    // careers form as UNVERIFIED over it exits zero.
    return haystack.includes(normalize(exact)) ? 'met' : 'unmet';
  }
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
