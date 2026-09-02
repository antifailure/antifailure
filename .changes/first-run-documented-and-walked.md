# fixed

The README told you to install Antifailure and then run `af init`, and never
mentioned `af runner install`. The runner drives the browser that produces every
verdict, it is a separate program in a separate language, and it needs node, so
the one dependency a reader had to know about was the one the front page left
out. The quickstart had the same shape one level deeper: it went from install to
a running environment and stopped, so the page that walks a new user through the
product never ran a workflow, never mentioned the runner, and never showed a
verdict or the evidence behind one.

Both now cover the whole path, and `tools/walkthrough` walks it: `af start`,
`af doctor`, `af runner install`, `af runner check`, `af explain`, `af up`, the
URL it printed, `af status`, `af test`, the artifacts on disk, `af start` again,
`af down`. The `af test` step does not assert on the exit code, because `af test`
exits 0 on `unverified` and `blocked` does not count against a run, so a run that
reached no workflow at all exits 0 exactly like one where everything passed. It
asserts that a verdict came back for at least one workflow and that at least one
of those verdicts is about the application rather than about the harness.
