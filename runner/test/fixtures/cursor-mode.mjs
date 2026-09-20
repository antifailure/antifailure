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
process.stdin.on('data', (chunk) => {
  const bytes = chunk.toString('utf8');
  if (bytes === 'OA') process.stdout.write('[3;1Happlication up');
  else if (bytes === '[A') process.stdout.write('[3;1Hnormal up');
  else if (bytes === 'q') process.exit(0);
  else process.stdout.write('[3;1Hsomething else');
});
