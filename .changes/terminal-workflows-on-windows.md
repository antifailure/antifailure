# fixed

Terminal workflows on Windows reported wrong verdicts about programs that were
behaving correctly.

The driver handed the terminal emulator one chunk of output at a time and
waited for each to be drawn. Every wait costs a timer tick, which is about a
millisecond on Linux and macOS and 15.6 ms on Windows, and ConPTY delivers
output a dozen bytes at a time. A ten thousand line program that Linux draws in
a second was still 72723 of 108989 bytes behind after twenty seconds, so the
workflow was blocked for a screen it could not finish drawing. Output that
arrives while a chunk is being drawn is now drawn together with it, and the same
program is drawn in under two seconds on Windows.

Arrow keys reached a program in the wrong encoding. ConPTY keeps a program's
request for application cursor keys to itself, so the driver always sent the
normal encoding and a program that had asked for the other one ignored the key.
On Windows an arrow is now sent as a key press and release, which the console
turns into the encoding the program asked for.

A `never` check that saw nothing forbidden now blocks rather than passes on
Windows. ConPTY hands over the screen as drawn, and a line a program printed and
then overwrote in place was missed in every one of fifteen measured runs, so a
pass there would claim something nobody could see. A forbidden string that does
arrive still fails the workflow.

A desktop application that exits while starting, and on Windows one whose path
does not exist, crashed the runner with no result at all. It is now a blocked
workflow that says the application did not start.

On Windows every terminal workflow left its console host and two pipes open
until the runner exited. They are now closed when the workflow ends.
