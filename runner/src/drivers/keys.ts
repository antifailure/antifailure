// What a keystroke is, as bytes, so a workflow can say `<down>` and a curses
// program receives what a real keyboard would have sent it.
//
// A full screen program does not read lines. It reads the raw bytes a terminal
// emits for every key, and those bytes are not the character on the keycap:
// Enter is a carriage return, Escape is one byte that also begins every other
// key's sequence, and an arrow is three or four bytes of escape sequence. A
// driver that could only write text could type into a prompt and could never
// press anything, which is the difference between reading a CLI and driving a
// TUI.
//
// THE PART THAT IS EASY TO GET WRONG AND SILENT WHEN YOU DO. Arrows, Home and
// End have TWO encodings, and which one is correct is decided by the PROGRAM,
// not by us. A program that sets DECCKM (the private mode `?1`, "application
// cursor keys", which vim, htop, less and every Bubble Tea and Ink application
// do while they own the screen) expects ESC O A for Up; one that has not set it
// expects ESC [ A. Send the wrong one and nothing happens at all: no error, no
// beep, the program simply ignores bytes it does not recognise and the
// expectation fails for a reason the report cannot name. So the encoder is
// given the mode the emulator has observed rather than guessing, and the
// emulator learns it by parsing the program's own output.
//
// Anything that is not a recognised key name is typed literally, including a
// `<` that begins no key. That is the tolerant read boundary rule: a workflow
// that types `<html>` into a form field gets `<html>`, and no escape syntax has
// to be learned or remembered to make that work.

/** Where the cursor key encoding is decided. `application` is DECCKM set,
 *  which is what a program that has taken over the screen almost always does;
 *  `normal` is the encoding a shell prompt expects. */
export type CursorKeyMode = 'normal' | 'application';

const ESC = '';

/** The keys whose bytes never depend on the cursor key mode. */
const FIXED: Record<string, string> = {
  enter: '\r',
  return: '\r',
  tab: '\t',
  backtab: ESC + '[Z',
  esc: ESC,
  escape: ESC,
  space: ' ',
  backspace: '',
  delete: ESC + '[3~',
  insert: ESC + '[2~',
  pageup: ESC + '[5~',
  pagedown: ESC + '[6~',
  f1: ESC + 'OP',
  f2: ESC + 'OQ',
  f3: ESC + 'OR',
  f4: ESC + 'OS',
  f5: ESC + '[15~',
  f6: ESC + '[17~',
  f7: ESC + '[18~',
  f8: ESC + '[19~',
  f9: ESC + '[20~',
  f10: ESC + '[21~',
  f11: ESC + '[23~',
  f12: ESC + '[24~',
};

/** The keys the cursor key mode decides, as the final letter of the sequence.
 *  ESC [ <letter> in normal mode, ESC O <letter> in application mode. */
const CURSOR: Record<string, string> = {
  up: 'A',
  down: 'B',
  right: 'C',
  left: 'D',
  home: 'H',
  end: 'F',
};

/** encodeKey returns the bytes for one `<name>` token, or null when the name is
 *  not a key, in which case the caller types the token literally. */
export function encodeKey(name: string, mode: CursorKeyMode): string | null {
  const key = name.trim().toLowerCase();
  if (Object.hasOwn(FIXED, key)) return FIXED[key]!;
  if (Object.hasOwn(CURSOR, key)) {
    return (mode === 'application' ? ESC + 'O' : ESC + '[') + CURSOR[key]!;
  }
  // ctrl-<letter> is the letter's position in the alphabet as a control byte,
  // which is how a terminal has encoded it since the teletype: ctrl-c is 3,
  // ctrl-d is 4. Written with a hyphen because that is how a person writing a
  // manifest spells it out loud.
  const ctrl = /^ctrl-([a-z])$/.exec(key);
  if (ctrl) {
    return String.fromCharCode(ctrl[1]!.charCodeAt(0) - 96);
  }
  return null;
}

/** encodeKeys turns one input entry into the bytes to write to the pseudo
 *  terminal. Text is typed as written; every `<name>` that names a key becomes
 *  that key's bytes; every other `<...>` is typed as it appears. */
export function encodeKeys(entry: string, mode: CursorKeyMode): string {
  let out = '';
  let i = 0;
  while (i < entry.length) {
    if (entry[i] !== '<') {
      out += entry[i];
      i += 1;
      continue;
    }
    const close = entry.indexOf('>', i + 1);
    if (close === -1) {
      // No closing bracket at all, so there is no token here to read. The rest
      // of the entry is text.
      out += entry.slice(i);
      break;
    }
    const bytes = encodeKey(entry.slice(i + 1, close), mode);
    if (bytes === null) {
      // Not a key. Type the `<` and carry on from the next character, so a
      // later token in the same entry is still read: `<b><enter>` types `<b>`
      // and then presses Enter.
      out += '<';
      i += 1;
      continue;
    }
    out += bytes;
    i = close + 1;
  }
  return out;
}

/** describeEntry is what the step reads as in the cast and the report. A key
 *  is pressed and text is typed, which is the distinction a person watching
 *  wants to see, and it never prints the raw escape bytes: an escape sequence
 *  in a report is unreadable and, in a terminal rendering that report, is
 *  executed rather than shown. */
export function describeEntry(entry: string): string {
  const single = /^<([^<>]+)>$/.exec(entry);
  if (single && encodeKey(single[1]!, 'normal') !== null) {
    return `Press ${single[1]!.trim().toLowerCase()}`;
  }
  return `Type ${JSON.stringify(entry)}`;
}
