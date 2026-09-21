# fixed

A terminal workflow could fail about output the program certainly wrote, and it
failed more often the busier the machine was.

The terminal driver feeds the program's bytes to an emulator that parses them
asynchronously, so the writes are chained and the chain is awaited before the
grid is read. Awaiting it once is not enough: the chain GROWS while it is
awaited, because the program keeps writing and every chunk appends another
link. The driver stopped waiting the moment it saw the program exit, so a
program that printed a lot and exited at once was judged with its final redraw
still queued. The expectation then went unmet about lines that were on their
way to the screen, and the verdict depended on how much CPU the parser happened
to get.

The driver now waits until the chain stops changing, which is the fixed point
that makes a snapshot mean what the code around it already claimed: everything
received so far has been drawn. It waits again before the transcript is read,
because waiting for the exit waits for the process and not for the emulator.

Forty lines never caught this and ten thousand catch it every time, so the
regression test prints ten thousand.
