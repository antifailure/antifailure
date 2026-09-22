# fixed

Undoing a container pause could report success and leave the database frozen.

The undo asked the daemon whether the container was paused before thawing it,
and returned success when the answer was no. The helper it asked also answers
no when the inspect itself fails, so one stumble from the daemon at that moment
skipped the thaw and still called the fault undone. The undo now always asks
for the thaw, then reads the container back: a container still frozen, or one
whose state could not be read, is reported as a failed undo. A container that
was already thawed or has gone away is still not an error.
