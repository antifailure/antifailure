# fixed

A terminal workflow could be told its program never printed something the program
was about to print.

The driver waits for a program's last word and accepted a silence of 600 ms as
proof it had finished. That silence is measured on DELIVERY, so its clock counts
the gap since the DRIVER last read rather than the gap since the PROGRAM last
wrote, and those are the same number only while the program is the one deciding. A
program merely descheduled on a loaded machine is silent without being finished,
and the verdict was then `expectation-not-met`, which says the program did not
print something, about output nobody had waited for. On a developer's own machine
that is a wrong red pointing at a bug that does not exist.

It was reachable rather than theoretical. Induced at one position with only the
length of the silence varying, 300 ms and 500 ms never failed, 700 ms failed two
runs in three and 1000 ms failed three in three, so the transition sat exactly on
the constant and raising it would only have moved it. The same defect failed this
repository's own `runner` check once in 333 observations, on pull requests that had
not touched the runner.

A silence now ends the wait only when the screen ALREADY shows what the workflow
expected, which is the rule both web planners follow: more output can only ever
turn an unmet expectation into a met one, because the transcript accumulates. An
exit still ends it at once, and the budget still bounds it.

WHAT THIS CHANGES FOR A WORKFLOW YOU ALREADY HAVE, because it is a timing change you
will see. A terminal workflow that FAILS, against a program that does not exit on
its own, now takes the `maxMs` it declared before reporting the failure, where it
used to report about 600 ms after the program's last byte. With no `maxMs` that is
the 30 s default. A workflow that PASSES is unchanged, and so is one whose program
exits: a met expectation still ends the wait one silence after the last byte,
measured at 640 ms against a 30 s budget. If a pipeline gets slower after this
upgrade, it is a workflow that was failing, and the extra time is the driver making
sure the failure is the program's rather than its own.

That is the cost, it falls only on the case that has already gone wrong, and it is
the right way round: the alternative spends correctness to buy latency, and what it
bought was a fast answer that was sometimes untrue.

Two residuals that were only in a handover are now stated beside the code they
constrain: the drain for a program that has exited still has no ceiling, so such a
program holds the driver for as long as its backlog takes to parse, and a program
whose expectation is already met and which then contradicts itself is still judged
on the earlier screen.
