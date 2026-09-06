# fixed

This repository's own rehearsal never ran a pull request's migration inside the twin.

The dogfood job shaped its source database with the head's own seeder, which
applies every migration in the head's tree before the golden is made. So the
copy carried a ledger that already recorded the pull request's migration as
applied, the twin had nothing left to run, and the one ordering a customer's
golden always produces, a copy of production that predates the change, was
never rehearsed here. Migration 0037 granted to a role the copy did not carry;
every environment branched from a real golden died on it for two days, and the
job was green on the pull request that merged it.

On a pull request the base commit's own tree now shapes the source, in a
worktree outside the workspace, and the head's migrations run for the first
time inside the twin. The log names how many. Reproduced before the change
with a golden made at 0036 and the release copy tool, which died on 0037 with
`role "antifailure_admin" does not exist`, and again after it with the copy
tool that carries the roles, which came up.
