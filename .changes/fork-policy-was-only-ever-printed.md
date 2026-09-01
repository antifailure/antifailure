# security

`github.fork_policy` was sold as a security control and refused nothing.

The manifest schema said the default requires "a maintainer to add
`antifailure:allow` first, which is the only safe default: a fork's code would
otherwise run with the environment's credentials", and the pull request guide
said "nothing runs until a maintainer adds the label, which is a person
deciding". Nothing anywhere read the setting. `af explain` printed "forks
never, no environment is created for a fork" and `af up` on a fork's pull
request answered "Bringing up forkrepro-main-0cd221" and went to the Docker
daemon. The label appeared in two descriptions, one test asserting `af explain`
prints it, and the string `af explain` prints.

What customers actually had was GitHub's own default, which withholds secrets
from a fork's `pull_request` job on a GitHub-hosted runner. That is real and it
is not this control. It does nothing on a self-hosted runner, where the Docker
daemon, the registry login and the network are already on the machine, and
self-hosted is the ordinary shape here because an environment needs a daemon
and a golden. It does nothing under `pull_request_target`, which hands the base
repository's secrets to a job checking out a stranger's code on purpose.

`af ci`, `af up`, `af test` and `af load run` now refuse, before an environment
is named and before the daemon is touched, with `AF-GH-003`. `af ci` writes a
report saying the check did not run rather than exiting non zero, because a
fork waiting on a maintainer is not a finding about the change and `never`
would otherwise leave every fork pull request permanently red.

The policy is read from the BASE branch, not from the checkout. The manifest is
a file in the repository, so on a fork's pull request the checked out
`antifailure.yaml` is the fork's own copy, and reading the setting from there
would have let anybody add `fork_policy: always` to their pull request and walk
through the control. A checkout that does not carry the base branch falls back
to `label` and says so.

The example workflow now subscribes to `labeled`, because adding the label is
an event and a workflow that does not listen for it does not notice the
approval until the next push. Without that line the instruction to add a label
was itself a claim nothing acted on.

Two more settings in the same block, found in the same pass. `github.comment:
false` was never consulted, so turning comments off left them on; `af change
--write` and `af ci --report` now both honour it inside GitHub Actions, and the
report file an earlier step wrote is removed rather than left to be posted.
And nothing reads `github.teardown_on`, which cannot be fixed rather than
stated: teardown is unconditional in a workflow, and the control plane never
reads your manifest. `af explain`, the guide and the reference now say so
against the setting instead of printing it as though it were in force.
