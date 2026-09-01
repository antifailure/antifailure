// Signing in is where most agent runs die, and the reason is that every
// application does it differently while claiming to do it the same way.
//
// The four strategies below are the ones that actually exist. What they share
// is that none of them guesses at a selector: the page is found by its
// accessible name, the way a person finds it, so a workflow keeps working when
// somebody changes a class name and stops working when somebody removes the
// label a screen reader depends on. That second case is a bug worth failing on.

import type { Message, InboxSource, Match } from './inbox.ts';
import { waitFor } from './inbox.ts';
import { totpCode, secondsIntoWindow, PERIOD } from './totp.ts';

/** How a persona signs in.
 *
 * These are the values engine/pkg/schema's LoginStrategy emits, and that is
 * the point of the list rather than a detail of it. It used to carry a value
 * the engine could never send ('none') and to be missing two the engine does
 * send ('totp' and 'session'), so a manifest declaring either fell through to
 * the default branch and the run was blocked with "the runner does not know
 * how to drive it". 'none' is still accepted, because an older job document
 * may carry it and refusing to sign somebody in is a poor way to handle a
 * spelling change.
 */
export type LoginStrategy =
  | 'password'
  | 'magic_link'
  | 'email_code'
  | 'sms_code'
  | 'totp'
  | 'session'
  | 'none';

/** Who is signing in. */
export interface Persona {
  readonly name: string;
  readonly email: string;
  /** The number an sms code is sent to. Present only for sms_code, and the
   *  reason it exists at all: the inbox routes a captured text by recipient,
   *  and the recipient of a text is a number rather than an address. Waiting
   *  on the email address for an sms code is waiting for a message that will
   *  never match. */
  readonly phone?: string;
  readonly password?: string;
  readonly role?: string;
  readonly login: LoginStrategy;
  /** The base32 TOTP secret the engine enrolled for this persona, present
   *  when the persona has a second factor. The runner holds it so that it can
   *  complete a challenge, which is what the manifest schema's `mfa` field
   *  says happens. */
  readonly totpSecret?: string;
}

/** The part of a browser page this needs.
 *
 * An interface rather than Playwright's Page, so the logic is testable without
 * a browser and so the browser is one implementation rather than the only one.
 */
export interface Page {
  goto(url: string): Promise<void>;
  /** fill types into the field with an accessible name matching the pattern. */
  fill(field: RegExp, value: string): Promise<void>;
  /** has answers whether that field is on the page right now, without
   *  throwing. It is what lets the sign-in path be searched rather than
   *  assumed: a fill against the wrong page costs ten seconds and reports a
   *  regex, and this costs the caller's own budget and reports a path. */
  has(field: RegExp, timeoutMs: number): Promise<boolean>;
  /** click presses the control whose accessible name matches. */
  click(control: RegExp): Promise<void>;
  /** waitForAny resolves with the first pattern that appears, or null on a
   *  timeout. Returning null rather than throwing is what lets the caller say
   *  which of several possible outcomes happened. */
  waitForAny(patterns: readonly RegExp[], timeoutMs: number): Promise<RegExp | null>;
  /** text returns the page's visible text, for the assertions. */
  text(): Promise<string>;
  url(): string;
}

/** What a sign in attempt produced. */
export interface LoginResult {
  readonly ok: boolean;
  /** blocked marks a failure the environment owes rather than the application:
   *  a code that never arrived, a persona with no password. Charging those to
   *  the application is how people learn to ignore the results. */
  readonly blocked: boolean;
  readonly detail: string;
}

const SIGNED_IN = [
  /sign out/i, /log out/i, /dashboard/i, /account/i, /settings/i, /welcome back/i,
];
const STILL_OUT = [
  /invalid|incorrect|wrong password/i, /try again/i, /couldn.t sign you in/i,
];

/** What a page asking for a second factor says.
 *
 * Unanchored, unlike FIELD.code, and that difference is the whole point of it
 * being a separate constant. FIELD.code is matched against one element's
 * accessible name by getByLabel, so it is anchored with ^ and $. These are
 * matched against the whole page's text by waitForAny, where an anchored
 * pattern can only match a page whose entire body is the word "Code", which is
 * to say never.
 */
const CHALLENGED = [
  /verification code/i, /one.time code/i, /two.factor/i, /authentication code/i,
  /enter the code/i, /6.digit code/i, /security code/i,
];

/** Fields and controls, matched by accessible name.
 *
 * Ordered from most specific to least, because "email" matches a newsletter
 * box on a marketing page and "email address" almost never does.
 */
export const FIELD = {
  email: /^(email|email address|e-mail|username or email)$/i,
  phone: /^(phone|phone number|mobile|mobile number|telephone)$/i,
  password: /^(password|your password)$/i,
  code: /^(code|verification code|one time code|otp|confirmation code)$/i,
} as const;

export const CONTROL = {
  signIn: /^(sign in|log in|login|continue|submit|next)$/i,
  /** The button that asks for a link or a code.
   *
   * The list used to be four literal strings and it did not include "Send a
   * sign-in link", which is what this repository's own console says and what
   * a great many applications say. The article and the qualifier are the part
   * that gets written differently every time, so they are optional here
   * rather than enumerated: send / email / get / request, optionally "me",
   * optionally an article, optionally what kind of link it is, then link or
   * code.
   */
  sendLink:
    /^((send|email|get|request)( me)?( a| the)?( magic| sign.?in| log.?in| one.?time)? (link|code)|continue with email)$/i,
} as const;

/** Where a sign-in form lives, in the order worth trying.
 *
 * This used to be the single string '/login', with a signInPath option that
 * nothing passed, so the path was hardcoded while looking configurable. It
 * held for as long as every application under test had a /login, and this
 * repository's own control plane stopped being one: its console is a static
 * export with a file per route, /login is not a route, and the request lands
 * on the 404 page. Every Dogfood run since the console was folded into the
 * API reported all six workflows blocked with a ten second timeout waiting
 * for an email field, on a page whose entire text was "That page is not
 * here".
 *
 * The workflow's own start path goes first, ahead of this list, because an
 * application that answers every protected route with its sign-in screen is
 * the common shape and is the one this repository has. '/' comes last, for
 * the application whose front door is its home page.
 */
export const SIGN_IN_PATHS = [
  '/login', '/signin', '/sign-in', '/auth/login', '/users/sign_in', '/',
] as const;

/** How long each candidate path gets to show the field before the next is
 *  tried. Six candidates at this budget is under fifteen seconds in the worst
 *  case, which is the case where no sign-in form exists anywhere and the run
 *  is about to be reported blocked regardless. */
const PROBE_MS = 2_000;

/**
 * Navigate to wherever this application's sign-in form actually is, and say
 * whether one was found.
 *
 * The page is left on the candidate that answered, so the caller fills the
 * form it just found rather than navigating again.
 */
async function openSignIn(
  page: Page, baseURL: string, first: string | undefined, wanted: RegExp,
): Promise<{ readonly path: string | null; readonly tried: readonly string[] }> {
  const tried: string[] = [];
  const candidates = first ? [first, ...SIGN_IN_PATHS] : [...SIGN_IN_PATHS];
  for (const path of candidates) {
    if (tried.includes(path)) continue;
    tried.push(path);
    await page.goto(join(baseURL, path));
    if (await page.has(wanted, PROBE_MS)) return { path, tried };
  }
  return { path: null, tried };
}

/** watermark is the highest sequence the inbox holds right now.
 *
 * Read before the button is pressed, and it is the whole correctness of a
 * retry. `waitFor` deliberately looks at what already arrived before it waits,
 * because the message is usually captured before anybody starts waiting for
 * it. That is right for a message that need only exist, and wrong for one that
 * has to be NEW: a magic link is single use, so a second attempt that matches
 * the first attempt's message follows a token the first attempt already spent
 * and the application answers "This sign-in link is no longer valid."
 *
 * It was a race rather than a certainty, which is why it survived: whether the
 * fresh message had landed by the first poll decided it. Driving this
 * repository's own six workflows produced two that signed in and four that did
 * not, from one code path, in one run.
 *
 * A floor rather than a filter on time, because the sequence is the only thing
 * that orders two messages captured in the same millisecond.
 */
async function watermark(inbox: InboxSource, floor: number): Promise<number> {
  try {
    const messages = await inbox.list(200);
    return messages.reduce((highest, m) => (m.seq > highest ? m.seq : highest), floor);
  } catch {
    // An inbox that cannot be read here cannot be read a moment later either,
    // and waitFor says so far better than this could. Carrying on with the
    // caller's floor keeps the diagnosis in the one place that has it.
    return floor;
  }
}

/** signIn drives one persona through its strategy. */
export async function signIn(
  page: Page,
  persona: Persona,
  options: {
    readonly baseURL: string;
    readonly signInPath?: string;
    readonly inbox?: InboxSource;
    readonly timeoutMs?: number;
    readonly after?: number;
    /** now overrides the clock, so a test can pin down a TOTP window instead
     *  of being flaky once every thirty seconds. */
    readonly now?: () => number;
  },
): Promise<LoginResult> {
  const timeoutMs = options.timeoutMs ?? 30_000;
  if (persona.login === 'session' || persona.login === 'none') {
    // The environment provides the session rather than the agent typing
    // credentials, so there is no form to drive and the workflow starts where
    // it starts.
    return { ok: true, blocked: false, detail: 'This persona does not sign in through a form.' };
  }

  // An sms code is asked for by number and everything else by address, so the
  // field that says "this is the sign-in form" differs by strategy.
  const wanted = persona.login === 'sms_code' ? FIELD.phone : FIELD.email;
  const found = await openSignIn(page, options.baseURL, options.signInPath, wanted);
  if (found.path === null) {
    // The environment's problem rather than the change's, and named by path
    // rather than by regex. A run that says which addresses were tried is one
    // somebody can fix; a run that says `locator.fill: Timeout 10000ms` is one
    // somebody reruns.
    return {
      ok: false, blocked: true,
      detail:
        `No sign-in form was found for ${persona.name}. Tried ${found.tried.join(', ')} ` +
        `on ${options.baseURL}, and none of them showed a field labelled for an ` +
        `${persona.login === 'sms_code' ? 'phone number' : 'email address'}.`,
    };
  }

  switch (persona.login) {
    case 'password':
      return signInWithPassword(page, persona, timeoutMs);
    case 'magic_link':
      return signInWithLink(page, persona, options.inbox, timeoutMs, options.after ?? 0);
    case 'email_code':
      return signInWithCode(page, persona, options.inbox, timeoutMs, options.after ?? 0);
    case 'sms_code':
      return signInWithCode(page, persona, options.inbox, timeoutMs, options.after ?? 0);
    case 'totp':
      return signInWithTOTP(page, persona, timeoutMs, options.now);
    default:
      return {
        ok: false, blocked: true,
        detail: `This persona uses ${persona.login}, which the runner does not know how to drive.`,
      };
  }
}

async function signInWithPassword(
  page: Page, persona: Persona, timeoutMs: number,
): Promise<LoginResult> {
  if (!persona.password) {
    // The environment's problem, not the application's. A persona with no
    // password is a manifest that has not finished, and reporting it as a
    // failing test would point at the wrong file.
    return {
      ok: false, blocked: true,
      detail: `The persona ${persona.name} signs in with a password and has none set.`,
    };
  }
  await page.fill(FIELD.email, persona.email);
  await page.fill(FIELD.password, persona.password);
  await page.click(CONTROL.signIn);
  return settle(page, timeoutMs, persona);
}

async function signInWithLink(
  page: Page, persona: Persona, inbox: InboxSource | undefined,
  timeoutMs: number, after: number,
): Promise<LoginResult> {
  if (!inbox) {
    return {
      ok: false, blocked: true,
      detail: `The persona ${persona.name} signs in by magic link, and no inbox is available to read it from.`,
    };
  }
  // Before the button, never after. A message already in the inbox is not the
  // answer to a request that has not been made yet.
  const floor = await watermark(inbox, after);
  await page.fill(FIELD.email, persona.email);
  await page.click(CONTROL.sendLink);

  const want: Match = { to: persona.email, hasLink: true };
  let message: Message;
  try {
    message = await waitFor(inbox, want, { timeoutMs, after: floor });
  } catch (err) {
    return {
      ok: false, blocked: true,
      detail: err instanceof Error ? err.message : String(err),
    };
  }
  if (!message.link) {
    return {
      ok: false, blocked: true,
      detail: `The message to ${persona.email} carried no link to follow.`,
    };
  }
  await page.goto(message.link);
  return settle(page, timeoutMs, persona);
}

async function signInWithCode(
  page: Page, persona: Persona, inbox: InboxSource | undefined,
  timeoutMs: number, after: number,
): Promise<LoginResult> {
  if (!inbox) {
    return {
      ok: false, blocked: true,
      detail: `The persona ${persona.name} signs in with a code, and no inbox is available to read it from.`,
    };
  }

  // A text is addressed to a number and mail is addressed to an address, and
  // the inbox routes both by recipient. Waiting on the email address for an
  // sms code waits for a message that can never match, which reads as "the
  // code never arrived" and is really "we asked the wrong question".
  const recipient = persona.login === 'sms_code' ? persona.phone : persona.email;
  if (!recipient) {
    return {
      ok: false, blocked: true,
      detail: `The persona ${persona.name} signs in with an SMS code and has no phone number set.`,
    };
  }

  // Before the button, for the reason on watermark. A one time code is spent
  // the same way a link is.
  const floor = await watermark(inbox, after);
  await page.fill(persona.login === 'sms_code' ? FIELD.phone : FIELD.email, recipient);
  await page.click(CONTROL.sendLink);

  let message: Message;
  try {
    message = await waitFor(inbox, { to: recipient, hasCode: true }, { timeoutMs, after: floor });
  } catch (err) {
    return { ok: false, blocked: true, detail: err instanceof Error ? err.message : String(err) };
  }
  if (!message.code) {
    return {
      ok: false, blocked: true,
      detail: `The message to ${recipient} carried no code to enter.`,
    };
  }
  await page.fill(FIELD.code, message.code);
  await page.click(CONTROL.signIn);
  return settle(page, timeoutMs, persona);
}

async function signInWithTOTP(
  page: Page, persona: Persona, timeoutMs: number, now?: () => number,
): Promise<LoginResult> {
  if (!persona.totpSecret) {
    // The environment's problem, not the application's. A persona that signs
    // in with a second factor and has no secret enrolled is an environment
    // that has not finished.
    return {
      ok: false, blocked: true,
      detail: `The persona ${persona.name} signs in with a one time code and has no secret enrolled.`,
    };
  }
  if (!persona.password) {
    return {
      ok: false, blocked: true,
      detail: `The persona ${persona.name} signs in with a password and a one time code, and has no password set.`,
    };
  }

  // A second factor comes after the password, which is the flow every
  // application implements: credentials, then the challenge.
  await page.fill(FIELD.email, persona.email);
  await page.fill(FIELD.password, persona.password);
  await page.click(CONTROL.signIn);

  // The challenge field is what says the password was accepted. Waiting for
  // either it or a refusal distinguishes "wrong password" from "no second
  // factor was asked for", which are different bugs.
  const asked = await page.waitForAny([...CHALLENGED, ...STILL_OUT, ...SIGNED_IN], timeoutMs);
  if (asked === null) {
    return {
      ok: false, blocked: false,
      detail:
        `After entering the password for ${persona.name}, the page asked for no code and showed ` +
        `no error. The last address was ${page.url()}.`,
    };
  }
  if (STILL_OUT.some((p) => p.source === asked.source)) {
    return {
      ok: false, blocked: false,
      detail: `Signing in as ${persona.name} was refused by the application.`,
    };
  }
  if (SIGNED_IN.some((p) => p.source === asked.source)) {
    // Signed in without being challenged. The application did not enforce the
    // second factor, and that is worth reporting as a finding rather than
    // quietly passing, because "mfa: true" in the manifest says it should
    // have been asked for.
    return {
      ok: false, blocked: false,
      detail:
        `${persona.name} has a second factor enrolled and the application signed them in ` +
        `without asking for it.`,
    };
  }

  const clock = now ?? Date.now;
  // A code produced at the very end of a window is one the application may
  // reject by the time it is typed. Waiting out the last two seconds is much
  // cheaper than a failed sign in that reads as a real bug.
  const remaining = PERIOD - secondsIntoWindow(clock());
  if (remaining <= 2) {
    await new Promise((resolve) => setTimeout(resolve, (remaining + 1) * 1000));
  }

  let code: string;
  try {
    code = totpCode(persona.totpSecret, clock());
  } catch (err) {
    return {
      ok: false, blocked: true,
      detail: `The secret enrolled for ${persona.name} is not usable: ` +
        (err instanceof Error ? err.message : String(err)),
    };
  }

  await page.fill(FIELD.code, code);
  await page.click(CONTROL.signIn);
  return settle(page, timeoutMs, persona);
}

/** settle waits for the page to say whether it worked.
 *
 * It waits for either outcome rather than for a fixed time, so a fast sign in
 * is fast and a slow one is not cut off. A timeout with neither outcome is
 * reported as its own thing, because "the page never said" is a different
 * problem from "the password was wrong".
 */
async function settle(page: Page, timeoutMs: number, persona: Persona): Promise<LoginResult> {
  const found = await page.waitForAny([...SIGNED_IN, ...STILL_OUT], timeoutMs);
  if (found === null) {
    return {
      ok: false, blocked: false,
      detail:
        `After signing in as ${persona.name}, the page showed neither a signed in state nor an ` +
        `error. The last address was ${page.url()}.`,
    };
  }
  if (STILL_OUT.some((p) => p.source === found.source)) {
    return {
      ok: false, blocked: false,
      detail: `Signing in as ${persona.name} was refused by the application.`,
    };
  }
  return { ok: true, blocked: false, detail: `Signed in as ${persona.name}.` };
}

function join(base: string, path: string): string {
  if (/^https?:\/\//.test(path)) return path;
  return base.replace(/\/+$/, '') + '/' + path.replace(/^\/+/, '');
}
