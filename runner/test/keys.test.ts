// The key encoder, which is the half of the terminal driver that can be
// checked without a process.
//
// Every assertion here is about bytes, because bytes are what the failure
// looks like: a program ignores a sequence it does not recognise in complete
// silence, so the wrong encoding is not an error, it is a key that did
// nothing, and the only symptom is an expectation that fails about a screen
// the program would have drawn.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { encodeKey, encodeKeys, describeEntry } from '../src/drivers/keys.ts';

const ESC = '';

test('the keys with one encoding are the bytes a keyboard sends', () => {
  assert.equal(encodeKeys('<enter>', 'normal'), '\r');
  assert.equal(encodeKeys('<tab>', 'normal'), '\t');
  assert.equal(encodeKeys('<esc>', 'normal'), ESC);
  assert.equal(encodeKeys('<space>', 'normal'), ' ');
  assert.equal(encodeKeys('<backspace>', 'normal'), '');
  assert.equal(encodeKeys('<delete>', 'normal'), ESC + '[3~');
  assert.equal(encodeKeys('<pageup>', 'normal'), ESC + '[5~');
  assert.equal(encodeKeys('<pagedown>', 'normal'), ESC + '[6~');
  assert.equal(encodeKeys('<f1>', 'normal'), ESC + 'OP');
  assert.equal(encodeKeys('<f12>', 'normal'), ESC + '[24~');
});

test('ctrl and a letter is the letter position as a control byte', () => {
  assert.equal(encodeKeys('<ctrl-c>', 'normal'), '');
  assert.equal(encodeKeys('<ctrl-d>', 'normal'), '');
  assert.equal(encodeKeys('<ctrl-a>', 'normal'), '');
  assert.equal(encodeKeys('<ctrl-z>', 'normal'), '');
});

test('the cursor keys follow the mode the program asked for', () => {
  // The whole reason the mode is threaded through at all. A program in
  // application cursor keys mode, which is every full screen program while it
  // owns the screen, ignores the other encoding without a word.
  assert.equal(encodeKeys('<up>', 'normal'), ESC + '[A');
  assert.equal(encodeKeys('<up>', 'application'), ESC + 'OA');
  assert.equal(encodeKeys('<down>', 'normal'), ESC + '[B');
  assert.equal(encodeKeys('<down>', 'application'), ESC + 'OB');
  assert.equal(encodeKeys('<right>', 'normal'), ESC + '[C');
  assert.equal(encodeKeys('<right>', 'application'), ESC + 'OC');
  assert.equal(encodeKeys('<left>', 'normal'), ESC + '[D');
  assert.equal(encodeKeys('<left>', 'application'), ESC + 'OD');
  assert.equal(encodeKeys('<home>', 'application'), ESC + 'OH');
  assert.equal(encodeKeys('<end>', 'application'), ESC + 'OF');
});

test('text is typed as written and mixes with keys', () => {
  assert.equal(encodeKeys('hello', 'normal'), 'hello');
  assert.equal(encodeKeys('yes<enter>', 'normal'), 'yes\r');
  assert.equal(encodeKeys('<esc>:wq<enter>', 'normal'), ESC + ':wq\r');
});

test('an angle bracket that names no key is typed literally', () => {
  // The tolerant read boundary: a workflow typing markup into a field must not
  // need an escape syntax, and a name nobody recognises must not vanish.
  assert.equal(encodeKeys('<html>', 'normal'), '<html>');
  assert.equal(encodeKeys('<notakey><enter>', 'normal'), '<notakey>\r');
  assert.equal(encodeKeys('a < b', 'normal'), 'a < b');
  // An opening bracket with no closing one is text to the end of the entry.
  assert.equal(encodeKeys('3 <', 'normal'), '3 <');
  assert.equal(encodeKeys('<enter', 'normal'), '<enter');
  assert.equal(encodeKey('notakey', 'normal'), null);
});

test('key names are read whatever case and spacing they are written in', () => {
  assert.equal(encodeKeys('<Enter>', 'normal'), '\r');
  assert.equal(encodeKeys('< ctrl-C >', 'normal'), '');
});

test('a step says press for a key and type for text, and never prints escape bytes', () => {
  assert.equal(describeEntry('<enter>'), 'Press enter');
  assert.equal(describeEntry('<Ctrl-C>'), 'Press ctrl-c');
  assert.equal(describeEntry('yes'), 'Type "yes"');
  // Not a key, so it is text, and the report shows the text rather than a
  // sequence that a terminal rendering the report would execute.
  assert.equal(describeEntry('<html>'), 'Type "<html>"');
  for (const entry of ['<enter>', '<up>', '<ctrl-c>', 'hello']) {
    assert.ok(!describeEntry(entry).includes(ESC),
      `a raw escape byte reached the report for ${entry}`);
  }
});

test('under win32-input-mode a cursor key is a key event, so the console host encodes it for the program', () => {
  // ConPTY consumes the program's DECCKM, so the emulator cannot know which
  // encoding the program wants. A key event carries none, and the console host
  // writes the sequence the program asked for. Down then up, as a keyboard
  // reports it, with ENHANCED_KEY set because these are the navigation keys.
  const up = `${ESC}[38;72;0;1;256;1_${ESC}[38;72;0;0;256;1_`;
  assert.equal(encodeKeys('<up>', 'normal', 'win32'), up);
  // The mode the emulator reports does not change the event, because under
  // ConPTY that report is not the program's.
  assert.equal(encodeKeys('<up>', 'application', 'win32'), up);
  assert.equal(encodeKey('left', 'normal', 'win32'), `${ESC}[37;75;0;1;256;1_${ESC}[37;75;0;0;256;1_`);
  assert.equal(encodeKey('end', 'normal', 'win32'), `${ESC}[35;79;0;1;256;1_${ESC}[35;79;0;0;256;1_`);
});

test('under win32-input-mode text and the keys with one encoding are sent as they are', () => {
  // Measured: ConPTY hands typed text and fixed sequences to the program
  // unchanged, so only the keys whose bytes depend on the program's modes need
  // to be events.
  assert.equal(encodeKeys('hi<enter>', 'normal', 'win32'), 'hi\r');
  assert.equal(encodeKeys('<ctrl-c>', 'normal', 'win32'), '\u0003');
  assert.equal(encodeKeys('<f5>', 'normal', 'win32'), `${ESC}[15~`);
});
