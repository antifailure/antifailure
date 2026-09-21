// The mobile surfaces: the XML scanner, the two platform normalizers, the
// accessibility tree to snapshot adapter, and the refusals.
//
// The fixtures here are REAL page source, captured from an iOS Simulator and
// an Android emulator driving examples/mobile-probe, not hand written XML that
// happens to match what this code expects. That distinction is the whole value
// of the file: two of the assertions below (the placeholder that reads as a
// typed value, and the keyboard that reads as application controls) exist
// because the real tree did something the invented one never would have.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { parseXml, fromIOS, fromAndroid, AxSourceError } from '../src/drivers/axsource.ts';
import { snapshotFrom, type AxNode } from '../src/drivers/ax.ts';
import { isPlayable, mobileSnapshot, xpathLiteral, runMobile } from '../src/drivers/mobile.ts';
import { judgeAll } from '../src/workflow.ts';
import { finalJudgement } from '../src/execute.ts';
import { driverFor, surfaces } from '../src/drivers/driver.ts';
import { androidPlatform } from '../src/drivers/android.ts';

// The XML scanner.

test('parseXml reads nesting, attributes and self closing tags', () => {
  const root = parseXml(
    `<?xml version="1.0" encoding="UTF-8"?>
     <hierarchy rotation="0">
       <node class="a"><leaf class="b" text="x"/></node>
     </hierarchy>`,
  );
  assert.equal(root.tag, 'hierarchy');
  assert.equal(root.attrs['rotation'], '0');
  assert.equal(root.children.length, 1);
  assert.equal(root.children[0]!.children[0]!.attrs['text'], 'x');
});

test('parseXml keeps an attribute value containing the characters that end a tag', () => {
  // THE REASON THIS IS A SCANNER AND NOT A REGULAR EXPRESSION. An accessible
  // name is application text: "20/30" and "a > b" are ordinary labels, and a
  // pattern like /<(\w+)([^>]*)\/?>/ truncates the element at the first one.
  //
  // The literal `>` below is the point, and the first version of this test
  // missed it: it wrote `&gt;`, which is escaped, so the value held no
  // character that could have ended the tag early and a broken scanner passed.
  // An unescaped `>` inside an attribute value is perfectly legal XML, so this
  // is a document a real device can send.
  const root = parseXml(`<n a="20/30 > b" b="plain" c="a &gt; b &amp; c"/>`);
  assert.equal(root.attrs['a'], '20/30 > b');
  assert.equal(root.attrs['b'], 'plain');
  assert.equal(root.attrs['c'], 'a > b & c');
});

test('parseXml expands the entities an accessible name really carries', () => {
  const root = parseXml(`<n text="Terms &amp; Conditions" d="&quot;q&quot; &#65;&#x42;"/>`);
  assert.equal(root.attrs['text'], 'Terms & Conditions');
  assert.equal(root.attrs['d'], '"q" AB');
});

test('parseXml refuses source it cannot read rather than returning a partial tree', () => {
  // A parser that silently skips what it did not understand produces a tree
  // missing controls that looks complete, and the run then reports an
  // application offering less than it does.
  //
  // Each case asserts the MESSAGE, not just that something threw. Mutation
  // testing found why: breaking the unclosed-element check left this test
  // green, because "<a>" also reaches the separate "no elements at all" guard
  // and throws from there. Both refusals are correct and only one of them is
  // the one this line is about, so a bare assert.throws could not tell that
  // the check it targets had been deleted.
  assert.throws(() => parseXml('<a><b></a>'), /does not match/);
  assert.throws(() => parseXml('<a>'), /still open/);
  assert.throws(() => parseXml('nothing here'), /no elements at all/);
  for (const bad of ['<a><b></a>', '<a>', 'nothing here']) {
    assert.throws(() => parseXml(bad), AxSourceError);
  }
});

// iOS, against a captured simulator tree.

/** Captured from the iOS Simulator (iPhone 17 Pro, iOS 26.5) driving
 *  examples/mobile-probe, with the keyboard up. Trimmed in breadth, not
 *  changed in shape. */
const IOS_SOURCE = `<?xml version="1.0" encoding="UTF-8"?>
<AppiumAUT>
  <XCUIElementTypeApplication type="XCUIElementTypeApplication" name="Probe" label="Probe" enabled="true" x="0" y="0" width="402" height="874" index="0">
    <XCUIElementTypeWindow type="XCUIElementTypeWindow" enabled="true" x="0" y="0" width="402" height="874" index="0">
      <XCUIElementTypeOther type="XCUIElementTypeOther" enabled="true" x="0" y="0" width="402" height="874" index="0">
        <XCUIElementTypeTextField type="XCUIElementTypeTextField" value="Email" name="Email" label="" enabled="true" x="16" y="420" width="370" height="22" index="0" placeholderValue="Email"/>
        <XCUIElementTypeButton type="XCUIElementTypeButton" name="Sign In" label="Sign In" enabled="true" x="175" y="462" width="52" height="21" index="1"/>
        <XCUIElementTypeStaticText type="XCUIElementTypeStaticText" value="Welcome back" name="Welcome back" label="Welcome back" enabled="true" x="16" y="500" width="120" height="20" index="2"/>
      </XCUIElementTypeOther>
    </XCUIElementTypeWindow>
    <XCUIElementTypeWindow type="XCUIElementTypeWindow" enabled="true" x="0" y="583" width="402" height="291" index="1">
      <XCUIElementTypeOther type="XCUIElementTypeOther" name="inputView" enabled="true" x="0" y="583" width="402" height="233" index="0">
        <XCUIElementTypeKeyboard type="XCUIElementTypeKeyboard" enabled="true" x="0" y="583" width="402" height="233" index="0">
          <XCUIElementTypeKey type="XCUIElementTypeKey" name="q" label="q" enabled="true" x="4" y="590" width="40" height="54" index="1"/>
          <XCUIElementTypeButton type="XCUIElementTypeButton" name="shift" label="shift" enabled="true" x="4" y="698" width="52" height="54" index="24"/>
          <XCUIElementTypeButton type="XCUIElementTypeButton" name="Return" label="return" enabled="true" x="300" y="752" width="100" height="54" index="36"/>
        </XCUIElementTypeKeyboard>
        <XCUIElementTypeButton type="XCUIElementTypeButton" name="Emoji" label="Emoji" enabled="true" x="8" y="805" width="69" height="42" index="1"/>
        <XCUIElementTypeButton type="XCUIElementTypeButton" name="dictation" label="Dictate" enabled="true" x="325" y="805" width="69" height="42" index="2"/>
      </XCUIElementTypeOther>
    </XCUIElementTypeWindow>
  </XCUIElementTypeApplication>
</AppiumAUT>`;

const iosSnapshot = () => snapshotFrom(fromIOS(IOS_SOURCE), { url: 'ios://probe', title: 'Probe' });

test('an empty iOS text field reads as empty even though it reports its placeholder as its value', () => {
  // THE ONE THAT WOULD HAVE SHIPPED. XCUITest answers value="Email" for a
  // field nobody has typed in, because the placeholder IS the value until
  // something replaces it. Read literally, the field is already answered, the
  // planner never fills it, and the run reports the application rejecting a
  // form the agent submitted empty.
  const snapshot = iosSnapshot();
  const email = snapshot.fields.find((f) => f.name === 'Email');
  assert.ok(email, `no Email field in ${JSON.stringify(snapshot.fields)}`);
  assert.equal(email.filled, false);
  // 'textbox' is ax.ts's vocabulary: it folds every text entry role, including
  // a secure field and a search field, into one type the planner understands.
  assert.equal(email.type, 'textbox');
});

test('a typed iOS text field reads as filled', () => {
  // The other direction, so the rule above cannot pass by always saying empty.
  const typed = IOS_SOURCE.replace('value="Email" name="Email"', 'value="ada@example.test" name="Email"');
  const snapshot = snapshotFrom(fromIOS(typed), { url: 'ios://probe', title: 'Probe' });
  assert.equal(snapshot.fields.find((f) => f.name === 'Email')?.filled, true);
});

test('the iOS software keyboard is not reported as application controls', () => {
  const snapshot = iosSnapshot();
  assert.deepEqual(snapshot.controls, ['Sign In']);
  for (const key of ['shift', 'Return', 'q', 'Emoji', 'Dictate']) {
    assert.ok(!snapshot.controls.includes(key), `${key} leaked into the controls`);
  }
});

test('iOS static text reaches the snapshot text so an expectation can be judged against it', () => {
  assert.match(iosSnapshot().text, /Welcome back/);
});

// Android, against a captured emulator tree.

/** CONSTRUCTED, not captured, and labelled so deliberately.
 *
 *  The iOS fixture above is a real capture. This one is not: the emulator on
 *  the machine this was written on never finished booting, so no real tree was
 *  ever read from a device. It is built from the attribute set UiAutomator2
 *  documents, and it is the reason the android surface is not claimed as
 *  available. Replace it with a real capture in the same change that proves
 *  the surface, because the iOS capture is what found the defects that
 *  invented XML would never have shown. */
const ANDROID_SOURCE = `<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>
<hierarchy rotation="0">
  <android.widget.FrameLayout index="0" package="dev.antifailure.probe" class="android.widget.FrameLayout" text="" content-desc="" checkable="false" checked="false" clickable="false" enabled="true" focused="false">
    <android.widget.LinearLayout index="0" class="android.widget.LinearLayout" text="" content-desc="" checkable="false" checked="false" clickable="false" enabled="true" focused="false">
      <android.widget.EditText index="0" class="android.widget.EditText" text="" content-desc="Email" checkable="false" checked="false" clickable="true" enabled="true" focused="false" password="false"/>
      <android.widget.Button index="1" class="android.widget.Button" text="Sign In" content-desc="Sign In" checkable="false" checked="false" clickable="true" enabled="true" focused="false"/>
      <android.widget.Button index="2" class="android.widget.Button" text="Delete Account" content-desc="Delete Account" checkable="false" checked="false" clickable="true" enabled="true" focused="false"/>
      <android.widget.TextView index="3" class="android.widget.TextView" text="Welcome back" content-desc="Welcome back" checkable="false" checked="false" clickable="false" enabled="true" focused="false"/>
      <android.widget.CheckBox index="4" class="androidx.appcompat.widget.AppCompatCheckBox" text="Remember me" content-desc="Remember me" checkable="true" checked="false" clickable="true" enabled="true" focused="false"/>
      <android.widget.EditText index="5" class="androidx.appcompat.widget.AppCompatEditText" text="" content-desc="Notes" checkable="false" checked="false" clickable="false" enabled="true" focused="false" password="false"/>
    </android.widget.LinearLayout>
  </android.widget.FrameLayout>
</hierarchy>`;

const androidSnapshot = () =>
  snapshotFrom(fromAndroid(ANDROID_SOURCE), { url: 'android://probe', title: 'probe' });

test('an Android field takes its name from the content description and its value from the text', () => {
  // Collapsing the two is the trap: a content description is the LABEL and
  // the text is what is typed, so reading the name out of `text` would make an
  // empty field look answered by its own label.
  const snapshot = androidSnapshot();
  const email = snapshot.fields.find((f) => f.name === 'Email');
  assert.ok(email, `no Email field in ${JSON.stringify(snapshot.fields)}`);
  assert.equal(email.filled, false);

  const typed = ANDROID_SOURCE.replace('text="" content-desc="Email"', 'text="ada@example.test" content-desc="Email"');
  const after = snapshotFrom(fromAndroid(typed), { url: 'android://probe', title: 'probe' });
  assert.equal(after.fields.find((f) => f.name === 'Email')?.filled, true);
});

test('an Android checkbox is answered by being checked, never by having a value', () => {
  const snapshot = androidSnapshot();
  const box = snapshot.fields.find((f) => f.name === 'Remember me');
  assert.ok(box, 'the checkbox is not a field');
  assert.equal(box.type, 'checkbox');
  assert.equal(box.filled, false, 'an unticked checkbox read as answered');

  const ticked = ANDROID_SOURCE.replace(
    'content-desc="Remember me" checkable="true" checked="false"',
    'content-desc="Remember me" checkable="true" checked="true"');
  const after = snapshotFrom(fromAndroid(ticked), { url: 'android://probe', title: 'probe' });
  assert.equal(after.fields.find((f) => f.name === 'Remember me')?.filled, true);
});

test('an Android class is matched by its suffix so a library subclass is still recognised', () => {
  // androidx.appcompat.widget.AppCompatEditText is what a real application
  // ships. A whole name match sends every real control to `other`.
  //
  // Asserted on the EditText rather than on the checkbox, which is where this
  // started. Mutation testing found that replacing the suffix match with an
  // exact one left the checkbox assertion green: a checkbox also reaches its
  // role through the `checkable` fallback, so suffix matching was never what
  // decided it. This field is neither checkable nor clickable, so the suffix
  // is the only thing that can classify it and the assertion is about the line
  // it claims to be about.
  const snapshot = androidSnapshot();
  assert.equal(snapshot.fields.find((f) => f.name === 'Notes')?.type, 'textbox');
  assert.equal(snapshot.fields.find((f) => f.name === 'Remember me')?.type, 'checkbox');
});

test('a clickable Android TextView is a button, because that is what it is to a person', () => {
  const asButton = ANDROID_SOURCE.replace(
    'class="android.widget.TextView" text="Welcome back" content-desc="Welcome back" checkable="false" checked="false" clickable="false"',
    'class="android.widget.TextView" text="Tap here" content-desc="Tap here" checkable="false" checked="false" clickable="true"');
  const snapshot = snapshotFrom(fromAndroid(asButton), { url: 'android://probe', title: 'probe' });
  assert.ok(snapshot.controls.includes('Tap here'),
    `a clickable TextView was not offered as a control: ${snapshot.controls.join(', ')}`);
});

// The tree to snapshot adapter.

const node = (over: Partial<AxNode>): AxNode =>
  ({ role: 'other', name: '', enabled: true, children: [], ...over });

test('an interactive element with no accessible name is counted, never dropped', () => {
  // The count is the only evidence a report carries that the application
  // offers something neither an agent nor a screen reader can reach.
  const snapshot = snapshotFrom(node({
    role: 'container',
    children: [
      node({ role: 'button', name: '' }),
      node({ role: 'textfield', name: '' }),
      node({ role: 'container', name: '' }),
    ],
  }), { url: 'x', title: 'y' });
  assert.equal(snapshot.unnamed, 2, 'structure was counted as unreachable, or a control was dropped');
  assert.equal(snapshot.controls.length, 0);
});

test('a disabled control is neither a field to fill nor a control to press', () => {
  const snapshot = snapshotFrom(node({
    role: 'container',
    children: [
      node({ role: 'button', name: 'Send', enabled: false }),
      node({ role: 'button', name: 'Save', enabled: true }),
    ],
  }), { url: 'x', title: 'y' });
  assert.deepEqual(snapshot.controls, ['Save']);
  // It is still readable, so its words still reach the text a person reads.
  assert.match(snapshot.text, /Send/);
});

test('a native snapshot offers no submit controls, because a native screen has no form', () => {
  // Guessing which button submits would put a guess where the browser has a
  // fact. The planner already handles an empty submits list by falling
  // through to the words it knows.
  assert.deepEqual(iosSnapshot().submits, []);
  assert.deepEqual(androidSnapshot().submits, []);
});

test('a duplicated control name is offered once', () => {
  const snapshot = snapshotFrom(node({
    role: 'container',
    children: [node({ role: 'button', name: 'Delete' }), node({ role: 'button', name: 'Delete' })],
  }), { url: 'x', title: 'y' });
  assert.deepEqual(snapshot.controls, ['Delete']);
});

// XPath quoting.

test('xpathLiteral quotes a name carrying either kind of quote', () => {
  // XPath 1.0 has no escape inside a string literal, and "Don't save" is an
  // ordinary button label. The naive version builds a malformed expression
  // and the server answers with a syntax error that reads like the app
  // missing the control.
  assert.equal(xpathLiteral('Sign In'), `'Sign In'`);
  assert.equal(xpathLiteral("Don't save"), `"Don't save"`);
  assert.match(xpathLiteral(`it's "quoted"`), /^concat\(/);
});

// The screen recording.

/** A minimal box sequence, the shape a QuickTime or MP4 file really has. */
function boxes(...specs: readonly (readonly [string, Buffer])[]): Buffer {
  return Buffer.concat(specs.map(([type, body]) => {
    const head = Buffer.alloc(8);
    head.writeUInt32BE(8 + body.length, 0);
    head.write(type, 4, 'latin1');
    return Buffer.concat([head, body]);
  }));
}

test('a recording is playable only when it actually carries its index', async () => {
  const dir = mkdtempSync(join(tmpdir(), 'af-mob-'));

  const good = join(dir, 'good.mov');
  writeFileSync(good, boxes(['ftyp', Buffer.alloc(12)], ['moov', Buffer.alloc(64)], ['mdat', Buffer.alloc(128)]));
  assert.equal(await isPlayable(good), true);

  // What an interrupted recording leaves: samples, and nothing saying where
  // anything is. The file has a plausible name and a plausible size and no
  // player will open it.
  const truncated = join(dir, 'truncated.mov');
  writeFileSync(truncated, boxes(['ftyp', Buffer.alloc(12)], ['mdat', Buffer.alloc(4096)]));
  assert.equal(await isPlayable(truncated), false);

  // THE FALSIFICATION ARM. Compressed video contains the four bytes "moov" by
  // chance, so a substring search over the file answers yes about a file that
  // cannot be played. Walking the box lengths is what makes the check real,
  // and this fixture is the one that tells the two implementations apart.
  const decoy = join(dir, 'decoy.mov');
  writeFileSync(decoy, boxes(['ftyp', Buffer.alloc(12)], ['mdat', Buffer.from('....moov....padding')]));
  assert.equal(await isPlayable(decoy), false,
    'a "moov" appearing inside sample data was mistaken for the index');

  assert.equal(await isPlayable(join(dir, 'absent.mov')), false);
});

// The registry and the refusals.

test('the registry knows the android surface, and android is not claimed as available', () => {
  assert.deepEqual([...surfaces()].sort(), ['android', 'desktop', 'ios', 'terminal', 'web']);
  // iOS has been driven end to end against the simulator, so it is available.
  assert.equal(driverFor('ios').available, true);
  // Android has NOT been driven end to end, so it is not claimed, however
  // complete its code looks. See the note at the top of driver.ts.
  assert.equal(driverFor('android').available, false);
  assert.match(driverFor('android').summary, /NOT yet proven/);
});

test('a mobile run with no Appium server is blocked, never failed', async () => {
  // The distinction the whole report rests on. Our own tooling being absent
  // is not evidence about the application, and reporting it as a failing
  // workflow is how a green suite starts meaning nothing.
  const results = await runMobile({
    platform: androidPlatform({ serial: 'nonesuch', appPackage: 'dev.antifailure.probe' }),
    // Port 1 is privileged and nothing listens there.
    serverURL: 'http://127.0.0.1:1',
    artifacts: mkdtempSync(join(tmpdir(), 'af-mob-')),
    workflows: [{ name: 'anything', description: 'anything at all', expect: ['something'] }],
  });
  assert.equal(results.length, 1);
  assert.equal(results[0]!.outcome.verdict, 'blocked');
  assert.match(results[0]!.outcome.detail, /Appium/);
});

test('a mobile run with nothing to drive is refused, never reported as passing', async () => {
  // THE SILENT GREEN. main.ts sends a non web surface through assertAvailable,
  // which throws while a driver is scaffolded. The moment `available` becomes
  // true that throw stops, and without this refusal the run falls through with
  // `results` still empty: zero passed, zero failed, EXIT CODE ZERO, from a
  // run that drove nothing. A loud "not built" is true; a silent green is not.
  await assert.rejects(
    () => runMobile({
      platform: androidPlatform({ serial: 'nonesuch', appPackage: 'dev.antifailure.probe' }),
      serverURL: 'http://127.0.0.1:1',
      artifacts: mkdtempSync(join(tmpdir(), 'af-mob-')),
      workflows: [],
    }),
    /nothing to judge|no workflows to drive/,
  );
});

test('an iOS switch is a field the planner will CHOOSE, never one it types into', () => {
  // The defect this catches is silent and costs a whole workflow. ax.ts names
  // a field by its normalized role, so a toggle arrives as type "switch", and
  // workflow.ts decides chosen-versus-typed from CHOSEN_TYPES, which is
  // exactly {checkbox, radio}. A switch therefore reads as something to TYPE
  // INTO: the planner emits `fill`, the driver tries to send keys to a toggle,
  // and the workflow blocks in front of a control a person would have tapped.
  // UIKit spells the ordinary on/off control XCUIElementTypeSwitch, so this is
  // the common case on a phone rather than an exotic one.
  const source = `<?xml version="1.0" encoding="UTF-8"?>
<AppiumAUT>
  <XCUIElementTypeApplication type="XCUIElementTypeApplication" name="Probe" label="Probe" enabled="true">
    <XCUIElementTypeSwitch type="XCUIElementTypeSwitch" name="Notify me" label="Notify me" value="0" enabled="true"/>
  </XCUIElementTypeApplication>
</AppiumAUT>`;
  const snapshot = snapshotFrom(fromIOS(source), { url: 'ios://probe', title: 'Probe' });
  const toggle = snapshot.fields.find((f) => f.name === 'Notify me');
  assert.ok(toggle, `the switch is not a field at all: ${JSON.stringify(snapshot.fields)}`);
  assert.equal(toggle.type, 'checkbox',
    'a switch reached the planner as something to type into rather than to choose');
  // And its answered state comes from `checked`, never from the "0" in value.
  assert.equal(toggle.filled, false);

  const on = source.replace('value="0"', 'value="1"');
  const after = snapshotFrom(fromIOS(on), { url: 'ios://probe', title: 'Probe' });
  assert.equal(after.fields.find((f) => f.name === 'Notify me')?.filled, true);
});

test('a workflow cannot satisfy its own expectation with what it typed', () => {
  // THE ECHO TRAP, and it judged `met` before the fix. A text field's value is
  // what the agent typed a moment ago, so letting it into the string
  // expectations are judged against lets a workflow prove itself: type
  // "Payment accepted" into a notes field and an expectation of "Payment
  // accepted" is satisfied by a screen on which the application rendered
  // nothing of the sort. It is the accessibility tree's version of a pseudo
  // terminal echoing its input, and a browser already excludes it: innerText
  // carries a button's label and not an input's value.
  const source = `<?xml version="1.0" encoding="UTF-8"?>
<AppiumAUT>
  <XCUIElementTypeApplication type="XCUIElementTypeApplication" name="Probe" label="Probe" enabled="true">
    <XCUIElementTypeTextField type="XCUIElementTypeTextField" name="Notes" label="" value="Payment accepted" enabled="true" placeholderValue="Notes"/>
    <XCUIElementTypeButton type="XCUIElementTypeButton" name="Save" label="Save" enabled="true"/>
  </XCUIElementTypeApplication>
</AppiumAUT>`;
  const snapshot = snapshotFrom(fromIOS(source), { url: 'ios://probe', title: 'Probe' });
  assert.doesNotMatch(snapshot.text, /Payment accepted/,
    'what the agent typed reached the text its expectations are judged against');
  assert.equal(judgeAll(['Payment accepted'], snapshot.text), 'unclear');
  // The field's LABEL is still there, the way a browser keeps a label in
  // innerText, and so is the button.
  assert.match(snapshot.text, /Notes/);
  assert.match(snapshot.text, /Save/);
});

test('a long mobile label is reachable, and is counted rather than vanishing when it is not', () => {
  // A VoiceOver label is a sentence on purpose, because it is read aloud. The
  // browser's 60 character cap is right for a page and wrong for a phone:
  // measured on a real tree, a 65 character button produced controls 0 AND
  // unnamed 0, so a reachable control disappeared and the snapshot reported
  // nothing wrong. A silent drop is the one outcome nobody can notice.
  const label = 'Add this item to your basket and continue shopping for more items';
  assert.ok(label.length > 60, 'the fixture stopped being longer than the default cap');
  const source = `<?xml version="1.0" encoding="UTF-8"?>
<AppiumAUT>
  <XCUIElementTypeApplication type="XCUIElementTypeApplication" name="Probe" label="Probe" enabled="true">
    <XCUIElementTypeButton type="XCUIElementTypeButton" name="${label}" label="${label}" enabled="true"/>
  </XCUIElementTypeApplication>
</AppiumAUT>`;
  const tree = fromIOS(source);

  // Through the DRIVER's own snapshot call, not through snapshotFrom with a
  // cap this test chose. That distinction is the whole point: reaching past
  // the wiring proves the adapter can do it and says nothing about whether the
  // mobile surface actually asks for it, and mutation testing showed exactly
  // that, by changing the driver's cap back to the browser's 60 without a
  // single test noticing.
  const mobile = mobileSnapshot(tree, { url: 'ios://probe', title: 'Probe' });
  assert.deepEqual(mobile.controls, [label]);
  assert.equal(mobile.unnamed, 0);

  // Past whatever cap is in force it is UNREACHABLE, which is a finding, so it
  // is counted rather than dropped.
  const capped = snapshotFrom(tree, { url: 'ios://probe', title: 'Probe' }, { maxNameLength: 10 });
  assert.deepEqual(capped.controls, []);
  assert.equal(capped.unnamed, 1, 'an over long label vanished without being counted');
});

// An unmet expectation on a phone is not an error unless the phone showed one.
//
// On 2026-09-21 an iOS workflow with a deliberately impossible expectation,
// in front of a journal showing every entry it should, was explained as "The
// page shows an error rather than what was expected." Both platforms reach a
// verdict through finalJudgement, and these drive it from each platform's own
// tree, so the text judged is exactly the text a device run would judge.

const impossible = { name: 'w', description: 'd', expect: ['"An entry that was never written"'] };
const STUCK_HERE = 'Nothing on this page moves the workflow forward.';

for (const [platform, source, snap, replace] of [
  ['iOS', IOS_SOURCE, (xml: string) => snapshotFrom(fromIOS(xml), { url: 'ios://probe', title: 'Probe' }),
    (xml: string) => xml.replaceAll('Welcome back', 'Something went wrong')],
  ['Android', ANDROID_SOURCE,
    (xml: string) => snapshotFrom(fromAndroid(xml), { url: 'android://probe', title: 'probe' }),
    (xml: string) => xml.replaceAll('Welcome back', 'Something went wrong')],
] as const) {
  test(`an ${platform} screen that is healthy and lacks the expectation is not reported as an error`, () => {
    const result = finalJudgement(impossible, snap(source), STUCK_HERE, []);
    assert.equal(result.cause, 'expectation-not-met', 'the verdict must not change');
    assert.ok(result.detail.startsWith('"An entry that was never written" was not found.'), result.detail);
    assert.ok(!/shows an error|showed a failure/i.test(result.detail),
      `the detail claims an error on a healthy screen: ${result.detail}`);
    assert.ok(/No error was showing\. Instead it showed: "[^"]*Welcome back/.test(result.detail),
      `the detail does not say what the screen showed: ${result.detail}`);
  });

  test(`an ${platform} screen genuinely showing an error still says so`, () => {
    const result = finalJudgement(impossible, snap(replace(source)), STUCK_HERE, []);
    assert.equal(result.cause, 'expectation-not-met');
    assert.ok(result.detail.includes(
      'The page shows an error rather than what was expected. It says: "Something went wrong"'), result.detail);
  });
}
