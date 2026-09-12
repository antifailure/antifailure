# fix

Re-running the check from the Actions tab reported success having verified
nothing.

GitHub has three Re-run buttons. The two on a check send `check_run` or
`check_suite` rerequested, and the control plane reopened the check for both.
The one in the Actions tab re-runs the workflow run, which starts a second
attempt of the same run and sends neither of those events, so nothing reopened
the check and the second attempt's claim was refused with "the check on a1b2c3d
is already passed". The workflow could not tell that refusal from a fork, so it
skipped its report step and exited zero: a green job, and a pull request still
showing the verdict of the attempt that had been replaced.

A claim from a later attempt of the run already checking a commit now reopens
the check, counts the attempt, clears the previous verdict and takes a fresh
deadline. A claim from the same or an earlier attempt is refused, because a
signed identity outlives the report it bought. A completed check run cannot be
moved back out of completed at GitHub, so each attempt gets its own check run
and GitHub shows the most recent one.

Both workflow surfaces now say which silence they hit. `action.yml` fails a
customer's job when the control plane refuses a report the run held a
credential to send, and warns without failing when it could not be reached at
all.
