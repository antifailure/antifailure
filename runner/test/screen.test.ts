// The emulator wrapper's own reading of the program's output: which key
// protocol the terminal has been asked for.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { openScreen } from '../src/drivers/screen.ts';

test('a request for win32-input-mode switches keys to events, and its reset switches them back', async () => {
  // ConPTY asks for mode 9001 at startup. The emulator has no such mode, so
  // without the screen watching for the request it would be dropped silently
  // and every arrow on Windows would be sent in an encoding the program may
  // have turned off.
  const screen = await openScreen(5, 20);
  try {
    assert.equal(screen.keyProtocol(), 'vt');
    await screen.write('\u001b[?9001h\u001b[?1004h');
    assert.equal(screen.keyProtocol(), 'win32');
    await screen.write('\u001b[?9001l');
    assert.equal(screen.keyProtocol(), 'vt');
  } finally {
    screen.dispose();
  }
});

test('watching for win32-input-mode does not swallow the modes the emulator applies', async () => {
  // The watcher reports the sequence as unhandled so the emulator still sets
  // the modes beside it, here application cursor keys in the same sequence.
  const screen = await openScreen(5, 20);
  try {
    await screen.write('\u001b[?1;9001h');
    assert.equal(screen.keyProtocol(), 'win32');
    assert.equal(screen.cursorKeys(), 'application');
  } finally {
    screen.dispose();
  }
});
