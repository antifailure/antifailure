// A full screen terminal program, so the terminal driver can be proved against
// something that genuinely draws rather than prints.
//
// It is deliberately a real one rather than a mock of one. It takes over the
// alternate screen buffer, turns off echo and line buffering so it reads raw
// keystrokes, moves the cursor to draw, redraws the whole screen on every key
// and restores the terminal when it leaves. A driver that can drive this can
// drive vim, and a driver that only appended text to a stream could not drive
// it at all: the highlighted row exists only as the grid of cells the escape
// sequences leave behind, and the row that used to be highlighted is written
// three times in the byte stream and appears nowhere on the screen.
//
// It refuses to run without a terminal, which is the other half of the proof:
// if the driver ever stopped allocating a pseudo terminal the test would fail
// loudly here instead of passing against a program in a degraded mode.

const ITEMS = ['Drafts', 'Scheduled', 'Published', 'Archived'];
const BODY = {
  Drafts: 'Two drafts are waiting for you.',
  Scheduled: 'Nothing is scheduled this week.',
  Published: 'Eleven posts are live.',
  Archived: 'The archive holds 340 posts.',
};

if (!process.stdin.isTTY || !process.stdout.isTTY) {
  process.stderr.write('menu-tui needs a terminal\n');
  process.exit(2);
}

let selected = 0;
let opened = null;

const write = (s) => process.stdout.write(s);
const clear = () => write('[2J[H');

function draw() {
  clear();
  write('[1;1H  Inbox[0m');
  ITEMS.forEach((item, i) => {
    const row = 3 + i;
    const marker = i === selected ? '>' : ' ';
    write(`[${row};1H ${marker} ${item}`);
  });
  const footer = 8;
  if (opened !== null) {
    write(`[${footer};1H${BODY[ITEMS[opened]]}`);
  } else {
    write(`[${footer};1Hj and k move, enter opens, q quits`);
  }
}

// The alternate screen buffer is what a full screen program takes: it has no
// scrollback, so nothing the program draws is recoverable from history and the
// grid is the only record of what it showed.
write('[?1049h');
write('[?25l');
process.stdin.setRawMode(true);
process.stdin.resume();
draw();

function leave(code) {
  write('[?25h');
  write('[?1049l');
  process.stdin.setRawMode(false);
  process.exit(code);
}

// Keys arrive as raw bytes, and an arrow is an escape sequence rather than a
// character. Both encodings are read, because which one a terminal sends is
// decided by the application cursor keys mode this program has not set: a
// driver that always sent one of the two would work against half the programs
// in the world and silently do nothing against the other half.
const SEQUENCES = [
  ['[A', 'up'], ['OA', 'up'],
  ['[B', 'down'], ['OB', 'down'],
];

process.stdin.on('data', (chunk) => {
  let rest = chunk.toString('utf8');
  while (rest.length > 0) {
    const sequence = SEQUENCES.find(([bytes]) => rest.startsWith(bytes));
    let key;
    if (sequence) {
      key = sequence[1];
      rest = rest.slice(sequence[0].length);
    } else {
      key = rest[0];
      rest = rest.slice(1);
    }
    if (key === 'q') {
      leave(0);
      return;
    }
    if (key === '') {
      leave(130);
      return;
    }
    if (key === 'j' || key === 'down') selected = Math.min(ITEMS.length - 1, selected + 1);
    if (key === 'k' || key === 'up') selected = Math.max(0, selected - 1);
    if (key === '\r') opened = selected;
  }
  draw();
});
