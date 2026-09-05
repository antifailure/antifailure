# fixed

`af runner check` said the runner was complete about a tree it had just said it
could not read.

A runner whose `package.json` cannot be parsed reports its dependencies as not
checked, which is right: an unanswered question is not a proven failure, and
reporting one as the other sends somebody to reinstall over the manifest that
is the thing wrong with the tree. The verdict underneath was then computed as
"nothing proved a blockage, therefore ready". So the command printed the honest
line and the wrong conclusion beneath it, answered `complete: true`, and exited
0. A script reading the exit code was told a tree nobody could inspect was fine.

The verdict now has three values rather than two, because `complete` and
`not complete` cannot say "I could not tell". Ready exits 0, blocked exits 3,
and undetermined exits 9, the code the error reference publishes as "nothing
was measured". `complete` is true only for ready, and the document names the
question that went unanswered.

`af start` had the same gap on the same function and now reports that runner
step as not checked, which is the state it already uses everywhere else.
