// Appium's page source, turned into the normalized accessibility tree that
// runner/src/drivers/ax.ts understands.
//
// Appium renders a device's accessibility tree as XML, and the two mobile
// drivers spell it differently: XCUITest names an element by its XCUIElement
// type and carries `label`, `name` and `value`; UiAutomator2 names it by its
// Java class and carries `content-desc`, `text` and a row of boolean state
// attributes. Neither is a shape this runner should know about anywhere else,
// so both are flattened here into AxNode and nothing downstream branches on
// the platform again.
//
// The XML is parsed by hand rather than with a library. The runner carries one
// production dependency and adding a parser for two well formed, machine
// generated documents is not worth a second. That is a defensible choice only
// because the parser below is a real scanner rather than a regular expression:
// an accessible name is arbitrary application text, so an attribute value here
// genuinely does contain `>`, `/` and quotes, and a pattern like /<(\w+)([^>]*)\/?>/
// silently truncates the element the moment a label says "20/30" or "a > b".

import type { AxNode } from './ax.ts';

/** A parsed XML element. Attribute only: neither driver puts anything in a
 *  text node, so text content is skipped rather than modelled. */
export interface XmlElement {
  readonly tag: string;
  readonly attrs: Readonly<Record<string, string>>;
  readonly children: readonly XmlElement[];
}

/** Raised when the source is not the XML this expects. A parse failure is the
 *  runner's own problem and must block a run rather than fail it, so it is a
 *  named error rather than a silently empty tree: an empty tree would render
 *  as an application showing nothing, which is a lie about the application. */
export class AxSourceError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'AxSourceError';
  }
}

const ENTITIES: Readonly<Record<string, string>> = {
  amp: '&', lt: '<', gt: '>', quot: '"', apos: "'",
};

/** decodeEntities expands the five XML entities and numeric character
 *  references. An accessible name is application text and routinely carries an
 *  ampersand ("Terms & Conditions"), which arrives escaped. Leaving it escaped
 *  would mean a workflow expecting the visible words never matched them. */
function decodeEntities(text: string): string {
  if (!text.includes('&')) return text;
  return text.replace(/&(#x?[0-9a-fA-F]+|[a-zA-Z]+);/g, (whole, body: string) => {
    if (body.startsWith('#')) {
      const code = body[1] === 'x' || body[1] === 'X'
        ? Number.parseInt(body.slice(2), 16)
        : Number.parseInt(body.slice(1), 10);
      // An out of range or unparseable reference is left exactly as it was
      // rather than turned into a replacement character: showing the raw text
      // is honest, and inventing a character is not.
      return Number.isFinite(code) && code >= 0 && code <= 0x10ffff
        ? String.fromCodePoint(code)
        : whole;
    }
    return ENTITIES[body] ?? whole;
  });
}

/** parseXml turns an Appium page source into an element tree.
 *
 *  A scanner rather than a pattern, for the reason in this file's header. It
 *  understands exactly what these two documents contain: a declaration, nested
 *  elements, attributes in single or double quotes, self closing tags, and
 *  comments. Anything else is refused by name rather than skipped, because a
 *  parser that quietly ignores what it did not understand produces a tree that
 *  is missing controls and looks complete. */
export function parseXml(source: string): XmlElement {
  let at = 0;
  const stack: { tag: string; attrs: Record<string, string>; children: XmlElement[] }[] = [];
  let root: XmlElement | undefined;

  // Annotated on the VARIABLE rather than only on the arrow's return type.
  // TypeScript narrows a caller's control flow through a never-returning
  // function only when the binding itself is annotated, so without this the
  // compiler still believes execution continues past every fail() and reports
  // the values it guards as possibly undefined.
  const fail: (why: string) => never = (why) => {
    throw new AxSourceError(`${why} at offset ${at} of the page source`);
  };

  while (at < source.length) {
    const open = source.indexOf('<', at);
    if (open < 0) break;
    at = open;

    // <?xml ... ?> and <!-- ... --> and <!DOCTYPE ...>: skipped whole.
    if (source.startsWith('<?', at)) {
      const end = source.indexOf('?>', at);
      if (end < 0) fail('an unterminated processing instruction');
      at = end + 2;
      continue;
    }
    if (source.startsWith('<!--', at)) {
      const end = source.indexOf('-->', at);
      if (end < 0) fail('an unterminated comment');
      at = end + 3;
      continue;
    }
    if (source.startsWith('<!', at)) {
      const end = source.indexOf('>', at);
      if (end < 0) fail('an unterminated declaration');
      at = end + 1;
      continue;
    }

    // A closing tag.
    if (source.startsWith('</', at)) {
      const end = source.indexOf('>', at);
      if (end < 0) fail('an unterminated closing tag');
      const tag = source.slice(at + 2, end).trim();
      const top = stack.pop();
      if (!top) fail(`a closing tag </${tag}> with nothing open`);
      if (top && top.tag !== tag) fail(`a closing tag </${tag}> that does not match <${top.tag}>`);
      at = end + 1;
      const finished: XmlElement = {
        tag: top!.tag, attrs: top!.attrs, children: top!.children,
      };
      const parent = stack[stack.length - 1];
      if (parent) parent.children.push(finished); else root = finished;
      continue;
    }

    // An opening tag. Read the name, then attributes, one character at a time,
    // so a quoted value carrying '>' or '/' cannot end the tag early.
    at += 1;
    const nameStart = at;
    while (at < source.length && !/[\s/>]/.test(source[at]!)) at += 1;
    const tag = source.slice(nameStart, at);
    if (!tag) fail('an element with no name');

    const attrs: Record<string, string> = {};
    let selfClosing = false;
    for (;;) {
      while (at < source.length && /\s/.test(source[at]!)) at += 1;
      if (at >= source.length) fail(`an unterminated tag <${tag}>`);
      if (source[at] === '>') { at += 1; break; }
      if (source.startsWith('/>', at)) { selfClosing = true; at += 2; break; }

      const keyStart = at;
      while (at < source.length && !/[\s=/>]/.test(source[at]!)) at += 1;
      const key = source.slice(keyStart, at);
      if (!key) fail(`an attribute with no name in <${tag}>`);

      while (at < source.length && /\s/.test(source[at]!)) at += 1;
      if (source[at] !== '=') {
        // A valueless attribute is legal in HTML and not in XML, and neither
        // driver emits one. Recorded as empty rather than refused, because a
        // stray one must not lose the whole tree.
        attrs[key] = '';
        continue;
      }
      at += 1;
      while (at < source.length && /\s/.test(source[at]!)) at += 1;
      const quote = source[at];
      if (quote !== '"' && quote !== "'") fail(`an unquoted value for ${key} in <${tag}>`);
      at += 1;
      const valueStart = at;
      const close = source.indexOf(quote, at);
      if (close < 0) fail(`an unterminated value for ${key} in <${tag}>`);
      attrs[key] = decodeEntities(source.slice(valueStart, close));
      at = close + 1;
    }

    if (selfClosing) {
      const finished: XmlElement = { tag, attrs, children: [] };
      const parent = stack[stack.length - 1];
      if (parent) parent.children.push(finished); else root = finished;
    } else {
      stack.push({ tag, attrs, children: [] });
    }
  }

  if (stack.length > 0) {
    throw new AxSourceError(
      `the page source ended with <${stack[stack.length - 1]!.tag}> still open`,
    );
  }
  if (!root) throw new AxSourceError('the page source carried no elements at all');
  return root;
}

/** truthy reads the string booleans both drivers emit. Absent means false, and
 *  anything other than "true" is false, so a driver that starts emitting "1"
 *  would read as false rather than as true by accident. */
function truthy(value: string | undefined): boolean {
  return value === 'true';
}

// The iOS half: how the XCUITest driver spells a tree.

/** fromIOS turns an XCUITest page source into a normalized tree.
 *
 *  The name is `label` before `name`. XCUITest sets `name` from the
 *  accessibility IDENTIFIER when an app sets one and falls back to the label
 *  otherwise, and an identifier is a developer's internal string that no
 *  screen reader ever says. A workflow is written in the words a person sees,
 *  so the label is what it has to match, and preferring `name` would mean a
 *  well instrumented app, one that sets identifiers for its own test suite,
 *  became the one this could not drive. */
export function fromIOS(source: string): AxNode {
  return iosNode(withoutKeyboard(parseXml(source)));
}

/** holdsKeyboard answers whether a subtree contains the software keyboard. */
function holdsKeyboard(el: XmlElement): boolean {
  if (el.tag === 'XCUIElementTypeKeyboard') return true;
  return el.children.some(holdsKeyboard);
}

/** withoutKeyboard removes the software keyboard's window from the tree.
 *
 *  The keyboard is SYSTEM user interface, not the application's, and leaving
 *  it in makes the snapshot describe iOS rather than the app under test.
 *  Measured on a real screen: opening one text field turns a two control
 *  screen into one offering "Sign In, shift, Return, Emoji, Dictate" plus
 *  twenty six letter keys. The planner then has "Return" and "more" as things
 *  it could press, and its own rule against pressing the same control twice
 *  is spent on keys rather than on the application. It is also simply untrue
 *  as a description of the app: nobody writes a workflow about the letter q.
 *
 *  Cutting at the WINDOW rather than at the keyboard element is what makes it
 *  complete. iOS puts the keyboard in its own window together with the
 *  accessory controls that travel with it, and "Emoji" and "Dictate" are
 *  siblings OUTSIDE the XCUIElementTypeKeyboard element, so pruning only that
 *  element leaves them behind looking like application controls. A window
 *  holding a keyboard is the input surface in its entirety, and no application
 *  puts its own content there: UIKit hosts the keyboard in a dedicated window
 *  precisely so it is separate from the app's.
 *
 *  Typing is unaffected: text is entered through the WebDriver value endpoint
 *  against the field element, which never needed the keys to be visible. */
function withoutKeyboard(root: XmlElement): XmlElement {
  const keep = (el: XmlElement): boolean =>
    !(el.tag === 'XCUIElementTypeWindow' && holdsKeyboard(el))
    && el.tag !== 'XCUIElementTypeKeyboard';
  const prune = (el: XmlElement): XmlElement => ({
    tag: el.tag,
    attrs: el.attrs,
    children: el.children.filter(keep).map(prune),
  });
  return prune(root);
}

function iosNode(el: XmlElement): AxNode {
  // The tag IS the role, handed over in the platform's own spelling.
  // ax.ts's normalizeRole strips the XCUIElementType prefix itself, so a
  // second role table here would be a copy of one that already exists and the
  // two would drift.
  const role = el.tag;
  const label = (el.attrs['label'] ?? '').trim();
  const name = label || (el.attrs['name'] ?? '').trim();
  const isChosen = /Switch|Toggle|CheckBox|RadioButton/.test(el.tag);

  // AN EMPTY TEXT FIELD REPORTS ITS PLACEHOLDER AS ITS VALUE, and this is the
  // one that silently breaks everything downstream.
  //
  // Measured from a real tree rather than assumed: a SwiftUI TextField with
  // placeholder "Email" and nothing typed into it comes back as
  //   value="Email" name="Email" label="" placeholderValue="Email"
  // so `value` is non-empty for a field nobody has touched. Read literally,
  // the snapshot says the field is already answered, the planner skips it,
  // presses the submit control against an empty form, and reports the
  // application rejecting valid input. Every layer above behaves correctly on
  // a fact that is wrong.
  //
  // A value equal to the placeholder therefore means EMPTY. The narrow cost is
  // that somebody who genuinely types the placeholder text into the field
  // reads as not having typed it, which costs one redundant fill and is the
  // right trade against never filling anything.
  const raw = el.attrs['value'];
  const placeholder = el.attrs['placeholderValue'];
  const value = raw !== undefined && placeholder !== undefined && raw === placeholder
    ? ''
    : raw;

  return {
    role,
    name,
    // A StaticText carries its words in `label` AND in `value`, identically,
    // and ax.ts already drops a value that repeats the name, so passing it
    // through costs nothing and keeps a real field's contents.
    ...(value !== undefined ? { value } : {}),
    enabled: truthy(el.attrs['enabled']),
    ...(el.attrs['focused'] !== undefined ? { focused: truthy(el.attrs['focused']) } : {}),
    // XCUITest reports a switch's state in `value` as "0" or "1".
    ...(isChosen ? { checked: raw === '1' || raw === 'true' } : {}),
    children: el.children.map(iosNode),
  };
}

// The Android half: how the UiAutomator2 driver spells a tree.

/** The android.widget and androidx class names worth distinguishing, mapped
 *  to the role tokens ax.ts already understands.
 *
 *  Matched on the class SUFFIX rather than the whole name, because an app's
 *  own subclass ("com.example.BrandButton") and a support library's
 *  ("androidx.appcompat.widget.AppCompatButton") both end in the widget they
 *  extend, and a whole name match would send every real application's controls
 *  to nothing at all.
 *
 *  The values are ax.ts's OWN vocabulary rather than a parallel one. A class
 *  name means nothing to normalizeRole, which knows how to strip an Apple or a
 *  macOS prefix and has no reason to learn Java package names, so Android is
 *  the one platform whose spelling has to be translated before the tree is
 *  handed over. Translating it to the tokens ax.ts already uses is what keeps
 *  that a translation rather than a second role system.
 */
const ANDROID_ROLES: readonly (readonly [string, string])[] = [
  ['EditText', 'textbox'],
  ['AutoCompleteTextView', 'textbox'],
  ['CheckBox', 'checkbox'],
  ['Switch', 'switch'],
  ['ToggleButton', 'switch'],
  ['RadioButton', 'radio'],
  ['ImageButton', 'button'],
  ['Button', 'button'],
  ['TextView', 'text'],
  ['ImageView', 'image'],
  ['ViewGroup', 'group'],
  ['Layout', 'group'],
  ['RecyclerView', 'group'],
  ['ScrollView', 'group'],
];

/** androidRole maps a class name to a role token.
 *
 *  Order matters and the list above is ordered deliberately: "ImageButton"
 *  ends in "Button" too, and a checkbox subclass ends in "Button" in some
 *  libraries, so the more specific suffixes are tested first.
 */
function androidRole(className: string, clickable: boolean, checkable: boolean): string {
  for (const [suffix, role] of ANDROID_ROLES) {
    if (className.endsWith(suffix)) {
      // A TextView that is clickable is a button to everyone who uses the
      // screen, and applications build buttons out of TextViews constantly.
      // Read what it DOES rather than what it extends.
      if (role === 'text' && clickable) return 'button';
      return role;
    }
  }
  if (checkable) return 'checkbox';
  if (clickable) return 'button';
  return 'group';
}

/** fromAndroid turns a UiAutomator2 page source into a normalized tree.
 *
 *  The name is `content-desc` before `text`, which is the Android ordering a
 *  screen reader uses: a content description exists precisely to say what an
 *  element IS when its visible text does not, and TalkBack announces it in
 *  preference to the text. Falling back to `text` is what makes an app that
 *  sets no content descriptions, which is most of them, still drivable. */
export function fromAndroid(source: string): AxNode {
  return androidNode(parseXml(source));
}

function androidNode(el: XmlElement): AxNode {
  const className = el.attrs['class'] ?? el.tag;
  const clickable = truthy(el.attrs['clickable']);
  const checkable = truthy(el.attrs['checkable']);
  const role = androidRole(className, clickable, checkable);
  const text = (el.attrs['text'] ?? '').trim();
  const desc = (el.attrs['content-desc'] ?? '').trim();
  const isField = role === 'textbox' || role === 'checkbox' || role === 'radio' || role === 'switch';

  return {
    role,
    name: desc || text,
    // An EditText carries what is typed into it in `text`. Its content
    // description is its label, which is why the two are read separately here:
    // the name comes from the description and the value from the text, and
    // collapsing them would make an empty field look answered by its own
    // label.
    ...(isField ? { value: text } : {}),
    enabled: truthy(el.attrs['enabled']),
    ...(el.attrs['focused'] !== undefined ? { focused: truthy(el.attrs['focused']) } : {}),
    ...(checkable ? { checked: truthy(el.attrs['checked']) } : {}),
    children: el.children.map(androidNode),
  };
}
