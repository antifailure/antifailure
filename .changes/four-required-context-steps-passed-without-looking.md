# fixed

Four steps in required contexts reported a pass without having looked.

`if grep -rn ... engine tools; then` reads grep's exit 2, which means it could
not search, as no match, so the `if` is false and the step passes. Rename the
search root and the check is green forever. Measured: with `engine/` renamed
away the old edition boundary step exits 0, and with `web/packages` renamed
away so does the enterprise one. Both refuse now, and both still refuse a real
enterprise reference, so neither was loosened to stop it complaining.

`for path in $(grep ...)` over the error catalog runs its body zero times when
the grep stops matching, and exits 0 having checked no page. The step directly
above it already guards exactly this, with a count and a comment saying why.
Measured: rename the `docs:` key and the old loop exits 0; the guard now reads
46 paths on this commit and refuses anything under 20.

The step called "The suites did not skip" opened a TCP socket to Postgres and
examined nothing else, while the next step's `npm test --workspaces
--if-present` is the actual skip mechanism. It is renamed to the question it
answers, and a second step now refuses a workspace whose `test` script has gone
and refuses a workspaces glob that resolves to nothing.
