// The live channel's test endpoint, shared by every test that stands in for the
// engine's watcher. One copy, because four copies is how all four came to
// listen on a unix socket path that Windows refuses.

import { createServer, type Server } from 'node:net';
import { randomBytes } from 'node:crypto';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { decode, type LiveEvent } from '../src/live.ts';

/** liveAddress is an endpoint shaped the way the engine's live.Address shapes
 *  one on this platform: a named pipe on Windows, where Node maps a path to a
 *  pipe and refuses a socket file with EACCES, and a socket file in a fresh
 *  private directory everywhere else. */
export function liveAddress(prefix: string): string {
  if (process.platform === 'win32') {
    return `\\\\.\\pipe\\${prefix}-${randomBytes(16).toString('hex')}`;
  }
  return join(mkdtempSync(join(tmpdir(), `${prefix}-`)), 'l.sock');
}

export interface Collector {
  readonly path: string;
  readonly server: Server;
  /** Every decodable NDJSON event that crossed the wire, in order. */
  lines(): LiveEvent[];
  /** Exactly the bytes that crossed the wire. */
  raw(): string;
  close(): void;
}

/** collector listens where a sink can reach it and keeps everything a sink
 *  writes, so a test can assert on what actually crossed the wire.
 *
 *  A listen that fails rejects. It used to be a promise with only a success
 *  path, so on Windows the EACCES became an uncaught error and the awaiting
 *  test waited out the whole 240 second test timeout, with whatever it had
 *  opened (a browser) still open. */
export function collector(prefix = 'af-live'): Promise<Collector> {
  const path = liveAddress(prefix);
  let buffer = '';
  const server = createServer((socket) => {
    socket.setEncoding('utf8');
    socket.on('data', (chunk) => { buffer += chunk; });
  });
  return new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(path, () => {
      server.off('error', reject);
      resolve({
        path,
        server,
        lines: () => buffer.split('\n').map(decode).filter((e): e is LiveEvent => !!e),
        raw: () => buffer,
        close: () => server.close(),
      });
    });
  });
}
