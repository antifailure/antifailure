# fixed

`af explore` could not produce a report on any machine. The engine marshals the
runner's job document from Go, where a nil slice becomes `null`, and the
exploration path never sets the `workflows` field. The runner read
`doc.workflows.length` before it looked at the goals it was given, so every run
exited `AF-AGT-003` with a TypeError and no output.

Nothing caught it because nothing anywhere drove the runner's entry point. The
runner's own suite tests `explore()` directly, and the one Go test that reaches
a real subprocess replaces `node` with a shell script. Both halves worked and
the document between them was never sent by a test.

Fixed on both sides. The engine sends empty lists rather than nulls, so the
shape it promises is the shape it sends. The runner tolerates an absent or null
list, because it is the side of a boundary it does not control. Either fix
alone prevents the crash, and each has a test that fails when its own half is
reverted.
