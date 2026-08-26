// The inbox is how an agent gets past a sign in it did not set up.
//
// A workflow that says "sign up and confirm your email" is waiting on a
// message nobody will deliver, because the environment captured it instead.
// This reads it back, and it waits by polling rather than by sleeping: a
// fixed sleep is either too short on a slow machine or wasted on a fast one,
// and a suite of thirty workflows pays that cost thirty times.

import { setTimeout as delay } from 'node:timers/promises';

/** One captured message. */
export interface Message {
  readonly seq: number;
  readonly at: string;
  readonly provider: string;
  readonly kind: string;
  readonly from?: string;
  readonly to?: readonly string[];
  readonly subject?: string;
  readonly text?: string;
  readonly html?: string;
  readonly links?: readonly string[];
  /** The link most likely to be the one the workflow needs. */
  readonly link?: string;
  /** A one time code found in the body. */
  readonly code?: string;
}

/** What to wait for. */
export interface Match {
  readonly to?: string;
  readonly subjectContains?: string;
  /** hasLink waits for a message carrying a link, which is what a magic link
   *  flow needs and what an announcement email does not have. */
  readonly hasLink?: boolean;
  readonly hasCode?: boolean;
}

/** Reads what the environment captured. */
export interface InboxSource {
  list(limit: number): Promise<readonly Message[]>;
}

/** matches reports whether a message is the one being waited for. */
export function matches(message: Message, want: Match): boolean {
  if (want.to) {
    const to = message.to ?? [];
    if (!to.some((r) => r.toLowerCase() === want.to!.toLowerCase())) return false;
  }
  if (want.subjectContains) {
    const subject = (message.subject ?? '').toLowerCase();
    if (!subject.includes(want.subjectContains.toLowerCase())) return false;
  }
  if (want.hasLink && !message.link) return false;
  if (want.hasCode && !message.code) return false;
  return true;
}

/** waitFor blocks until a matching message arrives.
 *
 * It checks what already arrived before waiting, and that order is the whole
 * correctness of it. The message is usually sent before anybody starts waiting
 * for it, so a wait that only looks forward passes on a slow machine and fails
 * on a fast one, which is the definition of a flaky test.
 */
export async function waitFor(
  source: InboxSource,
  want: Match,
  options: { timeoutMs?: number; intervalMs?: number; after?: number } = {},
): Promise<Message> {
  const timeoutMs = options.timeoutMs ?? 30_000;
  const intervalMs = options.intervalMs ?? 250;
  const after = options.after ?? 0;
  const deadline = Date.now() + timeoutMs;

  for (;;) {
    const messages = await source.list(200);
    // Newest first, because a flow that sends two of the same message wants
    // the one it just triggered rather than the one from the last run.
    const found = [...messages]
      .filter((m) => m.seq > after)
      .sort((a, b) => b.seq - a.seq)
      .find((m) => matches(m, want));
    if (found) return found;

    if (Date.now() >= deadline) {
      throw new InboxTimeout(want, messages);
    }
    await delay(intervalMs);
  }
}

/** InboxTimeout says what was waited for and what did arrive.
 *
 * Listing what did arrive is the difference between "no message came" and a
 * diagnosis: nine times out of ten the message is there and addressed to a
 * different persona, or the subject is not what the workflow assumed.
 */
export class InboxTimeout extends Error {
  readonly want: Match;
  readonly arrived: readonly Message[];

  constructor(want: Match, arrived: readonly Message[]) {
    const described = describeMatch(want);
    const summary = arrived.length === 0
      ? 'Nothing was captured at all.'
      : `What did arrive: ${arrived
          .slice(-5)
          .map((m) => `${(m.to ?? []).join(', ')} "${m.subject ?? ''}"`)
          .join('; ')}.`;
    super(`No message matching ${described} arrived. ${summary}`);
    this.name = 'InboxTimeout';
    this.want = want;
    this.arrived = arrived;
  }
}

function describeMatch(want: Match): string {
  const parts: string[] = [];
  if (want.to) parts.push(`to ${want.to}`);
  if (want.subjectContains) parts.push(`with "${want.subjectContains}" in the subject`);
  if (want.hasLink) parts.push('carrying a link');
  if (want.hasCode) parts.push('carrying a code');
  return parts.length > 0 ? parts.join(' ') : 'anything';
}

/** CommandInbox reads the inbox through the engine's own command.
 *
 * Going through af rather than talking to the sidecar directly means the
 * runner does not need to know where the environment is, and the two cannot
 * disagree about what a captured message looks like.
 */
export class CommandInbox implements InboxSource {
  readonly #run: (args: readonly string[]) => Promise<string>;

  // Written out rather than as a parameter property, because the runner has no
  // build step: Node runs the TypeScript directly by stripping the types, and
  // stripping cannot rewrite a parameter property into an assignment. The same
  // rules out enums and namespaces. The cost is a line; the saving is an
  // entire toolchain between the source and what runs.
  constructor(run: (args: readonly string[]) => Promise<string>) {
    this.#run = run;
  }

  async list(limit: number): Promise<readonly Message[]> {
    const out = await this.#run(['inbox', 'list', '--limit', String(limit), '-o', 'json']);
    const parsed: unknown = JSON.parse(out || '[]');
    return Array.isArray(parsed) ? (parsed as Message[]) : [];
  }
}
