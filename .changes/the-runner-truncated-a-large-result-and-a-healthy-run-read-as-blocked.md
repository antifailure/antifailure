# fixed

The runner truncated a large result document, so a healthy run read as blocked.

The runner writes one JSON document to standard output and the process exits the
instant it is written. Writing to a pipe buffers, the pipe buffer is 64 KiB, and
process.exit discards whatever has not drained, so a document larger than that
reached the engine cut off in the middle of its JSON. The engine read a document
that would not parse, reported "the runner did not return a result document",
and called the run blocked. A small run flushed inside the buffer and looked
fine, so the defect hid until a run captured enough evidence, a page's rendered
text and its response bodies, to cross the buffer. The site smoke, which drives a
content heavy page, was the first to cross it and turned main red.

The runner now waits for the document to reach the operating system before it
exits, so a result of any size arrives whole. The wait is one function every
result document goes through, and a test drives a real process across a real
pipe to prove a payload past the buffer is delivered without loss, with a naive
control that truncates so the test cannot pass by writing too little.
