# fixed

A network partition held for its full five seconds was reported as lasting no
time at all.

An agent driving `inject_declared_faults` against a service declared to be cut
off from its database for five seconds got back `duration_ms: 0`, and said the
number made it doubt the cut had lasted. The cut had lasted. Measured from the
daemon every 200 milliseconds, the service was off its network for between 4.9
and 5.6 seconds. The zero came from the line that records a fault's duration:
it was deferred, and it wrote into a local copy after the return statement had
already handed the entry back. Every fault outside a durability proof reported
zero, in the MCP result and in `af chaos -o json`, whatever happened.

The duration is now the step's real length, and every fault also reports how
long it was measured to be in place, from the moment the injection returned to
the moment its undo began, beside the hold the manifest declared. The terminal
and the pull request comment say it in words, "It was in place for 5.001s
(declared 5s), then undone.", and the MCP result carries `in_place_ms`,
`hold_declared_ms` and the same sentence as `in_place`. A measured zero is said
as a sentence rather than printed as a number, and a killed process, which has
nothing to undo, is described as the wait before the result was read.

The same run showed that `after` was not honoured either. The manifest
describes it as the least time the run waits before a fault, and the code only
used it as part of a timeout, which is a ceiling. The partition went in less
than half a second into a run that declared `after: 5s`, and a database freeze
did the same. Both now wait the declared time.

The new measurement also shows something the old number hid: a frozen database
stays frozen about two seconds past its declared hold, because the durability
proof gives its writers that long to finish the statement they were on before
the database is thawed. The report now says so, for example "in place for
5.003s (declared 3s)", instead of repeating the manifest.
