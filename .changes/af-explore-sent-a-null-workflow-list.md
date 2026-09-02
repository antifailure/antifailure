# fixed

`af explore` now runs. It never had.

The engine built the runner's job document with a nil Go slice for
`workflows`, which marshals as `null`, and the runner read
`doc.workflows.length` with no guard. Every exploration, on every application,
died with a TypeError before the browser opened, so nothing downstream of it
had ever run either, `--emit-workflow` included.

Both sides compiled and both typechecked. They disagreed only on the wire, and
it was found by running the command rather than by reading either side. The
engine now sends `[]`, which is strict on the write, and the runner tolerates
a null or an absent list, which is tolerant on the read: a runner keeps
working against an engine that predates the fix, and one bad field on a
boundary does not take a whole run with it.
