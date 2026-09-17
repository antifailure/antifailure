// The runner writes exactly one JSON document to standard output, and the
// engine reads that document to learn what the run found. Writing it is not the
// same as it arriving: process.stdout.write to a pipe buffers, and the OS pipe
// buffer is small (64 KiB on the machines this runs on). A document larger than
// that does not finish writing synchronously, so the bytes past the buffer sit
// in the stream waiting to drain.
//
// THE FAILURE. main resolves and the process exits the instant the document is
// handed to write, and process.exit discards whatever has not drained. A run
// whose evidence, a page's rendered text and its captured response bodies, grew
// the document past the pipe buffer therefore reached the engine cut off in the
// middle of the JSON. The engine read a document that would not parse, reported
// "the runner did not return a result document", and called a healthy run
// blocked. A small run flushed inside the buffer and looked fine, so this hid
// until a run captured enough to cross it.

/** writeFully resolves once the stream has handed the data to the operating
 *  system, not merely queued it, so a caller may exit without truncating it.
 *  The write callback is the signal that the chunk has been flushed; waiting on
 *  it is the whole fix, and it is why emit is awaited before the process exits. */
export function writeFully(stream: NodeJS.WriteStream, data: string): Promise<void> {
  return new Promise((resolve, reject) => {
    stream.write(data, (err) => (err ? reject(err) : resolve()));
  });
}

/** replaceRegExp makes the patterns readable in the output document.
 *
 * A RegExp serialises to {} by default, so a step that says which control it
 * pressed would come out empty, which is exactly the field somebody reads. */
export function replaceRegExp(_key: string, value: unknown): unknown {
  return value instanceof RegExp ? value.source : value;
}

/** emit writes the one result document and waits for it to reach the operating
 *  system, so a document of any size arrives whole rather than being cut off at
 *  the pipe buffer when the process exits. It is the only writer of the result
 *  document, so the flush lives here rather than at each call site. */
export async function emit(out: unknown): Promise<void> {
  await writeFully(process.stdout, JSON.stringify(out, replaceRegExp, 2) + '\n');
}
