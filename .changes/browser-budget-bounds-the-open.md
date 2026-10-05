# fixed

A browser workflow's `budget.duration` did not bound the time it took to open
the browser. The budget is raced against the attempt, and when it ran out while
Chromium was still starting, the runner waited for the whole open, the launch,
the context, the trace and the first page, and only then closed what it had
made. A two second budget came back after twelve seconds on a Windows machine
starting its first browser, and the required Windows check failed once on a
pull request that changed no runner code, at 8332 ms against a test that
allows 8000.

The open now carries the budget. The launch is handed what is left of it as
Playwright's own launch limit, so a launch that cannot finish in time is
stopped by Playwright with nothing left running, and a budget that runs out
after the launch closes the browser at once, so the context and the page that
were still being made fail instead of finishing. A 50 ms budget that used to
take 1.1 to 1.5 s on macOS now ends in under a quarter of a second.

The Windows check could not see a leaked browser at all. The test helper that
looks for Chromium below the test process used `pgrep` and `ps`, neither of
which exists on Windows, and read the failure as "nothing running", so every
assertion that a stopped workflow left no browser behind passed there without
having looked. It reads the Windows process table now, and refuses rather than
answering when it cannot.
