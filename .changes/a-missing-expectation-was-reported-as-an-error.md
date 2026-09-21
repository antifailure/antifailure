# fixed

A workflow that failed because an expectation was not found was explained as
the application showing an error, whether or not anything on screen was one.

The runner answers "not met" for two different reasons: the screen shows a
failure banner, or a quoted expectation is simply absent. Both were explained
with the same sentence, "The page shows an error rather than what was
expected" on the web, desktop, iOS and Android, and "The screen showed a
failure" or "The output showed a failure" for a terminal program. Over a
healthy iOS journal and over a terminal menu drawn in full, that sent the
reader looking for an error that did not exist.

The detail now names the expectation, or expectations, that were not found
and quotes what was showing instead, and says an error was showing only when
the runner actually recognised one, which it still quotes. Verdicts, causes
and exit codes are unchanged.
