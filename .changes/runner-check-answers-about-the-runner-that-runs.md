# fixed

`af runner check` reported the runner ready while `af test` ran a different
copy and died in node.

The check read `~/.antifailure/runner`, which is where `af runner install` puts
its copy. A run resolves its runner with a search that offers the checkout's own
`runner/` first, and on a fresh clone that directory is source with no
`node_modules`. Both commands were honest about the directory each looked at,
neither said which, and the failure surfaced three commands later as
`Cannot find package 'playwright'` from inside the runner.

The check now reports on the runner a run started in that directory would use,
and prints its path. A run takes the nearest runner that can actually run rather
than the nearest one that exists, so a `runner/` whose dependencies were never
installed is passed over and named rather than started and crashed. When nothing
anywhere can run, `af test` refuses with the directory and the missing package
instead of a node stack trace. `af runner check -o json` now exits non zero when
it reports the runner incomplete, which it previously did only without `-o json`.
