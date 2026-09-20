// The rendered screen: a terminal emulator with no terminal attached, so the
// runner can read what a program DREW rather than the bytes it wrote.
//
// This is the terminal's accessibility tree, and it is the reason the surface
// abstraction holds. A browser exposes a tree of roles and names, and the web
// driver asserts against that rather than against the HTML that produced it. A
// full screen program exposes nothing at all: it writes cursor moves, colour
// changes, scroll regions and erases, and the thing a person reads exists only
// as the grid of cells those instructions leave behind. Matching an
// expectation against the raw byte stream would be matching against the HTML,
// and worse: a menu item that was drawn, then erased, then redrawn one row up
// appears three times in the stream and once on the screen, and the position a
// person would name is in neither.
//
// So the bytes go through a real vt100 and xterm emulator and the assertions
// read the cell grid it maintains. `@xterm/headless` is that emulator: the
// parser from the terminal in VS Code, with the rendering removed, pure
// JavaScript and no native code. Writing one here would be writing a worse
// one, and the escape sequences a program can send are not a small set.
//
// TWO BUFFERS, AND WHY BOTH ARE READ. A program that takes over the screen
// switches to the ALTERNATE buffer, which is exactly the visible grid and
// keeps no history: what has scrolled past is gone, because on a real terminal
// it was never there. A program that just prints stays on the NORMAL buffer,
// where everything it has ever printed is still above the viewport. Judging
// only the visible rows would lose the first ninety lines of a command's
// output; judging only the history would lose everything a TUI is showing
// right now. So `screen()` is what is visible and `everything()` is the
// visible rows plus the history behind them, and the driver judges against the
// history plus every screen the program showed along the way.

import type { Terminal } from '@xterm/headless';
import type { CursorKeyMode } from './keys.ts';

/** Screen is one emulator, fed the bytes a program writes and read for what
 *  those bytes drew. */
export class Screen {
  readonly rows: number;
  readonly cols: number;
  private readonly term: Terminal;

  constructor(term: Terminal, rows: number, cols: number) {
    this.term = term;
    this.rows = rows;
    this.cols = cols;
  }

  /** write feeds the program's output to the emulator and resolves once it has
   *  been PARSED, not merely queued. xterm parses asynchronously in chunks, so
   *  a snapshot taken straight after a write reads the grid as it was before
   *  the program's last redraw. That is a race whose only symptom is an
   *  expectation that fails about a screen the program did draw. */
  write(data: string): Promise<void> {
    return new Promise((resolve) => this.term.write(data, resolve));
  }

  /** screen is what a person looking at the terminal would see right now: the
   *  visible rows, with trailing blank lines dropped so the report is not
   *  mostly whitespace. Trailing spaces on each row go too; a terminal pads
   *  every row to its full width and none of that padding is content. */
  screen(): string {
    const buffer = this.term.buffer.active;
    const lines: string[] = [];
    for (let i = 0; i < this.rows; i++) {
      const line = buffer.getLine(buffer.viewportY + i);
      lines.push(line ? line.translateToString(true) : '');
    }
    return trimTrailingBlankLines(lines).join('\n');
  }

  /** everything is the visible rows and the history behind them. On the
   *  alternate buffer there is no history, so this and `screen` agree, which
   *  is correct rather than a special case: a program that owns the screen has
   *  shown only what is on it. */
  everything(): string {
    const buffer = this.term.buffer.active;
    const lines: string[] = [];
    for (let i = 0; i < buffer.length; i++) {
      const line = buffer.getLine(i);
      lines.push(line ? line.translateToString(true) : '');
    }
    return trimTrailingBlankLines(lines).join('\n');
  }

  /** alternate reports whether the program has taken over the screen. A true
   *  here is the emulator's own evidence that this is a full screen program,
   *  and it is what the report says instead of the driver claiming it. */
  alternate(): boolean {
    return this.term.buffer.active.type === 'alternate';
  }

  /** cursorKeys is the encoding the PROGRAM has asked for, read from the modes
   *  it set rather than assumed. See keys.ts: sending the other encoding is
   *  ignored in silence, so this is the difference between an arrow key
   *  working and an expectation failing for no visible reason. */
  cursorKeys(): CursorKeyMode {
    return this.term.modes.applicationCursorKeysMode ? 'application' : 'normal';
  }

  dispose(): void {
    this.term.dispose();
  }
}

/** trimTrailingBlankLines drops the empty rows at the end. A 24 row terminal
 *  showing three lines of output is three lines of content and twenty one rows
 *  of padding, and the padding is not evidence. */
function trimTrailingBlankLines(lines: readonly string[]): string[] {
  let end = lines.length;
  while (end > 0 && lines[end - 1]!.trim() === '') end -= 1;
  return lines.slice(0, end);
}

/** openScreen loads the emulator and returns one sized to the workflow.
 *
 *  Loaded here, and only when a screen is actually wanted, so that a run with
 *  no terminal workflow in it never touches the dependency. `scrollback` holds
 *  the history a printing program leaves above the viewport; the default is
 *  1000 rows and a command that prints more than that would lose its first
 *  lines from `everything`, which is a quiet wrong answer rather than a loud
 *  one. */
export async function openScreen(rows: number, cols: number): Promise<Screen> {
  // @xterm/headless is CommonJS, so its exports arrive under `default` when it
  // is imported from a module. Named imports of it fail at RUN time and not at
  // type check time, because the typings describe an ES module and the package
  // is not one; that asymmetry is why this reads the namespace rather than
  // destructuring the import.
  const loaded = await import('@xterm/headless');
  const emulator = loaded.default ?? loaded;
  const term = new emulator.Terminal({
    rows,
    cols,
    scrollback: 20_000,
    allowProposedApi: true,
  });
  return new Screen(term, rows, cols);
}
