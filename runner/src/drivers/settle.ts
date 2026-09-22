// Waiting for an accessibility tree to finish drawing before it is judged.
//
// THE FAILURE. A desktop workflow expecting "transfer.posted" failed in 3.7
// seconds, in front of an Electron ledger client whose ledger had answered
// with thousands of transfer.posted rows. The detail quoted the screen the
// runner had judged: "Journal Reading the ledger Reload The newest journal
// entries SEQ EVENT ACCOUNT AMOUNT RECORDED". That is the client's LOADING
// screen. It fetches the journal in Electron's main process and hands the rows
// to the page over IPC, and until the answer lands the page draws skeleton
// rows and the words "Reading the ledger". The runner read the tree the moment
// the document parsed, the planner found nothing to press and said stuck, and
// that first look became the final judgement. An application was failed for
// loading, on the surface customers judge this product by.
//
// The browser surface has never had this failure, and the reason it has not is
// why this file exists rather than a line in one driver. runner/src/browser.ts
// waits for networkidle before every snapshot and for the text to stop
// changing after every press. Neither is available here. A main process fetch
// is not the page's network, so no network signal would see it, and an iOS or
// Android tree read through Appium has no network signal at all. The one thing
// every accessibility tree surface has is the tree itself, so the tree is what
// is watched: read it again until it stops changing.
//
// That alone is NOT enough, and the case above is exactly why. The loading
// screen is perfectly still. It is drawn once and does not move until the
// answer lands, a second and a half later in the reproduction, so "two
// identical reads in a row" calls it settled and judges it. Stillness says the
// screen is not mid redraw; it cannot say the screen is finished. So there are
// two questions, asked at two different moments:
//
//   before the first decision  has the screen stopped moving? Cheap: one extra
//                              read when it already has.
//   before a final verdict     given the remaining patience, does the screen
//   that is not a pass         CHANGE? If it does, the new screen is what gets
//                              decided about. If it holds still for the whole
//                              budget, it was finished, and it is judged.
//
// The second question costs time only on the path that was about to report a
// failure. A workflow that passes never pays it, and a failure that is real
// pays it once per attempt, because the patience is one budget per attempt and
// not one per question. That trade is deliberate: a slower true FAIL costs
// seconds, and a false FAIL costs the reader's trust in every other verdict.
//
// What this can never do is turn a failure into a pass by itself. It only
// chooses WHICH screen is judged, always a later read of the same application,
// and the judgement is the same finalJudgement every surface uses. A screen
// that settles without the expectation still fails, and a screen that never
// settles is judged on its last read with the detail saying so.

import type { Snapshot } from '../workflow.ts';

/** How long a tree must hold still to count as having stopped moving, and the
 *  gap between two reads.
 *
 *  300 ms because it is the browser's own QUIET_MS in runner/src/browser.ts,
 *  so "the screen stopped changing" means the same length of stillness on
 *  every surface. One read that matches the one before it, taken this long
 *  later, is the whole test, so a screen that is already still costs one read
 *  and one gap. */
export const SETTLE_QUIET_MS = 300;

/** The most one attempt spends waiting for its screen, in total.
 *
 *  10 seconds because it is RENDER_MS in runner/src/browser.ts, the budget the
 *  web surface gives a page that is still fetching before it reads it, and an
 *  application does not load faster because it is a desktop client. How long
 *  a loading screen lasts is the application's own network time, so no
 *  measurement can make a smaller number safe; what the measurements show is
 *  that there is no length short enough to ignore. Measured for this change
 *  against a copy of the ledger client and a local ledger answering at once
 *  with five thousand rows: the first row appeared 186 to 273 ms after the
 *  document finished parsing, which is where the old loop looked, across five
 *  launches. So even an instant backend lost that race. With the answer held
 *  back 1.5 seconds, the reproduction of the failure, the old loop failed at
 *  0.4 and 1.6 seconds in two runs, and this one passed at 3.6 and 4.0,
 *  reading the loaded journal.
 *
 *  It bounds the cost on the other side too. A screen that never stops
 *  changing, a clock or a counter, holds a run for at most this long before it
 *  is judged anyway, and a real failure is reported at most this much later
 *  than it used to be. */
export const SETTLE_BUDGET_MS = 10_000;

/** Tuning, for a test that cannot spend ten seconds per failing case. A run
 *  uses the defaults above. */
export interface SettleOptions {
  readonly budgetMs?: number;
  readonly quietMs?: number;
}

/** What watching the screen found. */
export interface Settled {
  /** snapshot is the last read, which is the one to decide about. */
  readonly snapshot: Snapshot;
  /** still is true when the last read matched the one before it and the
   *  platform did not report the screen busy. False means the budget ran out
   *  while the screen was still moving, and a verdict on it has to say so. */
  readonly still: boolean;
  /** changed is true when the last read differs from the one watching started
   *  from: the screen became something else while it was watched. */
  readonly changed: boolean;
  /** waitedMs is the time spent watching, charged against the budget. */
  readonly waitedMs: number;
}

/** fingerprint is everything about a snapshot a decision or a verdict could
 *  depend on. Two reads with the same fingerprint are the same screen as far
 *  as anything downstream can tell. */
export function fingerprint(s: Snapshot): string {
  return JSON.stringify([
    s.title, s.text, s.controls, s.submits,
    s.fields.map((f) => [f.name, f.type, f.filled, f.required]),
    s.unnamed, s.busy === true,
  ]);
}

/** settle watches the screen, starting from a read already taken, until it
 *  holds still, or when untilChanged is set, until it holds still AS
 *  SOMETHING ELSE. It gives up when budgetMs is spent and reports what it had.
 *
 *  A read the platform marks busy (aria-busy, AXElementBusy) never counts as
 *  still, whatever it matches: an application that says it is working has said
 *  it is not finished, and that is a stronger signal than two reads agreeing.
 *
 *  Never throws on its own account. A read that throws is passed up, because a
 *  tree that cannot be read is the driver's failure and is reported as one by
 *  the loop that called this. */
export async function settle(
  read: () => Promise<Snapshot>,
  from: Snapshot,
  options: { readonly budgetMs: number; readonly quietMs?: number; readonly untilChanged?: boolean },
): Promise<Settled> {
  const quietMs = options.quietMs ?? SETTLE_QUIET_MS;
  const started = Date.now();
  const origin = fingerprint(from);
  let last = from;
  let lastPrint = origin;
  let still = false;
  for (;;) {
    const changed = lastPrint !== origin;
    if (still && (!options.untilChanged || changed)) {
      return { snapshot: last, still, changed, waitedMs: Date.now() - started };
    }
    // Checked before sleeping rather than after, so a spent budget costs no
    // further read at all. That is what makes a budget of zero mean "judge
    // what you have", which the loops rely on once an attempt's patience is
    // used up.
    if (Date.now() - started + quietMs > options.budgetMs) {
      return { snapshot: last, still, changed, waitedMs: Date.now() - started };
    }
    await new Promise((resolve) => setTimeout(resolve, quietMs));
    const next = await read();
    const print = fingerprint(next);
    still = print === lastPrint && next.busy !== true;
    last = next;
    lastPrint = print;
  }
}

/** Patience is one attempt's budget for waiting on its screen, shared by
 *  every settle in that attempt, so the total an attempt can add is bounded by
 *  one budget however many times it is asked. */
export class Patience {
  #remaining: number;
  readonly #quietMs: number;

  constructor(options: SettleOptions = {}) {
    this.#remaining = options.budgetMs ?? SETTLE_BUDGET_MS;
    this.#quietMs = options.quietMs ?? SETTLE_QUIET_MS;
  }

  /** first settles the first read of an attempt: has it stopped moving? */
  async first(read: () => Promise<Snapshot>): Promise<Settled> {
    return this.#spend(read, await read(), false);
  }

  /** beforeVerdict is asked before a verdict that is not a pass: does the
   *  screen change if it is given the patience that is left? */
  async beforeVerdict(read: () => Promise<Snapshot>, from: Snapshot): Promise<Settled> {
    return this.#spend(read, from, true);
  }

  async #spend(read: () => Promise<Snapshot>, from: Snapshot, untilChanged: boolean): Promise<Settled> {
    const settled = await settle(read, from, {
      budgetMs: this.#remaining, quietMs: this.#quietMs, untilChanged,
    });
    this.#remaining = Math.max(0, this.#remaining - settled.waitedMs);
    return settled;
  }
}

/** withSettling appends what watching the screen found to a verdict that is
 *  not a pass. A pass is returned untouched, because a pass needs no excuse and
 *  the report's cell is short.
 *
 *  Appended rather than prepended, because the report keeps the first 120
 *  characters of a detail and those belong to what was missing and what was
 *  there instead. This is the sentence somebody reads once they are asking
 *  whether the runner simply looked too early. */
export function withSettling<T extends { readonly cause: string; readonly detail: string }>(
  judged: T, settled: Settled,
): T {
  if (judged.cause === 'succeeded') return judged;
  const seconds = (settled.waitedMs / 1000).toFixed(1);
  const note = settled.still
    ? settled.waitedMs > 0
      ? ` The screen was watched for ${seconds} s before this verdict and had stopped changing, so it was judged finished.`
      : ''
    : ` The screen was still changing when it was judged: it did not hold still within the ${seconds} s it was ` +
      `watched, so this verdict is about a screen that may not have finished drawing.`;
  return note ? { ...judged, detail: `${judged.detail}${note}` } : judged;
}
