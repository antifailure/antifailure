# fixed

On Linux a terminal workflow could fail a program for output it really printed.

When a program exited, the end of its output could be thrown away before
Antifailure read it, so a program that printed what was expected as its last
line was reported as never having printed it. With a slow reader this happened
in 122 of 150 runs. Antifailure now keeps the terminal open until it has read
everything the program wrote.
