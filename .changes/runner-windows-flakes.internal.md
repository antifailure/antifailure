# fixed

Three runner tests failed the required Windows check at random, on pull
requests that changed no runner code, and one of them could hold the job until
its thirty minute timeout.

`a program still writing when the budget runs out is blocked, not judged`
asserted the screen was behind the program, on the belief that a program
writing flat out always outruns the emulator. On Windows ConPTY hands over the
screen frame by frame rather than every byte, and the emulator kept up with all
211487 bytes, so the check failed with the screen 0 bytes behind. The test now
lets the first parse through and holds the second past the budget, so the
screen is behind on every host and the measurement it checks is real.

The `keys-pending` case of `a queued parse respects the budget` assumed the
startup wait would spend the whole budget before the key went out. On Windows
the wait could end on ConPTY's own setup bytes, the key was sent, and the run
ended "still writing" instead of "keys still to send". It now holds its first
parse across the deadline, as the `quiet` case already did. Under load on a
Windows VM, 1 of 20 runs failed before and 0 of 20 after.

`npm test` passes `--test-force-exit`, so a test that hangs past its timeout
cannot keep its file's process, and with it the job, alive: a test stuck on a
promise never reaches the `finally` that closes its server. The tests that
check a stopped workflow left no browser now give it five seconds on the clock
rather than fifty polls, which on Windows cost PowerShell's start up each.

`TestRun_MeasuresARealServer` in the engine's load package compared the slow
route's p95 with the fast route's, and on a loaded Windows runner the fast
route measured 56.44 ms against the slow route's 28.41. It now checks what
the server guarantees on every host: every request to the slow route waited
25 ms, so that route's median cannot be lower. The sort order it checks is
decided by baselines far enough apart that no host speed can reverse it.
