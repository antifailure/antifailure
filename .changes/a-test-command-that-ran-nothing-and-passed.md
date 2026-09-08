# fixed

Seven test commands reported success after running no tests.

Every JavaScript suite in this repository is started by handing node a shell
glob: `node --test test/*.test.ts` and six variations of it. When the glob
matches nothing, `sh` passes the pattern through as a literal, node's own
runner treats it as a pattern of its own, matches nothing, prints `pass 0` and
exits 0. Rename `runner/test`, or rename the files inside it, and the `runner`
required context goes green having executed nothing. The same shape covers
`www`, `console`, `api`, and the three workspaces behind the `control plane`
required context.

Measured rather than reasoned about. With `runner/test` renamed away the old
command exits 0 and reports `pass 0`; the same tree with the guard exits 1.

Each script now requires its own files to exist before node is started. It
closes the case where there is nothing to run. It does not close a file that
exists and registers no test, and it does not close `npm run test
--workspaces --if-present`, which silently skips a workspace whose `test`
script has gone. Both are named here rather than left to be discovered.
