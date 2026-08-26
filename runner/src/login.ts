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

/** How a persona signs in. */
export type LoginStrategy = 'password' | 'magic_link' | 'email_code' | 'sms_code' | 'none';

/** Who is signing in. */
export interface Persona {
  readonly name: string;
  readonly email: string;
  readonly password?: string;
  readonly role?: string;
  readonly login: LoginStrategy;
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

/** Fields and controls, matched by accessible name.
 *
 * Ordered from most specific to least, because "email" matches a newsletter
 * box on a marketing page and "email address" almost never does.
 */
export const FIELD = {
  email: /^(email|email address|e-mail|username or email)$/i,
  password: /^(password|your password)$/i,
  code: /^(code|verification code|one time code|otp|confirmation code)$/i,
} as const;

export const CONTROL = {
  signIn: /^(sign in|log in|login|continue|submit|next)$/i,
  sendLink: /^(send (magic )?link|email me a link|continue with email|send code)$/i,
} as const;

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
  },
): Promise<LoginResult> {
  const timeoutMs = options.timeoutMs ?? 30_000;
  if (persona.login === 'none') {
    return { ok: true, blocked: false, detail: 'This persona does not sign in.' };
  }

  await page.goto(join(options.baseURL, options.signInPath ?? '/login'));

  switch (persona.login) {
    case 'password':
      return signInWithPassword(page, persona, timeoutMs);
    case 'magic_link':
      return signInWithLink(page, persona, options.inbox, timeoutMs, options.after ?? 0);
    case 'email_code':
    case 'sms_code':
      return signInWithCode(page, persona, options.inbox, timeoutMs, options.after ?? 0);
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
  await page.fill(FIELD.email, persona.email);
  await page.click(CONTROL.sendLink);

  const want: Match = { to: persona.email, hasLink: true };
  let message: Message;
  try {
    message = await waitFor(inbox, want, { timeoutMs, after });
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
  await page.fill(FIELD.email, persona.email);
  await page.click(CONTROL.sendLink);

  let message: Message;
  try {
    message = await waitFor(inbox, { to: persona.email, hasCode: true }, { timeoutMs, after });
  } catch (err) {
    return { ok: false, blocked: true, detail: err instanceof Error ? err.message : String(err) };
  }
  if (!message.code) {
    return {
      ok: false, blocked: true,
      detail: `The message to ${persona.email} carried no code to enter.`,
    };
  }
  await page.fill(FIELD.code, message.code);
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
