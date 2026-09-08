# fixed

The scheduled site smoke installed a browser the runner would never launch.

`sitesmoke.yml` installed the runner with `npm --prefix runner ci` and then ran
`npx playwright install` from the repository root. There is no `package.json` at
the root, so npx did not resolve the runner's pinned playwright at all. It
fetched the newest one from the registry and downloaded browsers for that
version instead. The runner then launched its own pinned playwright, looked for
a revision nothing had installed, and could not start a browser on either
hostname.

The smoke reported COULD NOT TELL and exited non zero rather than passing, which
is the behaviour it was built for, so this cost a red rather than a false green.
`deploy.yml` already carried this fix and its explanation, and `ci.yml`,
`dogfood.yml` and `antifailure.yml` have run both commands under
`working-directory: runner` since they were written. This file was the one site
that still installed from the root.
