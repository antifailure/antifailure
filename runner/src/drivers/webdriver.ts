// A small W3C WebDriver client, over fetch, for the native surfaces.
//
// Why this exists rather than a client library. Appium's own JavaScript client
// pulls a large dependency tree into a runner whose only production dependency
// today is Playwright, and it buys nothing: the WebDriver protocol is HTTP and
// JSON, the subset a surface driver needs is a dozen endpoints, and Node has
// had fetch built in since well before the version this package requires. So
// the Appium server stays an external tool, exactly as the engine binary is,
// and the runner's package.json does not grow.
//
// The error handling is the part worth reading. A WebDriver server answers a
// failure with a 4xx or 5xx AND a JSON body naming the error, and the two say
// different things: `no such element` is the application not showing what we
// looked for, while a connection refused is our own tooling being absent. A
// driver has to tell those apart to decide between failing a workflow and
// blocking it, so this module preserves the distinction instead of flattening
// everything into one thrown Error.

/** A WebDriver level failure that carries the server's own error code.
 *
 *  `code` is the W3C error name, "no such element" and "stale element
 *  reference" being the ones a driver acts on. `status` is the HTTP status.
 *  Both are preserved because a caller deciding whether a run is a failure or
 *  is blocked needs to know which kind of no it got. */
export class WebDriverError extends Error {
  readonly code: string;
  readonly status: number;
  constructor(code: string, message: string, status: number) {
    super(message);
    this.name = 'WebDriverError';
    this.code = code;
    this.status = status;
  }
}

/** Thrown when the server could not be reached at all.
 *
 *  Kept separate from WebDriverError on purpose: a refused connection means
 *  the Appium server is not running, which is the runner's own problem and
 *  must block a run rather than fail it. Reporting "the app is broken" because
 *  our own tool was not started is the exact failure this repository keeps
 *  finding in its instruments. */
export class WebDriverUnreachable extends Error {
  constructor(url: string, cause: unknown) {
    super(
      `could not reach the WebDriver server at ${url}: ` +
      `${cause instanceof Error ? cause.message : String(cause)}`,
    );
    this.name = 'WebDriverUnreachable';
  }
}

/** How an element is located. The strategies differ per platform, so each
 *  driver builds its own and this module stays generic. */
export interface Locator {
  readonly using: string;
  readonly value: string;
}

const DEFAULT_TIMEOUT_MS = 60_000;

export interface SessionOptions {
  /** Where the Appium server is listening. */
  readonly serverURL: string;
  /** How long any one request may take. Session creation is given its own,
   *  longer budget by the caller, because a first run builds WebDriverAgent. */
  readonly timeoutMs?: number;
}

/** WebDriverSession is one live automation session against one device. */
export class WebDriverSession {
  readonly #base: string;
  readonly #id: string;
  readonly #timeoutMs: number;

  private constructor(base: string, id: string, timeoutMs: number) {
    this.#base = base;
    this.#id = id;
    this.#timeoutMs = timeoutMs;
  }

  get id(): string { return this.#id; }

  /** create opens a session with the given capabilities.
   *
   *  `createTimeoutMs` is separate from the per request timeout and is
   *  deliberately generous: the first iOS session on a machine builds and
   *  installs WebDriverAgent with xcodebuild, which takes minutes, and a
   *  timeout there reads exactly like a broken driver. */
  static async create(
    options: SessionOptions,
    capabilities: Record<string, unknown>,
    createTimeoutMs: number,
  ): Promise<WebDriverSession> {
    const base = options.serverURL.replace(/\/+$/, '');
    const body = await request(
      base, 'POST', '/session',
      { capabilities: { alwaysMatch: capabilities, firstMatch: [{}] } },
      createTimeoutMs,
    );
    const id = (body as { sessionId?: string }).sessionId;
    if (typeof id !== 'string' || !id) {
      throw new WebDriverError(
        'session not created',
        `the server accepted the request but returned no session id: ${JSON.stringify(body)}`,
        200,
      );
    }
    return new WebDriverSession(base, id, options.timeoutMs ?? DEFAULT_TIMEOUT_MS);
  }

  async #call(method: string, path: string, body?: unknown): Promise<unknown> {
    return request(this.#base, method, `/session/${this.#id}${path}`, body, this.#timeoutMs);
  }

  /** source returns the page source. Appium renders the accessibility tree as
   *  XML for both the XCUITest and the UiAutomator2 driver. */
  async source(): Promise<string> {
    const value = await this.#call('GET', '/source');
    return typeof value === 'string' ? value : String(value);
  }

  /** findElement returns an element id, or undefined when there is no such
   *  element.
   *
   *  Undefined rather than a thrown error for the "not found" case only: an
   *  element that is not on the screen is an ordinary, expected answer that
   *  the caller decides about, while any other failure is still thrown. */
  async findElement(locator: Locator): Promise<string | undefined> {
    try {
      const value = await this.#call('POST', '/element', locator);
      const record = value as Record<string, string>;
      // The W3C element identifier is a fixed magic key. Older servers also
      // answered with ELEMENT, and Appium still does for some drivers, so both
      // are read rather than assuming the modern one.
      const id = record['element-6066-11e4-a52e-4f735466cecf'] ?? record['ELEMENT'];
      return typeof id === 'string' && id ? id : undefined;
    } catch (err) {
      if (err instanceof WebDriverError && err.code === 'no such element') return undefined;
      throw err;
    }
  }

  /** execute runs one of Appium's `mobile:` commands.
   *
   *  The W3C protocol has no verb for "restart this application", and both
   *  mobile drivers expose theirs through the execute-script endpoint. */
  async execute(script: string, args: readonly unknown[] = []): Promise<unknown> {
    return this.#call('POST', '/execute/sync', { script, args });
  }

  async click(elementId: string): Promise<void> {
    await this.#call('POST', `/element/${elementId}/click`);
  }

  async clear(elementId: string): Promise<void> {
    await this.#call('POST', `/element/${elementId}/clear`);
  }

  /** sendKeys types into an element. The W3C body is `text`; Appium also reads
   *  `value` as an array of characters, and sending both is what keeps this
   *  working across the two drivers without branching. */
  async sendKeys(elementId: string, text: string): Promise<void> {
    await this.#call('POST', `/element/${elementId}/value`, { text, value: [...text] });
  }

  /** screenshot returns the screen as base64 PNG, which is exactly the shape
   *  the live channel's frame event carries. */
  async screenshot(): Promise<string> {
    const value = await this.#call('GET', '/screenshot');
    return typeof value === 'string' ? value : '';
  }

  /** quit ends the session. Never throws: it runs in a finally, and a cleanup
   *  failure must not replace the verdict the run just produced with a
   *  teardown error. */
  async quit(): Promise<void> {
    try {
      await request(this.#base, 'DELETE', `/session/${this.#id}`, undefined, 15_000);
    } catch {
      // Deliberately swallowed. See the docstring.
    }
  }
}

/** ping answers whether an Appium server is listening and ready.
 *
 *  Used to tell "no server" apart from "the app misbehaved" BEFORE a run
 *  starts, so a missing tool is reported as a missing tool. */
export async function ping(serverURL: string, timeoutMs = 5_000): Promise<boolean> {
  try {
    const value = await request(
      serverURL.replace(/\/+$/, ''), 'GET', '/status', undefined, timeoutMs,
    );
    return (value as { ready?: boolean })?.ready !== false;
  } catch {
    return false;
  }
}

async function request(
  base: string, method: string, path: string, body: unknown, timeoutMs: number,
): Promise<unknown> {
  const url = `${base}${path}`;
  let response: Response;
  try {
    response = await fetch(url, {
      method,
      headers: { 'content-type': 'application/json' },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      signal: AbortSignal.timeout(timeoutMs),
    });
  } catch (err) {
    throw new WebDriverUnreachable(url, err);
  }

  const text = await response.text();
  let parsed: unknown;
  try {
    parsed = text ? JSON.parse(text) : {};
  } catch {
    // A body that is not JSON is a server that is not a WebDriver server, or
    // one that died mid response. Either way the text itself is the most
    // useful thing to report.
    throw new WebDriverError(
      'invalid response', `${method} ${path} answered ${response.status} with: ${text.slice(0, 500)}`,
      response.status,
    );
  }

  const value = (parsed as { value?: unknown }).value;
  if (!response.ok) {
    const failure = (value ?? {}) as { error?: string; message?: string };
    throw new WebDriverError(
      failure.error ?? 'unknown error',
      failure.message ?? `${method} ${path} answered ${response.status}`,
      response.status,
    );
  }
  // A 200 can still carry an error object: some Appium endpoints answer that
  // way, and treating it as success is how a failure becomes a green step.
  if (value && typeof value === 'object' && 'error' in value && 'message' in value) {
    const failure = value as { error: string; message: string };
    throw new WebDriverError(failure.error, failure.message, response.status);
  }
  return value;
}
