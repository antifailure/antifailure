# fixed

A long `af load compare` filled the terminal with stacked copies of its own
status line. Each round was printed while the line that counts elapsed time was
still on the screen, so "round 4 of 48: sending the mix at this build" was
written onto the end of "Ctrl-C to stop and roll back". The pair was wider than
a 110 column terminal, it wrapped, and every round left one more fused line and
a fragment such as "ld" on a row of its own. `af oracle` and `af scenario`
print their progress the same way and were exposed to the same thing.

Anything printed during a run now erases the status line first, and the line is
drawn again under it at the next second. The status line is also measured
against the terminal as it is when it is drawn rather than as it was when the
command started, so a pane resized during a run, or one narrower than 40
columns, gets a line that fits. It gives up the interrupt reminder first, then
the step timer, and keeps the run timer. Each compare round now also restarts
"on this step", which had been counting from the start of the whole compare.
