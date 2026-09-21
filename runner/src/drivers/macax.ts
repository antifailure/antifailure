// The native macOS surface: a real application, driven through AXUIElement.
//
// This is the half of the desktop driver that has no browser under it at all.
// It launches (or attaches to) a macOS application, reads its accessibility
// tree through runner/src/drivers/axhelper.swift, which it compiles once and
// caches, reduces that tree to the
// Snapshot the planner already consumes with runner/src/drivers/ax.ts, and
// acts on it with the platform's own AXPress and AXValue. Nothing here knows
// what a selector is, which is the whole point: the agent presses "Continue"
// because a screen reader announces "Continue", the same sentence that drives
// the browser.
//
// Accessibility on macOS is a TCC permission. A process that has not been
// granted it can list applications and can read nothing, and the two look
// identical from the outside: an empty tree. So trust is asked for once, up
// front, and a run that has not been allowed to look is BLOCKED with the exact
// step a person has to take, never reported as an application with no controls.

import { execFile, spawn, type ChildProcess } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync, renameSync, rmSync, existsSync } from 'node:fs';
import { homedir, tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { locate, snapshotFrom, chosen, type AxNode } from './ax.ts';
import type { Snapshot } from '../workflow.ts';
import type { AxSurface } from './surface.ts';

const HELPER_SOURCE = join(dirname(fileURLToPath(import.meta.url)), 'axhelper.swift');

/** How long one helper call may take before it is the runner's own failure. */
const HELPER_TIMEOUT_MS = 15_000;

/** How long the helper may take to compile. Measured on this machine at ten
 *  seconds for the first build of a session and under three after, because the
 *  module cache is cold exactly once. Sixty is generous and still finite. */
const COMPILE_TIMEOUT_MS = 60_000;

/** AxError is a failure of the reader rather than of the application. It is
 *  raised so the driver can charge it to the runner, which is what keeps a
 *  missing permission from reading as a broken application. */
export class AxError extends Error {
  /** blocked marks the failures the environment owes: no accessibility grant,
   *  no window yet, no such application. */
  readonly blocked: boolean;
  /** locked is set when the reader's refusal was a LOCKED SCREEN rather than
   *  anything about the application.
   *
   *  Carried on the error rather than left behind in the JSON, because the
   *  caller that needs it is the launch poll, and the poll only ever sees the
   *  thrown error. The first version of this dropped the flag at the throw and
   *  then branched on it in the poll, which is a branch that could never be
   *  taken: a locked screen would have been waited out for the whole launch
   *  budget and then reported as a slow application. */
  readonly locked: boolean;
  constructor(message: string, blocked = true, locked = false) {
    super(message);
    this.name = 'AxError';
    this.blocked = blocked;
    this.locked = locked;
  }
}

interface HelperOk { readonly ok: true; readonly [key: string]: unknown }
interface HelperNo {
  readonly ok: false; readonly error: string;
  readonly noWindow?: boolean; readonly locked?: boolean;
}

/** Where the compiled helper is cached, keyed by the source it was built from.
 *
 * Keyed by a hash of the source, so editing axhelper.swift rebuilds it and an
 * older build is never picked up: a stale binary answering a probe is a trap
 * this repository has been caught by before. In the user's cache directory
 * rather than in /tmp, which on this machine is swept nightly, and rather than
 * next to the source, which is read only inside a container image. */
function helperPath(source: string): string {
  const key = createHash('sha256').update(source).digest('hex').slice(0, 16);
  const home = homedir();
  const base = home && home !== '/'
    ? join(home, 'Library', 'Caches', 'antifailure')
    : join(tmpdir(), 'antifailure');
  return join(base, `axhelper-${key}`);
}

let compiled: Promise<string> | undefined;

/** helper compiles the accessibility reader if it is not already built, and
 *  returns the path to the binary.
 *
 * Compiled once per source revision per machine, and cached, so the cost lands
 * on the first run and never again. Built to a temporary name and renamed into
 * place, because two runs starting at once would otherwise each write the same
 * file while the other executed it; rename is atomic, so the loser's work is
 * merely wasted rather than the winner's run being handed half a binary.
 */
async function helper(): Promise<string> {
  compiled ??= (async () => {
    let source: string;
    try {
      source = readFileSync(HELPER_SOURCE, 'utf8');
    } catch (err) {
      throw new AxError(
        `The accessibility reader's source is missing at ${HELPER_SOURCE}: ` +
        `${err instanceof Error ? err.message : String(err)}`,
      );
    }
    const binary = helperPath(source);
    if (existsSync(binary)) return binary;
    mkdirSync(dirname(binary), { recursive: true });
    const staging = `${binary}.${process.pid}.building`;
    try {
      await new Promise<void>((resolve, reject) => {
        execFile(
          'swiftc',
          ['-O', '-o', staging, HELPER_SOURCE],
          { timeout: COMPILE_TIMEOUT_MS },
          (err, _out, stderr) => {
            if (err) {
              reject(new AxError(
                `The accessibility reader could not be compiled. It is Swift, and it needs the ` +
                `compiler that ships with the Xcode command line tools: run ` +
                `\`xcode-select --install\` if swiftc is missing. ${stderr.trim() || err.message}`,
              ));
              return;
            }
            resolve();
          },
        );
      });
      renameSync(staging, binary);
    } finally {
      rmSync(staging, { force: true });
    }
    return binary;
  })();
  try {
    return await compiled;
  } catch (err) {
    // A failed build is not cached: a machine that grows a compiler between
    // two runs should not keep being told it has none.
    compiled = undefined;
    throw err;
  }
}

/** ask runs one helper request and returns its answer.
 *
 * osascript is spawned per request rather than held open, because the
 * automation host has no request loop and a held process would be a second
 * protocol to get wrong. The cost is the host's own startup, which is the
 * entire two seconds a snapshot takes, and it buys a reader that cannot wedge.
 */
async function ask(request: Record<string, unknown>): Promise<HelperOk> {
  const binary = await helper();
  const raw = await new Promise<string>((resolve, reject) => {
    execFile(
      binary,
      [JSON.stringify(request)],
      { timeout: HELPER_TIMEOUT_MS, maxBuffer: 32 * 1024 * 1024 },
      (err, stdout, stderr) => {
        if (err) {
          reject(new AxError(
            `The accessibility reader could not run: ${stderr.trim() || err.message}`,
          ));
          return;
        }
        resolve(stdout);
      },
    );
  });
  let parsed: HelperOk | HelperNo;
  try {
    parsed = JSON.parse(raw.trim()) as HelperOk | HelperNo;
  } catch {
    throw new AxError(`The accessibility reader answered something that is not JSON: ${raw.slice(0, 200)}`);
  }
  if (!parsed.ok) throw new AxError(parsed.error, true, parsed.locked === true);
  return parsed;
}

/** ask, but returning the refusal instead of throwing it, for the two callers
 *  that have to read a `locked` or `noWindow` flag off a failure rather than
 *  simply give up on it. */
async function tryAsk(
  request: Record<string, unknown>,
): Promise<HelperOk | (HelperNo & { readonly ok: false })> {
  try {
    return await ask(request);
  } catch (err) {
    return {
      ok: false,
      error: err instanceof Error ? err.message : String(err),
      locked: err instanceof AxError && err.locked,
    };
  }
}

/** trusted answers whether this process may read another application's tree.
 *
 * Asked rather than assumed, and reported rather than worked around. There is
 * no way to grant accessibility from a program: a person opens System Settings,
 * Privacy and Security, Accessibility, and ticks the application that runs this
 * runner. Anything else this function could do would be a guess dressed as a
 * capability. */
export async function trusted(): Promise<boolean> {
  const answer = await ask({ op: 'trust' });
  return answer['trusted'] === true;
}

/** screenIsLocked reports whether this GUI session is locked.
 *
 * Asked separately from `trusted`, because the permission survives a lock and
 * the answers do not: a locked Mac grants accessibility and returns nothing
 * through it, which is indistinguishable from an application that renders
 * nothing unless somebody asks this question. */
export async function screenIsLocked(): Promise<boolean> {
  const answer = await ask({ op: 'trust' });
  return answer['locked'] === true;
}

/** THE OTHER HUMAN STEP, and the one nobody expects. A locked screen has every
 *  permission and no answers. */
export const LOCKED_SCREEN =
  'The screen is locked, and macOS withholds every application\'s accessibility tree while it ' +
  'is. This is not an application with nothing on it; it is a screen nothing is allowed to ' +
  'read. Unlock the Mac, or run the desktop surface on a host whose session stays unlocked.';

/** THE ONE HUMAN STEP. Quoted verbatim by the driver when trust is missing, so
 *  the person reading a blocked run is told what to click rather than told that
 *  the desktop surface did not work. */
export const GRANT_INSTRUCTION =
  'This process has not been granted macOS Accessibility. Open System Settings, ' +
  'Privacy and Security, Accessibility, and turn it on for the application that runs ' +
  'the runner (the terminal, or the CI agent). Nothing in software can grant it: it is ' +
  'a permission a person gives, and a run without it has not failed, it has not been ' +
  'allowed to look.';

interface RunningApp {
  readonly name: string;
  readonly pid: number;
  readonly bundleId: string;
  readonly active: boolean;
}

/** apps lists the applications with an interface to drive.
 *
 *  Module private, because findPid below is its only caller. Exported it would
 *  be public surface with nothing on the other end of it, which is the shape
 *  of a half finished feature rather than of a finished one. */
async function apps(): Promise<readonly RunningApp[]> {
  const answer = await ask({ op: 'apps' });
  return (answer['apps'] ?? []) as readonly RunningApp[];
}

/** What the native surface needs to reach an application. */
export interface MacTarget {
  /** bundlePath launches a copy of the application, "/Applications/Notes.app".
   *  Absent attaches to one that is already running.
   *
   *  A path given while an application of that name is ALREADY running is
   *  refused rather than attached to, because `open -a` would activate the
   *  running copy and the run would then be about a screen it did not create.
   *  Attaching on purpose is what leaving this out means, which is why the
   *  refusal costs nobody the ability to do it. */
  readonly bundlePath?: string;
  /** name is the application's name as macOS reports it, used to find the
   *  process this surface drives. Required whether launching or attaching,
   *  because a launch returns when `open` returns and not when the application
   *  is ready, so the process still has to be found by name. */
  readonly name: string;
  /** maxNodes caps the tree walk. A large window can carry tens of thousands
   *  of elements and a planner cannot read one. */
  readonly maxNodes?: number;
  /** readyTimeoutMs is how long to wait for the application to draw a window.
   *  A launch that never draws one is blocked with that said. */
  readonly readyTimeoutMs?: number;
}

const DEFAULT_READY_MS = 20_000;

async function findPid(name: string): Promise<number | undefined> {
  return (await apps()).find((a) => a.name === name)?.pid;
}

/** openMac launches or attaches to a macOS application and returns a surface.
 *
 * The trust check is first and is fatal, because every later symptom of a
 * missing grant is indistinguishable from an application that renders nothing.
 */
export async function openMac(target: MacTarget): Promise<AxSurface> {
  if (process.platform !== 'darwin') {
    throw new AxError(
      `The native desktop surface drives macOS applications through AXUIElement, and this ` +
      `is ${process.platform}. Run it on a macOS host.`,
    );
  }
  const state = await ask({ op: 'trust' });
  if (state['trusted'] !== true) throw new AxError(GRANT_INSTRUCTION);
  // The lock is checked BEFORE anything is launched, because every later
  // symptom of it is an application that appears to render nothing, and
  // because launching an application onto a locked screen is a mess nobody
  // asked for.
  if (state['locked'] === true) throw new AxError(LOCKED_SCREEN, true, true);

  let launched: ChildProcess | undefined;
  if (target.bundlePath) {
    // A RUN MAY NOT INHERIT AN APPLICATION IT DID NOT START, and this is the
    // one refusal in this file that is about the product rather than about
    // macOS.
    //
    // `open -a` ACTIVATES an application that is already running instead of
    // launching a fresh one. So without this check, a run that names a bundle
    // drives whatever the last run, or the person at the keyboard, left on
    // screen. That is not a rehearsal: the verdict is about a state nobody in
    // this run created.
    //
    // It is a FALSE PASS FACTORY rather than an inconvenience, and it was
    // caught in the act. A drive came back green whose own step list showed it
    // never filled the email, against a fixture that refuses an empty email,
    // because an instance left over from an earlier run had that field filled
    // already. The first customer with their application already open would
    // get a pass that means nothing, and the second would get a failure they
    // could not reproduce, and neither would suspect the launcher.
    //
    // REFUSED RATHER THAN TERMINATED, and the asymmetry decides it before any
    // argument about correctness. A bundle path can name Slack, Mail, or the
    // customer's own editor, and an agent that quits one of those has an
    // unbounded blast radius that belongs to somebody else. Refusing costs one
    // blocked run and a sentence saying what to do.
    //
    // This is also what makes MacTarget's own documentation true. It says a
    // bundlePath LAUNCHES a copy and that attaching is what happens when one
    // is absent; until this line the code did neither, and a caller who wanted
    // to attach deliberately still can by leaving the path out.
    const already = await findPid(target.name);
    if (already !== undefined) {
      throw new AxError(
        `${target.name} is already running, and this run did not start it. Attaching to it ` +
        `would drive whatever is on its screen now, so the verdict would be about a state ` +
        `this run never created: a workflow can pass because an earlier one left the form ` +
        `filled in. Quit ${target.name} and run again. If driving the copy that is already ` +
        `open is what you meant, name it without an application path, which is how this ` +
        `driver attaches on purpose.`,
      );
    }
    // `open` rather than exec'ing the binary, so the application is launched
    // the way the platform launches one: a real process with a dock entry, an
    // activation policy and a window server connection. A binary started
    // directly from the bundle gets none of those and draws nothing.
    launched = spawn('open', ['-a', target.bundlePath], { stdio: 'ignore' });
  }

  const deadline = Date.now() + (target.readyTimeoutMs ?? DEFAULT_READY_MS);
  let pid = await findPid(target.name);
  while (pid === undefined && Date.now() < deadline) {
    await sleep(250);
    pid = await findPid(target.name);
  }
  if (pid === undefined) {
    throw new AxError(
      `No running application is called ${JSON.stringify(target.name)}` +
      (target.bundlePath ? `, ${target.readyTimeoutMs ?? DEFAULT_READY_MS} ms after launching ${target.bundlePath}.` : '.'),
    );
  }
  const found = pid;

  // Brought to the front, and this is not cosmetic. Several applications draw
  // no window at all until they are activated, so a driver that launched one
  // and then waited politely in the background waited forever: TextEdit does
  // exactly this, and a cold launch of it reported no window for a full thirty
  // seconds while the application was running perfectly well. Real keyboard
  // and mouse input also goes to whatever is frontmost, so an application
  // being driven has to BE frontmost.
  await tryAsk({ op: 'activate', pid: found });

  // Then wait for a window. An application that has started and not yet drawn
  // is the commonest launch race there is, and an empty snapshot taken during
  // it would be reported as an application with no controls.
  let lastError = '';
  let ready = false;
  while (Date.now() < deadline) {
    const probe = await tryAsk({ op: 'snapshot', pid: found, maxNodes: 1, maxDepth: 1 });
    if (probe.ok) { ready = true; break; }
    lastError = probe.error;
    await sleep(250);
  }
  // The reader's own last sentence is quoted rather than summarised, which is
  // what makes a second lock check here unnecessary: a screen that locked
  // after the check above says so in that sentence, so the information
  // survives without a branch that no test could ever reach. A guard nobody
  // can aim a failing case at is scaffolding however reasonable it sounds.
  if (!ready) throw new AxError(`${target.name} never opened a window: ${lastError}`);

  return macSurface(found, target, launched);
}

function macSurface(pid: number, target: MacTarget, launched?: ChildProcess): AxSurface {
  let tree: AxNode | undefined;
  let where = { url: `macos://${target.name}`, title: target.name };

  const read = async (): Promise<AxNode> => {
    const answer = await ask({
      op: 'snapshot', pid, maxNodes: target.maxNodes ?? 1500, maxDepth: 40,
    });
    const window = String(answer['window'] ?? '');
    where = {
      url: `macos://${target.name}${window ? '/' + window : ''}`,
      title: window || target.name,
    };
    tree = answer['tree'] as AxNode;
    return tree;
  };

  /** act resolves a name against the tree that was last READ rather than a
   *  fresh one, because the `ref` the planner is about to act on came from
   *  that tree and an index path is only valid against the tree that produced
   *  it. A window that changed underneath is caught by the helper, which
   *  answers that the element is gone rather than pressing whatever moved into
   *  its place. */
  const resolve = async (pattern: RegExp, kind: 'field' | 'control'): Promise<AxNode> => {
    const current = tree ?? await read();
    const node = locate(current, pattern, kind);
    if (!node) {
      throw new AxError(
        `Nothing on this screen is a ${kind} a screen reader announces as ` +
        `${pattern.source.replace(/[\^$]/g, '')}.`,
        false,
      );
    }
    if (node.ref === undefined) {
      throw new AxError(`The element ${node.name} carries no handle to act on.`);
    }
    return node;
  };

  return {
    async snapshot(): Promise<Snapshot> {
      return snapshotFrom(await read(), where);
    },
    async fill(field: RegExp, value: string): Promise<void> {
      const node = await resolve(field, 'field');
      await ask({ op: 'act', pid, ref: node.ref, action: 'setValue', value });
    },
    async check(field: RegExp): Promise<void> {
      const node = await resolve(field, 'field');
      // Pressing a ticked checkbox unticks it. A check whose box is already
      // ticked is therefore not a no-op that is merely wasteful, it undoes the
      // answer, and the planner would then see it unfilled and press it again
      // forever.
      if (chosen(node) && node.checked === true) return;
      await ask({ op: 'act', pid, ref: node.ref, action: 'press' });
    },
    async click(control: RegExp): Promise<void> {
      const node = await resolve(control, 'control');
      await ask({ op: 'act', pid, ref: node.ref, action: 'press' });
    },
    async close(): Promise<void> {
      // Only an application this surface launched is closed. Attaching to
      // something a person already had open and then quitting it would take
      // their work with it.
      if (!target.bundlePath) return;
      launched?.kill();
      await new Promise<void>((resolve) => {
        execFile('kill', [String(pid)], () => resolve());
      });
    },
  };
}

/** sleep waits, and deliberately HOLDS THE PROCESS OPEN while it does.
 *
 * The timer is not unref'd, and that is the whole point of this comment,
 * because unref'ing it is the obvious thing to write and it is wrong here. The
 * only caller is the launch poll above, and while that poll is sleeping there
 * is nothing else pending: no socket, no child process, no other timer. An
 * unref'd timer does not count as work, so the event loop empties, node exits
 * mid await, and the run ends with "detected unsettled top-level await"
 * instead of with an application.
 *
 * It hides, too. An application that was ALREADY running is found on the first
 * try and never sleeps at all, so this only bites on the cold launch, which is
 * the case that matters and the case a developer with the application open
 * never sees. It cost this driver its first green run against a real native
 * application. The poll is bounded by readyTimeoutMs, so holding the loop open
 * cannot outlive the wait.
 */
function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => { setTimeout(resolve, ms); });
}
