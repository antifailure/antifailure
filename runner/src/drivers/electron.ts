// The Electron surface: a real desktop application, driven through Chromium's
// accessibility tree.
//
// Electron is where most of the desktop applications a team would want
// rehearsed actually live: VS Code, Slack, Discord, Claude's own desktop app.
// Underneath one is Chromium, so it publishes the same accessibility tree a
// web page does, and Playwright can launch and drive it. That makes this the
// highest coverage path to the desktop surface by a wide margin, and it needs
// no permission a person has to grant.
//
// It is still driven as an ACCESSIBILITY TREE and not as a page. The snapshot
// comes from Chrome DevTools Protocol's Accessibility.getFullAXTree, which is
// the tree a screen reader reads, and is reduced to a Snapshot by the same
// runner/src/drivers/ax.ts the native macOS surface uses. Actions resolve by
// role and accessible name through Playwright's role engine. No selector, no
// class name, no test id appears anywhere in this file, which is what makes a
// workflow written for the web port to an Electron app unchanged.

import { _electron as electron, type ElectronApplication, type Page } from 'playwright';
import {
  locate, normalizeRole, snapshotFrom, chosen, isChosenRole, type AxNode,
} from './ax.ts';
import type { Snapshot } from '../workflow.ts';
import type { AxSurface } from './surface.ts';

/** What one Electron application needs to be launched. */
export interface ElectronTarget {
  /** executablePath is the Electron binary. A packaged application's is
   *  inside its bundle, "/Applications/Slack.app/Contents/MacOS/Slack"; a
   *  project under development uses the electron from its own node_modules. */
  readonly executablePath: string;
  /** args are what the binary is given. A project under development is
   *  launched by passing the directory holding its package.json. */
  readonly args?: readonly string[];
  readonly cwd?: string;
  readonly env?: Readonly<Record<string, string>>;
  /** timeoutMs bounds one action against the application. */
  readonly timeoutMs?: number;
}

const DEFAULT_TIMEOUT_MS = 15_000;

/** ElectronError is a failure of the launch or of the reader rather than of
 *  the application under test, so a driver can charge it to the runner. */
export class ElectronError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ElectronError';
  }
}

/** One node of Chrome DevTools Protocol's accessibility tree, in the shape the
 *  protocol actually sends. Declared here rather than imported because
 *  Playwright does not type a raw CDP response. */
interface CdpAxNode {
  readonly nodeId: string;
  readonly ignored?: boolean;
  readonly role?: { readonly value?: unknown };
  readonly name?: { readonly value?: unknown };
  readonly value?: { readonly value?: unknown };
  readonly childIds?: readonly string[];
  readonly properties?: readonly {
    readonly name: string; readonly value?: { readonly value?: unknown };
  }[];
}

function text(field: { readonly value?: unknown } | undefined): string {
  const raw = field?.value;
  return typeof raw === 'string' ? raw : raw === undefined || raw === null ? '' : String(raw);
}

/** property reads one accessibility property by name.
 *
 * The values arrive as STRINGS, not as the JSON types their names suggest:
 * `checked` is "true", "false" or "mixed", and `required` is "true". Reading
 * `value.value === true` is therefore false for a ticked checkbox, which is
 * how a first version of this reader reported every acknowledgment on every
 * screen as unticked and sent the planner to tick them forever. */
function property(node: CdpAxNode, name: string): string | undefined {
  const found = node.properties?.find((p) => p.name === name);
  if (!found) return undefined;
  const raw = found.value?.value;
  return raw === undefined || raw === null ? undefined : String(raw);
}

/** treeFrom assembles the protocol's flat node list into the tree ax.ts reads.
 *
 * Nodes the tree marks `ignored` are kept as pass-through containers rather
 * than dropped, because dropping one severs everything below it: a form
 * wrapped in a presentational div is entirely inside an ignored node, and a
 * reader that removed it would report an empty screen. They carry role "none",
 * which is a field role, a control role and a text role in neither direction,
 * so they contribute nothing to the snapshot but their children.
 */
export function treeFrom(nodes: readonly CdpAxNode[], submitNames: ReadonlySet<string>): AxNode {
  const byId = new Map<string, CdpAxNode>();
  for (const node of nodes) byId.set(node.nodeId, node);
  const seen = new Set<string>();

  const build = (id: string): AxNode | undefined => {
    // A cycle in the tree would be a protocol bug rather than a page, and an
    // unguarded walk of one never returns. Guarded, the second visit is
    // dropped and the snapshot is merely incomplete.
    if (seen.has(id)) return undefined;
    seen.add(id);
    const node = byId.get(id);
    if (!node) return undefined;
    const name = text(node.name);
    const out: {
      role: string; name: string; ref: string;
      value?: string; enabled?: boolean; focused?: boolean;
      required?: boolean; checked?: boolean; isDefault?: boolean;
      busy?: boolean; children?: AxNode[];
    } = { role: text(node.role) || 'none', name, ref: id };

    const value = text(node.value);
    if (value) out.value = value;
    if (property(node, 'disabled') === 'true') out.enabled = false;
    if (property(node, 'focused') === 'true') out.focused = true;
    // aria-busy, which Chromium publishes as the property `busy`, and NOT in
    // the shape of its neighbours. MEASURED against a real Electron window: a
    // region carrying aria-busy="true" arrives as {type: "boolean", value: 1},
    // a number, where `checked` and `required` arrive as the strings above. So
    // "true" and "1" are both read as busy, and reading only the string that
    // every other property uses would have made this signal silently dead. An
    // ignored node's is not read either: a skeleton an application hid from
    // screen readers is not the application saying it is working, and the
    // same probe showed Chromium drops the property from such a node anyway.
    const busy = property(node, 'busy');
    if (!node.ignored && (busy === 'true' || busy === '1')) out.busy = true;
    const checked = property(node, 'checked');
    if (checked !== undefined) out.checked = checked === 'true';

    // Required, and the one place Chromium does not say so.
    //
    // MEASURED, not assumed. Against a form carrying `required` on two text
    // inputs and on a checkbox, the tree publishes required=true on both text
    // inputs and publishes NOTHING of the kind on the checkbox. What it
    // publishes there instead is invalid="true" while the box is unticked,
    // flipping to invalid="false" the moment it is ticked. That is the same
    // fact wearing the other name: a control the form will not accept
    // unanswered.
    //
    // It matters because the planner ticks required acknowledgments and leaves
    // optional ones alone, deliberately, so that an agent does not subscribe
    // somebody to a newsletter to see what happens. Read literally, every
    // mandatory "I accept the terms" on every Electron screen is optional, and
    // an agent fills the form, presses the button, and is refused by a
    // checkbox it was never willing to tick. That is exactly what the first
    // run of this driver against its own fixture did.
    //
    // Scoped to the roles whose state is chosen rather than typed, because
    // those are the only ones Chromium leaves out. On a text field `invalid`
    // means the value that was typed is wrong, which is a different fact, and
    // `required` is published there anyway.
    if (property(node, 'required') === 'true') out.required = true;
    else if (isChosenRole(out.role) && property(node, 'invalid') === 'true') out.required = true;
    if (name && submitNames.has(name.trim())) out.isDefault = true;

    const children: AxNode[] = [];
    for (const childId of node.childIds ?? []) {
      const child = build(childId);
      if (child) children.push(child);
    }
    if (children.length > 0) out.children = children;
    return out;
  };

  const root = nodes[0] ? build(nodes[0].nodeId) : undefined;
  return root ?? { role: 'none', name: '', ref: '' };
}

/** The ARIA role Playwright's role engine is asked for, per normalized role.
 *
 * Chromium's accessibility roles are already the ARIA ones for everything a
 * workflow acts on, so this map is short: it exists for the few the engine
 * spells differently, and for the macOS spellings that reach here when an
 * Electron application is read through the native surface instead. A role
 * with no entry is passed through, which is right for every ARIA role. */
const ARIA_ROLE: Readonly<Record<string, string>> = {
  popupbutton: 'combobox',
  menubaritem: 'menuitem',
  incrementor: 'spinbutton',
  datepicker: 'textbox',
};

function ariaRoleFor(role: string): string {
  const normal = normalizeRole(role);
  return ARIA_ROLE[normal] ?? normal;
}

/** openElectron launches an Electron application and returns a surface. */
export async function openElectron(target: ElectronTarget): Promise<AxSurface> {
  let app: ElectronApplication;
  try {
    app = await electron.launch({
      executablePath: target.executablePath,
      args: [...(target.args ?? [])],
      ...(target.cwd ? { cwd: target.cwd } : {}),
      ...(target.env ? { env: { ...target.env } } : {}),
      timeout: target.timeoutMs ?? DEFAULT_TIMEOUT_MS,
    });
  } catch (err) {
    throw new ElectronError(
      `The Electron application at ${target.executablePath} did not start: ` +
      `${err instanceof Error ? err.message : String(err)}`,
    );
  }

  let page: Page;
  try {
    page = await app.firstWindow({ timeout: target.timeoutMs ?? DEFAULT_TIMEOUT_MS });
    await page.waitForLoadState('domcontentloaded');
  } catch (err) {
    await app.close().catch(() => undefined);
    throw new ElectronError(
      `The Electron application started and never opened a window: ` +
      `${err instanceof Error ? err.message : String(err)}`,
    );
  }

  const cdp = await page.context().newCDPSession(page);
  await cdp.send('Accessibility.enable');
  const timeout = target.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  let tree: AxNode | undefined;

  /** submitNames asks the document which controls send a form.
   *
   * The accessibility tree does not carry the idea of a submit control: a
   * button that sends a form and a button that opens a menu are both role
   * button with a name. macOS publishes its window's default button and
   * Chromium publishes no equivalent, so the one place that knows is the
   * document, and asking it is exactly what runner/src/browser.ts does for
   * the same reason: a word list of the buttons that move a form can never be
   * finished, and this repository's own careers form ("Send application")
   * is the proof.
   *
   * Names only, correlated back onto the tree by accessible name, because a
   * protocol node id and a DOM node are not the same handle. Two controls
   * sharing a name would both be marked, which costs a planner one extra
   * press and never a wrong one.
   */
  const submitNames = async (): Promise<ReadonlySet<string>> => {
    const names = await page.evaluate(() => {
      const out: string[] = [];
      for (const el of document.querySelectorAll(
        'button[type="submit"], input[type="submit"], form button:not([type])')) {
        const input = el as HTMLInputElement;
        if (input.disabled) continue;
        if (typeof input.checkVisibility === 'function' && !input.checkVisibility()) continue;
        const name = (el.getAttribute('aria-label') ?? el.getAttribute('title')
          ?? el.textContent ?? input.value ?? '').trim();
        if (name && !out.includes(name)) out.push(name);
      }
      return out;
    }).catch(() => [] as string[]);
    return new Set(names);
  };

  const read = async (): Promise<AxNode> => {
    const response = await cdp.send('Accessibility.getFullAXTree') as unknown as {
      readonly nodes: readonly CdpAxNode[];
    };
    tree = treeFrom(response.nodes ?? [], await submitNames());
    return tree;
  };

  const where = async () => ({
    url: `electron://${await page.title().catch(() => '') || 'window'}`,
    title: await page.title().catch(() => ''),
  });

  /** act resolves a name against the tree and drives Playwright's role engine.
   *
   * Resolved against the tree FIRST rather than handed straight to the role
   * engine, so a name that is not on the screen is a sentence about what the
   * screen offers instead of a ten second locator timeout, and so the node's
   * own role decides which engine query is made. `.first()` because two
   * controls may honestly share a name and a strict-mode error about that is
   * a runner failure reported as an application one.
   */
  const act = async (
    pattern: RegExp, kind: 'field' | 'control',
    how: (locator: ReturnType<Page['getByRole']>, node: AxNode) => Promise<void>,
  ): Promise<void> => {
    const current = tree ?? await read();
    const node = locate(current, pattern, kind);
    if (!node) {
      throw new ElectronError(
        `Nothing in this window is a ${kind} a screen reader announces as ` +
        `${pattern.source.replace(/[\^$]/g, '')}.`,
      );
    }
    const locator = page.getByRole(
      ariaRoleFor(node.role) as Parameters<Page['getByRole']>[0],
      { name: node.name.trim(), exact: true },
    ).first();
    await how(locator, node);
  };

  return {
    async snapshot(): Promise<Snapshot> {
      const root = await read();
      return snapshotFrom(root, await where());
    },
    async fill(field: RegExp, value: string): Promise<void> {
      await act(field, 'field', (locator) => locator.fill(value, { timeout }));
    },
    async check(field: RegExp): Promise<void> {
      await act(field, 'field', async (locator, node) => {
        // check() rather than click(), because Playwright's check is a no-op
        // on something already ticked while a click unticks it. A planner
        // that saw an unfilled box, ticked it, and had its own tick undone by
        // the next pass would press it forever.
        if (chosen(node)) await locator.check({ timeout });
        else await locator.click({ timeout });
      });
    },
    async click(control: RegExp): Promise<void> {
      await act(control, 'control', (locator) => locator.click({ timeout }));
    },
    async close(): Promise<void> {
      await app.close().catch(() => undefined);
    },
  };
}
