# added

Terminal workflows: a manifest can now test a command line program, including
a full screen one.

The terminal driver had been written and unit tested for some time, and no
customer run had ever reached it. The runner read a `terminal` list out of the
job document, the engine's job document had no such field, and the manifest had
no key that could have filled one, so the surface abstraction described four
surfaces and the product could ask for exactly one. `terminal_workflows` is
that key, and a terminal result is counted in the verdict and printed in the
report exactly like a browser one.

The driver it reaches is a different driver. It was line oriented, which is
enough for a program that reads a line and prints lines and is nothing at all
for the one kind of command line application nobody can test by hand: a program
that takes over the screen will not start without a terminal, reads raw
keystrokes rather than lines, and leaves its meaning in a grid of cells rather
than in the bytes it wrote. A workflow that declares a `screen` is now given a
real pseudo terminal of that size, sent the bytes a keyboard sends for
`<down>`, `<ctrl-c>` and the rest, and judged against every screen the program
drew. Those screens are the steps in the report.

A failed terminal workflow carries steps a person can follow at their own
terminal: the invocation, the size it was given, and one line per key that was
pressed. The report skips its "how to see this yourself" block when that list
is empty, so a red check used to name a terminal workflow and offer no way at
all to reach it.

Arrow keys are sent in the encoding the program asked for rather than a guess,
because a program that has taken the screen ignores the other one in silence,
and a key that silently did nothing is the worst shape a test failure can take.
