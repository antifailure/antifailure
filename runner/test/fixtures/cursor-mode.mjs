// A program that asks for application cursor keys and then reports which
// encoding it was actually sent.
//
// It exists because the mode is the one part of the key encoding that cannot
// be checked by reading bytes in a unit test: the encoder is told the mode,
// and whether the DRIVER reads the mode the program set is a different claim.
// This program sets DECCKM, draws what it receives, and so can say no.

if (!process.stdin.isTTY) {
  process.stderr.write('cursor-mode needs a terminal\n');
  process.exit(2);
}

// DECCKM. From here an Up arrow is ESC O A and not ESC [ A.
process.stdout.write('[?1h');
process.stdout.write('[2J[H');
process.stdout.write('ready');

process.stdin.setRawMode(true);
process.stdin.resume();
// Input is a stream, not one key per read, and it is parsed as one here, the
// way every real program parses it. This used to compare each chunk with a
// whole key, and on a loaded Windows machine the arrow and the "q" after it
// arrived in one read, ESC [ A q, so the program drew "something else" for an
// arrow it had in fact received.
let pending = '';
process.stdin.on('data', (chunk) => {
  pending += chunk.toString('utf8');
  for (;;) {
    if (pending.startsWith('\u001bOA')) {
      process.stdout.write('\u001b[3;1Happlication up');
      pending = pending.slice(3);
    } else if (pending.startsWith('\u001b[A')) {
      process.stdout.write('\u001b[3;1Hnormal up');
      pending = pending.slice(3);
    } else if (pending.startsWith('q')) {
      process.exit(0);
    } else if (pending.length >= 3 || (pending.length > 0 && pending[0] !== '\u001b')) {
      process.stdout.write('\u001b[3;1Hsomething else');
      pending = pending.slice(1);
    } else {
      // A partial escape sequence: wait for the rest of it.
      return;
    }
  }
});
