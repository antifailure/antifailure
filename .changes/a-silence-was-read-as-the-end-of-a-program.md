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
exit still ends it at once, and the budget still bounds it. So a workflow whose
expectation never appears, against a program that never exits, now spends its
declared budget before reporting the failure rather than ending 600 ms after the
last byte. That is the cost, it falls only on the failing case, and it is the right
way round: the alternative spends correctness to buy latency.

Two residuals that were only in a handover are now stated beside the code they
constrain: the drain for a program that has exited still has no ceiling, so such a
program holds the driver for as long as its backlog takes to parse, and a program
whose expectation is already met and which then contradicts itself is still judged
on the earlier screen.
