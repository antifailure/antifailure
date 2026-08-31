// The browser is driven through the accessibility tree, not through selectors.
//
// A workflow that says "press Continue" should keep working when somebody
// renames a CSS class and should stop working when somebody removes the label
// that a screen reader depends on. That is the right way round, and it is the
// only way to write a workflow as a sentence rather than as a script.

import { chromium, type Browser, type BrowserContext, type Page as PWPage } from 'playwright';
import type { Page } from './login.ts';
import type { Snapshot } from './workflow.ts';

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
}

/** Session is one browser, one context, one page, and its evidence. */
export class Session {
  readonly #browser: Browser;
  readonly #context: BrowserContext;
  readonly #page: PWPage;
  readonly #console: string[] = [];
  readonly #failed: string[] = [];
  readonly #artifacts: string;

  private constructor(browser: Browser, context: BrowserContext, page: PWPage, artifacts: string) {
    this.#browser = browser;
    this.#context = context;
    this.#page = page;
    this.#artifacts = artifacts;
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
  }): Promise<Session> {
    const browser = await chromium.launch({ headless: options.headless ?? true });
    const context = await browser.newContext({
      recordVideo: { dir: options.artifacts },
      viewport: options.viewport ?? { width: 1280, height: 800 },
      // A preview environment serves its own certificate for any host the
      // policy inspects, and the browser is inside that environment.
      ignoreHTTPSErrors: true,
    });
    await context.tracing.start({ screenshots: true, snapshots: true, sources: false });

    const page = await context.newPage();
    const session = new Session(browser, context, page, options.artifacts);

    page.on('console', (m) => {
      if (m.type() === 'error' || m.type() === 'warning') {
        session.#console.push(`${m.type()}: ${m.text()}`);
      }
    });
    page.on('requestfailed', (r) => {
      // The egress policy is the usual cause, and naming the request is the
      // difference between a mystery and a one line fix.
      session.#failed.push(`${r.method()} ${r.url()}: ${r.failure()?.errorText ?? 'failed'}`);
    });
    return session;
  }

  /** page returns the adapter the login and workflow code drives. */
  page(): Page {
    const pw = this.#page;
    return {
      async goto(url: string) {
        await pw.goto(url, { waitUntil: 'domcontentloaded', timeout: 30_000 });
      },
      async fill(field: RegExp, value: string) {
        await pw.getByLabel(field).first().fill(value, { timeout: 10_000 });
      },
      async click(control: RegExp) {
        const button = pw.getByRole('button', { name: control });
        if (await button.count() > 0) {
          await button.first().click({ timeout: 10_000 });
          return;
        }
        const link = pw.getByRole('link', { name: control });
        if (await link.count() > 0) {
          await link.first().click({ timeout: 10_000 });
          return;
        }
        // Falls back to anything with that accessible name, which covers the
        // div somebody made into a button. Failing here rather than guessing
        // at a selector keeps the failure honest.
        await pw.getByText(control).first().click({ timeout: 10_000 });
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
      const out: { name: string; type: string; filled: boolean }[] = [];
      for (const el of document.querySelectorAll('input, textarea, select')) {
        const input = el as HTMLInputElement;
        if (input.type === 'hidden' || input.disabled) continue;
        const name = named(el);
        if (!name) continue;
        out.push({ name, type: input.type || el.tagName.toLowerCase(), filled: !!input.value });
      }
      return out;
    }).catch(() => [] as { name: string; type: string; filled: boolean }[]);

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
        add(el.getAttribute('aria-label') ?? el.getAttribute('title')
          ?? el.textContent ?? (el as HTMLInputElement).value);
      }
      return { controls: out, unnamed };
    }).catch(() => ({ controls: [] as string[], unnamed: 0 }));

    return {
      url: pw.url(),
      title: await pw.title().catch(() => ''),
      fields,
      controls: interactive.controls,
      unnamed: interactive.unnamed,
      text: await pw.locator('body').innerText().catch(() => ''),
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
  async close(name: string): Promise<Evidence> {
    if (this.#closed) return this.#closed;
    const evidence: { video?: string; trace?: string; screenshot?: string } = {};
    const safe = name.replace(/[^a-z0-9._-]/gi, '-');

    const screenshot = `${this.#artifacts}/${safe}.png`;
    await this.#page.screenshot({ path: screenshot, fullPage: true }).then(
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

    this.#closed = { ...evidence, console: this.#console, failed: this.#failed };
    return this.#closed;
  }
}
