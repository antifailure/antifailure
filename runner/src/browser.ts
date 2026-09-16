// The browser is driven through the accessibility tree, not through selectors.
//
// A workflow that says "press Continue" should keep working when somebody
// renames a CSS class and should stop working when somebody removes the label
// that a screen reader depends on. That is the right way round, and it is the
// only way to write a workflow as a sentence rather than as a script.

import {
  chromium, devices, type Browser, type BrowserContext, type Page as PWPage,
} from 'playwright';
import type { Request as PWRequest, Response as PWResponse } from 'playwright';
import type { Page } from './login.ts';
import type { Snapshot } from './workflow.ts';
import { FramePump, type LiveSink } from './live.ts';

/** The same pattern with its anchors taken off.
 *
 * The patterns in FIELD are anchored on purpose: an unanchored /email/ matches
 * the newsletter box on a marketing page and /email address/ almost never
 * does. What anchoring also excludes, and nobody noticed until this
 * repository's own console was driven, is a field whose label carries its own
 * hint text. A wrapping label computes ONE accessible name out of everything
 * inside it, so
 *
 *     <label>Email address <input> We send a link that signs you in.</label>
 *
 * is named "Email address We send a link that signs you in.", the anchored
 * pattern matches nothing at all, and the fill times out after ten seconds
 * with an error naming the regex and not the reason.
 */
function unanchored(field: RegExp): RegExp {
  return new RegExp(field.source.replace(/^\^/, '').replace(/\$$/, ''), field.flags);
}

/**
 * The field with exactly this accessible name, or failing that the one whose
 * name merely contains it.
 *
 * The exact match is waited for rather than counted, because a page that
 * renders its form after a fetch has no fields at all for the first few
 * hundred milliseconds, and counting would fall through to the loose pattern
 * every time on exactly the applications where precision matters most.
 */
async function locate(pw: PWPage, field: RegExp, timeoutMs: number) {
  const exact = pw.getByLabel(field).first();
  try {
    await exact.waitFor({ state: 'visible', timeout: timeoutMs });
    return exact;
  } catch {
    return pw.getByLabel(unanchored(field)).first();
  }
}

/** How long a page gets to finish rendering before anything reads it.
 *
 * An application that renders on the client has nothing on it when the
 * document finishes parsing. This repository's own console is the case that
 * found it: after the sign-in callback lands, the whole body text is the
 * single word "Loading" for about a second and a half. A snapshot taken there
 * reports a page that offers no fields and no controls at all, so two
 * workflows signed in successfully and were then reported as having proved
 * nothing, on a page that was a second away from showing everything they were
 * asked to look for.
 *
 * networkidle rather than a fixed sleep, because the cost is then paid only by
 * the pages that need it: a page that is already idle returns in about a
 * millisecond, and one that is still fetching waits exactly as long as it
 * fetches. Playwright's 500ms quiet period is the floor, which is what a
 * served JSON document pays.
 */
const RENDER_MS = 10_000;

/** Evidence captured from a run. */
export interface Evidence {
  /** Video is the recording, when one was made. */
  readonly video?: string;
  /** Trace is the Playwright trace, which is the thing somebody actually
   *  opens when they want to see what happened. */
  readonly trace?: string;
  /** Screenshot is the final state, which is what goes in a comment. */
  readonly screenshot?: string;
  /** Console is the browser's own output, which carries the stack trace a
   *  failing page printed and nothing else records. */
  readonly console: readonly string[];
  /** Failed lists network requests the page could not make, which is usually
   *  the egress policy and is worth saying so rather than leaving somebody to
   *  guess. */
  readonly failed: readonly string[];
  /** DOM is the rendered text of each page the run stood on, and Responses is
   *  the bounded body of each same-origin document, JSON or text response it
   *  received. They exist so a leak family can scan for a secret by SHAPE, a
   *  Stripe key or a private key that reached a response, without a second pass
   *  over the environment. Both stay inside the engine as evidence: a finding
   *  reports a location and a kind, never a captured byte. Bounded in count and
   *  in bytes so a chatty page cannot fill memory, and a cross-origin third
   *  party's body is never captured, because it is neither this run's secret to
   *  find nor a body this run is responsible for. */
  readonly dom: readonly string[];
  readonly responses: readonly string[];
}

/** How much captured evidence one session keeps, so a run against a chatty or
 *  large application cannot grow without bound. A body over the per-body cap is
 *  truncated rather than dropped, since a secret shape near the front is still
 *  worth catching; a body whose declared length is already over it is skipped
 *  before it is read at all. The total cap is the backstop across every stream. */
const MAX_RESPONSES = 200;
const MAX_RESPONSE_BYTES = 64 * 1024;
const MAX_DOM = 100;
const MAX_EVIDENCE_BYTES = 4 * 1024 * 1024;
/** How many distinct reached requests one session records. The injection family
 *  fuzzes a de-duplicated set of routes, so this bounds the raw reach before the
 *  dedup, and a page that fetches thousands of times cannot grow it unboundedly. */
const MAX_REACHED = 1_000;

/** originOf is the scheme://host:port of a URL, or undefined when it has none to
 *  speak of, so an about:blank or a data URL is never treated as same-origin. */
function originOf(raw: string): string | undefined {
  try {
    const u = new URL(raw);
    if (u.protocol !== 'http:' && u.protocol !== 'https:') return undefined;
    return u.origin;
  } catch {
    return undefined;
  }
}

/** pathWithQuery is a URL's path and query, without its origin, so a reached
 *  request carries a location and the query parameter names a fuzzer varies. The
 *  values are reduced to names only by the engine before a route can leave it,
 *  and the runner strips anything it typed on the way out. */
function pathWithQuery(raw: string): string | undefined {
  try {
    const u = new URL(raw);
    return u.pathname + u.search;
  } catch {
    return undefined;
  }
}

/** isTextResponse reports whether a content type is one a leak family can scan
 *  as text: a document, JSON, or another text or XML body. An image, a font or
 *  an opaque binary is not, and capturing it would spend the byte budget on
 *  something no detector reads. */
function isTextResponse(contentType: string): boolean {
  return (
    contentType.includes('text/')
    || contentType.includes('application/json')
    || contentType.includes('+json')
    || contentType.includes('application/xml')
    || contentType.includes('+xml')
  );
}

/** Waits for the page to stop fetching, and gives up quietly.
 *
 * Swallowed rather than thrown: a page holding a socket open, or polling once
 * a second, never goes idle at all, and those are applications this has to be
 * able to look at. Reading a busy page is better than refusing to read it.
 */
async function settled(pw: PWPage): Promise<void> {
  await pw.waitForLoadState('networkidle', { timeout: RENDER_MS }).catch(() => {});
}

/** How long a press gets to finish what it started, and how long still counts
 *  as the page having stopped changing.
 *
 * THE FAILURE. A press on a form that submits through `fetch` returns the
 * instant the click lands, and the page's own handler then disables the
 * fieldset while the request is in flight. The snapshot taken immediately
 * afterwards reads a form with every field disabled and a submit button
 * relabelled "Recording it", which the snapshot reports as a page offering
 * nothing at all. Driving this repository's own careers form, the agent filled
 * it in, pressed "Send application", looked at the page a millisecond later,
 * decided nothing there moved it forward, and followed the "Sign in" link in
 * the site header instead. It had filled in a form and then walked away from
 * it, and the run reported a careers page with no controls on it.
 *
 * `settled` CANNOT FIX THIS AND IT IS WORTH KNOWING WHY, because it looks like
 * it should. `waitForLoadState('networkidle')` asks whether the CURRENT
 * DOCUMENT has already reached that state, not whether the page is quiet right
 * now. A client rendered page reaches it seconds after it loads and stays
 * there, so every later call returns immediately, and a press that starts a
 * request is followed by a "wait" that waits for nothing. It is still right
 * after a navigation, which is a new document, and that is what it is kept
 * for.
 *
 * What is actually being waited for is the page settling, so that is what is
 * measured: the rendered text is read until it stops changing.
 */
const PRESS_MS = 15_000;
const QUIET_MS = 300;

/** Waits until the page's rendered text stops changing, or the budget runs out.
 *
 * Bounded on both sides on purpose. A page that never stops changing, because
 * something on it animates or polls, must not hold a run forever; and a page
 * that changed once must not be read in the middle of changing again.
 */
async function quiet(
  pw: PWPage, budgetMs: number, inFlight: () => number,
): Promise<void> {
  const deadline = Date.now() + budgetMs;
  let last: string | undefined;
  let since = Date.now();
  for (;;) {
    const now = await pw.locator('body').innerText().catch(() => '');
    // A request still in the air is the case this is really about. The form
    // disables its own fieldset for exactly as long as the request takes, so
    // the text is perfectly stable while the page is at its least readable:
    // no fields, no submit control, and a button labelled "Recording it".
    // Text alone said "settled" there, and a slow answer from the control
    // plane was enough to make the agent walk off to the site header.
    const busy = inFlight() > 0;
    if (busy || now !== last) {
      last = now;
      since = Date.now();
    } else if (Date.now() - since >= QUIET_MS) {
      return;
    }
    if (Date.now() >= deadline) return;
    await pw.waitForTimeout(100);
  }
}

/** How long an interrupted session waits for its last screenshot. */
const INTERRUPTED_SCREENSHOT_MS = 1_000;

/** The window a session opens when nobody asked for one. */
export const DEFAULT_VIEWPORT = { width: 1280, height: 800 } as const;

/** The user agent a phone session presents. Playwright's own description of
 *  the phone whose screen the phone viewport copies, rather than a string
 *  typed here, so it stays one a real phone sends. */
const PHONE_USER_AGENT = devices['iPhone 13']!.userAgent;

/** Session is one browser, one context, one page, and its evidence. */
export class Session {
  readonly #browser: Browser;
  readonly #context: BrowserContext;
  readonly #page: PWPage;
  readonly #console: string[] = [];
  readonly #failed: string[] = [];
  /** The rendered text of each page the run stood on and the bounded body of
   *  each same-origin text response it received, for a leak family to scan by
   *  shape. Bounded by the caps above; #evidenceBytes is the running total. */
  readonly #dom: string[] = [];
  readonly #responses: string[] = [];
  #evidenceBytes = 0;
  /** Every same-origin request that reached the server, deduped by method and
   *  path, so the injection family can fuzz a POST or fetch route and not only
   *  a GET navigation. #reachedSeen is the dedup key set. */
  readonly #reached: { method: string; path: string }[] = [];
  readonly #reachedSeen = new Set<string>();
  /** The origin of the first document this page navigated to, so a third party
   *  the page pulls in (analytics, a font CDN) is never captured as evidence or
   *  fuzzed as a route. Undefined until the first navigation. */
  #baseOrigin: string | undefined;
  /** How many requests this page has in the air right now. See quiet. */
  #inFlight = 0;
  /** The main frame's navigation that has not answered yet, if any. See click. */
  #navigating: PWRequest | undefined;
  /** lastStatus is the HTTP status of the last document navigated to,
   *  undefined until the first goto. See snapshot's own field for why this
   *  exists at all. */
  #lastStatus: number | undefined;
  readonly #artifacts: string;
  /** The width and height of the window, so a live frame can name its own
   *  dimensions without asking the page on every shot. */
  readonly #viewport: { readonly width: number; readonly height: number };
  /** The live frame pump, running only when somebody is watching. Undefined
   *  when no live sink was given, which is the ordinary case and costs nothing. */
  #pump: FramePump | undefined;

  private constructor(
    browser: Browser, context: BrowserContext, page: PWPage, artifacts: string,
    viewport: { readonly width: number; readonly height: number },
  ) {
    this.#browser = browser;
    this.#context = context;
    this.#page = page;
    this.#artifacts = artifacts;
    this.#viewport = viewport;
  }

  /** open starts a browser with recording on.
   *
   * Recording is always on rather than on failure, because a failure that was
   * not recorded is a failure somebody has to reproduce by hand, and the run
   * that failed is the run that is hardest to reproduce.
   */
  static async open(options: {
    readonly artifacts: string;
    readonly headless?: boolean;
    readonly viewport?: { readonly width: number; readonly height: number };
    /** mobile opens the window as a phone rather than as a narrow desktop:
     *  a touch screen, a phone's user agent and its pixel density. A layout
     *  that switches on a media query reflows for a small window alone, and
     *  one that switches on the user agent or on touch does not, so a window
     *  size with nothing else changed finds only half of what a phone does. */
    readonly mobile?: boolean;
    /** live, when given, streams frames from this session to a watcher while
     *  the run is still going. Absent means nobody is watching and no frame is
     *  ever taken for this purpose: the ordinary run pays nothing. The sink is
     *  best effort and never changes the run, exactly like the durable video. */
    readonly live?: { readonly sink: LiveSink; readonly agent: string };
  }): Promise<Session> {
    const browser = await chromium.launch({ headless: options.headless ?? true });
    const viewport = options.viewport ?? DEFAULT_VIEWPORT;
    const context = await browser.newContext({
      recordVideo: { dir: options.artifacts },
      viewport,
      ...(options.mobile
        ? { isMobile: true, hasTouch: true, deviceScaleFactor: 3, userAgent: PHONE_USER_AGENT }
        : {}),
      // A preview environment serves its own certificate for any host the
      // policy inspects, and the browser is inside that environment.
      ignoreHTTPSErrors: true,
    });
    await context.tracing.start({ screenshots: true, snapshots: true, sources: false });

    const page = await context.newPage();
    const session = new Session(browser, context, page, options.artifacts, viewport);

    page.on('request', (r) => {
      session.#inFlight++;
      if (r.isNavigationRequest() && r.frame() === page.mainFrame()) {
        session.#navigating = r;
        // The first document the page navigated to fixes the origin every later
        // capture is measured against, so a third party the page pulls in is
        // never mistaken for the application's own surface.
        if (session.#baseOrigin === undefined) session.#baseOrigin = originOf(r.url());
      }
    });
    page.on('response', (r) => { void session.#record(r); });
    page.on('requestfinished', (r) => {
      session.#inFlight--;
      if (r === session.#navigating) session.#navigating = undefined;
    });
    page.on('console', (m) => {
      if (m.type() === 'error' || m.type() === 'warning') {
        session.#console.push(`${m.type()}: ${m.text()}`);
      }
    });
    page.on('requestfailed', (r) => {
      session.#inFlight--;
      if (r === session.#navigating) session.#navigating = undefined;
      // The egress policy is the usual cause, and naming the request is the
      // difference between a mystery and a one line fix.
      session.#failed.push(`${r.method()} ${r.url()}: ${r.failure()?.errorText ?? 'failed'}`);
    });

    // The live frame pump samples the page while the run is going, for a
    // watcher only. It reuses the same screenshot the tracing path already
    // knows how to take, at a lower fidelity and on an interval, and feeds it
    // to the sink. A run with no watcher never constructs one.
    if (options.live) {
      session.#pump = new FramePump(
        () => session.liveFrame(),
        options.live.sink,
        options.live.agent,
      );
      session.#pump.start();
    }
    return session;
  }

  /** liveFrame takes one low-fidelity JPEG of the current viewport for a
   *  watcher. Viewport rather than full page, and a short timeout, because a
   *  live frame is worth having only if it is cheap and current: a slow full
   *  page shot mid navigation is neither. Returns undefined when a frame could
   *  not be taken, which the pump treats as a frame to skip, not an error. */
  async liveFrame(): Promise<{ w: number; h: number; b64: string } | undefined> {
    try {
      const buffer = await this.#page.screenshot({
        type: 'jpeg', quality: 45, timeout: 2_500, fullPage: false,
      });
      return { w: this.#viewport.width, h: this.#viewport.height, b64: buffer.toString('base64') };
    } catch {
      return undefined;
    }
  }

  /** record folds one response into the run's evidence: the request that
   *  reached the server, so a POST or fetch route can be fuzzed and not only a
   *  GET navigation, and the response body, so a leak family can scan it for a
   *  secret by shape. Same-origin only, text-like only, and bounded in count and
   *  in bytes, so a third party's body, a large download or an image never
   *  enters the evidence. Best effort: a body that cannot be read is skipped,
   *  never thrown, exactly like the live frame and the durable video, because a
   *  response that raced teardown must never fail a run.
   */
  async #record(resp: PWResponse): Promise<void> {
    try {
      const origin = originOf(resp.url());
      // A third party the page pulled in is neither a route this run reached in
      // the sense the fuzzer means nor a body this run is responsible for.
      if (origin === undefined || origin !== this.#baseOrigin) return;

      // The request reached the server: record its method and location, deduped.
      // The path carries its query for the engine to reduce to parameter names;
      // no value leaves in a route.
      const path = pathWithQuery(resp.url());
      if (path !== undefined) {
        const method = resp.request().method();
        const key = `${method} ${path}`;
        if (!this.#reachedSeen.has(key) && this.#reached.length < MAX_REACHED) {
          this.#reachedSeen.add(key);
          this.#reached.push({ method, path });
        }
      }

      // The body, for a leak family to scan. Text-like only, bounded per body
      // and in total, so a large or binary response never sits in memory.
      if (this.#responses.length >= MAX_RESPONSES) return;
      if (this.#evidenceBytes >= MAX_EVIDENCE_BYTES) return;
      const type = (resp.headers()['content-type'] ?? '').toLowerCase();
      if (!isTextResponse(type)) return;
      const declared = Number(resp.headers()['content-length'] ?? '');
      if (Number.isFinite(declared) && declared > MAX_RESPONSE_BYTES) return;
      const body = await resp.text().catch(() => '');
      this.#keep(this.#responses, MAX_RESPONSES, body);
    } catch {
      // Best effort: capturing evidence never changes the run.
    }
  }

  /** keep adds one bounded, de-duplicated text to a stream, respecting the
   *  stream's count cap and the total byte budget. A body longer than the
   *  per-body cap is truncated rather than dropped, since a secret shape near
   *  its front is still worth catching. */
  #keep(stream: string[], limit: number, text: string): void {
    if (!text) return;
    if (stream.length >= limit) return;
    if (this.#evidenceBytes >= MAX_EVIDENCE_BYTES) return;
    const bounded = text.length > MAX_RESPONSE_BYTES ? text.slice(0, MAX_RESPONSE_BYTES) : text;
    if (stream.includes(bounded)) return;
    if (this.#evidenceBytes + bounded.length > MAX_EVIDENCE_BYTES) return;
    this.#evidenceBytes += bounded.length;
    stream.push(bounded);
  }

  /** reached is every same-origin request the run made, deduped by method and
   *  path, so the injection family can fuzz a POST or fetch route and not only a
   *  GET navigation. The path carries its query for the engine to reduce to
   *  parameter names; no value leaves in a route. */
  reached(): readonly { method: string; path: string }[] {
    return this.#reached;
  }

  /** page returns the adapter the login and workflow code drives. */
  page(): Page {
    const pw = this.#page;
    // Read through a closure rather than passed as a number, because it has to
    // be the count AT THE MOMENT IT IS ASKED, not the count when the press
    // started, which is always zero.
    const inFlight = () => this.#inFlight;
    // A private field is reachable from any function defined textually
    // inside the class, but only through a reference to the instance, and
    // the object literal below is not one. `self` is that reference.
    const self = this;
    return {
      async goto(url: string) {
        const response = await pw.goto(url, { waitUntil: 'domcontentloaded', timeout: 30_000 });
        // The status of the document itself, not of every request the page
        // goes on to make. This is what tells a health check answering
        // SELECT 1 apart from the page it fronts answering with a stack
        // trace: the response is discarded here on purpose in every version
        // of this file before this one, so nothing downstream could ever
        // tell a crashed page from an unreadable one.
        self.#lastStatus = response ? response.status() : undefined;
        await settled(pw);
      },
      async fill(field: RegExp, value: string) {
        // Three seconds to prefer the exact name, then ten for whichever
        // locator that settled on. The loose pattern matches everything the
        // exact one does, so the first wait buys precision rather than
        // reachability and does not need to be long.
        await (await locate(pw, field, 3_000)).fill(value, { timeout: 10_000 });
      },
      async check(field: RegExp) {
        // A checkbox and a radio are not filled, they are chosen, and `fill`
        // on either throws. Separated here rather than branched inside `fill`
        // because the caller already knows which it wants: a planner that
        // decided to tick an acknowledgment has not decided to type into it.
        await (await locate(pw, field, 3_000)).check({ timeout: 10_000 });
      },
      async has(field: RegExp, timeoutMs: number) {
        const half = Math.max(250, Math.floor(timeoutMs / 2));
        try {
          await (await locate(pw, field, half)).waitFor({ state: 'visible', timeout: half });
          return true;
        } catch {
          return false;
        }
      },
      async click(control: RegExp) {
        // noWaitAfter, because the wait after a press is settled and quiet
        // below, and Playwright's own wait for "scheduled navigations to
        // finish" is one that can never end. A link to a host the environment
        // will not let answer, Continue with GitHub inside a preview, starts a
        // navigation that never commits: the click had landed, the wait timed
        // out after ten seconds, and the throw ended a whole exploration as
        // blocked with nothing explored. The ten second timeout still bounds
        // finding and pressing the control, which is what it is for.
        const press = async () => {
          const button = pw.getByRole('button', { name: control });
          if (await button.count() > 0) {
            await button.first().click({ timeout: 10_000, noWaitAfter: true });
            return;
          }
          const link = pw.getByRole('link', { name: control });
          if (await link.count() > 0) {
            await link.first().click({ timeout: 10_000, noWaitAfter: true });
            return;
          }
          // Falls back to anything with that accessible name, which covers the
          // div somebody made into a button. Failing here rather than guessing
          // at a selector keeps the failure honest.
          await pw.getByText(control).first().click({ timeout: 10_000, noWaitAfter: true });
        };
        const from = pw.url();
        await press();
        // A navigation first, which is a new document and the one thing
        // `settled` still answers honestly, then the page's own rerender.
        await settled(pw);
        await quiet(pw, PRESS_MS, inFlight);
        // A navigation the press started and nobody ever answered. Left
        // pending, it held every later read of the page, and the snapshot
        // after the press waited on it forever. Going back to where the
        // press started supersedes it, and the request is named with the
        // others the page could not make, so the evidence says which host
        // never answered rather than the run going quiet.
        const stuck = self.#navigating;
        if (stuck) {
          self.#navigating = undefined;
          self.#failed.push(`${stuck.method()} ${stuck.url()}: never answered, so the press was abandoned and ${from} opened again`);
          await pw.goto(from, { waitUntil: 'domcontentloaded', timeout: 30_000 }).catch(() => {});
          await settled(pw);
        }
      },
      async waitForAny(patterns: readonly RegExp[], timeoutMs: number) {
        const deadline = Date.now() + timeoutMs;
        for (;;) {
          const text = await pw.locator('body').innerText().catch(() => '');
          const found = patterns.find((p) => p.test(text));
          if (found) return found;
          if (Date.now() >= deadline) return null;
          await pw.waitForTimeout(200);
        }
      },
      async text() {
        return pw.locator('body').innerText().catch(() => '');
      },
      url() {
        return pw.url();
      },
    };
  }

  /** snapshot describes the page in the terms a decision is made in. */
  async snapshot(): Promise<Snapshot> {
    const pw = this.#page;
    // Again here and not only after a navigation, because a press that starts
    // a client side transition changes the page without one.
    await settled(pw);
    const fields = await pw.evaluate(() => {
      // Read in the page rather than through many round trips, because a form
      // with thirty fields would otherwise cost thirty messages.
      const named = (el: Element): string => {
        const label = el.getAttribute('aria-label');
        if (label) return label;
        const id = el.getAttribute('id');
        if (id) {
          const forLabel = document.querySelector(`label[for="${CSS.escape(id)}"]`);
          if (forLabel?.textContent) return forLabel.textContent.trim();
        }
        const wrapping = el.closest('label');
        if (wrapping?.textContent) return wrapping.textContent.trim();
        return el.getAttribute('placeholder') ?? el.getAttribute('name') ?? '';
      };
      // A checkbox and a radio carry a `value` whether or not anybody chose
      // them: an unticked <input type=checkbox> reads "on" and an unchosen
      // radio reads whatever the markup gave it. So `filled: !!input.value`
      // was TRUE for every one of them, always, and a planner that skips
      // filled fields skipped every acknowledgment and every option group on
      // every page. That is the reason this repository's own careers form
      // could not be completed by its own agent: the required "I understand
      // there is no salary" box and the role radios all reported themselves
      // as already answered.
      //
      // A radio is answered when its GROUP has a chosen member rather than
      // when this particular option is the chosen one. Reported per option it
      // would send a planner to tick the second role after the first, which
      // in a radio group means changing its mind rather than making progress.
      const chosenInGroup = (input: HTMLInputElement): boolean => {
        const scope = input.form ?? document;
        if (!input.name) return input.checked;
        return Array.from(
          scope.querySelectorAll<HTMLInputElement>(
            `input[type="radio"][name="${CSS.escape(input.name)}"]`),
        ).some((option) => option.checked);
      };
      const answered = (input: HTMLInputElement): boolean => {
        if (input.type === 'checkbox') return input.checked;
        if (input.type === 'radio') return chosenInGroup(input);
        return !!input.value;
      };
      const out: {
        name: string; type: string; filled: boolean; required: boolean;
      }[] = [];
      for (const el of document.querySelectorAll('input, textarea, select')) {
        const input = el as HTMLInputElement;
        if (input.type === 'hidden' || input.disabled) continue;
        // A field the browser will not let anybody interact with is not a
        // field. The honeypot on this repository's own careers form is the
        // case: it is a labelled text input inside a `hidden` div, so it
        // reported itself as ordinary here, and a planner that filled it
        // would either time out against an invisible element or, worse,
        // succeed and be refused by the server as a bot.
        if (typeof input.checkVisibility === 'function' && !input.checkVisibility()) continue;
        const name = named(el);
        if (!name) continue;
        out.push({
          name,
          type: input.type || el.tagName.toLowerCase(),
          filled: answered(input),
          required: input.required,
        });
      }
      return out;
    }).catch(() => [] as {
      name: string; type: string; filled: boolean; required: boolean;
    }[]);

    const interactive = await pw.evaluate(() => {
      const out: string[] = [];
      let unnamed = 0;
      const add = (s: string | null | undefined) => {
        const name = (s ?? '').trim();
        if (!name) {
          // An icon button with no label, a link whose only child is an image
          // with no alt text. A screen reader announces nothing here and
          // neither planner can press it, so it is counted rather than
          // dropped: the count is the evidence for the finding.
          unnamed++;
          return;
        }
        if (name.length < 60 && !out.includes(name)) out.push(name);
      };
      for (const el of document.querySelectorAll(
        'button, a[href], [role="button"], [role="link"], input[type="submit"]')) {
        // Only what a person could press. A control that is not rendered, the
        // mobile menu button a desktop layout hides with display: none, was
        // offered here anyway, and pressing it could never work: the role
        // locators skip hidden elements, the text fallback cannot match a
        // name that lives in aria-label, and the click waited ten seconds and
        // ended the whole exploration as blocked. The console's own "Open the
        // menu" did exactly that to two of this repository's goals. The
        // submits list below already asked this question; the list the
        // planner chooses from did not.
        if (typeof (el as HTMLElement).checkVisibility === 'function'
          && !(el as HTMLElement).checkVisibility()) continue;
        add(el.getAttribute('aria-label') ?? el.getAttribute('title')
          ?? el.textContent ?? (el as HTMLInputElement).value);
      }
      // Which of those controls submits a form, by name.
      //
      // A deterministic planner knows the words that move a sign up or a
      // checkout forward, and it cannot know the words on every form anybody
      // writes. "Send application" is the button on this repository's own
      // careers form and it matches none of them, so the agent filled the
      // whole form in and then had nothing it was willing to press. The
      // document already knows which control submits; asking it is better
      // than adding another word to a list that can never be finished.
      const submits: string[] = [];
      for (const el of document.querySelectorAll(
        'button[type="submit"], input[type="submit"], form button:not([type])')) {
        const input = el as HTMLInputElement;
        if (input.disabled) continue;
        if (typeof input.checkVisibility === 'function' && !input.checkVisibility()) continue;
        const name = (el.getAttribute('aria-label') ?? el.getAttribute('title')
          ?? el.textContent ?? input.value ?? '').trim();
        if (name && name.length < 60 && !submits.includes(name)) submits.push(name);
      }
      return { controls: out, unnamed, submits };
    }).catch(() => ({ controls: [] as string[], unnamed: 0, submits: [] as string[] }));

    const text = await pw.locator('body').innerText().catch(() => '');
    // The rendered text of a page the run observed, kept for a leak family to
    // scan by shape, deduped and bounded like the response bodies.
    this.#keep(this.#dom, MAX_DOM, text);
    return {
      url: pw.url(),
      title: await pw.title().catch(() => ''),
      fields,
      controls: interactive.controls,
      submits: interactive.submits,
      unnamed: interactive.unnamed,
      text,
      status: this.#lastStatus,
    };
  }

  #closed: Evidence | undefined;

  /** close stops the browser and returns what it recorded.
   *
   * Idempotent, because teardown paths overlap: a caller that closes on
   * success and a finally that closes on the way out are both right, and the
   * second call must return what the first found rather than an empty record
   * gathered from a page that is already gone.
   */
  async close(name: string, options: { readonly interrupted?: boolean } = {}): Promise<Evidence> {
    if (this.#closed) return this.#closed;
    // The pump stops before the final screenshot so a live sample cannot race
    // the full page shot the evidence needs. Stopping is idempotent and safe
    // even when no pump was ever started.
    this.#pump?.stop();
    const evidence: { video?: string; trace?: string; screenshot?: string } = {};
    const safe = name.replace(/[^a-z0-9._-]/gi, '-');

    // An interrupted session is one a budget stopped mid navigation, and a
    // full page screenshot waits for that navigation to finish. Unbounded, it
    // held a workflow stopped at its two second budget open until the slow
    // page finally answered at eight, so the budget capped the verdict and not
    // the time. Bounded, the screenshot is skipped when it cannot be taken and
    // the trace, which is the thing to open, is still kept.
    const screenshot = `${this.#artifacts}/${safe}.png`;
    await this.#page.screenshot({
      path: screenshot, fullPage: true,
      ...(options.interrupted ? { timeout: INTERRUPTED_SCREENSHOT_MS } : {}),
    }).then(
      () => { evidence.screenshot = screenshot; },
      () => undefined,
    );

    const trace = `${this.#artifacts}/${safe}.trace.zip`;
    await this.#context.tracing.stop({ path: trace }).then(
      () => { evidence.trace = trace; },
      () => undefined,
    );

    const video = this.#page.video();
    await this.#context.close();
    if (video) {
      const path = await video.path().catch(() => undefined);
      if (path) evidence.video = path;
    }
    await this.#browser.close();

    this.#closed = {
      ...evidence,
      console: this.#console,
      failed: this.#failed,
      dom: this.#dom,
      responses: this.#responses,
    };
    return this.#closed;
  }
}
