// A full screen program that opens on a blank screen and draws only once it
// has a key: it takes the alternate screen, clears it, hides the cursor and
// waits. Every byte it writes before the key is invisible, and all of them are
// its own.
//
// Raw mode comes before the first byte, so a key the driver sends once it has
// seen those bytes is read as a key and never echoed by the line discipline.
// It then stays on what it drew until the driver stops it, so the screen it is
// judged on is the one it drew and not the one it restores on its way out.

if (!process.stdin.isTTY || !process.stdout.isTTY) {
  process.stderr.write('blank-until-key needs a terminal\n');
  process.exit(2);
}
process.stdin.setRawMode(true);
process.stdin.resume();
process.stdin.once('data', (chunk) => {
  process.stdout.write(`\x1b[1;1Hgot ${chunk.toString('utf8')}`);
});
process.stdout.write('\x1b[?1049h\x1b[2J\x1b[H\x1b[?25l');
