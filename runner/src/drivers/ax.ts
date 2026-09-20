// The accessibility tree to snapshot adapter: one tree shape, every surface.
//
// A browser hands the planner a Snapshot (runner/src/workflow.ts): the fields
// by accessible name, the controls by accessible name, which of them submit,
// how many interactive things carry no name at all, and the visible text the
// expectations are judged against. Everything above the browser, the planner,
// the verdict, the live stream and the report, is written against that shape
// and against nothing else.
//
// macOS hands out AXUIElement. Chromium hands out its own accessibility tree.
// UIKit hands out a third. They differ in their spelling and not in their
// content: all three are what a screen reader reads, which is what these
// workflows are written against. So the adapter is one function, from a
// platform neutral node to the Snapshot the planner already consumes, and a
// surface is then a reader that produces nodes plus a way to act on one.
//
// Deliberately pure and deliberately surface agnostic. It imports no platform
// module, spawns nothing, and is where the desktop driver, and the iOS driver
// after it, both get their Snapshot. That is the point: a second surface
// should cost a tree reader, not a second copy of this reasoning.

import type { Snapshot } from '../workflow.ts';

/** One node of an accessibility tree, in platform neutral terms.
 *
 * Every field is what a screen reader would announce about the element rather
 * than how a particular framework stores it, so a macOS AXUIElement, a
 * Chromium node and a UIKit element all reduce to this without losing what a
 * decision is made from.
 */
export interface AxNode {
  /** role as the platform spells it. Normalized by normalizeRole below rather
   *  than by the reader, so the normalization is in one place and a new
   *  platform's spelling is added here once. */
  readonly role: string;
  /** name is the accessible name: what a screen reader announces. Empty when
   *  the element has none, which is a finding rather than a detail. */
  readonly name: string;
  /** value is the element's current contents: what is typed into a text
   *  field, the string a static text carries. */
  readonly value?: string;
  /** enabled is false for an element the platform will not let anybody use.
   *  Absent means enabled, because most platforms report only the exception. */
  readonly enabled?: boolean;
  readonly focused?: boolean;
  /** required is the platform's own "this has to be answered", which is what
   *  decides whether a field nothing recognises still blocks the form. */
  readonly required?: boolean;
  /** checked is the state of a checkbox, a radio or a switch. */
  readonly checked?: boolean;
  /** isDefault marks the control the platform itself considers the one that
   *  sends the form: the AXDefaultButton of a macOS window. It is the desktop
   *  analogue of a browser's submit control, and it is taken from the platform
   *  rather than guessed from a word list, because a word list is exactly what
   *  sent this repository's own agent to a header link instead of the button
   *  in front of it (see PROGRESS_CONTROLS in workflow.ts). A platform that
   *  marks no default leaves submits empty, and the planner falls through to
   *  the shared words and the workflow's own description, which is honest. */
  readonly isDefault?: boolean;
  /** ref is the reader's own handle for this element, opaque here. The macOS
   *  reader puts an index path in it so a second call can address the same
   *  element; a reader that acts by role and name leaves it out. */
  readonly ref?: string;
  readonly children?: readonly AxNode[];
}

/** Where a snapshot was taken, the desktop analogue of a url and a title.
 *
 * A desktop app has no url, and Snapshot requires one because everything
 * downstream quotes it when something fails. So a driver supplies a locator
 * that names the application and the window, which is the sentence a person
 * would write when saying where they were. */
export interface AxLocation {
  readonly url: string;
  readonly title: string;
}

/** normalizeRole reduces a platform's spelling of a role to one word.
 *
 * macOS spells a button AXButton, Chromium spells it button, XCUITest spells
 * it XCUIElementTypeButton. Stripping the prefix and lowercasing turns all
 * three into the same token, and the small map below covers the handful that
 * are genuinely named differently rather than merely decorated.
 */
export function normalizeRole(role: string): string {
  const bare = role
    .replace(/^XCUIElementType/, '')
    .replace(/^AX/, '')
    .replace(/[\s_-]+/g, '')
    .toLowerCase();
  return ROLE_ALIASES[bare] ?? bare;
}

/** The roles that mean the same thing under two names. Kept small on purpose:
 *  anything that only differs by a prefix is handled above, and a map that
 *  grows a line per element is a map nobody maintains. */
const ROLE_ALIASES: Readonly<Record<string, string>> = {
  textfield: 'textbox',
  textarea: 'textbox',
  securetextfield: 'textbox',
  secureeditfield: 'textbox',
  searchfield: 'textbox',
  statictext: 'text',
  menubutton: 'popupbutton',
  radiobutton: 'radio',
  togglebutton: 'switch',
  checkboxbutton: 'checkbox',
};

/** The roles that are fields: a thing a workflow answers. */
const FIELD_ROLES: ReadonlySet<string> = new Set([
  'textbox', 'combobox', 'checkbox', 'radio', 'switch', 'slider', 'incrementor',
  'datepicker', 'spinbutton', 'listbox', 'popupbutton',
]);

/** The roles whose state is chosen rather than typed. These are the ones the
 *  planner reaches with `check` rather than `fill`, and the ones whose
 *  `filled` means ticked rather than non-empty. */
const CHOSEN_ROLES: ReadonlySet<string> = new Set(['checkbox', 'radio', 'switch']);

/** The roles that are controls: a thing a workflow presses. */
const CONTROL_ROLES: ReadonlySet<string> = new Set([
  'button', 'link', 'menuitem', 'menubaritem', 'tab', 'popupbutton', 'disclosuretriangle',
]);

/** The roles that carry words a person reads. A field's own value is added
 *  separately below, so a form showing what was typed reads back. */
const TEXT_ROLES: ReadonlySet<string> = new Set([
  'text', 'heading', 'cell', 'row', 'listitem', 'link', 'button', 'paragraph',
  'menuitem', 'tab', 'image', 'textbox', 'checkbox', 'radio', 'switch',
]);

/** The role of a group whose radios are one choice. A radio's `filled` is
 *  decided across this group rather than per option, for the reason
 *  browser.ts gives at length: reported per option, a planner that skips
 *  filled fields ticks the second option after the first, which in a radio
 *  group is changing its mind rather than making progress. */
const RADIO_GROUP_ROLES: ReadonlySet<string> = new Set(['radiogroup', 'radiobuttongroup']);

/** A name longer than this is a paragraph that happened to be announced, not
 *  a label anybody would press. The browser adapter draws the line in the same
 *  place, so a control list means the same thing on both surfaces. */
const MAX_NAME = 60;

/** enabledOf reports whether the platform will let anybody use this element.
 *  Absent means enabled: most trees report only the exception, and treating a
 *  missing flag as disabled would empty every snapshot. */
function enabledOf(node: AxNode): boolean {
  return node.enabled !== false;
}

/** walk visits every node of the tree in the order a screen reader would
 *  announce them, parent before child, which is the order the text below is
 *  assembled in and the order `locate` resolves ties by. */
export function* walk(
  root: AxNode, parent?: AxNode,
): Generator<{ readonly node: AxNode; readonly parent?: AxNode }> {
  yield parent ? { node: root, parent } : { node: root };
  for (const child of root.children ?? []) yield* walk(child, root);
}

/** filledOf decides whether a field has been answered.
 *
 * Answered rather than non-empty, exactly as the browser adapter means it: a
 * ticked checkbox and a radio group with a chosen option are filled, an
 * untouched one is not, and a text field is filled when it carries something.
 */
export function filledOf(node: AxNode, parent: AxNode | undefined): boolean {
  const role = normalizeRole(node.role);
  if (role === 'radio') {
    // The group decides, not the option. A radio with no group above it is
    // the degenerate case and answers for itself.
    if (parent && RADIO_GROUP_ROLES.has(normalizeRole(parent.role))) {
      return (parent.children ?? []).some((option) => option.checked === true);
    }
    return node.checked === true;
  }
  if (CHOSEN_ROLES.has(role)) return node.checked === true;
  return !!node.value;
}

/** plannerType is the role under the name the PLANNER knows it by.
 *
 *  Added when the iOS surface arrived, and it is a correctness fix rather than
 *  a tidy up. workflow.ts decides whether a field is chosen or typed into from
 *  one set, CHOSEN_TYPES, and that set is exactly {checkbox, radio}. A role of
 *  `switch` is therefore not recognised as chosen, so the planner reaches a
 *  toggle with `fill` and tries to TYPE INTO IT, which throws, and the
 *  workflow blocks in front of a control a person would simply have tapped.
 *
 *  It matters more on a phone than on a desktop, which is why it surfaced
 *  here: XCUIElementTypeSwitch is how UIKit spells the ordinary on/off control
 *  and it is everywhere in a settings screen, while a macOS window more often
 *  carries a checkbox.
 *
 *  Reported here rather than widened in workflow.ts on purpose. CHOSEN_TYPES
 *  is the browser's vocabulary, where an input is type="checkbox" and there is
 *  no such thing as a switch, so the translation belongs on this side of the
 *  boundary. `filled` is unaffected: CHOSEN_ROLES above already counts a
 *  switch as chosen, so its answered state was always read from `checked`.
 */
function plannerType(role: string): string {
  return role === 'switch' ? 'checkbox' : role;
}

/** snapshotFrom turns an accessibility tree into the Snapshot the planner
 *  already consumes, so a desktop or mobile run reaches the same planner, the
 *  same judgement and the same report as a browser run.
 */
export function snapshotFrom(
  root: AxNode, at: AxLocation, options: AxOptions = {},
): Snapshot {
  const maxName = options.maxNameLength ?? MAX_NAME;
  const fields: {
    name: string; type: string; filled: boolean; required: boolean;
  }[] = [];
  const controls: string[] = [];
  const submits: string[] = [];
  const words: string[] = [];
  let unnamed = 0;

  for (const { node, parent } of walk(root)) {
    const role = normalizeRole(node.role);
    const name = node.name.trim();
    const interactive = FIELD_ROLES.has(role) || CONTROL_ROLES.has(role);

    if (interactive && enabledOf(node)) {
      if (!name) {
        // An icon button with no label. A screen reader announces nothing
        // here and no planner can press it, so it is counted rather than
        // dropped: the count is the only evidence that the application offers
        // something an agent, and a person using a screen reader, cannot
        // reach.
        unnamed++;
      } else if (name.length > maxName) {
        // TOO LONG TO BE A LABEL, AND STILL COUNTED. Falling through both
        // branches, which is what this used to do, makes the element vanish:
        // absent from `controls`, absent from `fields`, and absent from
        // `unnamed` too, so the snapshot reports no problem at all. A control
        // an agent cannot reach is exactly what `unnamed` exists to record,
        // and a silent drop is the one outcome that cannot be noticed.
        unnamed++;
      } else {
        if (FIELD_ROLES.has(role)) {
          if (!fields.some((f) => f.name === name)) {
            fields.push({
              name,
              type: plannerType(role),
              filled: filledOf(node, parent),
              required: node.required === true,
            });
          }
        }
        if (CONTROL_ROLES.has(role)) {
          if (!controls.includes(name)) controls.push(name);
          if (node.isDefault === true && !submits.includes(name)) submits.push(name);
        }
      }
    }

    // The words a person reads. A control's label counts, the way a browser's
    // innerText counts a button's text, so an expectation written about what
    // is on screen is judged against what is on screen.
    //
    // A line identical to the one before it is dropped. An accessibility tree
    // announces a heading and then the static text inside it, and a button and
    // then the static text inside that, so every visible string arrives twice
    // and a snapshot of a six word screen reads as twelve. The browser's own
    // innerText does not repeat itself, and an expectation is judged against
    // this string, so the two surfaces have to say the same thing.
    if (TEXT_ROLES.has(role)) {
      if (name && words[words.length - 1] !== name) words.push(name);
      // A FIELD'S OWN VALUE IS NOT PART OF WHAT THE APPLICATION SAID, and it
      // must never reach the string expectations are judged against.
      //
      // The value of a text field is what THE AGENT TYPED A MOMENT AGO. Let it
      // into `text` and a workflow can satisfy its own expectation: type
      // "Payment accepted" into a notes field, and an expectation of "Payment
      // accepted" is met by a screen on which the application rendered nothing
      // of the sort. Measured, not theorised, against a real iOS tree before
      // this line existed: judgeAll returned `met`.
      //
      // This is the accessibility tree's version of a pseudo terminal echoing
      // its input, which the terminal surface refuses at validation for the
      // same reason. It is also what a browser already does: innerText carries
      // a button's label and NOT an input's value, so excluding it is what
      // makes a web run and a native run mean the same thing.
      //
      // Read for every other role, because a macOS AXStaticText carries its
      // words in AXValue and dropping those would empty the text entirely.
      const value = FIELD_ROLES.has(role) ? '' : (node.value ?? '').trim();
      if (value && value !== name && words[words.length - 1] !== value) words.push(value);
    }
  }

  return {
    url: at.url,
    title: at.title,
    fields,
    controls,
    submits,
    unnamed,
    text: words.join('\n'),
  };
}

/** Per surface tuning for snapshotFrom. */
export interface AxOptions {
  /** The longest accessible name still treated as a label.
   *
   *  Defaults to MAX_NAME, which mirrors runner/src/browser.ts and is right
   *  for a web page and a desktop window, where a long announced string is
   *  usually a paragraph that happened to carry a role.
   *
   *  It is WRONG as a universal rule, and mobile is where that shows. A
   *  VoiceOver or TalkBack label is written as a sentence on purpose, because
   *  it is read aloud: "Add this item to your basket and continue shopping for
   *  more items" is 65 characters and is an ordinary button. Measured against
   *  a real iOS tree at the default: that button produced controls 0 and
   *  unnamed 0, so a perfectly reachable control disappeared and the snapshot
   *  said nothing was wrong. The mobile drivers therefore raise it rather than
   *  every surface quietly inheriting a web assumption. */
  readonly maxNameLength?: number;
}

/** What kind of element an action is looking for. A fill and a check both
 *  address a field, a click addresses a control, and asking for the right kind
 *  is what stops a click landing on the text field that happens to share a
 *  label with the button beside it. */
export type AxKind = 'field' | 'control';

/** locate finds the element an action names, by its accessible name.
 *
 * By name and nothing else, which is the whole design: the planner decides in
 * terms of what a screen reader announces, so the resolution here has to be
 * the same question. The first match in announcement order wins, and a
 * disabled element is not a match, because acting on one is a click that goes
 * nowhere and reports as a timeout rather than as the truth, which is that the
 * application had it greyed out.
 */
export function locate(
  root: AxNode, pattern: RegExp, kind: AxKind,
): AxNode | undefined {
  const roles = kind === 'field' ? FIELD_ROLES : CONTROL_ROLES;
  for (const { node } of walk(root)) {
    if (!roles.has(normalizeRole(node.role))) continue;
    if (!enabledOf(node)) continue;
    const name = node.name.trim();
    if (name && pattern.test(name)) return node;
  }
  return undefined;
}

/** chosen answers whether this element is ticked or typed into, which is what
 *  decides whether `check` has anything left to do. Exported because a reader
 *  needs it to make a tick idempotent: pressing a ticked checkbox unticks it,
 *  so a `check` on something already checked must do nothing rather than undo
 *  the answer the planner just recorded. */
export function chosen(node: AxNode): boolean {
  return CHOSEN_ROLES.has(normalizeRole(node.role));
}

/** isChosenRole answers the same question about a role token, for a reader
 *  that has the role and not the node. */
export function isChosenRole(role: string): boolean {
  return CHOSEN_ROLES.has(normalizeRole(role));
}
