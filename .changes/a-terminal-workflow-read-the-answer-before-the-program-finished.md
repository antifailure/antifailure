# fixed

A terminal workflow was judged on the part of a program's output that had
arrived.

After the last keystroke the driver gave the program six hundred milliseconds to
exit and then read the screen, however much of its output was still coming. A
program that answers a key, falls quiet and only THEN prints was therefore
judged on a fraction of what it wrote, and the report said the expectation was
not met, which is a different fact from "I did not wait for the rest of it". On a
loaded machine that read the transcript at line 3892 of 20000 and called a
working program broken, and it did so on a tree byte for byte identical to one
that had passed, which is how a race reads as a flake.

The wait now ends on a fact about the program rather than on a clock: it exited,
or it has been silent long enough that a redraw cannot explain the silence. Only
the budget the workflow declared bounds it. A program that reaches that budget
still writing is reported BLOCKED, and the report names how many bytes it had
written and how far behind the screen was, because "I could not look" and "it was
not there" send a reader to different places.

Two ceilings came with it. The emulator drain is bounded, because one without a
bound can never catch a program that writes faster than the emulator parses: it
hung a run for over four hundred seconds, and a run that never ends reports
nothing about anything. And nothing is fed to the emulator once the driver has
stopped reading it, so the backlog behind a spent budget is not parsed and a
disposed emulator is not written to.
