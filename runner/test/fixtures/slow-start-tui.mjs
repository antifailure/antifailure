// menu-tui.mjs behind a slow start, the way it arrives on a loaded Windows
// runner.
//
// ConPTY writes its own setup to the terminal the moment the session opens,
// before the program has drawn anything: it hides the cursor, clears the
// screen and sets the window title. None of it puts a character on the grid.
// This fixture does the same and then takes half a second to start, which is
// what a node process takes on a busy machine, and only then becomes the menu.
// A driver that reads those bytes as the program's first screen sends its keys
// before the program has drawn, and the screen the program opens on is never
// recorded.

process.stdout.write('\x1b[?25l\x1b[2J\x1b[H\x1b]0;slow-start-tui\x07');
setTimeout(() => import('./menu-tui.mjs'), 500);
