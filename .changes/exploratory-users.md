# added

`af explore` sends agents at a goal with no declared workflow. They read each
page through the accessibility tree, choose where to go, and report where the
application cost somebody effort without failing: a control that did nothing, a
page with nothing left to try, a route that loops back, an interactive element
with no accessible name, a step slower than the goal allows, and a goal never
reached. Every finding names the page, the control and the step.

Every choice comes from the goal's seed and every duration from the injected
clock, so the same seed takes the same path and each result carries the command
that replays it. An exploration reports `pass` and exits zero even when it finds
things, because nobody declared what should happen on the pages it wandered
onto and a red mark on a pull request that is fine is a check people mute. A run
that could not start is `blocked`, which means nobody looked rather than nothing
was found.

`af explore --emit-workflow` prints the `workflows:` block that replays what was
explored, so a discovery becomes a check that runs on every pull request.
